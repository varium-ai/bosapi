package interfaces

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"bosapi/backend/internal/auth/application"
	"bosapi/backend/internal/auth/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Service defines the authentication use cases needed by portal endpoints.
type Service interface {
	Login(context.Context, string, string) (application.LoginResponse, error)
	VerifyMFA(context.Context, string, string) (application.TokenPair, error)
	SetupMFA(context.Context, uint64) (application.MFASetupResult, error)
	EnableMFA(context.Context, uint64, string) error
	DisableMFA(context.Context, uint64, string, string) error
	Refresh(context.Context, string) (application.TokenPair, error)
	Logout(context.Context, string, string, time.Time) error
	VerifyAccess(context.Context, string) (domain.User, string, time.Time, error)
	ChangePassword(context.Context, uint64, string, string) error
	RequestPasswordReset(context.Context, string) (int, string, error)
	ResetPasswordWithOTP(context.Context, string, string, string) error
	RecordLoginAttempt(context.Context, string, string, bool, string, time.Time) error
}

// Handler adapts authentication use cases to Gin endpoints.
type Handler struct{ service Service }

// NewHandler constructs an authentication HTTP handler.
func NewHandler(service Service) *Handler { return &Handler{service: service} }

type loginRequest struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,min=12,max=128"`
}

type verifyMFARequest struct {
	MFAToken string `json:"mfa_token" binding:"required"`
	Code     string `json:"code" binding:"required,len=6"`
}

type enableMFARequest struct {
	Code string `json:"code" binding:"required,len=6"`
}

type disableMFARequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	Code            string `json:"code" binding:"required,len=6"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"max=256"`
}

type forgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email,max=320"`
}

type resetPasswordWithOTPRequest struct {
	Email       string `json:"email" binding:"required,email,max=320"`
	OTP         string `json:"otp" binding:"required,len=6"`
	NewPassword string `json:"new_password" binding:"required,min=12,max=128"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=12,max=128"`
}

// RegisterPublicRoutes installs login, mfa verify, and refresh endpoints outside JWT middleware.
func (handler *Handler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.POST("/login", handler.login)
	group.POST("/mfa/verify", handler.verifyMFA)
	group.POST("/refresh", handler.refresh)
	group.POST("/forgot-password", handler.forgotPassword)
	group.POST("/reset-password", handler.resetPasswordWithOTP)
}

// RegisterProtectedRoutes installs authenticated identity, logout, and mfa management endpoints.
func (handler *Handler) RegisterProtectedRoutes(group *gin.RouterGroup) {
	group.GET("/me", handler.me)
	group.POST("/logout", handler.logout)
	group.POST("/change-password", handler.changePassword)
	group.GET("/mfa", handler.mfaStatus)
	group.GET("/mfa/status", handler.mfaStatus)
	group.POST("/mfa/setup", handler.setupMFA)
	group.POST("/mfa/enable", handler.enableMFA)
	group.POST("/mfa/disable", handler.disableMFA)
}

// Authenticate verifies JWTs and reloads the identity so role changes take effect immediately.
func (handler *Handler) Authenticate() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		authorization := strings.Fields(ctx.GetHeader("Authorization"))
		if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") || len(authorization[1]) > 4096 {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
			return
		}
		user, jti, expiry, err := handler.service.VerifyAccess(ctx.Request.Context(), authorization[1])
		if err != nil {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
			return
		}
		ctx.Set("identity", httpapi.Claims{UserID: user.ID, OrganizationID: user.OrganizationID, Role: user.Role, Email: user.Email, JTI: jti})
		ctx.Set("access_expiry", expiry)
		ctx.Set("user", user)
		ctx.Request = ctx.Request.WithContext(httpapi.WithLogMetadata(ctx.Request.Context(), httpapi.LogMetadata{OrganizationID: user.OrganizationID}))
		ctx.Next()
	}
}

// RequireRoles denies access unless the authenticated role is explicitly allowed.
func RequireRoles(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(ctx *gin.Context) {
		claims, ok := httpapi.CurrentClaims(ctx)
		if !ok {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
			return
		}
		if _, exists := allowed[claims.Role]; !exists {
			httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
			return
		}
		ctx.Next()
	}
}

func (handler *Handler) login(ctx *gin.Context) {
	var request loginRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	resp, err := handler.service.Login(ctx.Request.Context(), request.Email, request.Password)
	if err != nil {
		if errors.Is(err, application.ErrInvalidCredentials) || errors.Is(err, application.ErrInactiveUser) || errors.Is(err, application.ErrLoginLocked) {
			handler.recordLogin(ctx, request.Email, false, loginFailureReason(err))
		}
		if errors.Is(err, application.ErrInvalidCredentials) || errors.Is(err, application.ErrInactiveUser) || errors.Is(err, application.ErrLoginLocked) {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "email or password is invalid")
			return
		}
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	if !resp.MFARequired {
		handler.recordLogin(ctx, resp.User.Email, true, "")
	}
	if resp.MFARequired {
		httpapi.WriteSuccess(ctx, http.StatusOK, resp)
		return
	}
	ctx.SetCookie("refresh_token", resp.RefreshToken, int((30 * 24 * time.Hour).Seconds()), "/api/v1/auth", "", secureCookie(ctx), true)
	httpapi.WriteSuccess(ctx, http.StatusOK, resp)
}

func (handler *Handler) verifyMFA(ctx *gin.Context) {
	var request verifyMFARequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "mfa_token and 6-digit code are required")
		return
	}
	pair, err := handler.service.VerifyMFA(ctx.Request.Context(), request.MFAToken, request.Code)
	if err != nil {
		if errors.Is(err, application.ErrInvalidMFA) {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.InvalidMFA, "verification code is invalid")
			return
		}
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, err.Error())
		return
	}
	handler.recordLogin(ctx, pair.User.Email, true, "")
	ctx.SetCookie("refresh_token", pair.RefreshToken, int((30 * 24 * time.Hour).Seconds()), "/api/v1/auth", "", secureCookie(ctx), true)
	httpapi.WriteSuccess(ctx, http.StatusOK, pair)
}

func (handler *Handler) recordLogin(ctx *gin.Context, email string, success bool, reason string) {
	if err := handler.service.RecordLoginAttempt(ctx.Request.Context(), email, requestClientIP(ctx), success, reason, time.Now().UTC()); err != nil {
		// Authentication stays available when audit storage is temporarily unavailable.
		ctx.Error(err)
	}
}

func loginFailureReason(err error) string {
	if errors.Is(err, application.ErrInactiveUser) {
		return string(errorcode.InactiveUser)
	}
	if errors.Is(err, application.ErrInvalidCredentials) {
		return string(errorcode.InvalidCredentials)
	}
	if errors.Is(err, application.ErrLoginLocked) {
		return "login_locked"
	}
	return string(errorcode.AuthenticationError)
}

func requestClientIP(ctx *gin.Context) string {
	for _, candidate := range []string{ctx.GetHeader("X-Real-IP"), strings.Split(ctx.GetHeader("X-Forwarded-For"), ",")[0]} {
		if parsed := net.ParseIP(strings.TrimSpace(candidate)); parsed != nil {
			return parsed.String()
		}
	}
	host, _, err := net.SplitHostPort(ctx.Request.RemoteAddr)
	if err != nil {
		host = ctx.Request.RemoteAddr
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.String()
	}
	return ""
}

func (handler *Handler) setupMFA(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	res, err := handler.service.SetupMFA(ctx.Request.Context(), claims.UserID)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, res)
}

func (handler *Handler) enableMFA(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request enableMFARequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "6-digit code is required")
		return
	}
	if err := handler.service.EnableMFA(ctx.Request.Context(), claims.UserID, request.Code); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Two-factor authentication enabled successfully",
	})
}

func (handler *Handler) disableMFA(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request disableMFARequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "current password and 6-digit code are required")
		return
	}
	if err := handler.service.DisableMFA(ctx.Request.Context(), claims.UserID, request.CurrentPassword, request.Code); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Two-factor authentication disabled successfully",
	})
}

func (handler *Handler) refresh(ctx *gin.Context) {
	var request refreshRequest
	if err := ctx.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if request.RefreshToken == "" {
		request.RefreshToken, _ = ctx.Cookie("refresh_token")
	}
	pair, err := handler.service.Refresh(ctx.Request.Context(), request.RefreshToken)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "refresh token is invalid")
		return
	}
	ctx.SetCookie("refresh_token", pair.RefreshToken, int((30 * 24 * time.Hour).Seconds()), "/api/v1/auth", "", secureCookie(ctx), true)
	httpapi.WriteSuccess(ctx, http.StatusOK, pair)
}

func (handler *Handler) logout(ctx *gin.Context) {
	var request refreshRequest
	if err := ctx.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if request.RefreshToken == "" {
		request.RefreshToken, _ = ctx.Cookie("refresh_token")
	}
	claims, _ := httpapi.CurrentClaims(ctx)
	expiry, _ := ctx.Get("access_expiry")
	accessExpiry, _ := expiry.(time.Time)
	if err := handler.service.Logout(ctx.Request.Context(), claims.JTI, request.RefreshToken, accessExpiry); err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	ctx.SetCookie("refresh_token", "", -1, "/api/v1/auth", "", secureCookie(ctx), true)
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func secureCookie(ctx *gin.Context) bool {
	return ctx.Request.TLS != nil || strings.EqualFold(ctx.GetHeader("X-Forwarded-Proto"), "https")
}

func (handler *Handler) me(ctx *gin.Context) {
	value, exists := ctx.Get("user")
	user, ok := value.(domain.User)
	if !exists || !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, user.PublicIdentity())
}

func (handler *Handler) mfaStatus(ctx *gin.Context) {
	value, exists := ctx.Get("user")
	user, ok := value.(domain.User)
	if !exists || !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, user.MFAStatus())
}

func (handler *Handler) forgotPassword(ctx *gin.Context) {
	var request forgotPasswordRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "valid email is required")
		return
	}
	expiresIn, devCode, err := handler.service.RequestPasswordReset(ctx.Request.Context(), request.Email)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	data := map[string]any{
		"message":    "Verification code sent successfully",
		"expires_in": expiresIn,
	}
	if devCode != "" {
		data["dev_code"] = devCode
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, data)
}

func (handler *Handler) resetPasswordWithOTP(ctx *gin.Context) {
	var request resetPasswordWithOTPRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid reset request parameters")
		return
	}
	if err := handler.service.ResetPasswordWithOTP(ctx.Request.Context(), request.Email, request.OTP, request.NewPassword); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Password reset successfully. Please sign in with your new password.",
	})
}

func (handler *Handler) changePassword(ctx *gin.Context) {
	var request changePasswordRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "new password must contain 12 to 128 characters")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	if err := handler.service.ChangePassword(ctx.Request.Context(), claims.UserID, request.CurrentPassword, request.NewPassword); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Password updated successfully",
	})
}

// ParseSignedClaims extracts signed standard JWT claims for tests and diagnostics.
func ParseSignedClaims(token string, secret []byte) (jwt.MapClaims, error) {
	parsed, err := jwt.Parse(token, func(parsed *jwt.Token) (any, error) {
		if parsed.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

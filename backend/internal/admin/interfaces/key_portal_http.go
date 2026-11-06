package interfaces

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminapp "bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	userdomain "bosapi/backend/internal/user/domain"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const keyPortalRole = "key_viewer"

const (
	keyPortalAccessTokenUse  = "key_portal_access"
	keyPortalRefreshTokenUse = "key_portal_refresh"
	keyPortalRefreshCookie   = "key_portal_refresh_token"
	keyPortalAccessTTL       = 30 * time.Minute
	keyPortalRefreshTTL      = 30 * 24 * time.Hour
)

type keyPortalRepository interface {
	FindGatewayKey(context.Context, string, time.Time) (userdomain.APIKey, bool, error)
	FindActiveKeyByID(context.Context, uint64, time.Time) (userdomain.APIKey, bool, error)
	FindActiveKeyIdentityByID(context.Context, uint64, time.Time) (userdomain.APIKey, bool, error)
}

// KeyPortalHandler exposes read-only, key-scoped dashboard data.
type KeyPortalHandler struct {
	keys     keyPortalRepository
	secret   []byte
	usage    *adminapp.AdminUsageService
	ipUsage  *adminapp.AdminUsageService
	errors   *adminapp.AdminErrorService
	security *adminapp.SecurityEventService
}

func NewKeyPortalHandler(keys keyPortalRepository, secret []byte, usage *adminapp.AdminUsageService, errors *adminapp.AdminErrorService, security *adminapp.SecurityEventService) *KeyPortalHandler {
	return &KeyPortalHandler{keys: keys, secret: append([]byte(nil), secret...), usage: usage, ipUsage: usage, errors: errors, security: security}
}

func (handler *KeyPortalHandler) WithIPUsageService(service *adminapp.AdminUsageService) {
	handler.ipUsage = service
}

// WithUsageService shares bounded display snapshots without caching key validation.
func (handler *KeyPortalHandler) WithUsageService(service *adminapp.AdminUsageService) {
	handler.usage = service
}

func (handler *KeyPortalHandler) RegisterPublicRoutes(group *gin.RouterGroup) {
	group.POST("/key-login", handler.login)
	group.POST("/key-refresh", handler.refresh)
	group.POST("/key-logout", handler.logout)
}

func (handler *KeyPortalHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.Use(handler.authenticate())
	group.GET("/info", handler.info)
	group.GET("/usage", handler.listUsage)
	group.GET("/usage/daily", handler.dailyUsage)
	group.GET("/usage/weekly", handler.weeklyUsage)
	group.GET("/usage/monthly", handler.monthlyUsage)
	group.GET("/usage/total", handler.totalUsage)
	group.GET("/ips", handler.listIPs)
	group.GET("/errors", handler.listErrors)
	group.GET("/security-events", handler.listSecurityEvents)
}

func (handler *KeyPortalHandler) info(ctx *gin.Context) {
	key, ok := keyPortalKey(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	key, active, err := handler.keys.FindActiveKeyByID(ctx.Request.Context(), key.ID, time.Now().UTC())
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "usage could not be loaded")
		return
	}
	if !active {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "api key is invalid or inactive")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, key.Redacted())
}

func (handler *KeyPortalHandler) dailyUsage(ctx *gin.Context)  { handler.aggregateUsage(ctx, "daily") }
func (handler *KeyPortalHandler) weeklyUsage(ctx *gin.Context) { handler.aggregateUsage(ctx, "weekly") }
func (handler *KeyPortalHandler) monthlyUsage(ctx *gin.Context) {
	handler.aggregateUsage(ctx, "monthly")
}
func (handler *KeyPortalHandler) totalUsage(ctx *gin.Context) { handler.aggregateUsage(ctx, "total") }

func (handler *KeyPortalHandler) aggregateUsage(ctx *gin.Context, period string) {
	key, _ := keyPortalKey(ctx)
	result, err := handler.usage.AggregateSnapshot(ctx.Request.Context(), domain.AdminUsageFilter{OrganizationID: key.OrganizationID, APIKeyID: key.ID, Refresh: ctx.Query("refresh") == "true"}, period)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"items": result.Items, "snapshot": result.Snapshot, "incomplete": false})
}

func (handler *KeyPortalHandler) login(ctx *gin.Context) {
	var request struct {
		APIKey string `json:"api_key" binding:"required,min=8,max=4096"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil || strings.ContainsAny(request.APIKey, "\r\n") {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "api key is invalid")
		return
	}
	key, ok, err := handler.keys.FindGatewayKey(ctx.Request.Context(), request.APIKey, time.Now().UTC())
	if err != nil || !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "api key is invalid or inactive")
		return
	}
	now := time.Now().UTC()
	accessToken, refreshToken, err := handler.issueSession(key, now)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	handler.setRefreshCookie(ctx, refreshToken, int(keyPortalRefreshTTL.Seconds()))
	ctx.Request = ctx.Request.WithContext(httpapi.WithLogMetadata(ctx.Request.Context(), httpapi.LogMetadata{OrganizationID: key.OrganizationID, APIKeyMasked: httpapi.MaskAPIKey(key.KeyPrefix)}))
	httpapi.WriteSuccess(ctx, http.StatusOK, handler.sessionResponse(accessToken, key))
}

func (handler *KeyPortalHandler) refresh(ctx *gin.Context) {
	rawToken, err := ctx.Cookie(keyPortalRefreshCookie)
	if err != nil || rawToken == "" {
		handler.clearRefreshCookie(ctx)
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "key session is invalid or expired")
		return
	}
	claims, ok := handler.parseSessionToken(rawToken, keyPortalRefreshTokenUse)
	if !ok {
		handler.clearRefreshCookie(ctx)
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "key session is invalid or expired")
		return
	}
	id, idOK := uintClaim(claims["api_key_id"])
	if !idOK {
		handler.clearRefreshCookie(ctx)
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "key session is invalid or expired")
		return
	}
	key, active, lookupErr := handler.keys.FindActiveKeyByID(ctx.Request.Context(), id, time.Now().UTC())
	if lookupErr != nil || !active || !handler.refreshSessionMatchesKey(claims, key) {
		handler.clearRefreshCookie(ctx)
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "api key is invalid or inactive")
		return
	}
	now := time.Now().UTC()
	accessToken, refreshToken, issueErr := handler.issueSession(key, now)
	if issueErr != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	handler.setRefreshCookie(ctx, refreshToken, int(keyPortalRefreshTTL.Seconds()))
	ctx.Request = ctx.Request.WithContext(httpapi.WithLogMetadata(ctx.Request.Context(), httpapi.LogMetadata{OrganizationID: key.OrganizationID, APIKeyMasked: httpapi.MaskAPIKey(key.KeyPrefix)}))
	httpapi.WriteSuccess(ctx, http.StatusOK, handler.sessionResponse(accessToken, key))
}

func (handler *KeyPortalHandler) logout(ctx *gin.Context) {
	handler.clearRefreshCookie(ctx)
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"ok": true})
}

func (handler *KeyPortalHandler) issueSession(key userdomain.APIKey, now time.Time) (string, string, error) {
	accessToken, err := handler.signSessionToken(key, now, keyPortalAccessTTL, keyPortalAccessTokenUse)
	if err != nil {
		return "", "", err
	}
	refreshToken, err := handler.signSessionToken(key, now, keyPortalRefreshTTL, keyPortalRefreshTokenUse)
	if err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

func (handler *KeyPortalHandler) signSessionToken(key userdomain.APIKey, now time.Time, ttl time.Duration, tokenUse string) (string, error) {
	jtiBytes := make([]byte, 24)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", err
	}
	claims := jwt.MapClaims{
		"sub": key.ID, "api_key_id": key.ID, "organization_id": key.OrganizationID,
		"role": keyPortalRole, "token_use": tokenUse, "jti": hex.EncodeToString(jtiBytes),
		"key_version": handler.keyVersion(key), "iat": now.Unix(), "exp": now.Add(ttl).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(handler.secret)
}

func (handler *KeyPortalHandler) keyVersion(key userdomain.APIKey) string {
	mac := hmac.New(sha256.New, handler.secret)
	_, _ = mac.Write([]byte(key.KeyHash))
	return hex.EncodeToString(mac.Sum(nil))
}

func (handler *KeyPortalHandler) refreshSessionMatchesKey(claims jwt.MapClaims, key userdomain.APIKey) bool {
	version, ok := claims["key_version"].(string)
	if !ok {
		return false
	}
	expected := handler.keyVersion(key)
	return subtle.ConstantTimeCompare([]byte(version), []byte(expected)) == 1
}

func (handler *KeyPortalHandler) parseSessionToken(rawToken, tokenUse string) (jwt.MapClaims, bool) {
	token, err := jwt.Parse(rawToken, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return handler.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || token == nil || !token.Valid {
		return nil, false
	}
	claims, valid := token.Claims.(jwt.MapClaims)
	role, roleOK := claims["role"].(string)
	use, useOK := claims["token_use"].(string)
	return claims, valid && roleOK && role == keyPortalRole && useOK && use == tokenUse
}

func (handler *KeyPortalHandler) sessionResponse(accessToken string, key userdomain.APIKey) gin.H {
	redacted := key.Redacted()
	identity := gin.H{
		"api_key_id": key.ID, "organization_id": key.OrganizationID, "name": key.Name, "key_prefix": key.KeyPrefix, "role": keyPortalRole,
		"quota_tokens": redacted.QuotaTokens, "quota_amount": redacted.QuotaAmount,
		"daily_quota_tokens": redacted.DailyQuotaTokens, "daily_quota_amount": redacted.DailyQuotaAmount,
		"weekly_quota_tokens": redacted.WeeklyQuotaTokens, "weekly_quota_amount": redacted.WeeklyQuotaAmount,
		"monthly_quota_tokens": redacted.MonthlyQuotaTokens, "monthly_quota_amount": redacted.MonthlyQuotaAmount,
		"quota_used_tokens": redacted.QuotaUsedTokens, "quota_used_amount": redacted.QuotaUsedAmount,
		"daily_used_tokens": redacted.DailyUsedTokens, "daily_used_amount": redacted.DailyUsedAmount,
		"weekly_used_tokens": redacted.WeeklyUsedTokens, "weekly_used_amount": redacted.WeeklyUsedAmount,
		"monthly_used_tokens": redacted.MonthlyUsedTokens, "monthly_used_amount": redacted.MonthlyUsedAmount,
	}
	return gin.H{"access_token": accessToken, "token_type": "Bearer", "expires_in": int(keyPortalAccessTTL.Seconds()), "key": identity}
}

func (handler *KeyPortalHandler) setRefreshCookie(ctx *gin.Context, value string, maxAge int) {
	ctx.SetSameSite(http.SameSiteStrictMode)
	ctx.SetCookie(keyPortalRefreshCookie, value, maxAge, "/api/v1/auth", "", keyPortalSecureCookie(ctx), true)
}

func (handler *KeyPortalHandler) clearRefreshCookie(ctx *gin.Context) {
	handler.setRefreshCookie(ctx, "", -1)
}

func keyPortalSecureCookie(ctx *gin.Context) bool {
	return ctx.Request.TLS != nil || strings.EqualFold(ctx.GetHeader("X-Forwarded-Proto"), "https")
}

func (handler *KeyPortalHandler) authenticate() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		authorization := strings.Fields(ctx.GetHeader("Authorization"))
		if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
			return
		}
		token, err := jwt.Parse(authorization[1], func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrTokenSignatureInvalid
			}
			return handler.secret, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
		if err != nil || token == nil || !token.Valid {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "session is invalid or expired")
			return
		}
		claims, valid := token.Claims.(jwt.MapClaims)
		id, idOK := uintClaim(claims["api_key_id"])
		role, roleOK := claims["role"].(string)
		tokenUse, tokenUseOK := claims["token_use"].(string)
		if !valid || !idOK || !roleOK || role != keyPortalRole || (tokenUseOK && tokenUse != keyPortalAccessTokenUse) {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "session is invalid or expired")
			return
		}
		key, ok, err := handler.keys.FindActiveKeyIdentityByID(ctx.Request.Context(), id, time.Now().UTC())
		if err != nil || !ok {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "api key is invalid or inactive")
			return
		}
		if _, versioned := claims["key_version"]; versioned && !handler.refreshSessionMatchesKey(claims, key) {
			httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "session is invalid or expired")
			return
		}
		ctx.Set("user", gin.H{"role": keyPortalRole, "api_key_id": key.ID, "organization_id": key.OrganizationID})
		ctx.Set("key_portal_key", key)
		ctx.Request = ctx.Request.WithContext(httpapi.WithLogMetadata(ctx.Request.Context(), httpapi.LogMetadata{OrganizationID: key.OrganizationID, APIKeyMasked: httpapi.MaskAPIKey(key.KeyPrefix)}))
		ctx.Next()
	}
}

func uintClaim(value any) (uint64, bool) {
	switch number := value.(type) {
	case float64:
		if number > 0 && number == float64(uint64(number)) {
			return uint64(number), true
		}
	case json.Number:
		parsed, err := strconv.ParseUint(number.String(), 10, 64)
		return parsed, err == nil && parsed > 0
	}
	return 0, false
}

func keyPortalKey(ctx *gin.Context) (userdomain.APIKey, bool) {
	key, ok := ctx.Get("key_portal_key")
	if !ok {
		return userdomain.APIKey{}, false
	}
	value, ok := key.(userdomain.APIKey)
	return value, ok
}

func (handler *KeyPortalHandler) listUsage(ctx *gin.Context) {
	key, _ := keyPortalKey(ctx)
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	clientIP := ctx.Query("client_ip")
	if clientIP != "" && net.ParseIP(clientIP) == nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "client_ip is invalid")
		return
	}
	items, total, err := handler.usage.List(ctx.Request.Context(), domain.AdminUsageFilter{OrganizationID: key.OrganizationID, APIKeyID: key.ID, ClientIP: clientIP}, page, size)
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, 200, gin.H{"items": items, "page": page, "page_size": size, "total": total})
}

func (handler *KeyPortalHandler) listIPs(ctx *gin.Context) {
	key, _ := keyPortalKey(ctx)
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	minutes, valid := ipUsageWindowMinutes(ctx)
	if !valid {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "IP window must be 30, 60, 360, 720 or 1440 minutes")
		return
	}
	result, err := handler.ipUsage.GroupByIPSnapshot(ctx.Request.Context(), domain.AdminUsageFilter{OrganizationID: key.OrganizationID, APIKeyID: key.ID, IPWindowMinutes: minutes, SnapshotID: ctx.Query("snapshot_id")}, page, size)
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, 200, gin.H{"items": result.Items, "page": page, "page_size": size, "total": result.Total, "snapshot": result.Snapshot})
}

func (handler *KeyPortalHandler) listErrors(ctx *gin.Context) {
	key, _ := keyPortalKey(ctx)
	limit, err := strconv.Atoi(ctx.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "limit is invalid")
		return
	}
	filter := domain.AdminErrorFilter{OrganizationID: key.OrganizationID, APIKeyID: key.ID}
	if cursor := ctx.Query("cursor"); cursor != "" {
		filter.Before, filter.BeforeID, err = decodeErrorCursor(cursor)
		if err != nil {
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "cursor is invalid")
			return
		}
	}
	items, more, err := handler.errors.List(ctx.Request.Context(), filter, limit)
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	next := ""
	if more && len(items) > 0 {
		last := items[len(items)-1]
		next = encodeErrorCursor(last.Time, last.ID)
	}
	httpapi.WriteSuccess(ctx, 200, gin.H{"items": items, "next_cursor": next})
}

func (handler *KeyPortalHandler) listSecurityEvents(ctx *gin.Context) {
	key, _ := keyPortalKey(ctx)
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	clientIP := ctx.Query("client_ip")
	if clientIP != "" && net.ParseIP(clientIP) == nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "client_ip is invalid")
		return
	}
	items, total, err := handler.security.List(ctx.Request.Context(), domain.SecurityEventFilter{OrganizationID: key.OrganizationID, APIKeyID: key.ID, ClientIP: clientIP}, page, size)
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, 200, gin.H{"items": items, "page": page, "page_size": size, "total": total})
}

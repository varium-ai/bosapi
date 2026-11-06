package interfaces

import (
	"errors"
	"net/http"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SuperAdminHandler exposes administrator creation with optional email verification.
type SuperAdminHandler struct {
	service *application.SuperAdminService
}

// NewSuperAdminHandler constructs the super-admin handler.
func NewSuperAdminHandler(service *application.SuperAdminService) *SuperAdminHandler {
	return &SuperAdminHandler{service: service}
}

// RegisterRoutes installs the super-admin OTP and administrator creation endpoints.
func (handler *SuperAdminHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/super-admins/otp", handler.startOTP)
	group.POST("/super-admins", handler.create)
}

func (handler *SuperAdminHandler) startOTP(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request struct {
		Email string `json:"email" binding:"required,email,max=320"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if err := handler.service.StartOTP(ctx.Request.Context(), claims.UserID, request.Email); err != nil {
		if errors.Is(err, application.ErrOTPDeliveryUnavailable) {
			httpapi.WriteError(ctx, 503, errorcode.ProviderUnavailable, "otp delivery is unavailable")
			return
		}
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusAccepted, map[string]any{"ok": true, "expires_in": 300})
}

func (handler *SuperAdminHandler) create(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request struct {
		Email          string `json:"email" binding:"required,email,max=320"`
		Password       string `json:"password" binding:"required,min=12,max=128"`
		OTP            string `json:"otp" binding:"omitempty,len=6"`
		OrganizationID uint64 `json:"organization_id"`
		Role           string `json:"role" binding:"required,oneof=org_admin super_admin"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	input := domain.SuperAdminInput{Email: request.Email, Password: request.Password, OTP: request.OTP, OrganizationID: request.OrganizationID, Role: request.Role}
	if err := handler.service.CreateAdmin(ctx.Request.Context(), claims.UserID, input); err != nil {
		switch {
		case errors.Is(err, application.ErrInvalidOTP):
			httpapi.WriteError(ctx, 400, errorcode.InvalidOTP, "otp is invalid or expired")
		case errors.Is(err, application.ErrInvalidSuperAdminRequest):
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "administrator request is invalid")
		case errors.Is(err, gorm.ErrRecordNotFound):
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "organization was not found")
		case errors.Is(err, gorm.ErrDuplicatedKey):
			httpapi.WriteError(ctx, 409, errorcode.Conflict, "administrator already exists")
		default:
			httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		}
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, map[string]bool{"ok": true})
}

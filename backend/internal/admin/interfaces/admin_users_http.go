package interfaces

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"github.com/gin-gonic/gin"
)

// AdminUserHandler exposes safe administrative user listing.
type AdminUserHandler struct{ service *application.AdminUserService }

// NewAdminUserHandler constructs the administrative user handler.
func NewAdminUserHandler(service *application.AdminUserService) *AdminUserHandler {
	return &AdminUserHandler{service: service}
}

// RegisterRoutes installs privileged admin user endpoints.
func (handler *AdminUserHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/admin-users", handler.list)
	group.POST("/admin-users/:id/reset-password", handler.resetPassword)
	group.POST("/admin-users/:id/reset-mfa", handler.resetMFA)
	group.POST("/admin-users/:id/unlock-login", handler.unlockLogin)
	group.POST("/admin-users/:id/role", handler.updateRole)
	group.PUT("/admin-users/:id/role", handler.updateRole)
	group.POST("/admin-users/:id/status", handler.updateStatus)
	group.PUT("/admin-users/:id/status", handler.updateStatus)
	group.POST("/admin-users/:id/organization", handler.updateOrganization)
	group.PUT("/admin-users/:id/organization", handler.updateOrganization)
	group.GET("/admin-users/:id/login-records", handler.loginRecords)
}

func (handler *AdminUserHandler) updateOrganization(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, domain.ErrAdminUserForbidden.Error())
		return
	}
	userID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid user id")
		return
	}
	var request struct {
		OrganizationID uint64 `json:"organization_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "organization_id must be a positive integer")
		return
	}
	if err := handler.service.UpdateOrganization(ctx.Request.Context(), claims.Role, claims.UserID, userID, request.OrganizationID); err != nil {
		switch {
		case errors.Is(err, domain.ErrAdminUserForbidden):
			httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, domain.ErrAdminUserForbidden.Error())
		case errors.Is(err, domain.ErrInvalidUserOrganization):
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "cannot change your own organization or use an invalid identity")
		case errors.Is(err, domain.ErrAdminUserNotFound):
			httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, domain.ErrAdminUserNotFound.Error())
		case errors.Is(err, domain.ErrTargetOrganizationUnavailable):
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, domain.ErrTargetOrganizationUnavailable.Error())
		default:
			logging.Printf(ctx.Request.Context(), "ERROR", "admin.user.organization_update_failed", "actor_id=%d user_id=%d organization_id=%d err=%v", claims.UserID, userID, request.OrganizationID, err)
			httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "user organization could not be updated")
		}
		return
	}
	logging.Printf(ctx.Request.Context(), "INFO", "admin.user.organization_updated", "actor_id=%d user_id=%d organization_id=%d", claims.UserID, userID, request.OrganizationID)
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"ok": true, "organization_id": request.OrganizationID})
}

func (handler *AdminUserHandler) loginRecords(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok || claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "only super administrators can view login records")
		return
	}
	userID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "user id is invalid")
		return
	}
	page, size, valid := httpapi.ParsePagination(ctx)
	if !valid {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	items, total, err := handler.service.ListLoginRecords(ctx.Request.Context(), claims.Role, userID, page, size)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "login records could not be loaded")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
}

type resetUserPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=12,max=128"`
}

func (handler *AdminUserHandler) list(ctx *gin.Context) {
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if raw := ctx.Query("organization_id"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || parsed == 0 {
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "organization_id is invalid")
			return
		}
		organizationID = parsed
	}
	items, total, err := handler.service.List(ctx.Request.Context(), domain.AdminUserFilter{OrganizationID: organizationID, Role: ctx.Query("role"), Status: ctx.Query("status"), Search: ctx.Query("search")}, page, size, claims.Role)
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
}

func (handler *AdminUserHandler) resetPassword(ctx *gin.Context) {
	idParam := ctx.Param("id")
	userID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid user id")
		return
	}
	var req resetUserPasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "new password must contain between 12 and 128 characters")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	if err := handler.service.ResetPassword(ctx.Request.Context(), claims.Role, userID, req.NewPassword); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "User password reset successfully",
	})
}

func (handler *AdminUserHandler) resetMFA(ctx *gin.Context) {
	idParam := ctx.Param("id")
	userID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid user id")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	if err := handler.service.ResetMFA(ctx.Request.Context(), claims.Role, userID); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "User two-factor authentication reset successfully",
	})
}

func (handler *AdminUserHandler) unlockLogin(ctx *gin.Context) {
	userID, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid user id")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	if err := handler.service.UnlockLogin(ctx.Request.Context(), claims.Role, userID); err != nil {
		if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "only super administrators") {
			httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, err.Error())
			return
		}
		if strings.Contains(err.Error(), "user not found") {
			httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, err.Error())
			return
		}
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "login lock could not be removed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "User login lock removed successfully",
	})
}

type updateUserRoleRequest struct {
	Role           string `json:"role" binding:"required,oneof=super_admin org_admin"`
	OrganizationID uint64 `json:"organization_id"`
}

func (handler *AdminUserHandler) updateRole(ctx *gin.Context) {
	idParam := ctx.Param("id")
	userID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid user id")
		return
	}
	var req updateUserRoleRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "valid role is required (super_admin, org_admin)")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	if err := handler.service.UpdateRole(ctx.Request.Context(), claims.Role, claims.UserID, userID, req.Role, req.OrganizationID); err != nil {
		if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "only super administrators") {
			httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, err.Error())
			return
		}
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "User role updated successfully",
	})
}

type updateUserStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled"`
}

func (handler *AdminUserHandler) updateStatus(ctx *gin.Context) {
	idParam := ctx.Param("id")
	userID, err := strconv.ParseUint(idParam, 10, 64)
	if err != nil || userID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid user id")
		return
	}
	var req updateUserStatusRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "valid status is required (active, disabled)")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication required")
		return
	}
	if err := handler.service.UpdateStatus(ctx.Request.Context(), claims.Role, claims.UserID, userID, req.Status); err != nil {
		if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "only super administrators") {
			httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, err.Error())
			return
		}
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, err.Error())
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "User status updated successfully",
	})
}

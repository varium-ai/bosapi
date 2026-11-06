package interfaces

import (
	"bosapi/backend/internal/admin/application"
	"errors"
	"net/http"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

// These routes manage the built-in tool inspection mode.
func (handler *SystemSettingHandler) getToolSecurityPolicy(ctx *gin.Context) {
	actor, ok := securityPolicyActor(ctx)
	if !ok {
		return
	}
	if actor.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "global tool security policy requires a super administrator")
		return
	}
	policy, err := handler.service.GetToolSecurityPolicy(ctx.Request.Context())
	if err != nil {
		writeSecurityPolicyError(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, policy)
}

func (handler *SystemSettingHandler) updateToolSecurityPolicy(ctx *gin.Context) {
	actor, ok := securityPolicyActor(ctx)
	if !ok {
		return
	}
	var policy domain.ToolSecurityPolicy
	if err := ctx.ShouldBindJSON(&policy); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "mode must be observe or block")
		return
	}
	if err := handler.service.UpdateToolSecurityPolicy(ctx.Request.Context(), policy, actor); err != nil {
		writeSecurityPolicyError(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, policy)
}

func securityPolicyActor(ctx *gin.Context) (application.SecurityPolicyActor, bool) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return application.SecurityPolicyActor{}, false
	}
	return application.SecurityPolicyActor{UserID: claims.UserID, OrganizationID: claims.OrganizationID, Role: claims.Role}, true
}
func writeSecurityPolicyError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrSecurityPolicyForbidden):
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "security policy access is forbidden")
	case errors.Is(err, domain.ErrInvalidSecurityPolicy):
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "security policy is invalid")
	default:
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
	}
}

package interfaces

import (
	"net/http"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

// These routes share the security-rules page but manage the global inspection
// pipeline. observe and block preserve existing behavior; disabled skips it.
func (handler *SystemSettingHandler) getSecurityInspectionPolicy(ctx *gin.Context) {
	actor, ok := securityRuleActor(ctx)
	if !ok {
		return
	}
	if actor.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "global security inspection policy requires a super administrator")
		return
	}
	policy, err := handler.service.GetSecurityInspectionPolicy(ctx.Request.Context())
	if err != nil {
		writeSecurityRuleError(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, policy)
}

func (handler *SystemSettingHandler) updateSecurityInspectionPolicy(ctx *gin.Context) {
	actor, ok := securityRuleActor(ctx)
	if !ok {
		return
	}
	var policy domain.SecurityInspectionPolicy
	if err := ctx.ShouldBindJSON(&policy); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "mode must be observe, block, or disabled")
		return
	}
	if err := handler.service.UpdateSecurityInspectionPolicy(ctx.Request.Context(), policy, actor); err != nil {
		writeSecurityRuleError(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, policy)
}

package interfaces

import (
	"net/http"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

func (handler *ProviderHandler) startOAuth(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	actor, ok := currentProviderActor(ctx)
	if !ok {
		return
	}
	authorization, err := handler.service.StartOAuth(actor, ctx.Param("platform"))
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, authorization)
}

func (handler *ProviderHandler) completeOAuth(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	actor, ok := currentProviderActor(ctx)
	if !ok {
		return
	}
	var input struct {
		ProviderID uint64                    `json:"provider_id"`
		SessionID  string                    `json:"session_id" binding:"required,max=128"`
		Callback   string                    `json:"callback" binding:"required,max=16384"`
		State      string                    `json:"state" binding:"required,max=256"`
		ProjectID  string                    `json:"project_id" binding:"max=128"`
		Provider   application.ProviderInput `json:"provider"`
	}
	if ctx.ShouldBindJSON(&input) != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "authorization request is invalid")
		return
	}
	if input.ProviderID > 0 && !handler.authorize(ctx, input.ProviderID) {
		return
	}
	provider, err := handler.service.CompleteOAuth(ctx.Request.Context(), actor, ctx.Param("platform"), input.SessionID, input.Callback, input.State, input.ProjectID, input.Provider, input.ProviderID)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, provider)
}

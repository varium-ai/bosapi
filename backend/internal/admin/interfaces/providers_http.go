package interfaces

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/subscription"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ProviderHandler exposes provider account administration endpoints.
type ProviderHandler struct {
	service *application.ProviderService
	models  *application.ProviderModelService
}

// NewProviderHandler constructs a provider handler.
func NewProviderHandler(service *application.ProviderService, models ...*application.ProviderModelService) *ProviderHandler {
	handler := &ProviderHandler{service: service}
	if len(models) > 0 {
		handler.models = models[0]
	}
	return handler
}

// RegisterRoutes installs provider administration routes under the admin group.
func (handler *ProviderHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/providers", handler.list)
	group.POST("/providers", handler.create)
	group.POST("/providers/oauth/:platform/start", handler.startOAuth)
	group.POST("/providers/oauth/:platform/complete", handler.completeOAuth)
	group.GET("/providers/:id", handler.find)
	group.PUT("/providers/:id", handler.update)
	group.PATCH("/providers/:id/status", handler.setStatus)
	group.DELETE("/providers/:id", handler.delete)
	group.POST("/providers/:id/disable", handler.disable)
	group.POST("/providers/:id/test", handler.test)
	group.POST("/providers/:id/discover-models", handler.discover)
	group.GET("/providers/:id/models", handler.listModels)
	group.PUT("/providers/:id/models/:model_id", handler.updateModel)
}

func (handler *ProviderHandler) discover(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	items, err := handler.service.Discover(ctx.Request.Context(), id)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"provider_id": id, "items": items})
}

func (handler *ProviderHandler) listModels(ctx *gin.Context) {
	if handler.models == nil {
		httpapi.WriteError(ctx, 503, errorcode.ProviderUnavailable, "provider model catalog is unavailable")
		return
	}
	providerID, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, providerID) {
		return
	}
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	items, total, err := handler.models.List(ctx.Request.Context(), providerID, ctx.Query("status"), page, size)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"provider_id": providerID, "items": items, "page": page, "page_size": size, "total": total})
}

func (handler *ProviderHandler) updateModel(ctx *gin.Context) {
	if handler.models == nil {
		httpapi.WriteError(ctx, 503, errorcode.ProviderUnavailable, "provider model catalog is unavailable")
		return
	}
	providerID, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "provider id is invalid")
		return
	}
	if !handler.authorize(ctx, providerID) {
		return
	}
	modelID, err := strconv.ParseUint(ctx.Param("model_id"), 10, 64)
	if err != nil || modelID == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "model id is invalid")
		return
	}
	var input application.UpdateModelPricingInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	model, err := handler.models.UpdatePricing(ctx.Request.Context(), providerID, modelID, input)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, model)
}

func (handler *ProviderHandler) list(ctx *gin.Context) {
	actor, ok := currentProviderActor(ctx)
	if !ok {
		return
	}
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	organizationID, err := queryID(ctx, "organization_id")
	if err != nil || actor.Role == "org_admin" && organizationID != 0 && organizationID != actor.OrganizationID {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "organization scope is invalid")
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), domain.ProviderFilter{OrganizationID: organizationID, Platform: ctx.Query("platform"), Status: ctx.Query("status"), Search: ctx.Query("search"), Actor: actor}, page, size)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
}

func (handler *ProviderHandler) create(ctx *gin.Context) {
	actor, ok := currentProviderActor(ctx)
	if !ok {
		return
	}
	var input application.ProviderInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	input.CreatedByUserID = actor.UserID
	if actor.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "only super administrators can create providers")
		return
	}
	provider, err := handler.service.Create(ctx.Request.Context(), input)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, provider)
}

func (handler *ProviderHandler) find(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	provider, err := handler.service.Find(ctx.Request.Context(), id)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, provider)
}

func (handler *ProviderHandler) update(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	var input application.ProviderInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	provider, err := handler.service.Update(ctx.Request.Context(), id, input)
	if err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, provider)
}

func (handler *ProviderHandler) disable(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	if err := handler.service.Disable(ctx.Request.Context(), id); err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, map[string]bool{"ok": true})
}

func (handler *ProviderHandler) delete(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	if err := handler.service.Delete(ctx.Request.Context(), id); err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, map[string]bool{"ok": true})
}

func (handler *ProviderHandler) setStatus(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	var input struct {
		Status string `json:"status" binding:"required,oneof=active disabled"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "status is invalid")
		return
	}
	if err := handler.service.SetStatus(ctx.Request.Context(), id, input.Status); err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, map[string]bool{"ok": true})
}

func (handler *ProviderHandler) test(ctx *gin.Context) {
	id, ok := providerID(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if !handler.authorize(ctx, id) {
		return
	}
	started := time.Now()
	if err := handler.service.Test(ctx.Request.Context(), id); err != nil {
		providerFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, map[string]any{"success": true, "latency_ms": time.Since(started).Milliseconds()})
}

func providerID(ctx *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

func currentProviderActor(ctx *gin.Context) (domain.ProviderActor, bool) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return domain.ProviderActor{}, false
	}
	if claims.Role != "super_admin" && claims.Role != "org_admin" || claims.UserID == 0 || claims.Role == "org_admin" && claims.OrganizationID == 0 {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return domain.ProviderActor{}, false
	}
	return domain.ProviderActor{UserID: claims.UserID, OrganizationID: claims.OrganizationID, Role: claims.Role}, true
}

func (handler *ProviderHandler) authorize(ctx *gin.Context, id uint64) bool {
	actor, ok := currentProviderActor(ctx)
	if !ok {
		return false
	}
	if err := handler.service.Authorize(ctx.Request.Context(), id, actor); err != nil {
		providerFailure(ctx, err)
		return false
	}
	// Public channels assigned to a tenant are read-only for its administrators.
	if actor.Role == "org_admin" && ctx.Request.Method != http.MethodGet {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "assigned providers are read-only")
		return false
	}
	return true
}

func providerFailure(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrProviderAuthentication):
		httpapi.WriteError(ctx, 502, errorcode.ProviderAuthFailed, "upstream rejected authentication")
	case errors.Is(err, application.ErrProviderAuthorization):
		httpapi.WriteError(ctx, 502, errorcode.ProviderAccessDenied, "upstream denied access")
	case errors.Is(err, application.ErrProviderRateLimited):
		httpapi.WriteError(ctx, 502, errorcode.ProviderRateLimited, "upstream rate limited")
	case errors.Is(err, application.ErrProviderTimeout):
		httpapi.WriteError(ctx, 504, errorcode.UpstreamTimeout, "upstream timed out")
	case errors.Is(err, application.ErrProviderProtocol):
		httpapi.WriteError(ctx, 502, errorcode.UnsupportedConversion, "check upstream protocol and model endpoint")
	case errors.Is(err, subscription.ErrClientConfig):
		httpapi.WriteError(ctx, http.StatusServiceUnavailable, errorcode.ProviderUnavailable, err.Error())
	case errors.Is(err, subscription.ErrSession):
		httpapi.WriteError(ctx, 400, errorcode.InvalidOAuthSession, "authorization session is invalid or expired; start sign in again")
	case errors.Is(err, subscription.ErrProject):
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, err.Error())
	case errors.Is(err, subscription.ErrToken):
		httpapi.WriteError(ctx, 502, errorcode.OAuthExchangeFailed, "official account authorization failed; start sign in again")
	case errors.Is(err, application.ErrProviderDiscovery):
		httpapi.WriteError(ctx, http.StatusBadGateway, errorcode.ProviderDiscoveryFailed, "provider model discovery failed")
	case errors.Is(err, application.ErrInvalidProvider), errors.Is(err, gorm.ErrInvalidData):
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "provider is invalid")
	case errors.Is(err, gorm.ErrRecordNotFound):
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
	default:
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
	}
}

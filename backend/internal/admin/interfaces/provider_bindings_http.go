package interfaces

import (
	"errors"
	"net/http"
	"strconv"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ProviderBindingHandler handles organization provider binding updates.
type ProviderBindingHandler struct {
	service *application.ProviderBindingService
}

// NewProviderBindingHandler constructs an organization provider binding handler.
func NewProviderBindingHandler(service *application.ProviderBindingService) *ProviderBindingHandler {
	return &ProviderBindingHandler{service: service}
}

// RegisterRoutes installs organization provider binding routes.
func (handler *ProviderBindingHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/organizations/:id/provider-bindings", handler.list)
	group.PUT("/organizations/:id/provider-bindings", handler.replace)
}

func (handler *ProviderBindingHandler) list(ctx *gin.Context) {
	id, ok := organizationID(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	items, err := handler.service.List(ctx.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "organization was not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "bindings could not be loaded")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "bindings": items})
}

func (handler *ProviderBindingHandler) replace(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	var request struct {
		Bindings []domain.ProviderBinding `json:"bindings"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if err := handler.service.Replace(ctx.Request.Context(), id, request.Bindings); err != nil {
		switch {
		case errors.Is(err, application.ErrInvalidProviderBindings):
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "provider bindings are invalid")
		case errors.Is(err, gorm.ErrRecordNotFound):
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "organization or provider was not found")
		default:
			httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		}
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]bool{"ok": true})
}

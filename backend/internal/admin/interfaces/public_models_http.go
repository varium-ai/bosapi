package interfaces

import (
	"net/http"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

// PublicModelHandler serves the anonymous portal model catalog.
type PublicModelHandler struct {
	service *application.PublicModelService
}

// NewPublicModelHandler constructs the public model handler.
func NewPublicModelHandler(service *application.PublicModelService) *PublicModelHandler {
	return &PublicModelHandler{service: service}
}

// RegisterRoutes installs the public model list endpoint.
func (handler *PublicModelHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/models", handler.list)
}

func (handler *PublicModelHandler) list(ctx *gin.Context) {
	items, err := handler.service.List(ctx.Request.Context())
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items})
}

package interfaces

import (
	"net/http"
	"strings"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

// ModelMetadataHandler exposes model capabilities knowledge base administration endpoints.
type ModelMetadataHandler struct {
	service *application.ModelMetadataService
}

// NewModelMetadataHandler constructs a model metadata HTTP handler.
func NewModelMetadataHandler(service *application.ModelMetadataService) *ModelMetadataHandler {
	return &ModelMetadataHandler{service: service}
}

// RegisterRoutes registers endpoints under the given router group.
func (handler *ModelMetadataHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/model-metadata", handler.list)
	group.POST("/model-metadata/sync", handler.sync)
	group.PUT("/model-metadata", handler.upsert)
}

func (handler *ModelMetadataHandler) list(ctx *gin.Context) {
	search := ctx.Query("search")
	page, pageSize, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "pagination is invalid")
		return
	}

	items, total, err := handler.service.List(ctx.Request.Context(), search, page, pageSize)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "failed to list model metadata")
		return
	}

	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (handler *ModelMetadataHandler) sync(ctx *gin.Context) {
	source := strings.ToLower(strings.TrimSpace(ctx.DefaultQuery("source", "openrouter")))
	var count int
	var err error

	switch source {
	case "litellm":
		count, err = handler.service.SyncFromLiteLLM(ctx.Request.Context())
	default:
		source = "openrouter"
		count, err = handler.service.SyncFromOpenRouter(ctx.Request.Context())
	}

	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadGateway, errorcode.SyncFailed, err.Error())
		return
	}

	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"source": source,
		"count":  count,
	})
}

type upsertModelMetadataRequest struct {
	ModelID           string   `json:"model_id" binding:"required"`
	DisplayName       string   `json:"display_name"`
	InputModalities   []string `json:"input_modalities"`
	OutputModalities  []string `json:"output_modalities"`
	SupportsVision    bool     `json:"supports_vision"`
	SupportsVideo     bool     `json:"supports_video"`
	SupportsAudio     bool     `json:"supports_audio"`
	SupportsTools     bool     `json:"supports_tools"`
	SupportsReasoning bool     `json:"supports_reasoning"`
	ContextWindow     int      `json:"context_window"`
	MaxOutputTokens   int      `json:"max_output_tokens"`
}

func (handler *ModelMetadataHandler) upsert(ctx *gin.Context) {
	var req upsertModelMetadataRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "invalid model metadata payload")
		return
	}

	item := domain.ModelMetadata{
		ModelID:           req.ModelID,
		DisplayName:       req.DisplayName,
		InputModalities:   req.InputModalities,
		OutputModalities:  req.OutputModalities,
		SupportsVision:    req.SupportsVision,
		SupportsVideo:     req.SupportsVideo,
		SupportsAudio:     req.SupportsAudio,
		SupportsTools:     req.SupportsTools,
		SupportsReasoning: req.SupportsReasoning,
		ContextWindow:     req.ContextWindow,
		MaxOutputTokens:   req.MaxOutputTokens,
		Source:            "manual",
	}

	if err := handler.service.Upsert(ctx.Request.Context(), item); err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, err.Error())
		return
	}

	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]bool{"ok": true})
}

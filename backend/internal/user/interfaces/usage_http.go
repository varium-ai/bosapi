package interfaces

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/user/application"
	"bosapi/backend/internal/user/domain"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// UsageService defines owner-scoped usage operations exposed to portal users.
type UsageService interface {
	List(context.Context, domain.UsageFilter, int, int) ([]domain.UsageRecord, int64, error)
	Find(context.Context, domain.UsageFilter, uint64) (domain.UsageRecord, error)
	Summarize(context.Context, domain.UsageFilter) (domain.UsageTotals, error)
	Trend(context.Context, domain.UsageFilter) ([]domain.UsageTrendPoint, error)
	ByModel(context.Context, domain.UsageFilter, int) ([]domain.UsageModelTotal, error)
	DailyByKey(context.Context, domain.UsageFilter, int) ([]domain.UsageDailyPoint, error)
	OwnsKey(context.Context, uint64, uint64) (bool, error)
	ByKeys(context.Context, domain.UsageFilter, []uint64) ([]domain.APIKeyUsagePoint, error)
	ListErrors(context.Context, domain.UsageFilter, string, int, int) ([]domain.UsageError, int64, error)
	FindError(context.Context, domain.UsageFilter, uint64) (domain.UsageError, error)
}

// UsageHandler adapts usage queries to the user portal contract.
type UsageHandler struct{ service UsageService }

// NewUsageHandler constructs the user usage HTTP handler.
func NewUsageHandler(service UsageService) *UsageHandler { return &UsageHandler{service: service} }

// RegisterRoutes installs owner-scoped usage list, detail, and summary routes.
func (handler *UsageHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/usage", handler.list)
	group.GET("/usage/:id", handler.get)
	group.GET("/usage/stats", handler.stats)
	group.GET("/usage/dashboard/stats", handler.stats)
	group.GET("/usage/dashboard/trend", handler.trend)
	group.GET("/usage/dashboard/models", handler.models)
	group.GET("/usage/dashboard/snapshot-v2", handler.snapshot)
	group.GET("/user/api-keys/:id/usage/daily", handler.dailyByKey)
	group.POST("/usage/dashboard/api-keys-usage", handler.byKeys)
	group.GET("/usage/errors", handler.errorList)
	group.GET("/usage/errors/:id", handler.errorGet)
}

func (handler *UsageHandler) errorList(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	page, size, valid := httpapi.ParsePagination(ctx)
	if !valid {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	items, total, err := handler.service.ListErrors(ctx.Request.Context(), filter, ctx.Query("error_code"), page, size)
	if errors.Is(err, domain.ErrInvalidUsageFilter) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

func (handler *UsageHandler) errorGet(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	item, err := handler.service.FindError(ctx.Request.Context(), filter, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, item)
}

func (handler *UsageHandler) byKeys(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request struct {
		APIKeyIDs []uint64 `json:"api_key_ids"`
		From      string   `json:"from"`
		To        string   `json:"to"`
		GroupBy   string   `json:"group_by"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil || len(request.APIKeyIDs) == 0 || len(request.APIKeyIDs) > 100 || request.GroupBy != "day" {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	from, to, err := application.NormalizeDateRange(strings.TrimSpace(request.From), strings.TrimSpace(request.To), time.Now())
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "date range is invalid")
		return
	}
	items, err := handler.service.ByKeys(ctx.Request.Context(), domain.UsageFilter{OrganizationID: claims.OrganizationID, From: from, To: to}, request.APIKeyIDs)
	if errors.Is(err, domain.ErrInvalidUsageFilter) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "one or more api keys were not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items      []domain.APIKeyUsagePoint `json:"items"`
		Incomplete bool                      `json:"incomplete"`
	}{items, false})
}

func (handler *UsageHandler) dailyByKey(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	days, err := strconv.Atoi(ctx.DefaultQuery("days", "30"))
	if err != nil || days < 1 || days > 90 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "days must be between 1 and 90")
		return
	}
	owned, err := handler.service.OwnsKey(ctx.Request.Context(), id, claims.OrganizationID)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	if !owned {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	items, err := handler.service.DailyByKey(ctx.Request.Context(), domain.UsageFilter{OrganizationID: claims.OrganizationID, APIKeyID: id}, days)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items []domain.UsageDailyPoint `json:"items"`
	}{items})
}

func (handler *UsageHandler) trend(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	items, err := handler.service.Trend(ctx.Request.Context(), filter)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items      []domain.UsageTrendPoint `json:"items"`
		Incomplete bool                     `json:"incomplete"`
	}{items, false})
}

func (handler *UsageHandler) models(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	items, err := handler.service.ByModel(ctx.Request.Context(), filter, 100)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items []domain.UsageModelTotal `json:"items"`
	}{items})
}

func (handler *UsageHandler) snapshot(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	totals, err := handler.service.Summarize(ctx.Request.Context(), filter)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	models, err := handler.service.ByModel(ctx.Request.Context(), filter, 10)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Totals domain.UsageTotals       `json:"totals"`
		Models []domain.UsageModelTotal `json:"models"`
	}{totals, models})
}

func (handler *UsageHandler) list(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	page, pageSize, valid := httpapi.ParsePagination(ctx)
	if !valid {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), filter, page, pageSize)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, pageResult{Items: items, Page: page, PageSize: pageSize, Total: total})
}

func (handler *UsageHandler) get(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	item, err := handler.service.Find(ctx.Request.Context(), filter, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, item)
}

func (handler *UsageHandler) stats(ctx *gin.Context) {
	filter, ok := usageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	totals, err := handler.service.Summarize(ctx.Request.Context(), filter)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, totals)
}

func usageFilter(ctx *gin.Context) (domain.UsageFilter, bool) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		return domain.UsageFilter{}, false
	}
	keyID, err := optionalID(ctx.Query("api_key_id"))
	if err != nil {
		return domain.UsageFilter{}, false
	}
	from, to, err := application.NormalizeDateRange(ctx.Query("from"), ctx.Query("to"), time.Now())
	if err != nil {
		return domain.UsageFilter{}, false
	}
	return domain.UsageFilter{UserID: claims.UserID, OrganizationID: claims.OrganizationID, APIKeyID: keyID, Model: ctx.Query("model"), From: from, To: to}, true
}

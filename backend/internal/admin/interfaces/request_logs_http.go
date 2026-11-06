package interfaces

import (
	"errors"
	"net/http"
	"strconv"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"github.com/gin-gonic/gin"
)

// RequestLogHandler exposes request-correlated server logs to portal administrators.
type RequestLogHandler struct {
	service *application.RequestLogService
}

// NewRequestLogHandler constructs the request log handler.
func NewRequestLogHandler(service *application.RequestLogService) *RequestLogHandler {
	return &RequestLogHandler{service: service}
}

// RegisterRoutes installs the exact request ID lookup endpoint.
func (handler *RequestLogHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/request-logs", handler.list)
}

func (handler *RequestLogHandler) list(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok || claims.Role != "org_admin" && claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	requestID := ctx.Query("request_id")
	if !httpapi.ValidRequestID(requestID) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request_id is invalid")
		return
	}
	limit := 200
	if value := ctx.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "limit is invalid")
			return
		}
		limit = parsed
	}
	offset := 0
	if value := ctx.Query("offset"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 || parsed > 1_000_000 {
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "offset is invalid")
			return
		}
		offset = parsed
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	}
	items, hasMore, err := handler.service.List(ctx.Request.Context(), domain.RequestLogFilter{RequestID: requestID, OrganizationID: organizationID}, offset, limit)
	if errors.Is(err, application.ErrInvalidRequestLogFilter) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request log query is invalid")
		return
	}
	if err != nil {
		logging.Printf(ctx.Request.Context(), "ERROR", "admin.request_logs.query_failed", "err=%v", err)
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "has_more": hasMore})
}

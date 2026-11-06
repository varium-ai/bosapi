package interfaces

import (
	"context"
	"errors"
	"net/http"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RateLimitHandler exposes persisted rate limit configuration.
type RateLimitHandler struct {
	service       *application.RateLimitService
	runtimeStatus func(context.Context) (bool, string)
}

// NewRateLimitHandler constructs the rate limit handler.
func NewRateLimitHandler(service *application.RateLimitService, runtimeStatus ...func(context.Context) (bool, string)) *RateLimitHandler {
	handler := &RateLimitHandler{service: service}
	if len(runtimeStatus) > 0 {
		handler.runtimeStatus = runtimeStatus[0]
	}
	return handler
}

// RegisterRoutes installs rate limit read and update endpoints.
func (handler *RateLimitHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/rate-limits", handler.read)
	group.PUT("/rate-limits", handler.write)
}

func (handler *RateLimitHandler) read(ctx *gin.Context) {
	organizationID, err := queryID(ctx, "organization_id")
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "organization_id is invalid")
		return
	}
	keyID, err := queryID(ctx, "key_id")
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "key_id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role == "org_admin" && organizationID != 0 && organizationID != claims.OrganizationID {
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
		return
	}
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	}
	configured, err := handler.service.Read(ctx.Request.Context(), organizationID, keyID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		logging.Printf(ctx.Request.Context(), "ERROR", "admin.rate_limits.read_failed", "organization_id=%d key_id=%d err=%v", organizationID, keyID, err)
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	var organization, key any
	if len(configured.Organizations) > 0 {
		organization = configured.Organizations[0]
	}
	if len(configured.Keys) > 0 {
		key = configured.Keys[0]
	}
	global := configured.Global
	if global == nil {
		global = &domain.RateLimitConfig{}
	}
	degraded, runtimeState := true, string(errorcode.CounterBackendUnavailable)
	if handler.runtimeStatus != nil {
		degraded, runtimeState = handler.runtimeStatus(ctx.Request.Context())
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"degraded": degraded, "runtime_state": runtimeState, "global": map[string]any{"qps": global.QPS, "current_qps": nil, "remaining": nil}, "organization": organization, "key": key})
}

func (handler *RateLimitHandler) write(ctx *gin.Context) {
	var request domain.RateLimitUpdate
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role == "org_admin" {
		if request.Global != nil || len(request.Keys) > 0 || len(request.Organizations) == 0 {
			httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
			return
		}
		for _, value := range request.Organizations {
			if value.OrganizationID != claims.OrganizationID {
				httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
				return
			}
		}
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	err := handler.service.Write(ctx.Request.Context(), request, claims.UserID)
	switch {
	case errors.Is(err, application.ErrInvalidRateLimit):
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "rate limit configuration is invalid")
	case errors.Is(err, gorm.ErrRecordNotFound):
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
	case err != nil:
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
	default:
		httpapi.WriteSuccess(ctx, http.StatusOK, map[string]bool{"ok": true})
	}
}

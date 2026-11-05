package interfaces

import (
	"context"
	"errors"
	"net/http"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/usagetime"
	userinfra "bosapi/backend/internal/user/infrastructure"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RateLimitHandler exposes persisted rate limit configuration.
type RateLimitHandler struct {
	service           *application.RateLimitService
	runtimeStatus     func(context.Context) (bool, string)
	cacheInvalidator  func(context.Context, domain.RateLimitUpdate)
	organizationUsage func(context.Context, uint64, time.Time) (userinfra.OrganizationPeriodicUsage, error)
}

// NewRateLimitHandler constructs the rate limit handler.
func NewRateLimitHandler(service *application.RateLimitService, runtimeStatus ...func(context.Context) (bool, string)) *RateLimitHandler {
	handler := &RateLimitHandler{service: service}
	if len(runtimeStatus) > 0 {
		handler.runtimeStatus = runtimeStatus[0]
	}
	return handler
}

// WithQuotaCacheInvalidator refreshes runtime configuration after a successful write.
func (handler *RateLimitHandler) WithQuotaCacheInvalidator(invalidator func(context.Context, domain.RateLimitUpdate)) *RateLimitHandler {
	handler.cacheInvalidator = invalidator
	return handler
}

// WithOrganizationUsage supplies the organization ledger reader or cached snapshot reader.
func (handler *RateLimitHandler) WithOrganizationUsage(reader func(context.Context, uint64, time.Time) (userinfra.OrganizationPeriodicUsage, error)) *RateLimitHandler {
	handler.organizationUsage = reader
	return handler
}

// RegisterRoutes installs rate limit read and update endpoints.
func (handler *RateLimitHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/rate-limits", handler.read)
	group.PUT("/rate-limits", handler.write)
}

func (handler *RateLimitHandler) read(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role != "super_admin" && (claims.Role != "org_admin" || claims.OrganizationID == 0) {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}

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
		config := configured.Organizations[0]
		organization = map[string]any{
			"organization_id": config.OrganizationID,
			"qps":             config.QPS,
			"quotas": map[string]any{
				"daily_tokens":   config.DailyQuotaTokens,
				"weekly_tokens":  config.WeeklyQuotaTokens,
				"monthly_tokens": config.MonthlyQuotaTokens,
				"daily_amount":   config.DailyQuotaAmount,
				"weekly_amount":  config.WeeklyQuotaAmount,
				"monthly_amount": config.MonthlyQuotaAmount,
			},
		}
	}
	if len(configured.Keys) > 0 {
		key = configured.Keys[0]
	}
	global := configured.Global
	if global == nil {
		global = &domain.RateLimitConfig{}
	}
	if organizationMap, ok := organization.(map[string]any); ok {
		now := time.Now()
		day, week, month := usagetime.PeriodStarts(now)
		organizationMap["resets"] = map[string]time.Time{
			"daily":   day.AddDate(0, 0, 1),
			"weekly":  week.AddDate(0, 0, 7),
			"monthly": month.In(usagetime.Location()).AddDate(0, 1, 0).UTC(),
		}
		if handler.organizationUsage == nil {
			httpapi.WriteError(ctx, http.StatusServiceUnavailable, errorcode.QuotaAccountingUnavailable, "organization usage is unavailable")
			return
		}
		usage, err := handler.organizationUsage(ctx.Request.Context(), organizationID, now)
		if err != nil {
			logging.Printf(ctx.Request.Context(), "ERROR", "admin.rate_limits.usage_failed", "organization_id=%d err=%v", organizationID, err)
			httpapi.WriteError(ctx, http.StatusServiceUnavailable, errorcode.QuotaAccountingUnavailable, "organization usage is unavailable")
			return
		}
		organizationMap["usage"] = usage
	}
	degraded, runtimeState := true, string(errorcode.CounterBackendUnavailable)
	if handler.runtimeStatus != nil {
		degraded, runtimeState = handler.runtimeStatus(ctx.Request.Context())
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"degraded": degraded, "runtime_state": runtimeState, "global": map[string]any{"qps": global.QPS, "current_qps": nil, "remaining": nil}, "organization": organization, "key": key})
}

func (handler *RateLimitHandler) write(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	var request domain.RateLimitUpdate
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
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
		if handler.cacheInvalidator != nil {
			handler.cacheInvalidator(ctx.Request.Context(), request)
		}
		httpapi.WriteSuccess(ctx, http.StatusOK, map[string]bool{"ok": true})
	}
}

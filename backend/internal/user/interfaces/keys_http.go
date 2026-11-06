package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/user/application"
	"bosapi/backend/internal/user/domain"
)

// KeyService defines the user-facing key use cases.
type KeyService interface {
	Create(context.Context, uint64, uint64, application.CreateKeyInput) (application.CreatedKey, error)
	List(context.Context, application.KeyFilter, int, int) ([]domain.SafeAPIKey, int64, error)
}

// KeyRepository provides owner-checked key reads and updates to HTTP handlers.
type KeyRepository interface {
	FindOwned(context.Context, uint64, uint64, uint64) (domain.APIKey, error)
	UpdateOwned(context.Context, uint64, uint64, uint64, map[string]any) error
	DeleteOwned(context.Context, uint64, uint64, uint64) error
}

// KeyHandler translates the self-service key contract to application operations.
type KeyHandler struct {
	service    KeyService
	repository KeyRepository
}

// NewKeyHandler constructs a self-service key handler.
func NewKeyHandler(service KeyService, repository KeyRepository) *KeyHandler {
	return &KeyHandler{service: service, repository: repository}
}

// RegisterRoutes installs user-owned key CRUD endpoints.
func (handler *KeyHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/keys", handler.list)
	group.POST("/keys", handler.create)
	group.GET("/keys/:id", handler.get)
	group.PUT("/keys/:id", handler.update)
	group.DELETE("/keys/:id", handler.delete)
}

func (handler *KeyHandler) list(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	page, pageSize, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	status := ctx.Query("status")
	if status != "" && status != "active" && status != "disabled" {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "status is invalid")
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), application.KeyFilter{UserID: claims.UserID, OrganizationID: claims.OrganizationID, Status: status, Search: ctx.Query("search")}, page, pageSize)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, pageResult{Items: items, Page: page, PageSize: pageSize, Total: total})
}

func (handler *KeyHandler) create(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	var input application.CreateKeyInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	created, err := handler.service.Create(ctx.Request.Context(), claims.UserID, claims.OrganizationID, input)
	if errors.Is(err, edition.ErrLimitExceeded) {
		httpapi.WriteError(ctx, http.StatusConflict, errorcode.Conflict, err.Error())
		return
	}
	if errors.Is(err, application.ErrInvalidKey) || errors.Is(err, gorm.ErrInvalidData) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "api key settings are invalid")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, created)
}

func (handler *KeyHandler) get(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	id, valid := pathID(ctx)
	if !valid {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	key, err := handler.repository.FindOwned(ctx.Request.Context(), id, claims.UserID, claims.OrganizationID)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, key.Redacted())
}

func (handler *KeyHandler) update(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	id, valid := pathID(ctx)
	if !valid {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	var request keyUpdate
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	updates, err := request.values()
	if err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if err := handler.repository.UpdateOwned(ctx.Request.Context(), id, claims.UserID, claims.OrganizationID, updates); err != nil {
		if errors.Is(err, gorm.ErrInvalidData) {
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "key access restrictions are invalid")
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
			return
		}
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	key, err := handler.repository.FindOwned(ctx.Request.Context(), id, claims.UserID, claims.OrganizationID)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, key.Redacted())
}

func (handler *KeyHandler) delete(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	id, valid := pathID(ctx)
	if !valid {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	if err := handler.repository.DeleteOwned(ctx.Request.Context(), id, claims.UserID, claims.OrganizationID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
			return
		}
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

type keyUpdate struct {
	OrganizationID     json.RawMessage `json:"organization_id"`
	UserID             json.RawMessage `json:"user_id"`
	AllowedProviderIDs *[]uint64       `json:"allowed_provider_ids"`
	AllowedModels      *[]string       `json:"allowed_models"`
	Name               *string         `json:"name"`
	Status             *string         `json:"status"`
	ExpiresAt          **time.Time     `json:"expires_at"`
	QuotaAmount        **float64       `json:"quota_amount"`
	QuotaTokens        **int64         `json:"quota_tokens"`
	DailyQuotaAmount   **float64       `json:"daily_quota_amount"`
	WeeklyQuotaAmount  **float64       `json:"weekly_quota_amount"`
	MonthlyQuotaAmount **float64       `json:"monthly_quota_amount"`
	DailyQuotaTokens   **int64         `json:"daily_quota_tokens"`
	WeeklyQuotaTokens  **int64         `json:"weekly_quota_tokens"`
	MonthlyQuotaTokens **int64         `json:"monthly_quota_tokens"`
	RateLimit5H        **int64         `json:"rate_limit_5h"`
	RateLimit1D        **int64         `json:"rate_limit_1d"`
	RateLimit7D        **int64         `json:"rate_limit_7d"`
	IPAllowlist        *[]string       `json:"ip_allowlist"`
	IPBlocklist        *[]string       `json:"ip_blocklist"`
}

func (request keyUpdate) values() (map[string]any, error) {
	if len(request.OrganizationID) > 0 || len(request.UserID) > 0 {
		return nil, application.ErrInvalidKey
	}
	var providers []uint64
	var models []string
	if request.AllowedProviderIDs != nil {
		providers = *request.AllowedProviderIDs
	}
	if request.AllowedModels != nil {
		models = *request.AllowedModels
	}
	if err := domain.ValidateKeyAccessPolicy(providers, models); err != nil {
		return nil, application.ErrInvalidKey
	}
	for _, limit := range []**int64{request.RateLimit5H, request.RateLimit1D, request.RateLimit7D} {
		if limit != nil {
			if err := domain.ValidateKeyRateLimits(*limit); err != nil {
				return nil, application.ErrInvalidKey
			}
		}
	}
	updates := make(map[string]any)
	if request.AllowedProviderIDs != nil {
		updates["allowed_provider_ids"] = providers
	}
	if request.AllowedModels != nil {
		updates["allowed_models"] = models
	}
	if request.Name != nil {
		if err := domain.ValidateKeyName(*request.Name); err != nil {
			return nil, application.ErrInvalidKey
		}
		updates["name"] = strings.TrimSpace(*request.Name)
	}
	if request.Status != nil {
		if *request.Status != "active" && *request.Status != "disabled" {
			return nil, application.ErrInvalidKey
		}
		updates["status"] = *request.Status
	}
	if request.ExpiresAt != nil {
		updates["expires_at"] = *request.ExpiresAt
	}
	if request.QuotaAmount != nil {
		if err := domain.ValidateKeyQuotaAmounts(*request.QuotaAmount); err != nil {
			return nil, application.ErrInvalidKey
		}
		updates["quota_amount"] = *request.QuotaAmount
	}
	if request.QuotaTokens != nil {
		if err := domain.ValidateKeyQuotaTokens(*request.QuotaTokens); err != nil {
			return nil, application.ErrInvalidKey
		}
		updates["quota_tokens"] = *request.QuotaTokens
	}
	for field, value := range map[string]**float64{"daily_quota_amount": request.DailyQuotaAmount, "weekly_quota_amount": request.WeeklyQuotaAmount, "monthly_quota_amount": request.MonthlyQuotaAmount} {
		if value != nil {
			if err := domain.ValidateKeyQuotaAmounts(*value); err != nil {
				return nil, application.ErrInvalidKey
			}
			updates[field] = *value
		}
	}
	for field, value := range map[string]**int64{"daily_quota_tokens": request.DailyQuotaTokens, "weekly_quota_tokens": request.WeeklyQuotaTokens, "monthly_quota_tokens": request.MonthlyQuotaTokens} {
		if value != nil {
			if err := domain.ValidateKeyQuotaTokens(*value); err != nil {
				return nil, application.ErrInvalidKey
			}
			updates[field] = *value
		}
	}
	if request.RateLimit5H != nil {
		updates["rate_limit_5h"] = *request.RateLimit5H
	}
	if request.RateLimit1D != nil {
		updates["rate_limit_1d"] = *request.RateLimit1D
	}
	if request.RateLimit7D != nil {
		updates["rate_limit_7d"] = *request.RateLimit7D
	}
	if request.IPAllowlist != nil {
		updates["ip_allowlist"] = *request.IPAllowlist
	}
	if request.IPBlocklist != nil {
		updates["ip_blocklist"] = *request.IPBlocklist
	}
	if len(updates) == 0 {
		return nil, application.ErrInvalidKey
	}
	return updates, nil
}

type pageResult struct {
	Items    any   `json:"items"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

func pathID(ctx *gin.Context) (uint64, bool) {
	value, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	return value, err == nil && value > 0
}
func optionalID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err
}

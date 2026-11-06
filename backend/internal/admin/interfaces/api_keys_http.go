package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	userdomain "bosapi/backend/internal/user/domain"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdminKeyService defines administrative key operations exposed to the API.
type AdminKeyService interface {
	List(context.Context, domain.AdminKeyFilter, int, int) ([]userdomain.SafeAPIKey, int64, error)
	Get(context.Context, uint64, uint64) (userdomain.SafeAPIKey, error)
	SetStatus(context.Context, uint64, uint64, string) error
	Update(context.Context, uint64, uint64, map[string]any) error
	Create(context.Context, application.AdminKeyInput) (application.CreatedAdminKey, error)
	CreateBatch(context.Context, []application.AdminKeyInput) ([]application.CreatedAdminKey, error)
	Import(context.Context, []application.AdminKeyInput) ([]application.CreatedAdminKey, error)
	Duplicate(context.Context, uint64, uint64) (application.CreatedAdminKey, error)
	Reset(context.Context, uint64, uint64) (application.CreatedAdminKey, error)
	BatchDelete(context.Context, []uint64, uint64) (int64, error)
	Reveal(context.Context, uint64, uint64, uint64, bool, string) (string, error)
}

// AdminKeyHandler handles protected key administration endpoints.
type AdminKeyHandler struct{ service AdminKeyService }

// NewAdminKeyHandler constructs an administrative key handler.
func NewAdminKeyHandler(service AdminKeyService) *AdminKeyHandler {
	return &AdminKeyHandler{service: service}
}

// RegisterRoutes installs role-restricted administrative key routes.
func (handler *AdminKeyHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/keys", handler.list)
	group.GET("/keys/options", handler.list)
	group.POST("/keys", handler.create)
	group.POST("/keys/import", handler.importKeys)
	group.POST("/keys/batch-create", handler.createBatch)
	group.POST("/keys/batch-delete", handler.batchDelete)
	group.GET("/keys/:id", handler.get)
	group.POST("/keys/:id/reveal", handler.reveal)
	group.PUT("/keys/:id", handler.update)
	group.POST("/keys/:id/reset", handler.reset)
	group.PATCH("/keys/:id/status", handler.setStatus)
	group.POST("/keys/:id/duplicate", handler.duplicate)
	group.DELETE("/keys/:id", handler.delete)
}

func (handler *AdminKeyHandler) createBatch(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request struct {
		Names  []string `json:"names" binding:"required,min=1,max=500,dive,required"`
		Status string   `json:"status" binding:"omitempty,oneof=active disabled"`
		domain.KeyTemplateSettings
	}
	if err := ctx.ShouldBindJSON(&request); err != nil || request.OrganizationID == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "batch settings are invalid")
		return
	}
	if claims.Role == "org_admin" {
		if request.OrganizationID != claims.OrganizationID {
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
			return
		}
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	inputs := make([]application.AdminKeyInput, 0, len(request.Names))
	for _, name := range request.Names {
		inputs = append(inputs, application.AdminKeyInput{Name: name, Status: request.Status, OrganizationID: request.OrganizationID, AllowedProviderIDs: request.AllowedProviderIDs, AllowedModels: request.AllowedModels,
			ExpiresAt: request.ExpiresAt, QuotaAmount: request.QuotaAmount, QuotaTokens: request.QuotaTokens,
			DailyQuotaAmount: request.DailyQuotaAmount, WeeklyQuotaAmount: request.WeeklyQuotaAmount, MonthlyQuotaAmount: request.MonthlyQuotaAmount,
			DailyQuotaTokens: request.DailyQuotaTokens, WeeklyQuotaTokens: request.WeeklyQuotaTokens, MonthlyQuotaTokens: request.MonthlyQuotaTokens,
			RateLimit5H: request.RateLimit5H, RateLimit1D: request.RateLimit1D, RateLimit7D: request.RateLimit7D, IPAllowlist: request.IPAllowlist, IPBlocklist: request.IPBlocklist})
	}
	created, err := handler.service.CreateBatch(ctx.Request.Context(), inputs)
	if err != nil {
		keyFailure(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	httpapi.WriteSuccess(ctx, 201, struct {
		Items []application.CreatedAdminKey `json:"items"`
	}{created})
}

func (handler *AdminKeyHandler) importKeys(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request struct {
		Keys []struct {
			Name   string `json:"name"`
			APIKey string `json:"api_key"`
		} `json:"keys"`
		domain.KeyTemplateSettings
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.import_invalid", "actor_id=%d reason=invalid_or_oversized_payload", claims.UserID)
		httpapi.WriteError(ctx, status, errorcode.InvalidRequest, "request is invalid or too large")
		return
	}
	if len(request.Keys) == 0 || len(request.Keys) > application.MaxAdminKeyImportCount || request.OrganizationID == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "import must contain 1 to 500 keys and a valid organization")
		return
	}
	if claims.Role == "org_admin" {
		if request.OrganizationID != claims.OrganizationID {
			logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.import_denied", "actor_id=%d requested_organization_id=%d actor_organization_id=%d reason=organization_scope_mismatch", claims.UserID, request.OrganizationID, claims.OrganizationID)
			httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
			return
		}
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	inputs := make([]application.AdminKeyInput, 0, len(request.Keys))
	for index, key := range request.Keys {
		if strings.TrimSpace(key.APIKey) == "" || len(key.APIKey) > 4096 || strings.ContainsAny(key.APIKey, "\r\n") {
			logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.import_rejected", "actor_id=%d organization_id=%d row=%d reason=invalid_key_format", claims.UserID, request.OrganizationID, index+1)
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "one or more API keys are invalid")
			return
		}
		inputs = append(inputs, application.AdminKeyInput{
			Name:               key.Name,
			APIKey:             strings.TrimSpace(key.APIKey),
			UserID:             request.UserID,
			AllowedProviderIDs: request.AllowedProviderIDs, AllowedModels: request.AllowedModels,
			OrganizationID: request.OrganizationID,
			ExpiresAt:      request.ExpiresAt,
			QuotaAmount:    request.QuotaAmount, QuotaTokens: request.QuotaTokens,
			DailyQuotaAmount: request.DailyQuotaAmount, WeeklyQuotaAmount: request.WeeklyQuotaAmount, MonthlyQuotaAmount: request.MonthlyQuotaAmount,
			DailyQuotaTokens: request.DailyQuotaTokens, WeeklyQuotaTokens: request.WeeklyQuotaTokens, MonthlyQuotaTokens: request.MonthlyQuotaTokens,
			RateLimit5H: request.RateLimit5H, RateLimit1D: request.RateLimit1D, RateLimit7D: request.RateLimit7D,
			IPAllowlist: request.IPAllowlist, IPBlocklist: request.IPBlocklist,
		})
	}
	created, err := handler.service.Import(ctx.Request.Context(), inputs)
	if err != nil {
		logging.Printf(ctx.Request.Context(), "ERROR", "admin.api_key.import_failed", "organization_id=%d user_id=%d actor_id=%d count=%d", request.OrganizationID, request.UserID, claims.UserID, len(inputs))
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			httpapi.WriteError(ctx, http.StatusConflict, errorcode.Conflict, "one or more API keys already exist")
			return
		}
		keyFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, gin.H{"items": created, "count": len(created)})
}

func (handler *AdminKeyHandler) update(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	var request struct {
		ExpiresAt                           json.RawMessage `json:"expires_at"`
		Name                                *string         `json:"name"`
		UserID                              json.RawMessage `json:"user_id"`
		OrganizationID                      json.RawMessage `json:"organization_id"`
		SecurityInspectionEnabled           *bool           `json:"security_inspection_enabled"`
		SecurityInspectionUpstreamEnabled   *bool           `json:"security_inspection_upstream_enabled"`
		SecurityInspectionDownstreamEnabled *bool           `json:"security_inspection_downstream_enabled"`
		QuotaAmount                         **float64       `json:"quota_amount"`
		QuotaTokens                         **int64         `json:"quota_tokens"`
		DailyQuotaAmount                    **float64       `json:"daily_quota_amount"`
		WeeklyQuotaAmount                   **float64       `json:"weekly_quota_amount"`
		MonthlyQuotaAmount                  **float64       `json:"monthly_quota_amount"`
		DailyQuotaTokens                    **int64         `json:"daily_quota_tokens"`
		WeeklyQuotaTokens                   **int64         `json:"weekly_quota_tokens"`
		MonthlyQuotaTokens                  **int64         `json:"monthly_quota_tokens"`
		IPAllowlist                         *[]string       `json:"ip_allowlist"`
		AllowedProviderIDs                  *[]uint64       `json:"allowed_provider_ids"`
		AllowedModels                       *[]string       `json:"allowed_models"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.update_invalid", "key_id=%d actor_id=%d error=%v", id, claims.UserID, err)
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if len(request.UserID) > 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "API keys do not have a user owner")
		return
	}
	// Gorm requires named columns to preserve omitted fields in a partial update.
	updates := map[string]any{}
	if len(request.OrganizationID) > 0 {
		var targetOrganizationID uint64
		if err := json.Unmarshal(request.OrganizationID, &targetOrganizationID); err != nil || targetOrganizationID == 0 {
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "organization_id must be a positive organization ID")
			return
		}
		if claims.Role != "super_admin" && (claims.Role != "org_admin" || targetOrganizationID != claims.OrganizationID) {
			httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "only super administrators can change a key organization")
			return
		}
		updates["organization_id"] = targetOrganizationID
	}
	if request.SecurityInspectionEnabled != nil {
		updates["security_inspection_upstream_enabled"] = *request.SecurityInspectionEnabled
		updates["security_inspection_downstream_enabled"] = *request.SecurityInspectionEnabled
	}
	if request.SecurityInspectionUpstreamEnabled != nil {
		updates["security_inspection_upstream_enabled"] = *request.SecurityInspectionUpstreamEnabled
	}
	if request.SecurityInspectionDownstreamEnabled != nil {
		updates["security_inspection_downstream_enabled"] = *request.SecurityInspectionDownstreamEnabled
	}
	if len(request.ExpiresAt) > 0 {
		var expiresAt *time.Time
		if err := json.Unmarshal(request.ExpiresAt, &expiresAt); err != nil {
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "expiration time is invalid")
			return
		}
		updates["expires_at"] = expiresAt
	}
	if request.Name != nil {
		updates["name"] = strings.TrimSpace(*request.Name)
	}
	for field, value := range map[string]**float64{"quota_amount": request.QuotaAmount, "daily_quota_amount": request.DailyQuotaAmount, "weekly_quota_amount": request.WeeklyQuotaAmount, "monthly_quota_amount": request.MonthlyQuotaAmount} {
		if value != nil {
			updates[field] = *value
		}
	}
	for field, value := range map[string]**int64{"quota_tokens": request.QuotaTokens, "daily_quota_tokens": request.DailyQuotaTokens, "weekly_quota_tokens": request.WeeklyQuotaTokens, "monthly_quota_tokens": request.MonthlyQuotaTokens} {
		if value != nil {
			updates[field] = *value
		}
	}
	if request.IPAllowlist != nil {
		updates["ip_allowlist"] = *request.IPAllowlist
	}
	if request.AllowedProviderIDs != nil {
		updates["allowed_provider_ids"] = *request.AllowedProviderIDs
	}
	if request.AllowedModels != nil {
		updates["allowed_models"] = *request.AllowedModels
	}
	if err := handler.service.Update(ctx.Request.Context(), id, organizationID, updates); err != nil {
		logging.Printf(ctx.Request.Context(), "ERROR", "admin.api_key.update_failed", "key_id=%d organization_id=%d actor_id=%d error=%v", id, organizationID, claims.UserID, err)
		keyFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"updated": true})
}

func (handler *AdminKeyHandler) create(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	var request struct {
		Status             string     `json:"status" binding:"omitempty,oneof=active disabled"`
		Name               string     `json:"name" binding:"required"`
		APIKey             string     `json:"api_key"`
		UserID             uint64     `json:"-"`
		AllowedProviderIDs []uint64   `json:"allowed_provider_ids"`
		AllowedModels      []string   `json:"allowed_models"`
		OrganizationID     uint64     `json:"organization_id" binding:"required"`
		ExpiresAt          *time.Time `json:"expires_at"`
		QuotaAmount        *float64   `json:"quota_amount"`
		QuotaTokens        *int64     `json:"quota_tokens"`
		DailyQuotaAmount   *float64   `json:"daily_quota_amount"`
		WeeklyQuotaAmount  *float64   `json:"weekly_quota_amount"`
		MonthlyQuotaAmount *float64   `json:"monthly_quota_amount"`
		DailyQuotaTokens   *int64     `json:"daily_quota_tokens"`
		WeeklyQuotaTokens  *int64     `json:"weekly_quota_tokens"`
		MonthlyQuotaTokens *int64     `json:"monthly_quota_tokens"`
		RateLimit5H        *int64     `json:"rate_limit_5h"`
		RateLimit1D        *int64     `json:"rate_limit_1d"`
		RateLimit7D        *int64     `json:"rate_limit_7d"`
		IPAllowlist        []string   `json:"ip_allowlist"`
		IPBlocklist        []string   `json:"ip_blocklist"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.create_invalid", "actor_id=%d error=%v", claims.UserID, err)
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "request is invalid")
		return
	}
	if claims.Role == "org_admin" {
		if request.OrganizationID != claims.OrganizationID {
			logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.create_denied", "actor_id=%d requested_organization_id=%d actor_organization_id=%d reason=organization_scope_mismatch", claims.UserID, request.OrganizationID, claims.OrganizationID)
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
			return
		}
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	if len(request.APIKey) > 4096 || strings.ContainsAny(request.APIKey, "\r\n") {
		logging.Printf(ctx.Request.Context(), "WARN", "admin.api_key.create_rejected", "actor_id=%d organization_id=%d reason=invalid_supplied_key_format", claims.UserID, request.OrganizationID)
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "api key is invalid")
		return
	}
	created, err := handler.service.Create(ctx.Request.Context(), application.AdminKeyInput{Status: request.Status, Name: request.Name, APIKey: request.APIKey, UserID: request.UserID, OrganizationID: request.OrganizationID, AllowedProviderIDs: request.AllowedProviderIDs, AllowedModels: request.AllowedModels, ExpiresAt: request.ExpiresAt, QuotaAmount: request.QuotaAmount, QuotaTokens: request.QuotaTokens, DailyQuotaAmount: request.DailyQuotaAmount, WeeklyQuotaAmount: request.WeeklyQuotaAmount, MonthlyQuotaAmount: request.MonthlyQuotaAmount, DailyQuotaTokens: request.DailyQuotaTokens, WeeklyQuotaTokens: request.WeeklyQuotaTokens, MonthlyQuotaTokens: request.MonthlyQuotaTokens, RateLimit5H: request.RateLimit5H, RateLimit1D: request.RateLimit1D, RateLimit7D: request.RateLimit7D, IPAllowlist: request.IPAllowlist, IPBlocklist: request.IPBlocklist})
	if err != nil {
		logging.Printf(ctx.Request.Context(), "ERROR", "admin.api_key.create_failed", "organization_id=%d user_id=%d actor_id=%d name=%q error=%v", request.OrganizationID, request.UserID, claims.UserID, request.Name, err)
		keyFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, created)
}

func (handler *AdminKeyHandler) reset(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	created, err := handler.service.Reset(ctx.Request.Context(), id, organizationID)
	if err != nil {
		keyFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, created)
}

func (handler *AdminKeyHandler) duplicate(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	created, err := handler.service.Duplicate(ctx.Request.Context(), id, organizationID)
	if err != nil {
		keyFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, created)
}

func keyFailure(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, edition.ErrLimitExceeded):
		httpapi.WriteError(ctx, http.StatusConflict, errorcode.Conflict, err.Error())
	case errors.Is(err, application.ErrInvalidOrganization), errors.Is(err, gorm.ErrInvalidData):
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "api key settings are invalid")
	case errors.Is(err, gorm.ErrRecordNotFound):
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
	default:
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
	}
}

func (handler *AdminKeyHandler) list(ctx *gin.Context) {
	page, pageSize, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	filter := domain.AdminKeyFilter{Search: ctx.Query("search"), Status: ctx.Query("status"), OptionsOnly: strings.HasSuffix(ctx.FullPath(), "/keys/options")}
	var err error
	if filter.OrganizationID, err = queryID(ctx, "organization_id"); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "organization_id is invalid")
		return
	}
	if claims.Role == "org_admin" {
		if claims.OrganizationID == 0 {
			httpapi.WriteError(ctx, 403, errorcode.Forbidden, "organization scope is required")
			return
		}
		filter.OrganizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	if filter.Status != "" && filter.Status != "active" && filter.Status != "disabled" {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "status is invalid")
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), filter, page, pageSize)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	if filter.OptionsOnly {
		type option struct {
			ID             uint64 `json:"id"`
			Name           string `json:"name"`
			OrganizationID uint64 `json:"organization_id"`
		}
		options := make([]option, 0, len(items))
		for _, item := range items {
			options = append(options, option{item.ID, item.Name, item.OrganizationID})
		}
		httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"items": options, "page": page, "page_size": pageSize, "total": total})
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items    []userdomain.SafeAPIKey `json:"items"`
		Page     int                     `json:"page"`
		PageSize int                     `json:"page_size"`
		Total    int64                   `json:"total"`
	}{items, page, pageSize, total})
}

func (handler *AdminKeyHandler) setStatus(ctx *gin.Context) {
	var request struct {
		Status string `json:"status" binding:"required,oneof=active disabled"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	handler.changeStatus(ctx, request.Status)
}

func (handler *AdminKeyHandler) delete(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	count, err := handler.service.BatchDelete(ctx.Request.Context(), []uint64{id}, organizationID)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	if count == 0 {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (handler *AdminKeyHandler) changeStatus(ctx *gin.Context, status string) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	if err := handler.service.SetStatus(ctx.Request.Context(), id, organizationID, status); errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	} else if errors.Is(err, application.ErrInvalidOrganization) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "status is invalid")
		return
	} else if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (handler *AdminKeyHandler) get(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	key, err := handler.service.Get(ctx.Request.Context(), id, organizationID)
	if err != nil {
		keyFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, key)
}

func (handler *AdminKeyHandler) batchDelete(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
	} else if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	var request struct {
		IDs []uint64 `json:"ids" binding:"required,min=1"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "ids are required")
		return
	}
	count, err := handler.service.BatchDelete(ctx.Request.Context(), request.IDs, organizationID)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"deleted_count": count})
}

func (handler *AdminKeyHandler) reveal(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Pragma", "no-cache")
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role != "super_admin" && claims.Role != "org_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	var request struct {
		Code string `json:"code" binding:"required,len=6"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "2fa code is required")
		return
	}
	isSuperAdmin := claims.Role == "super_admin"
	rawKey, err := handler.service.Reveal(ctx.Request.Context(), id, claims.UserID, claims.OrganizationID, isSuperAdmin, request.Code)
	if err != nil {
		switch {
		case errors.Is(err, application.ErrTOTPNotEnabled):
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "2FA is not enabled on your account. Please enable 2FA in settings first.")
		case errors.Is(err, application.ErrInvalidTOTPCode):
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "Invalid 2FA verification code")
		case errors.Is(err, application.ErrKeyNotRecoverable):
			httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "Key plaintext was not saved and cannot be viewed. Reset the key to generate a new one.")
		default:
			keyFailure(ctx, err)
		}
		return
	}
	logging.Printf(ctx.Request.Context(), "INFO", "admin.api_key.revealed", "actor_id=%d key_id=%d organization_id=%d", claims.UserID, id, claims.OrganizationID)
	httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"key": rawKey})
}

func queryID(ctx *gin.Context, key string) (uint64, error) {
	value := ctx.Query(key)
	if value == "" {
		return 0, nil
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid identifier")
	}
	return id, nil
}

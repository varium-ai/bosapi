package interfaces

import (
	"net/http"
	"time"

	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SetupHandler exposes evidence from existing resources; it never calls an upstream.
type SetupHandler struct{ database *gorm.DB }

func NewSetupHandler(database *gorm.DB) *SetupHandler { return &SetupHandler{database: database} }
func (handler *SetupHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/organizations/:id/setup-status", handler.get)
}
func (handler *SetupHandler) get(ctx *gin.Context) {
	id, valid := organizationID(ctx)
	claims, authenticated := httpapi.CurrentClaims(ctx)
	if !authenticated {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role != "super_admin" && (claims.Role != "org_admin" || claims.OrganizationID != id) {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return
	}
	if !valid {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "id is invalid")
		return
	}
	var organization struct {
		ID        uint64
		Status    string
		UpdatedAt time.Time
	}
	if err := handler.database.WithContext(ctx.Request.Context()).Table("organizations").Where("id = ?", id).Take(&organization).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
		} else {
			httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		}
		return
	}
	var result struct {
		HasUsableKey      bool       `json:"has_usable_key"`
		SuccessfulCallAt  *time.Time `json:"successful_call_at"`
		ProviderID        uint64     `json:"provider_id"`
		ProviderUpdatedAt *time.Time `json:"provider_updated_at"`
		ChannelVerified   bool       `json:"channel_verified"`
		CanTest           bool       `json:"can_test"`
	}
	if organization.Status == "active" {
		var channel struct {
			ID         uint64
			UpdatedAt  *time.Time
			VerifiedAt *time.Time
		}
		channelQuery := handler.database.WithContext(ctx.Request.Context()).Table("accounts AS a").
			Select("a.id, a.updated_at, a.verified_at").
			Joins("JOIN provider_models pm ON pm.account_id = a.id").
			Joins("LEFT JOIN organization_provider_bindings b ON b.account_id = a.id AND b.organization_id = ?", id).
			Where("a.status = 'active' AND a.schedulable = 1 AND pm.status = 'active' AND pm.model_name <> '' AND pm.model_name <> '*'").
			Where("b.status = 'active' AND (b.model_allowlist IS NULL OR JSON_TYPE(b.model_allowlist) = 'NULL' OR JSON_LENGTH(b.model_allowlist) = 0 OR JSON_CONTAINS(b.model_allowlist, JSON_QUOTE(pm.model_name)))")
		if err := channelQuery.Order("(a.verified_at IS NOT NULL AND a.verified_at >= a.updated_at) DESC, a.id ASC").Limit(1).Scan(&channel).Error; err != nil {
			httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
			return
		}
		result.ProviderID, result.ProviderUpdatedAt = channel.ID, channel.UpdatedAt
		result.ChannelVerified = channel.VerifiedAt != nil && channel.UpdatedAt != nil && !channel.VerifiedAt.Before(*channel.UpdatedAt)
		result.CanTest = claims.Role == "super_admin"
		var err error
		result.HasUsableKey, result.SuccessfulCallAt, err = loadSetupKeyStatus(
			handler.database.WithContext(ctx.Request.Context()), id, organization.UpdatedAt,
			time.Now().UTC(), false)
		if err != nil {
			httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
			return
		}
	}
	ctx.Header("Cache-Control", "no-store")
	httpapi.WriteSuccess(ctx, http.StatusOK, result)
}

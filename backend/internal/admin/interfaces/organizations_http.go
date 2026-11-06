package interfaces

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// OrganizationService defines organization use cases exposed to the admin API.
type OrganizationService interface {
	List(context.Context, domain.OrganizationFilter, int, int) ([]domain.Organization, int64, error)
	Find(context.Context, uint64) (domain.Organization, error)
	Create(context.Context, domain.Organization) (domain.Organization, error)
	Update(context.Context, uint64, map[string]any) (domain.Organization, error)
	Disable(context.Context, uint64) error
}

// OrganizationHandler adapts organization operations to the admin HTTP contract.
type OrganizationHandler struct{ service OrganizationService }

// NewOrganizationHandler constructs an organization HTTP handler.
func NewOrganizationHandler(service OrganizationService) *OrganizationHandler {
	return &OrganizationHandler{service: service}
}

// RegisterRoutes installs role-protected organization CRUD routes.
func (handler *OrganizationHandler) RegisterRoutes(admin *gin.RouterGroup) {
	admin.GET("/organizations", handler.list)
	admin.POST("/organizations", interfaces.RequireRoles("super_admin"), handler.create)
	admin.GET("/organizations/:id", handler.get)
	admin.PUT("/organizations/:id", handler.update)
	admin.DELETE("/organizations/:id", interfaces.RequireRoles("super_admin"), handler.disable)
}

func (handler *OrganizationHandler) list(ctx *gin.Context) {
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
	claims, authenticated := httpapi.CurrentClaims(ctx)
	if !authenticated {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	filter := domain.OrganizationFilter{Status: status, Search: ctx.Query("search")}
	if claims.Role == "org_admin" {
		filter.ID = claims.OrganizationID
	}
	items, total, err := handler.service.List(ctx.Request.Context(), filter, page, pageSize)
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items    []domain.Organization `json:"items"`
		Page     int                   `json:"page"`
		PageSize int                   `json:"page_size"`
		Total    int64                 `json:"total"`
	}{items, page, pageSize, total})
}

type organizationRequest struct {
	Bindings           []domain.ProviderBinding `json:"bindings"`
	Name               *string                  `json:"name"`
	Description        *string                  `json:"description"`
	Status             *string                  `json:"status"`
	AllowedProviderIDs *[]uint64                `json:"allowed_provider_ids"`
	AllowedModels      *[]string                `json:"allowed_models"`
}

func (handler *OrganizationHandler) create(ctx *gin.Context) {
	var request organizationRequest
	if err := ctx.ShouldBindJSON(&request); err != nil || request.Name == nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	organization := request.toDomain()
	organization.InitialBindings = request.Bindings
	created, err := handler.service.Create(ctx.Request.Context(), organization)
	if errors.Is(err, edition.ErrLimitExceeded) {
		httpapi.WriteError(ctx, http.StatusConflict, errorcode.Conflict, err.Error())
		return
	}
	if errors.Is(err, application.ErrInvalidOrganization) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "organization is invalid")
		return
	}
	if err != nil {
		writeOrganizationSaveError(ctx, err)
		return
	}

	httpapi.WriteSuccess(ctx, http.StatusCreated, created)
}

func (handler *OrganizationHandler) get(ctx *gin.Context) {
	id, ok := organizationID(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, authenticated := httpapi.CurrentClaims(ctx)
	if !authenticated {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role == "org_admin" && id != claims.OrganizationID {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	organization, err := handler.service.Find(ctx.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, organization)
}

func (handler *OrganizationHandler) update(ctx *gin.Context) {
	id, ok := organizationID(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, authenticated := httpapi.CurrentClaims(ctx)
	if !authenticated {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	var request organizationRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "request is invalid")
		return
	}
	organization, err := handler.service.Update(ctx.Request.Context(), id, request.values())
	if errors.Is(err, application.ErrInvalidOrganization) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "organization is invalid")
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		writeOrganizationSaveError(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, organization)
}

func (handler *OrganizationHandler) disable(ctx *gin.Context) {
	id, ok := organizationID(ctx)
	if !ok {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "id is invalid")
		return
	}
	claims, authenticated := httpapi.CurrentClaims(ctx)
	if !authenticated {
		httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
		return
	}
	if claims.Role != "super_admin" {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	err := handler.service.Disable(ctx.Request.Context(), id)
	if errors.Is(err, application.ErrInvalidOrganization) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "organization settings are invalid")
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (request organizationRequest) toDomain() domain.Organization {
	organization := domain.Organization{}
	if request.Name != nil {
		organization.Name = *request.Name
	}
	if request.Description != nil {
		organization.Description = *request.Description
	}
	if request.Status != nil {
		organization.Status = *request.Status
	}
	if request.AllowedProviderIDs != nil {
		organization.AllowedProviderIDs = *request.AllowedProviderIDs
	}
	if request.AllowedModels != nil {
		organization.AllowedModels = *request.AllowedModels
	}
	return organization
}

func (request organizationRequest) values() map[string]any {
	updates := make(map[string]any)
	if request.Name != nil {
		updates["name"] = strings.TrimSpace(*request.Name)
	}
	if request.Description != nil {
		updates["description"] = *request.Description
	}
	if request.Status != nil {
		updates["status"] = *request.Status
	}
	if request.AllowedProviderIDs != nil {
		updates["allowed_provider_ids"] = *request.AllowedProviderIDs
	}
	if request.AllowedModels != nil {
		updates["allowed_models"] = *request.AllowedModels
	}
	return updates
}

func organizationID(ctx *gin.Context) (uint64, bool) {
	value, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	return value, err == nil && value > 0
}

func writeOrganizationSaveError(ctx *gin.Context, err error) {
	var databaseError *mysql.MySQLError
	if errors.Is(err, gorm.ErrDuplicatedKey) || errors.As(err, &databaseError) && databaseError.Number == 1062 {
		httpapi.WriteError(ctx, http.StatusConflict, errorcode.Conflict, "organization name is already in use")
	} else {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "organization could not be saved")
	}
}

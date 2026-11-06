package interfaces

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
)

type keyTemplateService interface {
	List(context.Context, uint64, int, int) ([]domain.KeyTemplate, int64, error)
	Save(context.Context, uint64, uint64, uint64, string, domain.KeyTemplateSettings) (domain.KeyTemplate, error)
	Delete(context.Context, uint64, uint64) error
}

// KeyTemplateHandler derives private template scope exclusively from authenticated claims.
type KeyTemplateHandler struct{ service keyTemplateService }

// NewKeyTemplateHandler exposes creator-scoped configuration without any key secrets.
func NewKeyTemplateHandler(service keyTemplateService) *KeyTemplateHandler {
	return &KeyTemplateHandler{service: service}
}

// RegisterRoutes requires an administrator identity for every private template operation.
func (handler *KeyTemplateHandler) RegisterRoutes(group *gin.RouterGroup) {
	templates := group.Group("/key-templates", keyTemplateIdentity)
	templates.GET("", handler.list)
	templates.POST("", handler.save)
	templates.PUT("/:id", handler.save)
	templates.DELETE("/:id", handler.delete)
}

func keyTemplateIdentity(ctx *gin.Context) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok || claims.UserID == 0 {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		ctx.Abort()
		return
	}
	if claims.Role != "super_admin" && claims.Role != "org_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		ctx.Abort()
		return
	}
	ctx.Next()
}

func (handler *KeyTemplateHandler) list(ctx *gin.Context) {
	claims, _ := httpapi.CurrentClaims(ctx)
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		templateFailure(ctx, application.ErrInvalidKeyTemplate)
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), claims.UserID, page, size)
	if err != nil {
		templateFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, struct {
		Items []domain.KeyTemplate `json:"items"`
		Total int64                `json:"total"`
	}{items, total})
}

func (handler *KeyTemplateHandler) save(ctx *gin.Context) {
	var request struct {
		Name     string                     `json:"name" binding:"required"`
		Settings domain.KeyTemplateSettings `json:"settings" binding:"required"`
	}
	id, err := templateID(ctx)
	if err != nil {
		templateFailure(ctx, err)
		return
	}
	if err := ctx.ShouldBindJSON(&request); err != nil {
		templateFailure(ctx, application.ErrInvalidKeyTemplate)
		return
	}
	claims, _ := httpapi.CurrentClaims(ctx)
	organizationID := uint64(0)
	if claims.Role == "org_admin" {
		organizationID = claims.OrganizationID
		if organizationID == 0 {
			templateFailure(ctx, application.ErrInvalidKeyTemplate)
			return
		}
	}
	item, err := handler.service.Save(ctx.Request.Context(), claims.UserID, organizationID, id, request.Name, request.Settings)
	if err != nil {
		templateFailure(ctx, err)
		return
	}
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	httpapi.WriteSuccess(ctx, status, item)
}

func (handler *KeyTemplateHandler) delete(ctx *gin.Context) {
	id, err := templateID(ctx)
	if err != nil || id == 0 {
		templateFailure(ctx, application.ErrInvalidKeyTemplate)
		return
	}
	claims, _ := httpapi.CurrentClaims(ctx)
	if err := handler.service.Delete(ctx.Request.Context(), claims.UserID, id); err != nil {
		templateFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, 200, struct {
		Deleted bool `json:"deleted"`
	}{true})
}

func templateID(ctx *gin.Context) (uint64, error) {
	if ctx.Param("id") == "" {
		return 0, nil
	}
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, application.ErrInvalidKeyTemplate
	}
	return id, nil
}

func templateFailure(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidKeyTemplate):
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "key template settings are invalid")
	case errors.Is(err, gorm.ErrRecordNotFound):
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
	default:
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
	}
}

package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"bosapi/backend/internal/admin/domain"
	authinterfaces "bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
)

type announcementService interface {
	List(context.Context, domain.AnnouncementFilter, int, int) ([]domain.Announcement, int64, error)
	Create(context.Context, uint64, string, string) (domain.Announcement, error)
	Update(context.Context, domain.Announcement) (domain.Announcement, error)
	Delete(context.Context, uint64, uint64) error
}

// AnnouncementHandler derives readership and publication ownership from authenticated sessions.
type AnnouncementHandler struct{ service announcementService }

// NewAnnouncementHandler shares publication behavior across the administrator and key portals.
func NewAnnouncementHandler(service announcementService) *AnnouncementHandler {
	return &AnnouncementHandler{service: service}
}

// RegisterReadRoutes requires an already authenticated administrator or active key portal session.
func (handler *AnnouncementHandler) RegisterReadRoutes(group *gin.RouterGroup) {
	group.GET("/announcements", announcementReader, handler.list)
}

// RegisterAdminRoutes checks publication authority on the server for every mutation.
func (handler *AnnouncementHandler) RegisterAdminRoutes(group *gin.RouterGroup) {
	handler.RegisterReadRoutes(group)
	management := group.Group("/announcements", announcementReader, authinterfaces.RequireRoles("super_admin", "org_admin"))
	management.GET("/manage", handler.listManaged)
	management.POST("", handler.create)
	management.PUT("/:id", handler.update)
	management.DELETE("/:id", handler.delete)
}

func announcementReader(ctx *gin.Context) {
	if key, ok := keyPortalKey(ctx); ok {
		if key.OrganizationID != 0 {
			ctx.Next()
			return
		}
	} else if claims, ok := httpapi.CurrentClaims(ctx); ok && claims.UserID != 0 &&
		(claims.Role == "super_admin" || claims.OrganizationID != 0) {
		ctx.Next()
		return
	}
	httpapi.WriteError(ctx, http.StatusUnauthorized, errorcode.Unauthorized, "authentication is required")
}

func announcementOrganization(ctx *gin.Context) uint64 {
	if key, ok := keyPortalKey(ctx); ok {
		return key.OrganizationID
	}
	claims, _ := httpapi.CurrentClaims(ctx)
	if claims.Role == "super_admin" {
		return 0
	}
	return claims.OrganizationID
}

func (handler *AnnouncementHandler) list(ctx *gin.Context) {
	handler.listPublications(ctx, true)
}

func (handler *AnnouncementHandler) listManaged(ctx *gin.Context) {
	handler.listPublications(ctx, false)
}

func (handler *AnnouncementHandler) listPublications(ctx *gin.Context, includeGlobal bool) {
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		announcementFailure(ctx, domain.ErrInvalidAnnouncement)
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), domain.AnnouncementFilter{OrganizationID: announcementOrganization(ctx), IncludeGlobal: includeGlobal}, page, size)
	if err != nil {
		announcementFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Items []domain.Announcement `json:"items"`
		Total int64                 `json:"total"`
	}{items, total})
}

type announcementRequest struct {
	Title string `json:"title" validate:"required,max=200"`
	Body  string `json:"body" validate:"required"`
}

func (request announcementRequest) validate() error {
	if err := validator.New().Struct(request); err != nil {
		return domain.ErrInvalidAnnouncement
	}
	return (domain.Announcement{Title: request.Title, Body: request.Body}).Validate()
}

func decodeAnnouncement(ctx *gin.Context) (announcementRequest, error) {
	var request announcementRequest
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 512*1024)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, domain.ErrInvalidAnnouncement
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return request, domain.ErrInvalidAnnouncement
	}
	return request, request.validate()
}

func (handler *AnnouncementHandler) create(ctx *gin.Context) {
	request, err := decodeAnnouncement(ctx)
	if err != nil {
		announcementFailure(ctx, err)
		return
	}
	item, err := handler.service.Create(ctx.Request.Context(), announcementOrganization(ctx), request.Title, request.Body)
	if err != nil {
		announcementFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusCreated, item)
}

func (handler *AnnouncementHandler) update(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		announcementFailure(ctx, domain.ErrInvalidAnnouncement)
		return
	}
	request, err := decodeAnnouncement(ctx)
	if err != nil {
		announcementFailure(ctx, err)
		return
	}
	item, err := handler.service.Update(ctx.Request.Context(), domain.Announcement{ID: id, OrganizationID: announcementOrganization(ctx), Title: request.Title, Body: request.Body})
	if err != nil {
		announcementFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, item)
}

func (handler *AnnouncementHandler) delete(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		announcementFailure(ctx, domain.ErrInvalidAnnouncement)
		return
	}
	if err := handler.service.Delete(ctx.Request.Context(), announcementOrganization(ctx), id); err != nil {
		announcementFailure(ctx, err)
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, struct {
		Deleted bool `json:"deleted"`
	}{true})
}

func announcementFailure(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidAnnouncement):
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "announcement title, body or pagination is invalid")
	case errors.Is(err, domain.ErrAnnouncementNotFound):
		httpapi.WriteError(ctx, http.StatusNotFound, errorcode.NotFound, "announcement was not found")
	default:
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
	}
}

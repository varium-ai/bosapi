package interfaces

import (
	"errors"
	"net"
	"net/http"
	"strconv"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"

	"github.com/gin-gonic/gin"
)

// SecurityEventHandler exposes safe security event metadata to administrators.
type SecurityEventHandler struct {
	service *application.SecurityEventService
}

// NewSecurityEventHandler constructs a security event HTTP handler.
func NewSecurityEventHandler(service *application.SecurityEventService) *SecurityEventHandler {
	return &SecurityEventHandler{service: service}
}

// RegisterRoutes installs the paginated security event endpoint.
func (handler *SecurityEventHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/security-events", handler.list)
	group.GET("/security-events/unread", handler.unread)
	group.POST("/security-events/read", handler.markRead)
	group.GET("/security-events/:id", handler.detail)
}

func securityEventAdmin(ctx *gin.Context) (application.SecurityEventActor, bool) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return application.SecurityEventActor{}, false
	}
	if claims.UserID == 0 || claims.Role != "org_admin" && claims.Role != "super_admin" {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "role is not allowed")
		return application.SecurityEventActor{}, false
	}
	if claims.Role == "org_admin" && claims.OrganizationID == 0 {
		httpapi.WriteError(ctx, 403, errorcode.Forbidden, "organization is required")
		return application.SecurityEventActor{}, false
	}
	return application.SecurityEventActor{UserID: claims.UserID, OrganizationID: claims.OrganizationID, Role: claims.Role}, true
}

func (handler *SecurityEventHandler) detail(ctx *gin.Context) {
	actor, ok := securityEventAdmin(ctx)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "event id is invalid")
		return
	}
	organizationID := uint64(0)
	if actor.Role == "org_admin" {
		organizationID = actor.OrganizationID
	}
	detail, err := handler.service.Detail(ctx.Request.Context(), id, organizationID)
	if errors.Is(err, domain.ErrSecurityEventNotFound) {
		httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, detail)
}

func (handler *SecurityEventHandler) list(ctx *gin.Context) {
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	actor, ok := securityEventAdmin(ctx)
	if !ok {
		return
	}
	organizationID, err := queryID(ctx, "organization_id")
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "organization_id is invalid")
		return
	}
	if actor.Role == "org_admin" {
		if organizationID != 0 && organizationID != actor.OrganizationID {
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
			return
		}
		organizationID = actor.OrganizationID
	}
	keyID, err := queryID(ctx, "key_id")
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "key_id is invalid")
		return
	}
	clientIP := ctx.Query("client_ip")
	if clientIP != "" && net.ParseIP(clientIP) == nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "client_ip is invalid")
		return
	}
	items, total, err := handler.service.List(ctx.Request.Context(), domain.SecurityEventFilter{OrganizationID: organizationID, APIKeyID: keyID, RuleID: ctx.Query("rule_id"), Action: ctx.Query("action"), ToolName: ctx.Query("tool_name"), ClientIP: clientIP, EventType: ctx.Query("event_type"), Severity: ctx.Query("severity"), MinSeverity: ctx.Query("min_severity")}, page, size)
	if errors.Is(err, application.ErrInvalidAdminUsageFilter) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "security event filter is invalid")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
}

func (handler *SecurityEventHandler) unread(ctx *gin.Context) {
	actor, ok := securityEventAdmin(ctx)
	if !ok {
		return
	}
	state, err := handler.service.Unread(ctx.Request.Context(), actor)
	writeSecurityEventReadState(ctx, state, err)
}

func (handler *SecurityEventHandler) markRead(ctx *gin.Context) {
	actor, ok := securityEventAdmin(ctx)
	if !ok {
		return
	}
	state, err := handler.service.MarkRead(ctx.Request.Context(), actor)
	writeSecurityEventReadState(ctx, state, err)
}

func writeSecurityEventReadState(ctx *gin.Context, state domain.SecurityEventReadState, err error) {
	if errors.Is(err, application.ErrSecurityEventForbidden) {
		httpapi.WriteError(ctx, http.StatusForbidden, errorcode.Forbidden, "role is not allowed")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, state)
}

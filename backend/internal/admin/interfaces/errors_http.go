package interfaces

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

// AdminErrorHandler exposes keyset-paginated, sanitized failed request metadata.
type AdminErrorHandler struct {
	service *application.AdminErrorService
}

// NewAdminErrorHandler constructs the administrative error handler.
func NewAdminErrorHandler(service *application.AdminErrorService) *AdminErrorHandler {
	return &AdminErrorHandler{service: service}
}

// RegisterRoutes installs the administrative error dashboard endpoint.
func (handler *AdminErrorHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/errors", handler.list)
}

func (handler *AdminErrorHandler) list(ctx *gin.Context) {
	limit := 50
	if value := ctx.Query("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "limit is invalid")
			return
		}
		limit = parsed
	}
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		httpapi.WriteError(ctx, 401, errorcode.Unauthorized, "authentication is required")
		return
	}
	organizationID, err := queryID(ctx, "organization_id")
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "organization_id is invalid")
		return
	}
	if claims.Role == "org_admin" {
		if organizationID != 0 && organizationID != claims.OrganizationID {
			httpapi.WriteError(ctx, 404, errorcode.NotFound, "resource was not found")
			return
		}
		organizationID = claims.OrganizationID
	}
	keyID, err := queryID(ctx, "key_id")
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "key_id is invalid")
		return
	}
	filter := domain.AdminErrorFilter{OrganizationID: organizationID, APIKeyID: keyID, ErrorCode: ctx.Query("error_code")}
	minutes, err := parseAdminErrorWindow(ctx, &filter, time.Now().UTC())
	if err != nil {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, err.Error())
		return
	}
	bounded, cancel := context.WithTimeout(ctx.Request.Context(), 3*time.Second)
	defer cancel()
	items, more, err := handler.service.List(bounded, filter, limit)
	if errors.Is(err, application.ErrInvalidAdminUsageFilter) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "error filters are invalid")
		return
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(bounded.Err(), context.DeadlineExceeded) {
			httpapi.WriteError(ctx, http.StatusGatewayTimeout, errorcode.RequestTimeout, "error query timed out; select a shorter time range")
			return
		}
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	next := ""
	if more && len(items) > 0 {
		last := items[len(items)-1]
		next = encodeAdminErrorCursor(filter, last.Time, last.ID)
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next, "window_minutes": minutes, "from": filter.From, "to": filter.To,
	})
}

func encodeErrorCursor(timestamp time.Time, id uint64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(timestamp.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatUint(id, 10)))
}
func decodeErrorCursor(value string) (time.Time, uint64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, 0, err
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 2 {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, err
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || id == 0 {
		return time.Time{}, 0, errors.New("invalid cursor")
	}
	return at, id, nil
}

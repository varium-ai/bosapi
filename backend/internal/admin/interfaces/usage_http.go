package interfaces

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/usagetime"
	"github.com/gin-gonic/gin"
)

// AdminUsageHandler exposes bounded administrative usage queries.
type AdminUsageHandler struct {
	service *application.AdminUsageService
}

// NewAdminUsageHandler constructs an administrative usage handler.
func NewAdminUsageHandler(service *application.AdminUsageService) *AdminUsageHandler {
	return &AdminUsageHandler{service: service}
}

// RegisterRoutes installs usage detail and day/week/month aggregation endpoints.
func (handler *AdminUsageHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/usage", handler.list)
	group.GET("/usage/daily", handler.daily)
	group.GET("/usage/organizations/daily", handler.dailyByOrganization)
	group.GET("/usage/weekly", handler.weekly)
	group.GET("/usage/monthly", handler.monthly)
	group.GET("/usage/total", handler.total)
	group.GET("/usage/ips", handler.byIP)
}

func (handler *AdminUsageHandler) dailyByOrganization(ctx *gin.Context) {
	page, size, validPaging := httpapi.ParsePagination(ctx)
	filter, validFilter := adminUsageFilter(ctx)
	if !validPaging || !validFilter {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage filters or pagination are invalid")
		return
	}
	result, err := handler.service.DailyByOrganization(ctx.Request.Context(), domain.AdminOrganizationUsageFilter{AdminUsageFilter: filter, Search: ctx.Query("search")}, page, size)
	if errors.Is(err, application.ErrInvalidAdminUsageFilter) {
		httpapi.WriteError(ctx, http.StatusBadRequest, errorcode.InvalidRequest, "usage requires a valid date range of at most 366 days")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, http.StatusInternalServerError, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, result)
}

func (handler *AdminUsageHandler) byIP(ctx *gin.Context) {
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	filter, ok := adminUsageFilter(ctx)
	if !ok || filter.APIKeyID == 0 {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "key filter is required")
		return
	}
	minutes, valid := ipUsageWindowMinutes(ctx)
	if !valid {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "IP window must be 30, 60, 360, 720 or 1440 minutes")
		return
	}
	filter.IPWindowMinutes = minutes
	result, err := handler.service.GroupByIPSnapshot(ctx.Request.Context(), filter, page, size)
	if errors.Is(err, application.ErrInvalidAdminUsageFilter) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, 200, map[string]any{"items": result.Items, "page": page, "page_size": size, "total": result.Total, "snapshot": result.Snapshot})
}

func ipUsageWindowMinutes(ctx *gin.Context) (int, bool) {
	minutes, err := strconv.Atoi(ctx.DefaultQuery("window_minutes", "30"))
	_, valid := domain.IPUsageWindow(minutes)
	return minutes, err == nil && minutes > 0 && valid
}

func (handler *AdminUsageHandler) list(ctx *gin.Context) {
	page, size, ok := httpapi.ParsePagination(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "pagination is invalid")
		return
	}
	filter, ok := adminUsageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	filter.SkipTotal = ctx.Query("include_total") != "true"
	if filter.From == nil && filter.To == nil {
		from := usagetime.DayStart(time.Now()).UTC()
		to := from.Add(24 * time.Hour)
		filter.From, filter.To = &from, &to
	}
	if filter.From == nil || filter.To == nil || filter.To.Sub(*filter.From) > 366*24*time.Hour || (ctx.Query("cursor") == "" && (page > 1000 || (page-1)*size > 10000)) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage requires a bounded date range and pagination")
		return
	}
	scope := usageCursorScope(filter)
	if raw := ctx.Query("cursor"); raw != "" {
		var cursor usageCursor
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 1024 || err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Scope != scope || cursor.ID == 0 || cursor.At.IsZero() || cursor.At.Before(*filter.From) || !cursor.At.Before(*filter.To) || !filter.SkipTotal {
			httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage cursor is invalid for these filters")
			return
		}
		filter.Before, filter.BeforeID = &cursor.At, cursor.ID
		page = 1
	}
	items, total, err := handler.service.List(ctx.Request.Context(), filter, page, size)
	if errors.Is(err, application.ErrInvalidAdminUsageFilter) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	if filter.SkipTotal {
		hasMore := len(items) > size
		if hasMore {
			items = items[:size]
		}
		cursor := ""
		if hasMore && len(items) > 0 {
			last := items[len(items)-1]
			data, _ := json.Marshal(usageCursor{At: last.CreatedAt, ID: last.ID, Scope: scope})
			cursor = base64.RawURLEncoding.EncodeToString(data)
		}
		httpapi.WriteSuccess(ctx, http.StatusOK, gin.H{"items": items, "page_size": size, "has_more": hasMore, "next_cursor": cursor, "total": nil})
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": size, "total": total})
}

func (handler *AdminUsageHandler) daily(ctx *gin.Context)   { handler.aggregate(ctx, "daily") }
func (handler *AdminUsageHandler) weekly(ctx *gin.Context)  { handler.aggregate(ctx, "weekly") }
func (handler *AdminUsageHandler) monthly(ctx *gin.Context) { handler.aggregate(ctx, "monthly") }
func (handler *AdminUsageHandler) total(ctx *gin.Context)   { handler.aggregate(ctx, "total") }

func (handler *AdminUsageHandler) aggregate(ctx *gin.Context, period string) {
	filter, ok := adminUsageFilter(ctx)
	if !ok {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	result, err := handler.service.AggregateSnapshot(ctx.Request.Context(), filter, period)
	if errors.Is(err, application.ErrInvalidAdminUsageFilter) {
		httpapi.WriteError(ctx, 400, errorcode.InvalidRequest, "usage filters are invalid")
		return
	}
	if err != nil {
		httpapi.WriteError(ctx, 500, errorcode.InternalError, "request could not be completed")
		return
	}
	httpapi.WriteSuccess(ctx, http.StatusOK, map[string]any{"items": result.Items, "snapshot": result.Snapshot, "incomplete": false})
}

func adminUsageFilter(ctx *gin.Context) (domain.AdminUsageFilter, bool) {
	claims, ok := httpapi.CurrentClaims(ctx)
	if !ok {
		return domain.AdminUsageFilter{}, false
	}
	organizationID, err := queryID(ctx, "organization_id")
	if err != nil {
		return domain.AdminUsageFilter{}, false
	}
	if claims.Role == "org_admin" {
		if claims.OrganizationID == 0 || organizationID != 0 && organizationID != claims.OrganizationID {
			return domain.AdminUsageFilter{}, false
		}
		organizationID = claims.OrganizationID
	}
	keyID, err := queryID(ctx, "key_id")
	if err != nil {
		return domain.AdminUsageFilter{}, false
	}
	fromText, toText := ctx.Query("from"), ctx.Query("to")
	location := usagetime.Location()
	var from, to *time.Time
	if fromText != "" {
		parsed, parseErr := time.ParseInLocation("2006-01-02", fromText, location)
		if parseErr != nil {
			return domain.AdminUsageFilter{}, false
		}
		value := parsed.UTC()
		from = &value
	}
	if toText != "" {
		parsed, parseErr := time.ParseInLocation("2006-01-02", toText, location)
		if parseErr != nil {
			return domain.AdminUsageFilter{}, false
		}
		value := parsed.AddDate(0, 0, 1).UTC()
		to = &value
	}
	if from != nil && to != nil && !from.Before(*to) {
		return domain.AdminUsageFilter{}, false
	}
	if claims.Role != "org_admin" && claims.Role != "super_admin" {
		return domain.AdminUsageFilter{}, false
	}
	clientIP := ctx.Query("client_ip")
	if clientIP != "" && net.ParseIP(clientIP) == nil {
		return domain.AdminUsageFilter{}, false
	}
	return domain.AdminUsageFilter{OrganizationID: organizationID, APIKeyID: keyID, ClientIP: clientIP, Model: ctx.Query("model"), From: from, To: to, Refresh: ctx.Query("refresh") == "true", SnapshotID: ctx.Query("snapshot_id")}, true
}

// The scope digest prevents reusing a cursor after changing tenant or filters.
// Authorization is always checked independently before decoding a cursor.
type usageCursor struct {
	At    time.Time `json:"at"`
	ID    uint64    `json:"id"`
	Scope string    `json:"scope"`
}

func usageCursorScope(filter domain.AdminUsageFilter) string {
	filter.Refresh = false
	filter.SkipTotal = false
	filter.Before = nil
	filter.BeforeID = 0
	filter.SnapshotID = ""
	data, _ := json.Marshal(filter)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

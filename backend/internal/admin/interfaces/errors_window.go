package interfaces

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"bosapi/backend/internal/admin/domain"
	"github.com/gin-gonic/gin"
)

// Keep the time window and tenant/filter scope fixed while paging. This cursor
// is separate from the legacy cursor used by the key portal.
type adminErrorWindowCursor struct {
	Version int                     `json:"v"`
	Filter  domain.AdminErrorFilter `json:"filter"`
}

func parseAdminErrorWindow(ctx *gin.Context, filter *domain.AdminErrorFilter, now time.Time) (int, error) {
	window := "5"
	if ctx.Request.URL.Query().Has("window_minutes") {
		window = ctx.Query("window_minutes")
	}
	minutes, err := strconv.Atoi(window)
	if err != nil {
		return 0, errors.New("window_minutes is invalid")
	}
	switch minutes {
	case 5, 15, 30, 60, 120, 360, 720, 1440:
	default:
		return 0, errors.New("window_minutes must be 5, 15, 30, 60, 120, 360, 720 or 1440")
	}
	end := now
	if ctx.Request.URL.Query().Has("to") {
		end, err = time.Parse(time.RFC3339Nano, ctx.Query("to"))
		if err != nil || end.IsZero() {
			return 0, errors.New("to is invalid")
		}
	}
	if value := ctx.Query("cursor"); value != "" {
		invalid := errors.New("cursor is invalid or does not match the filters; restart from the first page")
		if len(value) > 4096 {
			return 0, invalid
		}
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		var cursor adminErrorWindowCursor
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Version != 1 {
			return 0, invalid
		}
		previous := cursor.Filter
		if previous.OrganizationID != filter.OrganizationID || previous.APIKeyID != filter.APIKeyID || previous.ErrorCode != filter.ErrorCode ||
			previous.From.IsZero() || previous.To.IsZero() || previous.To.Sub(previous.From) != time.Duration(minutes)*time.Minute ||
			previous.BeforeID == 0 || previous.Before.Before(previous.From) || !previous.Before.Before(previous.To) {
			return 0, invalid
		}
		if ctx.Query("to") != "" && !end.Equal(previous.To) {
			return 0, invalid
		}
		*filter = previous
		return minutes, nil
	}
	filter.To = end.UTC()
	filter.From = filter.To.Add(-time.Duration(minutes) * time.Minute)
	return minutes, nil
}

func encodeAdminErrorCursor(filter domain.AdminErrorFilter, before time.Time, id uint64) string {
	filter.Before, filter.BeforeID = before, id
	encoded, _ := json.Marshal(adminErrorWindowCursor{Version: 1, Filter: filter})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

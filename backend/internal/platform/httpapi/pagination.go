package httpapi

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// ParsePagination defaults to page 1 and 20 items, with at most 100 items per page.
// An empty page_size falls back to the legacy limit alias. Non-empty invalid
// values are rejected, and page_size takes precedence over limit.
func ParsePagination(ctx *gin.Context) (page, pageSize int, valid bool) {
	page, ok := positiveQueryInt(ctx.Query("page"), 1)
	if !ok {
		return 0, 0, false
	}
	rawSize := ctx.Query("page_size")
	if rawSize == "" {
		rawSize = ctx.Query("limit")
	}
	pageSize, ok = positiveQueryInt(rawSize, 20)
	if !ok || pageSize > 100 {
		return 0, 0, false
	}
	return page, pageSize, true
}

func positiveQueryInt(raw string, fallback int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0
}

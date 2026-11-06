package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParsePagination(t *testing.T) {
	for _, test := range []struct {
		name, query string
		page, size  int
		valid       bool
	}{
		{name: "defaults", page: 1, size: 20, valid: true},
		{name: "explicit", query: "page=2&page_size=100", page: 2, size: 100, valid: true},
		{name: "minimum size", query: "page_size=1", page: 1, size: 1, valid: true},
		{name: "legacy limit", query: "limit=30", page: 1, size: 30, valid: true},
		{name: "empty size uses limit", query: "page_size=&limit=30", page: 1, size: 30, valid: true},
		{name: "size takes precedence", query: "page_size=10&limit=invalid", page: 1, size: 10, valid: true},
		{name: "empty defaults", query: "page=&page_size=&limit=", page: 1, size: 20, valid: true},
		{name: "zero page", query: "page=0"},
		{name: "negative page", query: "page=-1"},
		{name: "malformed page", query: "page=abc"},
		{name: "overflow page", query: "page=999999999999999999999999999999"},
		{name: "zero size", query: "page_size=0"},
		{name: "negative size", query: "page_size=-1"},
		{name: "oversized", query: "page_size=101"},
		{name: "malformed size", query: "page_size=abc"},
		{name: "overflow size", query: "page_size=999999999999999999999999999999"},
		{name: "invalid alias", query: "limit=abc"},
		{name: "oversized alias", query: "limit=101"},
		{name: "invalid size cannot fall back", query: "page_size=abc&limit=10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", "/?"+test.query, nil)
			page, size, valid := ParsePagination(ctx)
			if page != test.page || size != test.size || valid != test.valid {
				t.Fatalf("got (%d, %d, %v), want (%d, %d, %v)", page, size, valid, test.page, test.size, test.valid)
			}
		})
	}
}

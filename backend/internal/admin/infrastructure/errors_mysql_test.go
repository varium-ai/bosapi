package infrastructure

import (
	"context"
	"reflect"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

func TestAdminErrorPagesAcrossPhysicalTablesMySQL(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixtures := []struct {
		org, key uint64
		status   int
		code     string
	}{
		{1, 7, 502, ""}, {1, 7, 429, ""}, {1, 7, 400, ""}, {1, 7, 502, "provider_unavailable"},
		{2, 8, 500, ""}, {1, 9, 503, "upstream_error"}, {1, 7, 200, ""},
		{1, 7, 403, "security_blocked"}, {1, 7, 503, "security_inspection_unavailable"},
		{1, 7, 400, "tool_call_invalid"}, {1, 7, 403, "unauthorized_tool"}, {1, 7, 503, "tool_call_inspection_unavailable"},
		{1, 7, 502, ""}, {1, 7, 502, ""}, {1, 7, 429, "rate_limited"}, {1, 7, 404, ""},
		{2, 7, 400, ""}, {1, 7, 200, "upstream_error"},
	}
	for i, row := range fixtures {
		at := now
		if i == 0 {
			at = now.Add(-60 * 24 * time.Hour)
		}
		if err := db.Exec(`INSERT INTO usage_logs(id,organization_id,api_key_id,status_code,error_code,model,endpoint,total_tokens,cost_amount,created_at) VALUES (?,?,?,?,?,'example','chat/completions',0,0,?)`, i+1, row.org, row.key, row.status, row.code, at).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Moving even IDs preserves tied timestamps and global IDs across both tables.
	if err := db.Exec(`INSERT INTO usage_logs_v2 SELECT * FROM usage_logs WHERE MOD(id,2)=0; DELETE FROM usage_logs WHERE MOD(id,2)=0`).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewAdminErrorRepository(db)
	for _, tc := range []struct {
		name   string
		filter domain.AdminErrorFilter
		want   []uint64
	}{
		{"all", domain.AdminErrorFilter{}, []uint64{17, 16, 15, 14, 13, 6, 5, 4, 3, 2, 1}},
		{"organization", domain.AdminErrorFilter{OrganizationID: 1}, []uint64{16, 15, 14, 13, 6, 4, 3, 2, 1}},
		{"key and organization", domain.AdminErrorFilter{OrganizationID: 1, APIKeyID: 7}, []uint64{16, 15, 14, 13, 4, 3, 2, 1}},
		{"key across organizations", domain.AdminErrorFilter{APIKeyID: 7}, []uint64{17, 16, 15, 14, 13, 4, 3, 2, 1}},
		{"legacy and explicit upstream", domain.AdminErrorFilter{ErrorCode: "upstream_error"}, []uint64{14, 13, 6, 5, 1}},
		{"legacy and explicit rate limit", domain.AdminErrorFilter{ErrorCode: "rate_limited"}, []uint64{15, 2}},
		{"legacy invalid request", domain.AdminErrorFilter{ErrorCode: "invalid_request"}, []uint64{17, 16, 3}},
		{"explicit code", domain.AdminErrorFilter{ErrorCode: "provider_unavailable"}, []uint64{4}},
		{"missing organization", domain.AdminErrorFilter{OrganizationID: 99}, []uint64{}},
		{"security remains excluded", domain.AdminErrorFilter{ErrorCode: "security_blocked"}, []uint64{}},
		{"bounded recent window", domain.AdminErrorFilter{From: now.Add(-5 * time.Minute), To: now.Add(time.Millisecond)}, []uint64{17, 16, 15, 14, 13, 6, 5, 4, 3, 2}},
		{"exclusive upper boundary", domain.AdminErrorFilter{From: now.Add(-5 * time.Minute), To: now}, []uint64{}},
		{"inclusive lower boundary with legacy errors", domain.AdminErrorFilter{From: now, To: now.Add(time.Minute), ErrorCode: "upstream_error"}, []uint64{14, 13, 6, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := []uint64{}
			filter := tc.filter
			for page := 0; ; page++ {
				if page > len(fixtures) {
					t.Fatal("cursor did not advance")
				}
				items, more, err := repo.ListAdminErrors(ctx, filter, 2)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					got = append(got, item.ID)
					if item.ErrorCode == "" || item.Model != "example" || item.Endpoint != "chat/completions" || item.Outcome != "failed" {
						t.Fatalf("lost error metadata: %+v", item)
					}
				}
				if more != (len(got) < len(tc.want)) {
					t.Fatalf("incorrect has_more=%v after IDs=%v", more, got)
				}
				if !more {
					break
				}
				last := items[len(items)-1]
				filter.Before, filter.BeforeID = last.Time, last.ID
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

package infrastructure

import (
	"bosapi/backend/internal/platform/usagerollup"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAdminKeySnapshotRollupBoundariesMySQL(t *testing.T) {
	db := newKeyUsageMySQL(t)
	ctx := context.Background()
	nows := []time.Time{time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	for _, now := range nows {
		day, week, month := usagetime.PeriodStarts(now)
		for _, b := range []time.Time{day, week, month, now, now.Add(-720 * time.Hour)} {
			for _, delta := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
				if err := db.Exec("INSERT INTO usage_logs (organization_id,api_key_id,total_tokens,cost_amount,created_at) VALUES (1,7,3,0.12345678,?),(2,7,5,0.00000001,?)", b.Add(delta), b.Add(delta)).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, batch := range []int{0, 3, 1000} {
		if batch > 0 {
			if _, err := usagerollup.New(db).Batch(ctx, batch); err != nil {
				t.Fatal(err)
			}
		}
		for _, now := range nows {
			q, args := adminKeyUsageQuery([]domain.APIKey{{ID: 7}, {ID: 8}}, now)
			var want, got []adminKeyUsageRecord
			if err := db.Raw(q, args...).Scan(&want).Error; err != nil {
				t.Fatal(err)
			}
			q, args = adminKeyRollupQuery([]uint64{7, 8}, now)
			if err := db.Raw(q, args...).Scan(&got).Error; err != nil {
				t.Fatal(err)
			}
			// Empty Keys are filled with zero by the caller; SQL emits used Keys only.
			if len(got) != 1 || !reflect.DeepEqual(got[0], want[0]) {
				t.Fatalf("batch=%d now=%s got=%+v want=%+v", batch, now, got, want)
			}
		}
	}
}

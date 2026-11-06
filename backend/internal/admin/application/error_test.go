package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

type boundedErrorRepository struct{ calls int }

func (repo *boundedErrorRepository) ListAdminErrors(context.Context, domain.AdminErrorFilter, int) ([]domain.AdminError, bool, error) {
	repo.calls++
	return nil, false, nil
}

func TestAdminErrorServiceValidatesTimeBounds(t *testing.T) {
	end := time.Now().UTC()
	for _, tc := range []struct {
		name  string
		from  time.Time
		to    time.Time
		valid bool
	}{
		{"five minutes", end.Add(-5 * time.Minute), end, true},
		{"maximum", end.Add(-24 * time.Hour), end, true},
		{"too large", end.Add(-24*time.Hour - time.Millisecond), end, false},
		{"missing start", time.Time{}, end, false},
		{"missing end", end, time.Time{}, false},
		{"empty", end, end, false},
		{"reversed", end, end.Add(-time.Minute), false},
		{"legacy key portal", time.Time{}, time.Time{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &boundedErrorRepository{}
			_, _, err := NewAdminErrorService(repo).List(context.Background(), domain.AdminErrorFilter{From: tc.from, To: tc.to}, 20)
			if tc.valid {
				if err != nil || repo.calls != 1 {
					t.Fatalf("valid range failed: %v", err)
				}
			} else if !errors.Is(err, ErrInvalidAdminUsageFilter) || repo.calls != 0 {
				t.Fatalf("invalid range reached repository: %v calls=%d", err, repo.calls)
			}
		})
	}
}

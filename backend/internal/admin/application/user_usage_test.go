package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

type userUsageRepositoryStub struct {
	domain.AdminUsageRepository
	filter  domain.AdminOrganizationUsageFilter
	calls   int
	failure error
}

func (repo *userUsageRepositoryStub) DailyAdminOrganizationUsage(ctx context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) (domain.AdminOrganizationUsageDashboard, error) {
	repo.calls++
	repo.filter = filter
	if err := ctx.Err(); err != nil {
		return domain.AdminOrganizationUsageDashboard{}, err
	}
	return domain.AdminOrganizationUsageDashboard{Total: 5, Summary: domain.AdminUsagePoint{TotalTokens: 150}}, repo.failure
}

func TestValidateAdminOrganizationUsage(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	for _, test := range []struct {
		name    string
		change  func(*domain.AdminOrganizationUsageFilter, *int, *int)
		invalid bool
	}{
		{"one day", func(_ *domain.AdminOrganizationUsageFilter, _, _ *int) {}, false},
		{"maximum range", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { v := start.AddDate(0, 0, 366); f.To = &v }, false},
		{"oversized range", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { v := start.AddDate(0, 0, 367); f.To = &v }, true},
		{"missing start", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.From = nil }, true},
		{"missing end", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.To = nil }, true},
		{"equal boundaries", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.To = f.From }, true},
		{"reversed range", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.From, f.To = f.To, f.From }, true},
		{"long model", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.Model = strings.Repeat("m", 192) }, true},
		{"long search", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.Search = strings.Repeat("s", 321) }, true},
		{"maximum search", func(f *domain.AdminOrganizationUsageFilter, _, _ *int) { f.Search = strings.Repeat("s", 320) }, false},
		{"zero page", func(_ *domain.AdminOrganizationUsageFilter, p, _ *int) { *p = 0 }, true},
		{"zero size", func(_ *domain.AdminOrganizationUsageFilter, _, s *int) { *s = 0 }, true},
		{"oversized page", func(_ *domain.AdminOrganizationUsageFilter, _, s *int) { *s = 101 }, true},
		{"maximum size", func(_ *domain.AdminOrganizationUsageFilter, _, s *int) { *s = 100 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			filter := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &start, To: &end}}
			page, size := 1, 20
			test.change(&filter, &page, &size)
			err := validateAdminOrganizationUsage(filter, page, size)
			if errors.Is(err, ErrInvalidAdminUsageFilter) != test.invalid {
				t.Fatalf("error=%v invalid=%v", err, test.invalid)
			}
			repo := &userUsageRepositoryStub{}
			_, err = NewAdminUsageService(repo).DailyByOrganization(context.Background(), filter, page, size)
			if errors.Is(err, ErrInvalidAdminUsageFilter) != test.invalid || test.invalid && repo.calls != 0 {
				t.Fatalf("error=%v calls=%d", err, repo.calls)
			}
		})
	}
}

func TestDailyUserUsageService(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	failure := errors.New("database unavailable")
	for _, test := range []struct {
		name     string
		failure  error
		canceled bool
	}{
		{name: "full summary"}, {name: "repository error", failure: failure}, {name: "canceled", canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &userUsageRepositoryStub{failure: test.failure}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if test.canceled {
				cancel()
			}
			result, err := NewAdminUsageService(repo).DailyByOrganization(ctx, domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &start, To: &end}, Search: " alice "}, 2, 1)
			want := test.failure
			if test.canceled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
			if want == nil && (result.Total != 5 || result.Summary.TotalTokens != 150 || repo.filter.Search != "alice") {
				t.Fatalf("result=%+v filter=%+v", result, repo.filter)
			}
		})
	}
}

type aggregateUsageRepositoryStub struct {
	domain.AdminUsageRepository
	points []domain.AdminUsagePoint
}

func (repo *aggregateUsageRepositoryStub) AggregateAdminUsage(context.Context, domain.AdminUsageFilter, string) ([]domain.AdminUsagePoint, error) {
	return repo.points, nil
}

func TestAggregateCacheHitRate(t *testing.T) {
	repo := &aggregateUsageRepositoryStub{points: []domain.AdminUsagePoint{
		{InputTokens: 100, CacheReadTokens: 40, CacheWriteTokens: 10},
		{InputTokens: 0, CacheReadTokens: 1},
	}}
	points, err := NewAdminUsageService(repo).Aggregate(context.Background(), domain.AdminUsageFilter{}, "total")
	if err != nil {
		t.Fatal(err)
	}
	if points[0].CacheHitRate != 0.4 || points[1].CacheHitRate != 0 {
		t.Fatalf("points=%+v", points)
	}
}

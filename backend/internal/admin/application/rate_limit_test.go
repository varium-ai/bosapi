package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

type rateLimitRepositoryStub struct {
	writes    int
	update    domain.RateLimitUpdate
	updatedBy uint64
	err       error
}

func (r *rateLimitRepositoryStub) ReadRateLimits(context.Context, uint64, uint64) (domain.RateLimitUpdate, error) {
	return r.update, r.err
}
func (r *rateLimitRepositoryStub) WriteRateLimits(_ context.Context, update domain.RateLimitUpdate, by uint64) error {
	r.writes++
	r.update = update
	r.updatedBy = by
	return r.err
}

func TestOrganizationQuotaValidation(t *testing.T) {
	tokenFields := []struct {
		name string
		set  func(*domain.OrganizationRateLimit, *int64)
	}{
		{"daily", func(v *domain.OrganizationRateLimit, n *int64) { v.DailyQuotaTokens = n }},
		{"weekly", func(v *domain.OrganizationRateLimit, n *int64) { v.WeeklyQuotaTokens = n }},
		{"monthly", func(v *domain.OrganizationRateLimit, n *int64) { v.MonthlyQuotaTokens = n }},
	}
	for _, field := range tokenFields {
		for _, value := range []int64{-1, 0, 1, math.MaxInt64} {
			t.Run(field.name+" tokens/"+fmt.Sprint(value), func(t *testing.T) {
				config := domain.OrganizationRateLimit{OrganizationID: 2}
				field.set(&config, &value)
				checkQuotaValidation(t, config, value >= 0)
			})
		}
	}
	amountFields := []struct {
		name string
		set  func(*domain.OrganizationRateLimit, *float64)
	}{
		{"daily", func(v *domain.OrganizationRateLimit, n *float64) { v.DailyQuotaAmount = n }},
		{"weekly", func(v *domain.OrganizationRateLimit, n *float64) { v.WeeklyQuotaAmount = n }},
		{"monthly", func(v *domain.OrganizationRateLimit, n *float64) { v.MonthlyQuotaAmount = n }},
	}
	for _, field := range amountFields {
		for _, value := range []float64{-1, 0, 0.00000001, 12.5, math.NaN(), math.Inf(1), math.Inf(-1)} {
			t.Run(field.name+" amount/"+fmt.Sprint(value), func(t *testing.T) {
				config := domain.OrganizationRateLimit{OrganizationID: 2}
				field.set(&config, &value)
				checkQuotaValidation(t, config, value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value))
			})
		}
	}
	t.Run("unlimited", func(t *testing.T) { checkQuotaValidation(t, domain.OrganizationRateLimit{OrganizationID: 2}, true) })
	for _, qps := range []int64{-1, 0, 100000000, 100000001} {
		t.Run("qps/"+fmt.Sprint(qps), func(t *testing.T) {
			checkQuotaValidation(t, domain.OrganizationRateLimit{OrganizationID: 2, QPS: qps}, qps >= 0 && qps <= 100000000)
		})
	}
}
func checkQuotaValidation(t *testing.T, config domain.OrganizationRateLimit, valid bool) {
	t.Helper()
	repository := &rateLimitRepositoryStub{}
	err := NewRateLimitService(repository).Write(context.Background(), domain.RateLimitUpdate{Organizations: []domain.OrganizationRateLimit{config}}, 7)
	if valid {
		if err != nil || repository.writes != 1 || repository.updatedBy != 7 {
			t.Fatalf("writes=%d by=%d err=%v", repository.writes, repository.updatedBy, err)
		}
	} else if !errors.Is(err, ErrInvalidRateLimit) || repository.writes != 0 {
		t.Fatalf("invalid update persisted: writes=%d err=%v", repository.writes, err)
	}
}
func TestRateLimitValidationPreservesAtomicUpdateRules(t *testing.T) {
	negative := int64(-1)
	for _, update := range []domain.RateLimitUpdate{
		{}, {Organizations: []domain.OrganizationRateLimit{{}}},
		{Organizations: []domain.OrganizationRateLimit{{OrganizationID: 2}, {OrganizationID: 2}}},
		{Global: &domain.RateLimitConfig{QPS: -1}}, {Global: &domain.RateLimitConfig{Quota5H: &negative}},
		{Keys: []domain.KeyRateLimit{{KeyID: 0}}},
		{Keys: []domain.KeyRateLimit{{KeyID: 2}, {KeyID: 2}}},
		{Keys: []domain.KeyRateLimit{{KeyID: 2, RateLimitConfig: domain.RateLimitConfig{Quota7D: &negative}}}},
	} {
		repository := &rateLimitRepositoryStub{}
		if err := NewRateLimitService(repository).Write(context.Background(), update, 7); !errors.Is(err, ErrInvalidRateLimit) || repository.writes != 0 {
			t.Fatalf("update=%+v writes=%d err=%v", update, repository.writes, err)
		}
	}
}
func TestRateLimitServicePropagatesRepositoryErrors(t *testing.T) {
	failure := errors.New("storage failed")
	service := NewRateLimitService(&rateLimitRepositoryStub{err: failure})
	if _, err := service.Read(context.Background(), 2, 0); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := service.Write(context.Background(), domain.RateLimitUpdate{Organizations: []domain.OrganizationRateLimit{{OrganizationID: 2}}}, 7); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

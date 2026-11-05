package infrastructure

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

type quotaJSONArgument struct {
	value   domain.OrganizationRateLimit
	encoded *string
}

func (a quotaJSONArgument) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	var got domain.OrganizationRateLimit
	if json.Unmarshal([]byte(text), &got) != nil || !reflect.DeepEqual(got, a.value) {
		return false
	}
	*a.encoded = text
	return true
}
func TestRateLimitOrganizationPersistence(t *testing.T) {
	zero := int64(0)
	weekly := int64(20)
	monthly := int64(30)
	amountZero := float64(0)
	weeklyAmount := 2.5
	monthlyAmount := 3.5
	for _, test := range []struct {
		name  string
		value domain.OrganizationRateLimit
	}{
		{"all quotas", domain.OrganizationRateLimit{OrganizationID: 2, QPS: 10, DailyQuotaTokens: &zero, WeeklyQuotaTokens: &weekly, MonthlyQuotaTokens: &monthly, DailyQuotaAmount: &amountZero, WeeklyQuotaAmount: &weeklyAmount, MonthlyQuotaAmount: &monthlyAmount}},
		{"clear quotas", domain.OrganizationRateLimit{OrganizationID: 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			repository := NewRateLimitRepository(db)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organizations` WHERE id = \\?").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			var encoded string
			mock.ExpectExec("INSERT INTO `settings` .*ON DUPLICATE KEY UPDATE `value_text`=VALUES\\(`value_text`\\),`updated_by`=VALUES\\(`updated_by`\\),`updated_at`=VALUES\\(`updated_at`\\)").WithArgs("rate_limit.organization.2", quotaJSONArgument{test.value, &encoded}, []byte(nil), false, uint64(7), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := repository.WriteRateLimits(context.Background(), domain.RateLimitUpdate{Organizations: []domain.OrganizationRateLimit{test.value}}, 7); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"rate_limit.global", "rate_limit.organization.2"} {
				rows := sqlmock.NewRows([]string{"setting_key", "value_text"})
				if key == "rate_limit.organization.2" {
					rows.AddRow(key, encoded)
				}
				mock.ExpectQuery("SELECT .* FROM `settings` WHERE setting_key = \\? LIMIT \\?").WithArgs(key, 1).WillReturnRows(rows)
			}
			got, err := repository.ReadRateLimits(context.Background(), 2, 0)
			if err != nil || len(got.Organizations) != 1 || !reflect.DeepEqual(got.Organizations[0], test.value) {
				t.Fatalf("read=%+v err=%v", got, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRateLimitReadLegacyNullAndMissingSettings(t *testing.T) {
	for _, text := range []string{"", `{"qps":12}`, `{"organization_id":999,"qps":12,"daily_quota_tokens":null,"weekly_quota_tokens":null,"monthly_quota_tokens":null,"daily_quota_amount":null,"weekly_quota_amount":null,"monthly_quota_amount":null}`} {
		t.Run(text, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			mock.ExpectQuery("SELECT .* FROM `settings`").WithArgs("rate_limit.global", 1).WillReturnRows(sqlmock.NewRows([]string{"value_text"}))
			rows := sqlmock.NewRows([]string{"value_text"})
			if text != "" {
				rows.AddRow(text)
			}
			mock.ExpectQuery("SELECT .* FROM `settings`").WithArgs("rate_limit.organization.2", 1).WillReturnRows(rows)
			got, err := NewRateLimitRepository(db).ReadRateLimits(context.Background(), 2, 0)
			want := domain.OrganizationRateLimit{OrganizationID: 2}
			if text != "" {
				want.QPS = 12
			}
			if err != nil || len(got.Organizations) != 1 || !reflect.DeepEqual(got.Organizations[0], want) {
				t.Fatalf("read=%+v err=%v", got, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRateLimitUpdateRollsBackAsAUnit(t *testing.T) {
	for _, failure := range []string{"missing organization", "missing key", "write failure"} {
		t.Run(failure, func(t *testing.T) {
			db, mock := newOrganizationSyncDatabase(t)
			mock.ExpectBegin()
			count := 1
			if failure == "missing organization" {
				count = 0
			}
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `organizations`").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
			if count > 0 {
				keyCount := 1
				if failure == "missing key" {
					keyCount = 0
				}
				mock.ExpectQuery("SELECT count\\(\\*\\) FROM `api_keys` WHERE id = \\? AND status <> \\?").WithArgs(uint64(9), "deleted").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(keyCount))
			}
			want := error(gorm.ErrRecordNotFound)
			if failure == "write failure" {
				mock.ExpectExec("INSERT INTO `settings`").WithArgs("rate_limit.global", `{"qps":10}`, []byte(nil), false, uint64(7), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				want = errors.New("failed second write")
				mock.ExpectExec("INSERT INTO `settings`").WillReturnError(want)
			}
			mock.ExpectRollback()
			err := NewRateLimitRepository(db).WriteRateLimits(context.Background(), domain.RateLimitUpdate{Global: &domain.RateLimitConfig{QPS: 10}, Organizations: []domain.OrganizationRateLimit{{OrganizationID: 2}}, Keys: []domain.KeyRateLimit{{KeyID: 9}}}, 7)
			if !errors.Is(err, want) {
				t.Fatalf("err=%v want=%v", err, want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRateLimitPersistenceHonorsCanceledContext(t *testing.T) {
	db, mock := newOrganizationSyncDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := NewRateLimitRepository(db)
	if _, err := repository.ReadRateLimits(ctx, 2, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error=%v", err)
	}
	if err := repository.WriteRateLimits(ctx, domain.RateLimitUpdate{Organizations: []domain.OrganizationRateLimit{{OrganizationID: 2}}}, 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("write error=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

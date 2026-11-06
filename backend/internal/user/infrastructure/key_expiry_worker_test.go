package infrastructure

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestDisableExpiredKeysUsesInclusiveCutoffAndPersistsDisabledStatus(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			mock.ExpectBegin()
			update := mock.ExpectExec(regexp.QuoteMeta("UPDATE `api_keys` SET `status`=?,`updated_at`=? WHERE status = ? AND expires_at IS NOT NULL AND expires_at <= ?")).WithArgs("disabled", now, "active", now)
			failure := errors.New("database unavailable")
			if failed {
				update.WillReturnError(failure)
				mock.ExpectRollback()
			} else {
				update.WillReturnResult(sqlmock.NewResult(0, 2))
				mock.ExpectCommit()
			}
			err = NewKeyRepository(db).DisableExpiredKeys(context.Background(), now)
			if failed && !errors.Is(err, failure) || !failed && err != nil {
				t.Fatalf("unexpected sweep error: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type expiryRepositoryFake struct{ calls chan time.Time }

func (fake expiryRepositoryFake) DisableExpiredKeys(ctx context.Context, now time.Time) error {
	select {
	case fake.calls <- now:
	case <-ctx.Done():
	}
	return errors.New("temporary failure")
}

func TestKeyExpiryWorkerRetriesAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan time.Time, 2)
	ticks := make(chan time.Time)
	done := make(chan struct{})
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	worker := &KeyExpiryWorker{}
	go func() {
		defer close(done)
		worker.run(ctx, expiryRepositoryFake{calls}, ticks, func() time.Time { return now })
	}()
	if got := <-calls; !got.Equal(now) {
		t.Fatal("worker did not use supplied time")
	}
	ticks <- now
	<-calls
	cancel()
	<-done
}

func TestKeyExpiryWorkerCloseIsIdempotent(t *testing.T) {
	worker := NewKeyExpiryWorker(expiryRepositoryFake{make(chan time.Time, 2)})
	worker.Close()
	worker.Close()
}

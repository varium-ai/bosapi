package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestVerificationUpdatesOnlyTheTestedVersionAndPreservesConfigurationTime(t *testing.T) {
	for _, passed := range []bool{true, false} {
		sqlDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
		if err != nil {
			t.Fatal(err)
		}
		version := time.Now().UTC().Truncate(time.Millisecond)
		var value any
		if passed {
			value = sqlmock.AnyArg()
		}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `accounts` SET `verified_at`=\\? WHERE id = \\? AND updated_at = \\?").WithArgs(value, uint64(7), version).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
		if err := NewProviderRepository(db).RecordProviderVerification(context.Background(), 7, version, passed); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		sqlDB.Close()
	}
}

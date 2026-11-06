package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRequestLogPartitionsExpandBeforeDroppingExpiredHistory(t *testing.T) {
	database, mock := requestLogsTestDatabase(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	var additions []string
	for day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC); day.Before(time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)); day = day.AddDate(0, 0, 1) {
		additions = append(additions, fmt.Sprintf("PARTITION p%s VALUES LESS THAN ('%s')", day.Format("20060102"), day.AddDate(0, 0, 1).Format("2006-01-02")))
	}
	additions = append(additions, "PARTITION p_future VALUES LESS THAN (MAXVALUE)")
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE request_logs_v2 REORGANIZE PARTITION p_future INTO (" + strings.Join(additions, ", ") + ")")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE request_logs_v2 DROP PARTITION `p_before`")).WillReturnResult(sqlmock.NewResult(0, 0))
	err := maintainRequestLogPartitionBounds(database.WithContext(context.Background()), []requestLogPartition{
		{"p_before", "'2026-10-23'"}, {"p_future", "MAXVALUE"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestLogPartitionsKeepPartialRetentionDay(t *testing.T) {
	database, mock := requestLogsTestDatabase(t)
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE request_logs_v2 DROP PARTITION `p20261022`")).WillReturnResult(sqlmock.NewResult(0, 0))
	err := maintainRequestLogPartitionBounds(database, []requestLogPartition{
		{"p20261022", "'2026-10-23'"}, {"p20261023", "'2026-10-24'"},
		{"p20261106", "'2026-11-07'"}, {"p_future", "MAXVALUE"},
	}, time.Date(2026, 10, 30, 23, 59, 59, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestLogPartitionsRejectUnexpectedLayout(t *testing.T) {
	for _, partitions := range [][]requestLogPartition{
		{{"p20261106", "'2026-11-07'"}},
		{{"p_future", "MAXVALUE"}},
		{{"p_before", "not-a-date"}, {"p_future", "MAXVALUE"}},
		{{"p_before`; DROP TABLE request_logs", "'2026-10-23'"}, {"p_future", "MAXVALUE"}},
	} {
		database, mock := requestLogsTestDatabase(t)
		if err := maintainRequestLogPartitionBounds(database, partitions, time.Now()); err == nil {
			t.Fatalf("accepted invalid layout: %+v", partitions)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestLogPartitionMaintenanceSerializesAndRestoresSession(t *testing.T) {
	for _, test := range []struct {
		name     string
		acquired int
		failure  error
	}{
		{name: "another replica owns maintenance", acquired: 0},
		{name: "successful cleanup", acquired: 1},
		{name: "DDL failure releases lock and restores timeout", acquired: 1, failure: errors.New("metadata lock unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, mock := requestLogsTestDatabase(t)
			metadata := func() {
				mock.ExpectQuery("SELECT PARTITION_NAME AS partition_name").WillReturnRows(sqlmock.NewRows([]string{"partition_name", "partition_description"}).
					AddRow("p_before", "'2026-10-23'").AddRow("p20261106", "'2026-11-07'").AddRow("p_future", "MAXVALUE"))
			}
			metadata()
			mock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(" + requestLogPartitionLock + ", 0)")).
				WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(test.acquired))
			if test.acquired == 1 {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT @@SESSION.lock_wait_timeout")).WillReturnRows(sqlmock.NewRows([]string{"timeout"}).AddRow(123))
				mock.ExpectExec("SET SESSION lock_wait_timeout = 1").WillReturnResult(sqlmock.NewResult(0, 0))
				metadata()
				ddl := mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE request_logs_v2 DROP PARTITION `p_before`"))
				if test.failure != nil {
					ddl.WillReturnError(test.failure)
				} else {
					ddl.WillReturnResult(sqlmock.NewResult(0, 0))
				}
				mock.ExpectExec("SET SESSION lock_wait_timeout = 123").WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery(regexp.QuoteMeta("SELECT RELEASE_LOCK(" + requestLogPartitionLock + ")")).WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
			}
			repository := &RequestLogRepository{database: database}
			partitioned, err := repository.maintainRequestLogPartitions(context.Background(), time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
			if !partitioned || !errors.Is(err, test.failure) {
				t.Fatalf("partitioned=%v err=%v, want %v", partitioned, err, test.failure)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

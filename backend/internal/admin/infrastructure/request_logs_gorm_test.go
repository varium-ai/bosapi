package infrastructure

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"bosapi/backend/internal/admin/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func requestLogsTestDatabase(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	return database, mock
}

func expectRequestLogCleanup(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT PARTITION_NAME AS partition_name").
		WillReturnRows(sqlmock.NewRows([]string{"partition_name", "partition_description"}).AddRow("p_future", "MAXVALUE"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(" + requestLogPartitionLock + ", 0)")).
		WillReturnRows(sqlmock.NewRows([]string{"lock"}).AddRow(0))
}

func TestRequestLogCleanupTimeoutStillAllowsLogPersistence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		database, mock := requestLogsTestDatabase(t)
		mock.ExpectQuery("SELECT PARTITION_NAME AS partition_name").
			WillDelayFor(4 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"partition_name", "partition_description"}))
		values := requestLogEntries(1)
		expectRequestLogBatch(mock, values).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		started := time.Now()
		repository := NewRequestLogRepository(database)
		enqueueRequestLogs(t, repository, values)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		repository.Close()
		if elapsed := time.Since(started); elapsed != 3*time.Second {
			t.Fatalf("cleanup blocked persistence for %v, want 3s maximum", elapsed)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func requestLogEntries(count int) []domain.RequestLog {
	values := make([]domain.RequestLog, count)
	for i := range values {
		values[i] = domain.RequestLog{
			CreatedAt: time.Date(2026, 10, 29, 6, 0, i, 0, time.UTC),
			Level:     "INFO", Event: "gateway.request.completed", Message: fmt.Sprintf("sequence=%d", i),
			RequestID: fmt.Sprintf("req_%d", i), ClientIP: "192.0.2.1", OrganizationID: uint64(i%3 + 1), APIKeyMasked: "sk-gw••••••",
		}
	}
	return values
}

func expectRequestLogBatch(mock sqlmock.Sqlmock, values []domain.RequestLog) *sqlmock.ExpectedExec {
	query := "INSERT INTO `request_logs_v2` (`created_at`,`level`,`event`,`message`,`request_id`,`client_ip`,`organization_id`,`api_key_masked`) VALUES " +
		strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?,?),", len(values)), ",")
	args := make([]driver.Value, 0, len(values)*8)
	for _, value := range values {
		args = append(args, value.CreatedAt, value.Level, value.Event, value.Message, value.RequestID, value.ClientIP, value.OrganizationID, value.APIKeyMasked)
	}
	mock.ExpectBegin()
	return mock.ExpectExec(regexp.QuoteMeta(query)).WithArgs(args...)
}

func enqueueRequestLogs(t *testing.T, repository *RequestLogRepository, values []domain.RequestLog) {
	t.Helper()
	for _, value := range values {
		if err := repository.AppendRequestLog(context.Background(), value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestLogsFlushPartialBatchOnInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		database, mock := requestLogsTestDatabase(t)
		expectRequestLogCleanup(mock)
		values := requestLogEntries(2)
		expectRequestLogBatch(mock, values).WillReturnResult(sqlmock.NewResult(1, 2))
		mock.ExpectCommit()
		repository := NewRequestLogRepository(database)
		defer repository.Close()
		enqueueRequestLogs(t, repository, values)
		synctest.Wait()
		if err := mock.ExpectationsWereMet(); err == nil {
			t.Fatal("partial batch was written before the flush interval")
		}
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRequestLogsFlushFullBatchWithoutWaitingForInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		database, mock := requestLogsTestDatabase(t)
		expectRequestLogCleanup(mock)
		values := requestLogEntries(100)
		expectRequestLogBatch(mock, values).WillReturnResult(sqlmock.NewResult(1, 100))
		mock.ExpectCommit()
		repository := NewRequestLogRepository(database)
		defer repository.Close()
		started := time.Now()
		enqueueRequestLogs(t, repository, values)
		synctest.Wait()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != 0 {
			t.Fatal("a full batch waited for a timer")
		}
	})
}

func TestRequestLogsCloseDrainsBoundedBatchesAndRejectsNewEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		database, mock := requestLogsTestDatabase(t)
		expectRequestLogCleanup(mock)
		values := requestLogEntries(250)
		for _, batch := range [][]domain.RequestLog{values[:100], values[100:200], values[200:]} {
			expectRequestLogBatch(mock, batch).WillReturnResult(sqlmock.NewResult(1, int64(len(batch))))
			mock.ExpectCommit()
		}
		repository := NewRequestLogRepository(database)
		enqueueRequestLogs(t, repository, values)
		repository.Close()
		repository.Close()
		if err := repository.AppendRequestLog(context.Background(), values[0]); err == nil {
			t.Fatal("accepted a log after Close")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRequestLogsFailedBatchIsNotRetriedAndNextBatchContinues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var output bytes.Buffer
		previousWriter := log.Writer()
		log.SetOutput(&output)
		defer log.SetOutput(previousWriter)
		database, mock := requestLogsTestDatabase(t)
		expectRequestLogCleanup(mock)
		values := requestLogEntries(101)
		expectRequestLogBatch(mock, values[:100]).WillReturnError(errors.New("database unavailable"))
		mock.ExpectRollback()
		expectRequestLogBatch(mock, values[100:]).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		repository := NewRequestLogRepository(database)
		enqueueRequestLogs(t, repository, values)
		repository.Close()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		if strings.Count(output.String(), "event=request_logs.persist_failed ") != 1 || !strings.Contains(output.String(), "batch_size=100") {
			t.Fatalf("expected one batch failure diagnostic, got %q", output.String())
		}
	})
}

func TestRequestLogsCloseBoundsTotalDrainTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		database, mock := requestLogsTestDatabase(t)
		expectRequestLogCleanup(mock)
		values := requestLogEntries(400)
		for _, batch := range [][]domain.RequestLog{values[:100], values[100:200]} {
			expectRequestLogBatch(mock, batch).WillDelayFor(2 * time.Second).WillReturnResult(sqlmock.NewResult(1, 100))
			mock.ExpectCommit()
		}
		expectRequestLogBatch(mock, values[200:300]).WillDelayFor(2 * time.Second).WillReturnResult(sqlmock.NewResult(1, 100))
		mock.ExpectRollback()
		repository := NewRequestLogRepository(database)
		enqueueRequestLogs(t, repository, values)
		started := time.Now()
		repository.Close()
		synctest.Wait()
		if elapsed := time.Since(started); elapsed != 5*time.Second {
			t.Fatalf("shutdown took %v, want 5s total", elapsed)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRequestLogsQueueRemainsBoundedAndSkipsUnscopedEntries(t *testing.T) {
	repository := &RequestLogRepository{queue: make(chan domain.RequestLog, 1)}
	if err := repository.AppendRequestLog(context.Background(), domain.RequestLog{}); err != nil || len(repository.queue) != 0 {
		t.Fatalf("unscoped log queued: depth=%d err=%v", len(repository.queue), err)
	}
	value := requestLogEntries(1)[0]
	if err := repository.AppendRequestLog(context.Background(), value); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendRequestLog(context.Background(), value); err == nil {
		t.Fatal("expected a full queue error")
	}
}

func TestRequestLogsConcurrentAppendAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		database, mock := requestLogsTestDatabase(t)
		expectRequestLogCleanup(mock)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO `request_logs_v2`").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		var persisted atomic.Int64
		if err := database.Callback().Create().Before("gorm:create").Register("test:count_logs", func(tx *gorm.DB) {
			persisted.Add(int64(len(*tx.Statement.Dest.(*[]requestLogRecord))))
		}); err != nil {
			t.Fatal(err)
		}
		repository := NewRequestLogRepository(database)
		value := requestLogEntries(1)[0]
		enqueueRequestLogs(t, repository, []domain.RequestLog{value})
		var accepted atomic.Int64
		accepted.Store(1)
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				if repository.AppendRequestLog(context.Background(), value) == nil {
					accepted.Add(1)
				}
			})
		}
		workers.Go(repository.Close)
		workers.Go(repository.Close)
		workers.Wait()
		if persisted.Load() != accepted.Load() {
			t.Fatalf("persisted %d logs, accepted %d", persisted.Load(), accepted.Load())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

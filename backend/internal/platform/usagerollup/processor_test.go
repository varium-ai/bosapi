package usagerollup

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestBatchAtomicity(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, stage := range []string{"success", "empty", "select error", "sum error", "ack error", "ack mismatch", "commit error"} {
		t.Run(stage, func(t *testing.T) {
			connection, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			selectQuery := mock.ExpectQuery("SELECT id FROM usage_logs FORCE INDEX .* WHERE rollup_applied = 0 ORDER BY id LIMIT \\? FOR UPDATE SKIP LOCKED").WithArgs(100)
			rows := sqlmock.NewRows([]string{"id"})
			if stage == "select error" {
				selectQuery.WillReturnError(failure)
			} else {
				if stage != "empty" {
					rows.AddRow(1).AddRow(3)
				}
				selectQuery.WillReturnRows(rows)
			}
			if stage != "empty" && stage != "select error" {
				pattern := regexp.QuoteMeta(strings.ReplaceAll(accumulateSQL, "IN ?", "IN (?,?)"))
				// Bind selected IDs; never interpolate them into the statement.
				sum := mock.ExpectExec(pattern).WithArgs(uint64(1), uint64(3))
				if stage == "sum error" {
					sum.WillReturnError(failure)
				} else {
					sum.WillReturnResult(sqlmock.NewResult(0, 1))
					ack := mock.ExpectExec(`UPDATE usage_logs SET rollup_applied = 1 WHERE rollup_applied = 0 AND id IN \(\?,\?\)`).WithArgs(uint64(1), uint64(3))
					switch stage {
					case "ack error":
						ack.WillReturnError(failure)
					case "ack mismatch":
						ack.WillReturnResult(sqlmock.NewResult(0, 1))
					default:
						ack.WillReturnResult(sqlmock.NewResult(0, 2))
					}
				}
			}
			if stage == "success" || stage == "empty" {
				mock.ExpectCommit()
			} else if stage == "commit error" {
				mock.ExpectCommit().WillReturnError(failure)
			} else {
				mock.ExpectRollback()
			}
			count, err := New(database).batchTable(context.Background(), 100, "usage_logs")
			if stage == "success" {
				if err != nil || count != 2 {
					t.Fatalf("count=%d error=%v", count, err)
				}
			} else if stage == "empty" {
				if err != nil || count != 0 {
					t.Fatalf("count=%d error=%v", count, err)
				}
			} else if err == nil || count != 0 {
				t.Fatalf("failure must not advance progress: count=%d error=%v", count, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBatchRejectsUnboundedSizes(t *testing.T) {
	for _, size := range []int{-1, 0, 5001} {
		if _, err := New(nil).Batch(context.Background(), size); err == nil {
			t.Fatalf("invalid size=%d", size)
		}
	}
}

func TestWorkerStopsInFlightBatchAndCanStopTwice(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	stop := start(func(ctx context.Context, size int) (int, error) {
		defer close(finished)
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	stop()
	stop()
	select {
	case <-finished:
	default:
		t.Fatal("worker stopped before the transaction finished")
	}
}

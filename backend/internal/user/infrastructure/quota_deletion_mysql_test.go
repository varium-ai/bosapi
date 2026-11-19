package infrastructure

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

func TestGatewayQuotaMySQLDeletionWaitsForSettlement(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	type pauseSettlement struct{}
	locked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	// Pause after the actual key read, before any receipt or quota writes.
	const callback = "test:pause_quota_after_key_read"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "api_keys" || tx.Statement.Context.Value(pauseSettlement{}) == nil {
			return
		}
		close(locked)
		select {
		case <-release:
		case <-ctx.Done():
			tx.AddError(ctx.Err())
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(callback)
	record := partitionUsage(now, "concurrent-key-delete")
	type settlementResult struct {
		inserted bool
		err      error
	}
	done := make(chan settlementResult, 1)
	go func() {
		inserted, err := repository.RecordGatewayUsageAndQuota(context.WithValue(ctx, pauseSettlement{}, true), record)
		done <- settlementResult{inserted, err}
	}()
	select {
	case <-locked:
	case result := <-done:
		t.Fatalf("settlement ended before key read: %+v", result)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A separate connection must not delete the key while settlement holds it.
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET SESSION innodb_lock_wait_timeout = 1").Error; err != nil {
			return err
		}
		return NewKeyRepository(tx).DeleteOwned(ctx, record.APIKeyID, 0, record.OrganizationID)
	})
	unblock()
	select {
	case result := <-done:
		if result.err != nil || !result.inserted {
			t.Fatalf("settlement=%+v", result)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var lockError *drivermysql.MySQLError
	if !errors.As(err, &lockError) || lockError.Number != 1205 {
		t.Fatalf("concurrent deletion was not blocked by settlement: %v", err)
	}
	var tokens uint64
	if err := db.Raw("SELECT quota_used_tokens FROM api_keys WHERE id = 7").Scan(&tokens).Error; err != nil || tokens != record.TotalTokens {
		t.Fatalf("tokens=%d err=%v", tokens, err)
	}
	if err := repository.DeleteOwned(ctx, record.APIKeyID, 0, record.OrganizationID); err != nil {
		t.Fatalf("delete after commit: %v", err)
	}
	if inserted, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || inserted {
		t.Fatalf("retry after deletion: inserted=%v err=%v", inserted, err)
	}
}

func TestGatewayQuotaMySQLUnchangedZeroUsage(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	record := partitionUsage(now, "zero-usage")
	record.InputTokens, record.OutputTokens, record.TotalTokens, record.CostAmount = 0, 0, 0, 0
	if err := db.Exec("UPDATE api_keys SET quota_used_tokens = 0, quota_used_amount = 0, last_used_at = ? WHERE id = 7", now).Error; err != nil {
		t.Fatal(err)
	}
	if inserted, err := repository.RecordGatewayUsageAndQuota(t.Context(), record); err != nil || !inserted {
		t.Fatalf("zero-usage settlement: inserted=%v err=%v", inserted, err)
	}
	if inserted, err := repository.RecordGatewayUsageAndQuota(t.Context(), record); err != nil || inserted {
		t.Fatalf("zero-usage retry: inserted=%v err=%v", inserted, err)
	}
}

package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/geoip"
	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/platform/logging"
	"gorm.io/gorm"
)

const (
	requestLogRetentionPeriod = 7 * 24 * time.Hour
	requestLogCleanupInterval = time.Minute
	requestLogCleanupTimeout  = 3 * time.Second
	requestLogBatchSize       = 100
	requestLogFlushInterval   = 100 * time.Millisecond
	requestLogShutdownTimeout = 5 * time.Second
)

type requestLogRecord struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	LegacyID       *uint64
	CreatedAt      time.Time `gorm:"primaryKey"`
	Level          string    `gorm:"size:16;not null"`
	Event          string    `gorm:"size:128;not null"`
	Message        string    `gorm:"size:2048;not null"`
	RequestID      string    `gorm:"size:128;not null;index:idx_request_logs_org_request_time,priority:2;index:idx_request_logs_request_time,priority:1"`
	ClientIP       string    `gorm:"size:64;not null"`
	OrganizationID uint64    `gorm:"not null;index:idx_request_logs_org_request_time,priority:1"`
	APIKeyMasked   string    `gorm:"size:128;not null"`
}

// TableName is the empty partitioned destination introduced by migration 021.
func (requestLogRecord) TableName() string { return "request_logs_v2" }

// RequestLogRepository stores and retrieves safe, request-correlated server events.
type RequestLogRepository struct {
	database *gorm.DB
	queue    chan domain.RequestLog
	geoip    *geoip.Resolver
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	queueMu  sync.RWMutex
	closed   bool
}

// NewRequestLogRepository constructs the request log repository.
func NewRequestLogRepository(database *gorm.DB, resolver ...*geoip.Resolver) *RequestLogRepository {
	var lookup *geoip.Resolver
	if len(resolver) > 0 {
		lookup = resolver[0]
	}
	ctx, cancel := context.WithCancel(context.Background())
	repository := &RequestLogRepository{
		database: database, queue: make(chan domain.RequestLog, 8192), geoip: lookup,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	go repository.persistLoop()
	return repository
}

// Append persists a shared logger record.
func (repository *RequestLogRepository) Append(ctx context.Context, record logging.Record) error {
	return repository.AppendRequestLog(ctx, domain.RequestLog{
		CreatedAt: record.CreatedAt, Level: record.Level, Event: record.Event, Message: record.Message,
		RequestID: record.RequestID, ClientIP: record.ClientIP, OrganizationID: record.OrganizationID, APIKeyMasked: record.APIKeyMasked,
	})
}

// AppendRequestLog stores one safe log entry.
func (repository *RequestLogRepository) AppendRequestLog(_ context.Context, value domain.RequestLog) error {
	if strings.TrimSpace(value.APIKeyMasked) == "" {
		return nil
	}
	repository.queueMu.RLock()
	defer repository.queueMu.RUnlock()
	if repository.closed {
		return errors.New("request log repository is closed")
	}
	select {
	case repository.queue <- value:
		return nil
	default:
		return errors.New("request log persistence queue is full")
	}
}

// Close stops admission and drains accepted logs before the shared database closes.
// The deadline bounds the entire drain, rather than granting every batch a new
// shutdown timeout when the database is unavailable.
func (repository *RequestLogRepository) Close() {
	var deadline *time.Timer
	repository.queueMu.Lock()
	if !repository.closed {
		repository.closed = true
		deadline = time.AfterFunc(requestLogShutdownTimeout, repository.cancel)
		close(repository.queue)
	}
	repository.queueMu.Unlock()
	<-repository.done
	if deadline != nil {
		deadline.Stop()
	}
	repository.cancel()
}

func (repository *RequestLogRepository) persistLoop() {
	defer close(repository.done)
	ticker := time.NewTicker(requestLogCleanupInterval)
	defer ticker.Stop()
	flushTicker := time.NewTicker(requestLogFlushInterval)
	defer flushTicker.Stop()
	batch := make([]domain.RequestLog, 0, requestLogBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// A timer may become ready during a slow INSERT. Fill from the backlog
		// before flushing so a stale tick does not create tiny transactions.
	fill:
		for len(batch) < requestLogBatchSize {
			select {
			case value, open := <-repository.queue:
				if !open {
					break fill
				}
				batch = append(batch, value)
			default:
				break fill
			}
		}
		repository.persistBatch(batch)
		clear(batch)
		batch = batch[:0]
	}
	repository.cleanupExpiredLogs()
	for {
		if repository.ctx.Err() != nil {
			logging.Fallback(context.Background(), "ERROR", "request_logs.shutdown_dropped", fmt.Sprintf("count=%d", len(batch)+len(repository.queue)))
			return
		}
		select {
		case <-repository.ctx.Done():
			continue
		case value, open := <-repository.queue:
			if !open {
				flush()
				return
			}
			batch = append(batch, value)
			if len(batch) == requestLogBatchSize {
				flush()
			}
		case <-flushTicker.C:
			flush()
		case <-ticker.C:
			flush()
			repository.cleanupExpiredLogs()
		}
	}
}

func (repository *RequestLogRepository) persistBatch(values []domain.RequestLog) {
	records := make([]requestLogRecord, len(values))
	for i, value := range values {
		records[i] = requestLogRecord{
			CreatedAt: value.CreatedAt, Level: value.Level, Event: value.Event, Message: value.Message,
			RequestID: value.RequestID, ClientIP: value.ClientIP, OrganizationID: value.OrganizationID, APIKeyMasked: value.APIKeyMasked,
		}
	}
	ctx, cancel := context.WithTimeout(repository.ctx, 3*time.Second)
	// One multi-row INSERT per bounded batch; never retry as individual writes.
	err := repository.database.WithContext(ctx).Omit("LegacyID").Create(&records).Error
	cancel()
	if err != nil {
		value := values[0]
		ctx := httpapi.WithRequestID(context.Background(), value.RequestID)
		ctx = httpapi.WithLogMetadata(ctx, httpapi.LogMetadata{RequestID: value.RequestID, ClientIP: value.ClientIP, OrganizationID: value.OrganizationID, APIKeyMasked: value.APIKeyMasked})
		logging.Fallback(ctx, "ERROR", "request_logs.persist_failed", fmt.Sprintf("batch_size=%d err=%v", len(values), err))
	}
}

func (repository *RequestLogRepository) cleanupExpiredLogs() {
	ctx, cancel := context.WithTimeout(repository.ctx, requestLogCleanupTimeout)
	defer cancel()
	now := time.Now().UTC()
	partitioned, err := repository.maintainRequestLogPartitions(ctx, now)
	if err == nil && !partitioned {
		err = errors.New("request_logs_v2 is missing its daily partitions")
	}
	if err != nil {
		logging.Fallback(ctx, "ERROR", "request_logs.cleanup_failed", "err="+err.Error())
	}
}

// ListRequestLogs returns oldest-first entries for a single request and tenant scope.
func (repository *RequestLogRepository) ListRequestLogs(ctx context.Context, filter domain.RequestLogFilter, offset, limit int) ([]domain.RequestLog, bool, error) {
	query, args := requestLogReadQuery(filter, offset, limit, time.Now().UTC())
	var records []requestLogRecord
	if err := repository.database.WithContext(ctx).Raw(query, args...).Scan(&records).Error; err != nil {
		return nil, false, err
	}
	more := len(records) > limit
	if more {
		records = records[:limit]
	}
	items := make([]domain.RequestLog, 0, len(records))
	for _, record := range records {
		items = append(items, domain.RequestLog{
			ID: record.ID, CreatedAt: record.CreatedAt, Level: record.Level, Event: record.Event, Message: record.Message,
			RequestID: record.RequestID, ClientIP: record.ClientIP, OrganizationID: record.OrganizationID, APIKeyMasked: record.APIKeyMasked,
		})
		items[len(items)-1].ClientIP = formatClientIP(record.ClientIP, repository.geoip)
	}
	return items, more, nil
}

// A single statement gives both sources one consistent snapshot while a batch
// commits. Only unmatched legacy rows are returned, so copying cannot duplicate
// events. Request and tenant predicates are applied independently to each table.
func requestLogReadQuery(filter domain.RequestLogFilter, offset, limit int, now time.Time) (string, []any) {
	columns := "id, created_at, level, event, message, request_id, client_ip, organization_id, api_key_masked"
	newWhere := "request_id = ?"
	oldWhere := "old.request_id = ? AND old.created_at >= ?"
	newArgs := []any{filter.RequestID}
	oldArgs := []any{filter.RequestID, now.Add(-requestLogRetentionPeriod)}
	if filter.OrganizationID > 0 {
		newWhere += " AND organization_id = ?"
		oldWhere += " AND old.organization_id = ?"
		newArgs = append(newArgs, filter.OrganizationID)
		oldArgs = append(oldArgs, filter.OrganizationID)
	}
	oldWhere += " AND NOT EXISTS (SELECT 1 FROM request_logs_v2 AS copied WHERE copied.legacy_id = old.id AND copied.created_at = old.created_at)"
	// Limiting both branches also bounds the intermediate merge for pagination.
	oldColumns := "old." + strings.ReplaceAll(columns, ", ", ", old.")
	query := "SELECT " + columns + " FROM ((SELECT " + columns + ", COALESCE(legacy_id, id) AS sort_id, 1 AS source FROM request_logs_v2 WHERE " + newWhere +
		" ORDER BY created_at, sort_id, id LIMIT ?) UNION ALL (SELECT " + oldColumns + ", old.id AS sort_id, 0 AS source FROM request_logs AS old WHERE " + oldWhere +
		" ORDER BY old.created_at, old.id LIMIT ?)) AS combined ORDER BY created_at, sort_id, source, id LIMIT ? OFFSET ?"
	args := append(newArgs, offset+limit+1)
	args = append(args, oldArgs...)
	args = append(args, offset+limit+1, limit+1, offset)
	return query, args
}

var _ domain.RequestLogRepository = (*RequestLogRepository)(nil)
var _ logging.Sink = (*RequestLogRepository)(nil)

package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	adminDomain "bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/edition"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/snapshot"
	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/application"
	"bosapi/backend/internal/user/domain"
)

// APIKeyRecord is the Gorm persistence model for hash-only gateway keys.
type APIKeyRecord struct {
	ID                                                      uint64  `gorm:"primaryKey"`
	Name                                                    string  `gorm:"size:128;not null"`
	KeyHash                                                 string  `gorm:"type:varbinary(128);uniqueIndex;not null"`
	KeyPrefix                                               string  `gorm:"size:24;not null"`
	KeySuffix                                               string  `gorm:"size:4;not null;default:''"`
	KeyEncrypted                                            *string `gorm:"column:key_encrypted;type:text"`
	UserID                                                  uint64  `gorm:"index"` // Legacy column, never used for access or billing.
	OrganizationID                                          uint64  `gorm:"index;not null"`
	AllowedProviderIDs                                      string  `gorm:"type:json"`
	AllowedModels                                           string  `gorm:"type:json"`
	Status                                                  string  `gorm:"size:24;index;not null"`
	SecurityInspectionUpstreamEnabled                       bool    `gorm:"column:security_inspection_upstream_enabled;not null;default:true"`
	SecurityInspectionDownstreamEnabled                     bool    `gorm:"column:security_inspection_downstream_enabled;not null;default:true"`
	ExpiresAt                                               *time.Time
	QuotaAmount                                             *float64
	QuotaTokens                                             *uint64
	QuotaUsedAmount                                         float64
	QuotaUsedTokens                                         uint64
	DailyQuotaAmount, WeeklyQuotaAmount, MonthlyQuotaAmount *float64
	DailyQuotaTokens, WeeklyQuotaTokens, MonthlyQuotaTokens *uint64
	RateLimit5H                                             *uint32 `gorm:"column:rate_limit_5h"`
	RateLimit1D                                             *uint32 `gorm:"column:rate_limit_1d"`
	RateLimit7D                                             *uint32 `gorm:"column:rate_limit_7d"`
	IPAllowlist                                             string  `gorm:"type:json"`
	IPBlocklist                                             string  `gorm:"type:json"`
	LastUsedAt                                              *time.Time
	CreatedAt                                               time.Time
	UpdatedAt                                               time.Time
}

// TableName maps the key entity to its dedicated table.
func (APIKeyRecord) TableName() string { return "api_keys" }

// KeyRepository implements organization-scoped key operations through Gorm.
type KeyRepository struct {
	database        *gorm.DB
	adminUsageCache *snapshot.Cache
}

// gatewayQuotaRetryRecord is the durable outbox used when a gateway request
// has completed but its organization quota settlement could not be committed.
// It intentionally stores only the usage metadata required to replay the
// idempotent settlement; request and provider credentials are never persisted.
type gatewayQuotaRetryRecord struct {
	ID                   uint64    `gorm:"primaryKey;index:idx_gateway_quota_retry_org,priority:2"`
	RequestID            string    `gorm:"size:128;not null;uniqueIndex:uq_gateway_quota_retry,priority:1"`
	UsageID              string    `gorm:"size:64;not null;uniqueIndex:uq_gateway_quota_retry,priority:2"`
	OrganizationID       uint64    `gorm:"not null;index:idx_gateway_quota_retry_org,priority:1"`
	APIKeyID             uint64    `gorm:"not null"`
	Model                string    `gorm:"size:191;not null"`
	Endpoint             string    `gorm:"size:128;not null"`
	StatusCode           uint16    `gorm:"not null"`
	ErrorCode            string    `gorm:"size:64;not null;default:''"`
	InputTokens          uint64    `gorm:"not null"`
	CacheReadTokens      uint64    `gorm:"not null"`
	CacheWriteTokens     uint64    `gorm:"not null"`
	CacheReadCostAmount  float64   `gorm:"type:decimal(20,8);not null"`
	CacheWriteCostAmount float64   `gorm:"type:decimal(20,8);not null"`
	OutputTokens         uint64    `gorm:"not null"`
	TotalTokens          uint64    `gorm:"not null"`
	CostAmount           float64   `gorm:"type:decimal(20,8);not null"`
	ClientIP             []byte    `gorm:"type:varbinary(16)"`
	IsStream             bool      `gorm:"not null"`
	DurationMillis       uint32    `gorm:"column:duration_ms;not null"`
	FirstTokenMillis     *uint32   `gorm:"column:first_token_ms"`
	CreatedAt            time.Time `gorm:"not null"`
}

func (gatewayQuotaRetryRecord) TableName() string { return "gateway_quota_retries" }

func newGatewayQuotaRetryRecord(value GatewayUsageRecord) gatewayQuotaRetryRecord {
	return gatewayQuotaRetryRecord{
		RequestID: value.RequestID, UsageID: value.UsageID, OrganizationID: value.OrganizationID, APIKeyID: value.APIKeyID,
		Model: value.Model, Endpoint: value.Endpoint, StatusCode: value.StatusCode, ErrorCode: value.ErrorCode,
		InputTokens: value.InputTokens, CacheReadTokens: value.CacheReadTokens, CacheWriteTokens: value.CacheWriteTokens,
		CacheReadCostAmount: value.CacheReadCostAmount, CacheWriteCostAmount: value.CacheWriteCostAmount,
		OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens, CostAmount: value.CostAmount,
		ClientIP: append([]byte(nil), value.ClientIP...), IsStream: value.IsStream, DurationMillis: value.DurationMillis,
		FirstTokenMillis: value.FirstTokenMillis, CreatedAt: value.CreatedAt,
	}
}

func (record gatewayQuotaRetryRecord) gatewayUsageRecord() GatewayUsageRecord {
	return GatewayUsageRecord{
		RequestID: record.RequestID, UsageID: record.UsageID, OrganizationID: record.OrganizationID, APIKeyID: record.APIKeyID,
		Model: record.Model, Endpoint: record.Endpoint, StatusCode: record.StatusCode, ErrorCode: record.ErrorCode,
		InputTokens: record.InputTokens, CacheReadTokens: record.CacheReadTokens, CacheWriteTokens: record.CacheWriteTokens,
		CacheReadCostAmount: record.CacheReadCostAmount, CacheWriteCostAmount: record.CacheWriteCostAmount,
		OutputTokens: record.OutputTokens, TotalTokens: record.TotalTokens, CostAmount: record.CostAmount,
		ClientIP: append([]byte(nil), record.ClientIP...), IsStream: record.IsStream, DurationMillis: record.DurationMillis,
		FirstTokenMillis: record.FirstTokenMillis, CreatedAt: record.CreatedAt,
	}
}

// NewKeyRepository constructs a Gorm key repository.
func NewKeyRepository(database *gorm.DB) *KeyRepository { return &KeyRepository{database: database} }

// AddUsage atomically accumulates usage for a gateway request.
func (repository *KeyRepository) AddUsage(ctx context.Context, id uint64, tokens int64, amount float64) error {
	if repository == nil || repository.database == nil || id == 0 {
		return errors.New("invalid api key repository")
	}
	if tokens < 0 {
		tokens = 0
	}
	if amount < 0 {
		amount = 0
	}
	return repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("id = ?", id).UpdateColumns(map[string]any{
		"quota_used_tokens": gorm.Expr("COALESCE(quota_used_tokens, 0) + ?", tokens),
		"quota_used_amount": gorm.Expr("COALESCE(quota_used_amount, 0) + ?", amount),
		"last_used_at":      time.Now().UTC(),
	}).Error
}

// RecordGatewayUsageAndQuota persists one request and charges its quota exactly once.
func (repository *KeyRepository) RecordGatewayUsageAndQuota(ctx context.Context, value GatewayUsageRecord) (bool, error) {
	if repository == nil || repository.database == nil || value.APIKeyID == 0 || value.RequestID == "" {
		return false, errors.New("invalid gateway usage record")
	}
	var inserted bool
	err := repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var key APIKeyRecord
		// Charge the tenant captured at authentication; historical ledger rows are immutable.
		if err := tx.Select("id", "user_id", "organization_id").Where("id = ?", value.APIKeyID).First(&key).Error; err != nil {
			return err
		}
		record := UsageRecord{
			RequestID: value.RequestID, UsageID: value.UsageID, OrganizationID: value.OrganizationID, APIKeyID: value.APIKeyID,
			Model: value.Model, Endpoint: value.Endpoint, StatusCode: value.StatusCode, ErrorCode: value.ErrorCode,
			InputTokens: value.InputTokens, CacheReadTokens: value.CacheReadTokens, CacheWriteTokens: value.CacheWriteTokens,
			OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens,
			CacheReadCostAmount: value.CacheReadCostAmount, CacheWriteCostAmount: value.CacheWriteCostAmount,
			CostAmount: value.CostAmount, ClientIP: value.ClientIP, IsStream: value.IsStream, DurationMillis: value.DurationMillis, FirstTokenMillis: value.FirstTokenMillis, CreatedAt: value.CreatedAt,
		}
		ok, err := insertUsageRecord(tx, &record)
		if err != nil || !ok {
			return err
		}
		inserted = true
		// Charge the receipt's rounded DECIMAL even for a late expired request.
		storedCost := tx.Table("usage_log_receipts").Select("cost_amount").Where("request_id = ? AND usage_id = ?", value.RequestID, value.UsageID)
		return tx.Model(&APIKeyRecord{}).Where("id = ?", value.APIKeyID).UpdateColumns(map[string]any{
			"quota_used_tokens": gorm.Expr("COALESCE(quota_used_tokens, 0) + ?", value.TotalTokens),
			"quota_used_amount": gorm.Expr("COALESCE(quota_used_amount, 0) + (?)", storedCost),
			"last_used_at":      value.CreatedAt,
		}).Error
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return false, nil
	}
	return inserted && err == nil, err
}

// SaveGatewayQuotaRetry records a failed settlement in an idempotent outbox.
// The unique request/usage identity makes repeated saves safe after ambiguous
// commits and process restarts.
func (repository *KeyRepository) SaveGatewayQuotaRetry(ctx context.Context, value GatewayUsageRecord) error {
	if repository == nil || repository.database == nil || value.APIKeyID == 0 || value.OrganizationID == 0 || value.RequestID == "" || value.UsageID == "" {
		return errors.New("invalid gateway quota retry")
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	retry := newGatewayQuotaRetryRecord(value)
	return repository.database.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "request_id"}, {Name: "usage_id"}},
		DoNothing: true,
	}).Create(&retry).Error
}

// ListGatewayQuotaRetries returns pending receipts for one organization in
// creation order so admission can preserve the original billing period.
func (repository *KeyRepository) ListGatewayQuotaRetries(ctx context.Context, organizationID uint64, limit int) ([]GatewayUsageRecord, error) {
	rows, _, err := repository.gatewayQuotaRetries(ctx, organizationID, 0, limit)
	return rows, err
}

// ScanGatewayQuotaRetries advances past failed rows so an unavailable tenant
// cannot monopolize the first page on every recovery pass.
func (repository *KeyRepository) ScanGatewayQuotaRetries(ctx context.Context, afterID uint64, limit int) ([]GatewayUsageRecord, uint64, error) {
	return repository.gatewayQuotaRetries(ctx, 0, afterID, limit)
}

func (repository *KeyRepository) gatewayQuotaRetries(ctx context.Context, organizationID, afterID uint64, limit int) ([]GatewayUsageRecord, uint64, error) {
	if repository == nil || repository.database == nil {
		return nil, afterID, errors.New("invalid gateway quota retry repository")
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	var rows []gatewayQuotaRetryRecord
	query := repository.database.WithContext(ctx)
	if organizationID > 0 {
		query = query.Where("organization_id = ?", organizationID)
	}
	if afterID > 0 {
		query = query.Where("id > ?", afterID)
	}
	if err := query.Order("id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, afterID, err
	}
	result := make([]GatewayUsageRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.gatewayUsageRecord())
		afterID = row.ID
	}
	return result, afterID, nil
}

// DeleteGatewayQuotaRetry removes a receipt only after its idempotent usage
// and quota transaction has committed successfully.
func (repository *KeyRepository) DeleteGatewayQuotaRetry(ctx context.Context, requestID, usageID string) error {
	if repository == nil || repository.database == nil || requestID == "" || usageID == "" {
		return errors.New("invalid gateway quota retry identity")
	}
	return repository.database.WithContext(ctx).Where("request_id = ? AND usage_id = ?", requestID, usageID).Delete(&gatewayQuotaRetryRecord{}).Error
}

// FindGatewayKey validates key and organization state before returning the credential.
func (repository *KeyRepository) FindGatewayKey(ctx context.Context, rawKey string, now time.Time) (domain.APIKey, bool, error) {
	digest := sha256.Sum256([]byte(rawKey))
	var record APIKeyRecord
	err := repository.database.WithContext(ctx).
		Where("key_hash = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", digest[:], "active", now).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.APIKey{}, false, nil
	}
	if err != nil {
		logging.Printf(ctx, "ERROR", "user.api_key.lookup_failed", "err=%v", err)
		return domain.APIKey{}, false, err
	}
	var organizationStatus string
	if err := repository.database.WithContext(ctx).Table("organizations").Select("status").Where("id = ?", record.OrganizationID).Scan(&organizationStatus).Error; err != nil {
		logging.Printf(ctx, "ERROR", "user.api_key.organization_lookup_failed", "organization_id=%d err=%v", record.OrganizationID, err)
		return domain.APIKey{}, false, err
	}
	if organizationStatus != "active" {
		return domain.APIKey{}, false, nil
	}
	key, err := recordToDomain(record)
	if err == nil && (key.DailyQuotaTokens != nil || key.WeeklyQuotaTokens != nil || key.MonthlyQuotaTokens != nil || key.DailyQuotaAmount != nil || key.WeeklyQuotaAmount != nil || key.MonthlyQuotaAmount != nil) {
		keys := []domain.APIKey{key}
		err = repository.populatePeriodicUsageAt(ctx, keys, now)
		key = keys[0]
	}
	if err == nil && key.KeySuffix == "" {
		if suffix := domain.SafeKeySuffix(rawKey, key.KeyPrefix); suffix != "" {
			// A concurrent reset or owner change must not attach the old secret's suffix to a new key.
			// Metadata recovery is best effort and never prevents an otherwise valid authentication.
			result := repository.database.WithContext(ctx).Session(&gorm.Session{SkipDefaultTransaction: true, Logger: logger.Discard}).
				Model(&APIKeyRecord{}).
				Where("id = ? AND key_hash = ? AND organization_id = ? AND status = ? AND key_suffix = ? AND (expires_at IS NULL OR expires_at > ?)", key.ID, digest[:], key.OrganizationID, "active", "", now).
				UpdateColumn("key_suffix", suffix)
			if result.Error == nil && result.RowsAffected > 0 {
				key.KeySuffix = suffix
			}
		}
	}
	return key, true, err
}

// FindActiveKeyIdentityByID validates portal sessions without loading display counters.
// Usage aggregation belongs to the info endpoint rather than every authenticated request.
func (repository *KeyRepository) FindActiveKeyIdentityByID(ctx context.Context, id uint64, now time.Time) (domain.APIKey, bool, error) {
	var record APIKeyRecord
	err := repository.database.WithContext(ctx).Where("id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", id, "active", now).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.APIKey{}, false, nil
	}
	if err != nil {
		return domain.APIKey{}, false, err
	}
	var organizationStatus string
	if err := repository.database.WithContext(ctx).Table("organizations").Select("status").Where("id = ?", record.OrganizationID).Scan(&organizationStatus).Error; err != nil {
		return domain.APIKey{}, false, err
	}
	if organizationStatus != "active" {
		return domain.APIKey{}, false, nil
	}
	key, err := recordToDomain(record)
	if err != nil {
		return domain.APIKey{}, false, err
	}
	return key, true, nil
}

// FindActiveKeyByID returns current portal counters after validating the identity.
func (repository *KeyRepository) FindActiveKeyByID(ctx context.Context, id uint64, now time.Time) (domain.APIKey, bool, error) {
	key, found, err := repository.FindActiveKeyIdentityByID(ctx, id, now)
	if err != nil || !found {
		return domain.APIKey{}, false, err
	}
	keys := []domain.APIKey{key}
	err = repository.populatePeriodicUsageAt(ctx, keys, now)
	return keys[0], err == nil, err
}

// FindGatewayProviderByHash preserves the legacy lookup contract without bypassing organization policy.
func (repository *KeyRepository) FindGatewayProviderByHash(ctx context.Context, hash string, now time.Time) (uint64, bool, error) {
	var record APIKeyRecord
	err := repository.database.WithContext(ctx).Where("key_hash = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", mustHashBytes(hash), "active", now).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

// Create inserts a key record containing only a digest and safe metadata.
func (repository *KeyRepository) Create(ctx context.Context, key *domain.APIKey) error {
	record, err := recordFromDomain(*key)
	if err != nil {
		return err
	}
	err = repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := edition.CheckCapacity(transaction, "api_keys", 1); err != nil {
			return err
		}
		if err := validateAPIKeyOrganization(transaction, key.OrganizationID); err != nil {
			return err
		}
		if err := validateKeyProviders(transaction, key.OrganizationID, key.AllowedProviderIDs); err != nil {
			return err
		}
		if err := transaction.Omit("UserID").Create(&record).Error; err != nil {
			return err
		}
		key.ID = record.ID
		return nil
	})
	return err
}

// CreateMany inserts keys for one active organization in a single transaction.
func (repository *KeyRepository) CreateMany(ctx context.Context, keys []*domain.APIKey) error {
	if len(keys) == 0 || keys[0] == nil {
		return errors.New("api key batch is empty")
	}
	organizationID := keys[0].OrganizationID
	records := make([]APIKeyRecord, len(keys))
	for index, key := range keys {
		if key == nil || key.OrganizationID != organizationID {
			return errors.New("api key batch must share one organization")
		}
		record, err := recordFromDomain(*key)
		if err != nil {
			return err
		}
		records[index] = record
	}
	return repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := edition.CheckCapacity(transaction, "api_keys", len(keys)); err != nil {
			return err
		}
		if err := validateAPIKeyOrganization(transaction, organizationID); err != nil {
			return err
		}
		for _, key := range keys {
			if err := validateKeyProviders(transaction, organizationID, key.AllowedProviderIDs); err != nil {
				return err
			}
		}
		if err := transaction.Omit("UserID").Create(&records).Error; err != nil {
			return err
		}
		for index := range keys {
			keys[index].ID = records[index].ID
		}
		return nil
	})
}

func validateAPIKeyOrganization(transaction *gorm.DB, organizationID uint64) error {
	if organizationID == 0 {
		return gorm.ErrInvalidData
	}
	var organization struct {
		ID     uint64
		Status string
	}
	if err := transaction.Table("organizations").Select("id, status").Where("id = ?", organizationID).First(&organization).Error; err != nil {
		return err
	}
	if organization.Status != "active" {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// List returns records and count constrained by owner, organization, and optional filters.
func (repository *KeyRepository) List(ctx context.Context, filter application.KeyFilter, page, pageSize int) ([]domain.APIKey, int64, error) {
	query := repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("organization_id = ? AND organization_id > 0 AND status <> ?", filter.OrganizationID, "deleted")
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		escaped := "%" + strings.ReplaceAll(search, "%", "\\%") + "%"
		hash := sha256.Sum256([]byte(search))
		hashBytes := string(hash[:])
		query = query.Where("name LIKE ? OR key_prefix LIKE ? OR key_hash = ?", escaped, escaped, hashBytes)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []APIKeyRecord
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	keys := make([]domain.APIKey, 0, len(records))
	for _, record := range records {
		key, err := recordToDomain(record)
		if err != nil {
			return nil, 0, err
		}
		keys = append(keys, key)
	}
	_ = repository.populatePeriodicUsage(ctx, keys)
	return keys, total, nil
}

// FindOwned returns a key only when all supplied ownership dimensions match.
func (repository *KeyRepository) FindOwned(ctx context.Context, id, userID, organizationID uint64) (domain.APIKey, error) {
	var record APIKeyRecord
	err := repository.database.WithContext(ctx).Where("id = ? AND organization_id = ? AND organization_id > 0 AND status <> ?", id, organizationID, "deleted").First(&record).Error
	if err != nil {
		return domain.APIKey{}, err
	}
	key, err := recordToDomain(record)
	if err != nil {
		return domain.APIKey{}, err
	}
	keys := []domain.APIKey{key}
	_ = repository.populatePeriodicUsage(ctx, keys)
	return keys[0], nil
}

// UpdateOwned performs a partial update while keeping every query ownership-scoped.
func (repository *KeyRepository) UpdateOwned(ctx context.Context, id, userID, organizationID uint64, updates map[string]any) error {
	if deletedAt, ok := updates["deleted_at"]; ok {
		delete(updates, "deleted_at")
		if _, hasUpdated := updates["updated_at"]; !hasUpdated {
			updates["updated_at"] = deletedAt
		}
	}
	allowed := map[string]struct{}{"allowed_provider_ids": {}, "allowed_models": {}, "name": {}, "status": {}, "expires_at": {}, "quota_amount": {}, "quota_tokens": {}, "daily_quota_amount": {}, "weekly_quota_amount": {}, "monthly_quota_amount": {}, "daily_quota_tokens": {}, "weekly_quota_tokens": {}, "monthly_quota_tokens": {}, "rate_limit_5h": {}, "rate_limit_1d": {}, "rate_limit_7d": {}, "ip_allowlist": {}, "ip_blocklist": {}, "updated_at": {}}
	for field := range updates {
		if _, ok := allowed[field]; !ok {
			return gorm.ErrInvalidData
		}
	}
	db := repository.database.WithContext(ctx)
	_, hasProviders := updates["allowed_provider_ids"]
	_, hasModels := updates["allowed_models"]
	if hasProviders || hasModels {
		return db.Transaction(func(tx *gorm.DB) error {
			var record APIKeyRecord
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND organization_id = ? AND organization_id > 0 AND status <> ?", id, organizationID, "deleted").First(&record).Error; err != nil {
				return err
			}
			if err := encodeKeyAccessUpdates(tx, record.OrganizationID, updates); err != nil {
				return err
			}
			return tx.Model(&record).Updates(updates).Error
		})
	}
	if err := encodeKeyAccessUpdates(db, organizationID, updates); err != nil {
		return err
	}
	result := db.Model(&APIKeyRecord{}).Where("id = ? AND organization_id = ? AND organization_id > 0 AND status <> ?", id, organizationID, "deleted").Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteOwned permanently removes a key only within the supplied ownership scope.
func (repository *KeyRepository) DeleteOwned(ctx context.Context, id, userID, organizationID uint64) error {
	result := repository.database.WithContext(ctx).Where("id = ? AND organization_id = ? AND organization_id > 0", id, organizationID).Delete(&APIKeyRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateAdminKey applies the administrative edit allowlist within an optional organization boundary.
func (repository *KeyRepository) UpdateAdminKey(ctx context.Context, id, organizationID uint64, updates map[string]any) error {
	allowed := map[string]struct{}{"expires_at": {}, "status": {}, "security_inspection_upstream_enabled": {}, "security_inspection_downstream_enabled": {}, "name": {}, "organization_id": {}, "allowed_provider_ids": {}, "allowed_models": {}, "quota_amount": {}, "quota_tokens": {}, "daily_quota_amount": {}, "weekly_quota_amount": {}, "monthly_quota_amount": {}, "daily_quota_tokens": {}, "weekly_quota_tokens": {}, "monthly_quota_tokens": {}, "ip_allowlist": {}, "updated_at": {}}
	for field := range updates {
		if _, ok := allowed[field]; !ok {
			return gorm.ErrInvalidData
		}
	}
	return repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var record APIKeyRecord
		query := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status <> ?", id, "deleted")
		if organizationID > 0 {
			query = query.Where("organization_id = ?", organizationID)
		}
		if err := query.First(&record).Error; err != nil {
			return err
		}
		targetOrganizationID := record.OrganizationID
		if value, supplied := updates["organization_id"]; supplied {
			var valid bool
			targetOrganizationID, valid = value.(uint64)
			if !valid || targetOrganizationID == 0 {
				return gorm.ErrInvalidData
			}
			var status string
			if err := transaction.Table("organizations").Select("status").Where("id = ?", targetOrganizationID).Scan(&status).Error; err != nil {
				return err
			}
			if status != "active" {
				return gorm.ErrInvalidData
			}
		}
		if err := encodeKeyAccessUpdates(transaction, targetOrganizationID, updates); err != nil {
			return err
		}
		result := transaction.Model(&record).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// ListAdminKeys returns keys matching administrative filters without exposing their hashes.
func (repository *KeyRepository) ListAdminKeys(ctx context.Context, filter adminDomain.AdminKeyFilter, page, pageSize int) ([]domain.APIKey, int64, error) {
	query := repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("status <> ?", "deleted")
	if filter.OrganizationID > 0 {
		query = query.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		trimmed := strings.TrimPrefix(search, "Key #")
		trimmed = strings.TrimPrefix(trimmed, "key #")
		trimmed = strings.TrimPrefix(trimmed, "#")
		trimmed = strings.TrimSpace(trimmed)
		escaped := "%" + strings.ReplaceAll(search, "%", "\\%") + "%"
		hash := sha256.Sum256([]byte(search))
		hashBytes := string(hash[:])
		if id, err := strconv.ParseUint(trimmed, 10, 64); err == nil && id > 0 {
			query = query.Where("name LIKE ? OR id = ? OR key_prefix LIKE ? OR key_hash = ?", escaped, id, escaped, hashBytes)
		} else {
			query = query.Where("name LIKE ? OR key_prefix LIKE ? OR key_hash = ?", escaped, escaped, hashBytes)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if filter.OptionsOnly {
		query = query.Select("id, name, organization_id")
	}
	var records []APIKeyRecord
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	keys := make([]domain.APIKey, 0, len(records))
	for _, record := range records {
		if filter.OptionsOnly {
			keys = append(keys, domain.APIKey{ID: record.ID, Name: record.Name, OrganizationID: record.OrganizationID})
			continue
		}
		key, err := recordToDomain(record)
		if err != nil {
			return nil, 0, err
		}
		keys = append(keys, key)
	}
	if !filter.OptionsOnly {
		if repository.adminUsageCache != nil {
			repository.populateCachedAdminUsage(ctx, keys)
		} else {
			_ = repository.populateAdminKeyUsageAt(ctx, keys, time.Now())
		}
	}
	return keys, total, nil
}

// FindAdminKey returns a key visible within the requested organization.
func (repository *KeyRepository) FindAdminKey(ctx context.Context, id, organizationID uint64) (domain.APIKey, error) {
	var record APIKeyRecord
	query := repository.database.WithContext(ctx).Where("id = ? AND status <> ?", id, "deleted")
	if organizationID > 0 {
		query = query.Where("organization_id = ?", organizationID)
	}
	if err := query.First(&record).Error; err != nil {
		return domain.APIKey{}, err
	}
	key, err := recordToDomain(record)
	if err != nil {
		return domain.APIKey{}, err
	}
	keys := []domain.APIKey{key}
	if repository.adminUsageCache != nil {
		repository.populateCachedAdminUsage(ctx, keys)
	} else {
		_ = repository.populatePeriodicUsage(ctx, keys)
	}
	return keys[0], nil
}

// DeleteAdminKeys permanently removes keys within an optional organization boundary.
func (repository *KeyRepository) DeleteAdminKeys(ctx context.Context, ids []uint64, organizationID uint64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	query := repository.database.WithContext(ctx).Where("id IN ?", ids)
	if organizationID > 0 {
		query = query.Where("organization_id = ?", organizationID)
	}
	result := query.Delete(&APIKeyRecord{})
	return result.RowsAffected, result.Error
}

type periodicUsageRecord struct {
	APIKeyID      uint64  `gorm:"column:api_key_id"`
	DailyTokens   int64   `gorm:"column:daily_tokens"`
	DailyAmount   float64 `gorm:"column:daily_amount"`
	WeeklyTokens  int64   `gorm:"column:weekly_tokens"`
	WeeklyAmount  float64 `gorm:"column:weekly_amount"`
	MonthlyTokens int64   `gorm:"column:monthly_tokens"`
	MonthlyAmount float64 `gorm:"column:monthly_amount"`
}

// Display-only aggregation is batched per admin page, never on gateway authentication.
// Nil amounts indicate unavailable data rather than a fabricated zero.
func (repository *KeyRepository) populateDisplayUsageAt(ctx context.Context, keys []domain.APIKey, now time.Time) error {
	ids := make([]uint64, 0, len(keys))
	for i := range keys {
		keys[i].UsageTodayAmount, keys[i].Usage30dAmount = nil, nil
		if keys[i].ID > 0 {
			ids = append(ids, keys[i].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	day, _, _ := usagetime.PeriodStarts(now)
	start := now.Add(-30 * 24 * time.Hour)
	var rows []struct {
		APIKeyID     uint64
		TodayAmount  float64
		RecentAmount float64
	}
	query := "SELECT api_key_id, " +
		"COALESCE(SUM(CASE WHEN created_at >= ? THEN cost_amount ELSE 0 END),0) AS today_amount, " +
		"COALESCE(SUM(cost_amount),0) AS recent_amount " +
		"FROM (SELECT api_key_id, created_at, total_tokens, cost_amount FROM usage_logs_v2 FORCE INDEX (idx_usage_key_time_totals) WHERE api_key_id IN ? AND created_at >= ? " +
		"UNION ALL SELECT api_key_id, created_at, total_tokens, cost_amount FROM usage_logs FORCE INDEX (idx_usage_key_time_totals) WHERE api_key_id IN ? AND created_at >= ?) AS usage_source WHERE created_at <= ? GROUP BY api_key_id"
	if err := repository.database.WithContext(ctx).Raw(query, day, ids, start, ids, start, now).Scan(&rows).Error; err != nil {
		return err
	}
	today, recent := make(map[uint64]float64), make(map[uint64]float64)
	for _, row := range rows {
		today[row.APIKeyID], recent[row.APIKeyID] = row.TodayAmount, row.RecentAmount
	}
	for i := range keys {
		a, b := today[keys[i].ID], recent[keys[i].ID]
		keys[i].UsageTodayAmount, keys[i].Usage30dAmount = &a, &b
	}
	return nil
}

func (repository *KeyRepository) populatePeriodicUsage(ctx context.Context, keys []domain.APIKey) error {
	return repository.populatePeriodicUsageAt(ctx, keys, time.Now())
}

func (repository *KeyRepository) populatePeriodicUsageAt(ctx context.Context, keys []domain.APIKey, now time.Time) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(keys))
	for _, k := range keys {
		if k.ID > 0 {
			ids = append(ids, k.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var usages []periodicUsageRecord
	if err := periodicUsageQuery(repository.database.WithContext(ctx), ids, now).Scan(&usages).Error; err != nil {
		return err
	}
	usageMap := make(map[uint64]periodicUsageRecord, len(usages))
	for _, u := range usages {
		usageMap[u.APIKeyID] = u
	}
	for i := range keys {
		u := usageMap[keys[i].ID]
		keys[i].DailyUsedTokens = u.DailyTokens
		keys[i].DailyUsedAmount = u.DailyAmount
		keys[i].WeeklyUsedTokens = u.WeeklyTokens
		keys[i].WeeklyUsedAmount = u.WeeklyAmount
		keys[i].MonthlyUsedTokens = u.MonthlyTokens
		keys[i].MonthlyUsedAmount = u.MonthlyAmount
	}
	return nil
}

// SetAdminKeyStatus changes a key status inside the optional organization boundary.
func (repository *KeyRepository) SetAdminKeyStatus(ctx context.Context, id, organizationID uint64, status string) error {
	query := repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("id = ? AND status <> ?", id, "deleted")
	if organizationID > 0 {
		query = query.Where("organization_id = ?", organizationID)
	}
	result := query.Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func recordFromDomain(key domain.APIKey) (APIKeyRecord, error) {
	if err := domain.ValidateKeyAccessPolicy(key.AllowedProviderIDs, key.AllowedModels); err != nil {
		return APIKeyRecord{}, gorm.ErrInvalidData
	}
	now := time.Now().UTC()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = now
	}
	allowlist, err := json.Marshal(key.IPAllowlist)
	if err != nil {
		return APIKeyRecord{}, err
	}
	blocklist, err := json.Marshal(key.IPBlocklist)
	if err != nil {
		return APIKeyRecord{}, err
	}
	hash, err := hex.DecodeString(key.KeyHash)
	if err != nil || len(hash) != 32 {
		return APIKeyRecord{}, errors.New("invalid api key hash")
	}
	var keyEncrypted *string
	if key.KeyEncrypted != "" {
		keyEncrypted = &key.KeyEncrypted
	}
	providers, _ := json.Marshal(key.AllowedProviderIDs)
	models, _ := json.Marshal(key.AllowedModels)
	return APIKeyRecord{ID: key.ID, Name: key.Name, KeyHash: string(hash), KeyPrefix: key.KeyPrefix, KeySuffix: key.KeySuffix, KeyEncrypted: keyEncrypted, UserID: key.UserID, OrganizationID: key.OrganizationID, AllowedProviderIDs: string(providers), AllowedModels: string(models), Status: key.Status, SecurityInspectionUpstreamEnabled: key.SecurityInspectionUpstreamEnabled, SecurityInspectionDownstreamEnabled: key.SecurityInspectionDownstreamEnabled, ExpiresAt: key.ExpiresAt, QuotaAmount: key.QuotaAmount, QuotaTokens: uintPtr(key.QuotaTokens), QuotaUsedAmount: key.QuotaUsedAmount, QuotaUsedTokens: uint64(key.QuotaUsedTokens), DailyQuotaAmount: key.DailyQuotaAmount, WeeklyQuotaAmount: key.WeeklyQuotaAmount, MonthlyQuotaAmount: key.MonthlyQuotaAmount, DailyQuotaTokens: uintPtr(key.DailyQuotaTokens), WeeklyQuotaTokens: uintPtr(key.WeeklyQuotaTokens), MonthlyQuotaTokens: uintPtr(key.MonthlyQuotaTokens), RateLimit5H: uint32Ptr(key.RateLimit5H), RateLimit1D: uint32Ptr(key.RateLimit1D), RateLimit7D: uint32Ptr(key.RateLimit7D), IPAllowlist: string(allowlist), IPBlocklist: string(blocklist), LastUsedAt: key.LastUsedAt, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt}, nil
}

func recordToDomain(record APIKeyRecord) (domain.APIKey, error) {
	var providers []uint64
	var models []string
	if record.AllowedProviderIDs != "" {
		if err := json.Unmarshal([]byte(record.AllowedProviderIDs), &providers); err != nil {
			return domain.APIKey{}, err
		}
	}
	if record.AllowedModels != "" {
		if err := json.Unmarshal([]byte(record.AllowedModels), &models); err != nil {
			return domain.APIKey{}, err
		}
	}
	var allowlist, blocklist []string
	if record.IPAllowlist != "" {
		if err := json.Unmarshal([]byte(record.IPAllowlist), &allowlist); err != nil {
			return domain.APIKey{}, err
		}
	}
	if record.IPBlocklist != "" {
		if err := json.Unmarshal([]byte(record.IPBlocklist), &blocklist); err != nil {
			return domain.APIKey{}, err
		}
	}
	var keyEncrypted string
	if record.KeyEncrypted != nil {
		keyEncrypted = *record.KeyEncrypted
	}
	return domain.APIKey{ID: record.ID, Name: record.Name, KeyHash: hex.EncodeToString([]byte(record.KeyHash)), KeyPrefix: record.KeyPrefix, KeySuffix: record.KeySuffix, KeyEncrypted: keyEncrypted, UserID: record.UserID, OrganizationID: record.OrganizationID, AllowedProviderIDs: providers, AllowedModels: models, Status: record.Status, SecurityInspectionUpstreamEnabled: record.SecurityInspectionUpstreamEnabled, SecurityInspectionDownstreamEnabled: record.SecurityInspectionDownstreamEnabled, ExpiresAt: record.ExpiresAt, QuotaAmount: record.QuotaAmount, QuotaTokens: int64Ptr(record.QuotaTokens), QuotaUsedAmount: record.QuotaUsedAmount, QuotaUsedTokens: int64(record.QuotaUsedTokens), DailyQuotaAmount: record.DailyQuotaAmount, WeeklyQuotaAmount: record.WeeklyQuotaAmount, MonthlyQuotaAmount: record.MonthlyQuotaAmount, DailyQuotaTokens: int64Ptr(record.DailyQuotaTokens), WeeklyQuotaTokens: int64Ptr(record.WeeklyQuotaTokens), MonthlyQuotaTokens: int64Ptr(record.MonthlyQuotaTokens), RateLimit5H: int64Ptr32(record.RateLimit5H), RateLimit1D: int64Ptr32(record.RateLimit1D), RateLimit7D: int64Ptr32(record.RateLimit7D), IPAllowlist: allowlist, IPBlocklist: blocklist, LastUsedAt: record.LastUsedAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

func uintPtr(value *int64) *uint64 {
	if value == nil || *value < 0 {
		return nil
	}
	converted := uint64(*value)
	return &converted
}

func mustHashBytes(value string) string {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return ""
	}
	return string(decoded)
}

func int64Ptr(value *uint64) *int64 {
	if value == nil || *value > uint64(^uint64(0)>>1) {
		return nil
	}
	converted := int64(*value)
	return &converted
}

func uint32Ptr(value *int64) *uint32 {
	if value == nil || *value < 0 || uint64(*value) > uint64(^uint32(0)) {
		return nil
	}
	converted := uint32(*value)
	return &converted
}

func int64Ptr32(value *uint32) *int64 {
	if value == nil {
		return nil
	}
	converted := int64(*value)
	return &converted
}

// DisableExpiredKeys persists automatic disablement without changing already disabled credentials.
func (repository *KeyRepository) DisableExpiredKeys(ctx context.Context, now time.Time) error {
	return repository.database.WithContext(ctx).Model(&APIKeyRecord{}).Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", "active", now).UpdateColumns(APIKeyRecord{Status: "disabled", UpdatedAt: now}).Error
}

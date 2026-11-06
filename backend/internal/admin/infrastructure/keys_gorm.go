package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	adminDomain "bosapi/backend/internal/admin/domain"
	userdomain "bosapi/backend/internal/user/domain"
	userinfra "bosapi/backend/internal/user/infrastructure"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdminKeyRepository implements privileged key reads over the user key repository.
type AdminKeyRepository struct {
	database   *gorm.DB
	cachedKeys *userinfra.KeyRepository
}

// NewAdminKeyRepository constructs an administrative key repository.
func NewAdminKeyRepository(database *gorm.DB) *AdminKeyRepository {
	return &AdminKeyRepository{database: database}
}

// ListAdminKeys returns keys matching administrative filters without exposing hashes.
func (repository *AdminKeyRepository) ListAdminKeys(ctx context.Context, filter adminDomain.AdminKeyFilter, page, pageSize int) ([]userdomain.APIKey, int64, error) {
	if repository.cachedKeys != nil {
		return repository.cachedKeys.ListAdminKeys(ctx, filter, page, pageSize)
	}
	return userinfra.NewKeyRepository(repository.database).ListAdminKeys(ctx, filter, page, pageSize)
}

// FindAdminKey returns a key visible within the requested organization.
func (repository *AdminKeyRepository) FindAdminKey(ctx context.Context, id, organizationID uint64) (userdomain.APIKey, error) {
	if repository.cachedKeys != nil {
		return repository.cachedKeys.FindAdminKey(ctx, id, organizationID)
	}
	return userinfra.NewKeyRepository(repository.database).FindAdminKey(ctx, id, organizationID)
}

// SetAdminKeyStatus changes an organization-scoped key state.
func (repository *AdminKeyRepository) SetAdminKeyStatus(ctx context.Context, id, organizationID uint64, status string) error {
	return userinfra.NewKeyRepository(repository.database).SetAdminKeyStatus(ctx, id, organizationID, status)
}

// UpdateAdminKey changes editable policy fields within the optional organization boundary.
func (repository *AdminKeyRepository) UpdateAdminKey(ctx context.Context, id, organizationID uint64, updates map[string]any) error {
	return userinfra.NewKeyRepository(repository.database).UpdateAdminKey(ctx, id, organizationID, updates)
}

// CreateAdminKey inserts a key after validating its user and group scope.
func (repository *AdminKeyRepository) CreateAdminKey(ctx context.Context, key *userdomain.APIKey) error {
	return userinfra.NewKeyRepository(repository.database).Create(ctx, key)
}

// CreateAdminKeys inserts a key batch atomically after validating its owner scope.
func (repository *AdminKeyRepository) CreateAdminKeys(ctx context.Context, keys []*userdomain.APIKey) error {
	return userinfra.NewKeyRepository(repository.database).CreateMany(ctx, keys)
}

// ResetAdminKey atomically rotates a key hash within the requested organization.
func (repository *AdminKeyRepository) ResetAdminKey(ctx context.Context, id, organizationID uint64, hash, prefix, keyEncrypted, keySuffix string) (uint64, error) {
	hashBytes, err := hex.DecodeString(hash)
	if err != nil || len(hashBytes) != sha256.Size {
		return 0, errors.New("invalid api key hash")
	}
	var actualOrganizationID uint64
	err = repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var record userinfra.APIKeyRecord
		query := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status <> ?", id, "deleted")
		if organizationID > 0 {
			query = query.Where("organization_id = ?", organizationID)
		}
		if err := query.First(&record).Error; err != nil {
			return err
		}
		actualOrganizationID = record.OrganizationID
		updates := map[string]any{"key_hash": string(hashBytes), "key_prefix": prefix, "key_suffix": keySuffix, "key_encrypted": nil, "status": "active"}
		if keyEncrypted != "" {
			updates["key_encrypted"] = keyEncrypted
		}
		return transaction.Model(&record).Updates(updates).Error
	})
	return actualOrganizationID, err
}

// DeleteAdminKeys permanently removes keys within an optional organization boundary.
func (repository *AdminKeyRepository) DeleteAdminKeys(ctx context.Context, ids []uint64, organizationID uint64) (int64, error) {
	return userinfra.NewKeyRepository(repository.database).DeleteAdminKeys(ctx, ids, organizationID)
}

// FindUserTOTP retrieves a user's 2FA status and encrypted secret for verification.
func (repository *AdminKeyRepository) FindUserTOTP(ctx context.Context, userID uint64) (bool, *string, error) {
	var user struct {
		TOTPEnabled         bool    `gorm:"column:totp_enabled"`
		TOTPSecretEncrypted *string `gorm:"column:totp_secret_encrypted"`
		Status              string  `gorm:"column:status"`
	}
	if err := repository.database.WithContext(ctx).Table("users").Select("totp_enabled, totp_secret_encrypted, status").Where("id = ?", userID).First(&user).Error; err != nil {
		return false, nil, err
	}
	if user.Status != "active" {
		return false, nil, errors.New("user is not active")
	}
	return user.TOTPEnabled, user.TOTPSecretEncrypted, nil
}

// TouchLastUsed records the last accepted gateway request for a key.
func (repository *AdminKeyRepository) TouchLastUsed(ctx context.Context, id uint64, at time.Time) error {
	return repository.database.WithContext(ctx).Model(&userinfra.APIKeyRecord{}).Where("id = ?", id).Update("last_used_at", at).Error
}

var _ adminDomain.AdminKeyRepository = (*AdminKeyRepository)(nil)

func NewCachedAdminKeyRepository(db *gorm.DB, client redis.UniversalClient) *AdminKeyRepository {
	return &AdminKeyRepository{database: db, cachedKeys: userinfra.NewCachedKeyRepository(db, client)}
}
func (r *AdminKeyRepository) Close() {
	if r.cachedKeys != nil {
		r.cachedKeys.Close()
	}
}

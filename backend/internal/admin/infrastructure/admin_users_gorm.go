package infrastructure

import (
	"context"
	"errors"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type adminUserRecord struct {
	ID             uint64
	OrganizationID uint64
	Email          string
	Role           string
	Status         string
	TOTPEnabled    bool `gorm:"column:totp_enabled"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (adminUserRecord) TableName() string { return "users" }

type adminLoginLogRecord struct {
	ID        uint64
	UserID    *uint64
	ClientIP  string
	Success   bool
	Reason    string
	CreatedAt time.Time
}

func (adminLoginLogRecord) TableName() string { return "login_logs" }

// AdminUserRepository reads safe user identity data using Gorm.
type AdminUserRepository struct{ database *gorm.DB }

// NewAdminUserRepository constructs the Gorm administrative user repository.
func NewAdminUserRepository(database *gorm.DB) *AdminUserRepository {
	return &AdminUserRepository{database: database}
}

// ListAdminUsers returns users matching role, status, and email search criteria.
func (repository *AdminUserRepository) ListAdminUsers(ctx context.Context, filter domain.AdminUserFilter, page, size int) ([]domain.AdminUser, int64, error) {
	query := repository.database.WithContext(ctx).Model(&adminUserRecord{})
	if filter.OrganizationID > 0 {
		query = query.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.Role != "" {
		query = query.Where("role = ?", filter.Role)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query = query.Where("email LIKE ?", "%"+strings.ReplaceAll(search, "%", "\\%")+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []adminUserRecord
	if err := query.Select("id, organization_id, email, role, status, totp_enabled, created_at, updated_at").Order("id ASC").Offset((page - 1) * size).Limit(size).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.AdminUser, 0, len(records))
	for _, record := range records {
		items = append(items, domain.AdminUser{
			ID:             record.ID,
			OrganizationID: record.OrganizationID,
			Email:          record.Email,
			Role:           record.Role,
			Status:         record.Status,
			TOTPEnabled:    record.TOTPEnabled,
			CreatedAt:      record.CreatedAt,
			UpdatedAt:      record.UpdatedAt,
		})
	}
	return items, total, nil
}

// FindByID finds an identity by its identifier.
func (repository *AdminUserRepository) FindByID(ctx context.Context, id uint64) (domain.AdminUser, error) {
	var record adminUserRecord
	if err := repository.database.WithContext(ctx).First(&record, id).Error; err != nil {
		return domain.AdminUser{}, err
	}
	return domain.AdminUser{
		ID:             record.ID,
		OrganizationID: record.OrganizationID,
		Email:          record.Email,
		Role:           record.Role,
		Status:         record.Status,
		TOTPEnabled:    record.TOTPEnabled,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}, nil
}

// UpdatePassword updates an identity's password hash and timestamp.
func (repository *AdminUserRepository) UpdatePassword(ctx context.Context, id uint64, passwordHash string) error {
	now := time.Now().UTC()
	err := repository.database.WithContext(ctx).Model(&adminUserRecord{}).Where("id = ?", id).Updates(map[string]any{
		"password_hash": passwordHash,
		"updated_at":    now,
	}).Error
	if err != nil {
		return err
	}
	_ = repository.database.WithContext(ctx).Table("refresh_records").Where("user_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
	return nil
}

// ResetMFA clears the two-factor authentication configuration for a user.
func (repository *AdminUserRepository) ResetMFA(ctx context.Context, id uint64) error {
	now := time.Now().UTC()
	err := repository.database.WithContext(ctx).Model(&adminUserRecord{}).Where("id = ?", id).Updates(map[string]any{
		"totp_enabled":          false,
		"totp_secret_encrypted": nil,
		"totp_enabled_at":       nil,
		"updated_at":            now,
	}).Error
	if err != nil {
		return err
	}
	_ = repository.database.WithContext(ctx).Table("refresh_records").Where("user_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
	return nil
}

// UpdateRole updates an identity's role and invalidates active refresh sessions.
func (repository *AdminUserRepository) UpdateRole(ctx context.Context, id uint64, role string, organizationIDs ...uint64) error {
	now := time.Now().UTC()
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"role": role, "updated_at": now}
		if role == "super_admin" {
			updates["organization_id"] = nil
		} else {
			if len(organizationIDs) == 0 || organizationIDs[0] == 0 {
				return domain.ErrInvalidUserOrganization
			}
			var organization OrganizationRecord
			if err := tx.Where("id = ? AND status = ?", organizationIDs[0], "active").First(&organization).Error; err != nil {
				return err
			}
			updates["organization_id"] = organizationIDs[0]
		}
		if err := tx.Model(&adminUserRecord{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Table("refresh_records").Where("user_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
	})
}

// UpdateStatus updates an identity's status and invalidates active refresh sessions when disabled.
func (repository *AdminUserRepository) UpdateStatus(ctx context.Context, id uint64, status string) error {
	now := time.Now().UTC()
	err := repository.database.WithContext(ctx).Model(&adminUserRecord{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"updated_at": now,
	}).Error
	if err != nil {
		return err
	}
	if status == "disabled" {
		_ = repository.database.WithContext(ctx).Table("refresh_records").Where("user_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
	}
	return nil
}

func (repository *AdminUserRepository) ListLoginRecords(ctx context.Context, userID uint64, page, size int) ([]domain.AdminLoginRecord, int64, error) {
	query := repository.database.WithContext(ctx).Model(&adminLoginLogRecord{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []adminLoginLogRecord
	if err := query.Select("id, client_ip, success, reason, created_at").Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.AdminLoginRecord, 0, len(records))
	for _, record := range records {
		items = append(items, domain.AdminLoginRecord{ID: record.ID, ClientIP: record.ClientIP, Success: record.Success, Reason: record.Reason, CreatedAt: record.CreatedAt})
	}
	return items, total, nil
}

// UpdateOrganization moves only the administrator; credentials and historical usage stay with their organizations.
func (repository *AdminUserRepository) UpdateOrganization(ctx context.Context, userID, organizationID uint64) error {
	return repository.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user adminUserRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrAdminUserNotFound
			}
			return err
		}
		var organization OrganizationRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", organizationID, "active").First(&organization).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrTargetOrganizationUnavailable
			}
			return err
		}
		if user.OrganizationID == organizationID {
			return nil
		}
		if user.Role == "super_admin" {
			return domain.ErrInvalidUserOrganization
		}
		now := time.Now().UTC()
		if err := tx.Model(&adminUserRecord{}).Where("id = ?", userID).Updates(map[string]any{"organization_id": organizationID, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Table("refresh_records").Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
	})
}

var _ domain.AdminUserRepository = (*AdminUserRepository)(nil)

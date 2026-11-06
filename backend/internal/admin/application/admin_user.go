package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	authDomain "bosapi/backend/internal/auth/domain"
)

// AdminUserService coordinates safe administrative user queries and operations.
type AdminUserService struct {
	repository        domain.AdminUserRepository
	passwordHash      func(string) (string, error)
	loginLockUnlocker LoginLockUnlocker
	loginLockStore    authDomain.LoginLockStore
}

// LoginLockUnlocker removes shared temporary login lock state.
type LoginLockUnlocker interface {
	Unlock(context.Context, string) error
}

// NewAdminUserService constructs administrative user query operations.
func NewAdminUserService(repository domain.AdminUserRepository, passwordHash ...func(string) (string, error)) *AdminUserService {
	var hasher func(string) (string, error)
	if len(passwordHash) > 0 {
		hasher = passwordHash[0]
	}
	return &AdminUserService{
		repository:   repository,
		passwordHash: hasher,
	}
}

// WithLoginLockUnlocker enables manual recovery from temporary account lockouts.
func (service *AdminUserService) WithLoginLockUnlocker(unlocker LoginLockUnlocker) *AdminUserService {
	service.loginLockUnlocker = unlocker
	return service
}

// WithLoginLockStore exposes real-time lock state only to super administrators.
func (service *AdminUserService) WithLoginLockStore(store authDomain.LoginLockStore) *AdminUserService {
	service.loginLockStore = store
	return service
}

// List returns a bounded page of users without credentials.
func (service *AdminUserService) List(ctx context.Context, filter domain.AdminUserFilter, page, size int, callerRole string) ([]domain.AdminUser, int64, error) {
	if page < 1 || size < 1 || size > 100 || filter.Role != "" && filter.Role != "org_admin" && filter.Role != "super_admin" || filter.Status != "" && filter.Status != "active" && filter.Status != "disabled" {
		return nil, 0, ErrInvalidOrganization
	}
	users, total, err := service.repository.ListAdminUsers(ctx, filter, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin users: %w", err)
	}
	service.withLoginLockState(ctx, users, callerRole)
	return users, total, nil
}

func (service *AdminUserService) withLoginLockState(ctx context.Context, users []domain.AdminUser, callerRole string) {
	if callerRole != "super_admin" || service.loginLockStore == nil {
		return
	}
	now := time.Now().UTC()
	for index := range users {
		decision, err := service.loginLockStore.Check(ctx, strings.ToLower(strings.TrimSpace(users[index].Email)), now)
		if err == nil && decision.Locked && decision.RetryAfter > 0 {
			until := now.Add(decision.RetryAfter)
			users[index].LoginLocked = true
			users[index].LoginLockedUntil = &until
		}
	}
}

func (service *AdminUserService) ListLoginRecords(ctx context.Context, callerRole string, userID uint64, page, size int) ([]domain.AdminLoginRecord, int64, error) {
	if callerRole != "super_admin" || userID == 0 || page < 1 || size < 1 || size > 100 {
		return nil, 0, errors.New("forbidden or invalid login record request")
	}
	items, total, err := service.repository.ListLoginRecords(ctx, userID, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("list login records: %w", err)
	}
	return items, total, nil
}

// ResetPassword resets a user's password with administrator privileges.
func (service *AdminUserService) ResetPassword(ctx context.Context, callerRole string, targetUserID uint64, newPassword string) error {
	if callerRole != "super_admin" {
		return errors.New("forbidden: only super administrators can reset user passwords")
	}
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return errors.New("password must contain between 12 and 128 characters")
	}
	targetUser, err := service.repository.FindByID(ctx, targetUserID)
	if err != nil {
		return errors.New("user not found")
	}
	if targetUser.Role == "super_admin" && callerRole != "super_admin" {
		return errors.New("only super administrators can reset super admin passwords")
	}
	if service.passwordHash == nil {
		return errors.New("password hasher not configured")
	}
	hash, err := service.passwordHash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := service.repository.UpdatePassword(ctx, targetUserID, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// ResetMFA resets a user's two-factor authentication with administrator privileges.
func (service *AdminUserService) ResetMFA(ctx context.Context, callerRole string, targetUserID uint64) error {
	if callerRole != "super_admin" {
		return errors.New("forbidden: only super administrators can reset user MFA")
	}
	targetUser, err := service.repository.FindByID(ctx, targetUserID)
	if err != nil {
		return errors.New("user not found")
	}
	if targetUser.Role == "super_admin" && callerRole != "super_admin" {
		return errors.New("only super administrators can reset super admin MFA")
	}
	return service.repository.ResetMFA(ctx, targetUserID)
}

// UnlockLogin removes a temporary login lock after explicit administrator action.
func (service *AdminUserService) UnlockLogin(ctx context.Context, callerRole string, targetUserID uint64) error {
	if callerRole != "super_admin" {
		return errors.New("forbidden: only super administrators can unlock logins")
	}
	targetUser, err := service.repository.FindByID(ctx, targetUserID)
	if err != nil {
		return errors.New("user not found")
	}
	if service.loginLockUnlocker == nil {
		return errors.New("login lock store is unavailable")
	}
	return service.loginLockUnlocker.Unlock(ctx, strings.ToLower(strings.TrimSpace(targetUser.Email)))
}

// UpdateRole updates an administrative user's role with permission enforcement.
func (service *AdminUserService) UpdateRole(ctx context.Context, callerRole string, callerID uint64, targetUserID uint64, newRole string, organizationIDs ...uint64) error {
	if newRole != "super_admin" && newRole != "org_admin" {
		return errors.New("invalid role")
	}
	if callerRole != "super_admin" {
		return errors.New("forbidden: insufficient privileges")
	}
	if callerID > 0 && callerID == targetUserID {
		return errors.New("cannot change your own role")
	}
	targetUser, err := service.repository.FindByID(ctx, targetUserID)
	if err != nil {
		return errors.New("user not found")
	}
	if targetUser.Role == "super_admin" && callerRole != "super_admin" {
		return errors.New("only super administrators can modify super admin roles")
	}
	if newRole == "super_admin" && callerRole != "super_admin" {
		return errors.New("only super administrators can promote users to super admin")
	}
	if targetUser.Role == newRole {
		return nil
	}
	organizationID := targetUser.OrganizationID
	if len(organizationIDs) > 0 {
		organizationID = organizationIDs[0]
	}
	if newRole == "super_admin" {
		organizationID = 0
	} else if organizationID == 0 {
		return domain.ErrInvalidUserOrganization
	}
	if err := service.repository.UpdateRole(ctx, targetUserID, newRole, organizationID); err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	return nil
}

// UpdateStatus updates an administrative user's status with permission enforcement.
func (service *AdminUserService) UpdateStatus(ctx context.Context, callerRole string, callerID uint64, targetUserID uint64, newStatus string) error {
	if newStatus != "active" && newStatus != "disabled" {
		return errors.New("invalid status")
	}
	if callerRole != "super_admin" {
		return errors.New("forbidden: only super administrators can update user status")
	}
	if callerID > 0 && callerID == targetUserID && newStatus == "disabled" {
		return errors.New("cannot disable your own account")
	}
	targetUser, err := service.repository.FindByID(ctx, targetUserID)
	if err != nil {
		return errors.New("user not found")
	}
	if targetUser.Status == newStatus {
		return nil
	}
	if err := service.repository.UpdateStatus(ctx, targetUserID, newStatus); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// UpdateOrganization changes only an administrator's organization, never keys or usage.
func (service *AdminUserService) UpdateOrganization(ctx context.Context, callerRole string, callerID, targetUserID, organizationID uint64) error {
	if callerRole != "super_admin" {
		return domain.ErrAdminUserForbidden
	}
	if callerID == 0 || targetUserID == 0 || organizationID == 0 || callerID == targetUserID {
		return domain.ErrInvalidUserOrganization
	}
	if err := service.repository.UpdateOrganization(ctx, targetUserID, organizationID); err != nil {
		return fmt.Errorf("update user organization: %w", err)
	}
	return nil
}

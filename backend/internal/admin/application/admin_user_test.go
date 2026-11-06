package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	authDomain "bosapi/backend/internal/auth/domain"
)

type mockAdminUserRepo struct {
	users               map[uint64]domain.AdminUser
	updatedPass         map[uint64]string
	organizationUpdates int
}

func (m *mockAdminUserRepo) ListAdminUsers(ctx context.Context, filter domain.AdminUserFilter, page, size int) ([]domain.AdminUser, int64, error) {
	var res []domain.AdminUser
	for _, u := range m.users {
		res = append(res, u)
	}
	return res, int64(len(res)), nil
}

func (m *mockAdminUserRepo) FindByID(ctx context.Context, id uint64) (domain.AdminUser, error) {
	u, ok := m.users[id]
	if !ok {
		return domain.AdminUser{}, errors.New("user not found")
	}
	return u, nil
}

func (m *mockAdminUserRepo) UpdatePassword(ctx context.Context, id uint64, hash string) error {
	m.updatedPass[id] = hash
	return nil
}

func (m *mockAdminUserRepo) ResetMFA(ctx context.Context, id uint64) error {
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.TOTPEnabled = false
	m.users[id] = u
	return nil
}

func (m *mockAdminUserRepo) UpdateRole(ctx context.Context, id uint64, role string, organizationIDs ...uint64) error {
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Role = role
	m.users[id] = u
	return nil
}

func (m *mockAdminUserRepo) UpdateStatus(ctx context.Context, id uint64, status string) error {
	u, ok := m.users[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Status = status
	m.users[id] = u
	return nil
}

func (m *mockAdminUserRepo) ListLoginRecords(context.Context, uint64, int, int) ([]domain.AdminLoginRecord, int64, error) {
	return nil, 0, nil
}

func (m *mockAdminUserRepo) UpdateOrganization(ctx context.Context, id, organizationID uint64) error {
	m.organizationUpdates++
	user, ok := m.users[id]
	if !ok {
		return domain.ErrAdminUserNotFound
	}
	user.OrganizationID = organizationID
	m.users[id] = user
	return nil
}

func TestUpdateUserOrganizationAuthorization(t *testing.T) {
	for _, test := range []struct {
		name                             string
		role                             string
		callerID, userID, organizationID uint64
		want                             error
	}{
		{"super admin", "super_admin", 9, 1, 2, nil},
		{"organization admin", "org_admin", 9, 1, 2, domain.ErrAdminUserForbidden},
		{"regular user", "user", 9, 1, 2, domain.ErrAdminUserForbidden},
		{"own organization", "super_admin", 1, 1, 2, domain.ErrInvalidUserOrganization},
		{"missing actor", "super_admin", 0, 1, 2, domain.ErrInvalidUserOrganization},
		{"missing user id", "super_admin", 9, 0, 2, domain.ErrInvalidUserOrganization},
		{"missing organization", "super_admin", 9, 1, 0, domain.ErrInvalidUserOrganization},
		{"unknown user", "super_admin", 9, 99, 2, domain.ErrAdminUserNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &mockAdminUserRepo{users: map[uint64]domain.AdminUser{1: {ID: 1, OrganizationID: 10}}}
			err := NewAdminUserService(repo).UpdateOrganization(context.Background(), test.role, test.callerID, test.userID, test.organizationID)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.want == nil && repo.users[1].OrganizationID != 2 {
				t.Fatal("user was not moved")
			}
			if test.want != nil && test.want != domain.ErrAdminUserNotFound && repo.organizationUpdates != 0 {
				t.Fatal("rejected request reached repository")
			}
		})
	}
}

func fakeHasher(pwd string) (string, error) {
	return "hashed:" + pwd, nil
}

type mockLoginLockUnlocker struct {
	key   string
	calls int
}

func (mock *mockLoginLockUnlocker) Unlock(_ context.Context, key string) error {
	mock.key = key
	mock.calls++
	return nil
}

func TestUnlockLoginAuthorizationAndEmailKey(t *testing.T) {
	repo := &mockAdminUserRepo{users: map[uint64]domain.AdminUser{1: {ID: 1, Email: "Locked@Example.com", Role: "org_admin"}}}
	unlocker := &mockLoginLockUnlocker{}
	svc := NewAdminUserService(repo).WithLoginLockUnlocker(unlocker)

	if err := svc.UnlockLogin(context.Background(), "org_admin", 1); err == nil {
		t.Fatal("expected permission error for non-super administrator")
	}
	if unlocker.calls != 0 {
		t.Fatal("rejected caller reached unlock store")
	}
	if err := svc.UnlockLogin(context.Background(), "super_admin", 1); err != nil {
		t.Fatalf("unlock login: %v", err)
	}
	if unlocker.calls != 1 || unlocker.key != "locked@example.com" {
		t.Fatalf("unlock calls=%d key=%q", unlocker.calls, unlocker.key)
	}
}

type mockLoginLockStore struct {
	decisions map[string]authDomain.LoginLockDecision
	err       error
	keys      []string
}

func (mock *mockLoginLockStore) Check(_ context.Context, key string, _ time.Time) (authDomain.LoginLockDecision, error) {
	mock.keys = append(mock.keys, key)
	return mock.decisions[key], mock.err
}

func (mock *mockLoginLockStore) RecordFailure(context.Context, string, time.Time) (authDomain.LoginLockDecision, error) {
	return authDomain.LoginLockDecision{}, nil
}

func (mock *mockLoginLockStore) Unlock(context.Context, string) error {
	return nil
}

func TestListExposesLoginLockStateToSuperAdmin(t *testing.T) {
	repo := &mockAdminUserRepo{users: map[uint64]domain.AdminUser{1: {ID: 1, Email: "Locked@Example.com", Role: "org_admin"}}}
	store := &mockLoginLockStore{decisions: map[string]authDomain.LoginLockDecision{
		"locked@example.com": {Locked: true, RetryAfter: 15 * time.Minute},
	}}
	svc := NewAdminUserService(repo).WithLoginLockStore(store)

	users, total, err := svc.List(context.Background(), domain.AdminUserFilter{}, 1, 20, "super_admin")
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("list users=%+v total=%d err=%v", users, total, err)
	}
	if !users[0].LoginLocked || users[0].LoginLockedUntil == nil {
		t.Fatalf("lock state=%+v", users[0])
	}
	if store.keys[0] != "locked@example.com" {
		t.Fatalf("lock key=%q", store.keys[0])
	}
	if now := time.Now().UTC(); now.Add(14*time.Minute).After(*users[0].LoginLockedUntil) || now.Add(16*time.Minute).Before(*users[0].LoginLockedUntil) {
		t.Fatalf("lock until=%s", *users[0].LoginLockedUntil)
	}
}

func TestListHidesLoginLockStateFromOrgAdmin(t *testing.T) {
	repo := &mockAdminUserRepo{users: map[uint64]domain.AdminUser{1: {ID: 1, Email: "Locked@Example.com", Role: "org_admin"}}}
	store := &mockLoginLockStore{decisions: map[string]authDomain.LoginLockDecision{
		"locked@example.com": {Locked: true, RetryAfter: 15 * time.Minute},
	}}
	svc := NewAdminUserService(repo).WithLoginLockStore(store)

	users, total, err := svc.List(context.Background(), domain.AdminUserFilter{}, 1, 20, "org_admin")
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("list users=%+v total=%d err=%v", users, total, err)
	}
	if users[0].LoginLocked || users[0].LoginLockedUntil != nil || len(store.keys) != 0 {
		t.Fatalf("lock state=%+v keys=%v", users[0], store.keys)
	}
}

func TestListToleratesLoginLockStoreFailure(t *testing.T) {
	repo := &mockAdminUserRepo{users: map[uint64]domain.AdminUser{1: {ID: 1, Email: "locked@example.com", Role: "org_admin"}}}
	store := &mockLoginLockStore{err: errors.New("redis unavailable")}
	svc := NewAdminUserService(repo).WithLoginLockStore(store)

	users, total, err := svc.List(context.Background(), domain.AdminUserFilter{}, 1, 20, "super_admin")
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("list users=%+v total=%d err=%v", users, total, err)
	}
	if users[0].LoginLocked || users[0].LoginLockedUntil != nil {
		t.Fatalf("lock state=%+v", users[0])
	}
}

func TestAdminResetPassword(t *testing.T) {
	now := time.Now()
	repo := &mockAdminUserRepo{
		users: map[uint64]domain.AdminUser{
			1: {ID: 1, Email: "org@example.com", Role: "org_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
			2: {ID: 2, Email: "super@example.com", Role: "super_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
		},
		updatedPass: make(map[uint64]string),
	}

	svc := NewAdminUserService(repo, fakeHasher)

	// Only super administrators may reset passwords.
	err := svc.ResetPassword(context.Background(), "org_admin", 1, "valid-long-password-123")
	if err == nil {
		t.Fatal("expected permission error for org admin")
	}

	err = svc.ResetPassword(context.Background(), "super_admin", 1, "short")
	if err == nil {
		t.Fatal("expected error for short password")
	}
	err = svc.ResetPassword(context.Background(), "super_admin", 1, "valid-long-password-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updatedPass[1] != "hashed:valid-long-password-123" {
		t.Fatal("password hash mismatch")
	}

	// Super admin resetting another super_admin
	err = svc.ResetPassword(context.Background(), "super_admin", 2, "new-super-password-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updatedPass[2] != "hashed:new-super-password-123" {
		t.Fatal("super admin password hash mismatch")
	}

	// 5. Non-existent user
	err = svc.ResetPassword(context.Background(), "super_admin", 999, "valid-long-password-123")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}
}

func TestAdminResetMFA(t *testing.T) {
	now := time.Now()
	repo := &mockAdminUserRepo{
		users: map[uint64]domain.AdminUser{
			1: {ID: 1, Email: "org@example.com", Role: "org_admin", Status: "active", TOTPEnabled: true, CreatedAt: now, UpdatedAt: now},
			2: {ID: 2, Email: "super@example.com", Role: "super_admin", Status: "active", TOTPEnabled: true, CreatedAt: now, UpdatedAt: now},
		},
		updatedPass: make(map[uint64]string),
	}

	svc := NewAdminUserService(repo, fakeHasher)

	if err := svc.ResetMFA(context.Background(), "org_admin", 1); err == nil {
		t.Fatal("expected permission error for org admin")
	}
	if err := svc.ResetMFA(context.Background(), "super_admin", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.users[1].TOTPEnabled {
		t.Fatal("expected TOTPEnabled to be false")
	}

	// Super admin resetting another super_admin MFA should succeed
	if err := svc.ResetMFA(context.Background(), "super_admin", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.users[2].TOTPEnabled {
		t.Fatal("expected super_admin TOTPEnabled to be false")
	}
}

func TestAdminUpdateRole(t *testing.T) {
	now := time.Now()
	repo := &mockAdminUserRepo{
		users: map[uint64]domain.AdminUser{
			1: {ID: 1, Email: "org@example.com", Role: "org_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
			2: {ID: 2, Email: "org2@example.com", Role: "org_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
			3: {ID: 3, Email: "super@example.com", Role: "super_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
		},
		updatedPass: make(map[uint64]string),
	}

	svc := NewAdminUserService(repo, fakeHasher)

	// Invalid role string
	err := svc.UpdateRole(context.Background(), "super_admin", 3, 1, "invalid_role")
	if err == nil {
		t.Fatal("expected error for invalid role")
	}

	// Org admins cannot change roles.
	err = svc.UpdateRole(context.Background(), "org_admin", 99, 1, "super_admin")
	if err == nil {
		t.Fatal("expected error for org_admin caller")
	}

	// Caller cannot modify their own role.
	err = svc.UpdateRole(context.Background(), "super_admin", 3, 3, "org_admin")
	if err == nil {
		t.Fatal("expected error when caller changes own role")
	}

	// Super admin promotes an org admin.
	err = svc.UpdateRole(context.Background(), "super_admin", 3, 1, "super_admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.users[1].Role != "super_admin" {
		t.Fatalf("expected role super_admin, got %s", repo.users[1].Role)
	}

	// Super admin demotes another super admin.
	err = svc.UpdateRole(context.Background(), "super_admin", 3, 1, "org_admin", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.users[1].Role != "org_admin" {
		t.Fatalf("expected role org_admin, got %s", repo.users[1].Role)
	}

	// Non-existent user
	err = svc.UpdateRole(context.Background(), "super_admin", 3, 999, "org_admin")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}

	// Same role (no-op)
	err = svc.UpdateRole(context.Background(), "super_admin", 3, 2, "org_admin")
	if err != nil {
		t.Fatalf("unexpected error on same role: %v", err)
	}
}

func TestAdminUpdateStatus(t *testing.T) {
	now := time.Now()
	repo := &mockAdminUserRepo{
		users: map[uint64]domain.AdminUser{
			1: {ID: 1, Email: "org@example.com", Role: "org_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
			2: {ID: 2, Email: "disabled@example.com", Role: "org_admin", Status: "disabled", CreatedAt: now, UpdatedAt: now},
			3: {ID: 3, Email: "super@example.com", Role: "super_admin", Status: "active", CreatedAt: now, UpdatedAt: now},
		},
		updatedPass: make(map[uint64]string),
	}

	svc := NewAdminUserService(repo, fakeHasher)

	// Invalid status
	err := svc.UpdateStatus(context.Background(), "super_admin", 3, 1, "invalid_status")
	if err == nil {
		t.Fatal("expected error for invalid status")
	}

	// Org admin cannot update status (forbidden)
	err = svc.UpdateStatus(context.Background(), "org_admin", 1, 2, "active")
	if err == nil {
		t.Fatal("expected error for org_admin caller")
	}

	// Caller cannot disable their own account
	err = svc.UpdateStatus(context.Background(), "super_admin", 3, 3, "disabled")
	if err == nil {
		t.Fatal("expected error when caller disables own account")
	}

	// Super admin disables an org admin
	err = svc.UpdateStatus(context.Background(), "super_admin", 3, 1, "disabled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.users[1].Status != "disabled" {
		t.Fatalf("expected status disabled, got %s", repo.users[1].Status)
	}

	// Super admin re-enables a disabled admin
	err = svc.UpdateStatus(context.Background(), "super_admin", 3, 2, "active")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.users[2].Status != "active" {
		t.Fatalf("expected status active, got %s", repo.users[2].Status)
	}

	// Non-existent user
	err = svc.UpdateStatus(context.Background(), "super_admin", 3, 999, "disabled")
	if err == nil {
		t.Fatal("expected error for non-existent user")
	}

	// Same status (no-op)
	err = svc.UpdateStatus(context.Background(), "super_admin", 3, 1, "disabled")
	if err != nil {
		t.Fatalf("unexpected error on same status: %v", err)
	}
}

package infrastructure

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestUpdateOrganizationTransaction(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, test := range []struct {
		name, failAt     string
		sameOrganization bool
		want             error
	}{
		{name: "administrator moves without moving keys"},
		{name: "same organization is a no-op", sameOrganization: true},
		{name: "missing user", failAt: "user missing", want: domain.ErrAdminUserNotFound},
		{name: "user lookup failure", failAt: "user", want: failure},
		{name: "missing or inactive organization", failAt: "organization missing", want: domain.ErrTargetOrganizationUnavailable},
		{name: "organization lookup failure", failAt: "organization", want: failure},
		{name: "user update failure", failAt: "user update", want: failure},
		{name: "session failure rolls back user", failAt: "sessions", want: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqlDatabase, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDatabase.Close()
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			userQuery := mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `users` WHERE `users`.`id` = ? ORDER BY `users`.`id` LIMIT ? FOR UPDATE")).WithArgs(uint64(1), 1)
			if test.failAt == "user missing" {
				userQuery.WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id"}))
			} else if test.failAt == "user" {
				userQuery.WillReturnError(failure)
			} else {
				oldOrganization := 10
				if test.sameOrganization {
					oldOrganization = 20
				}
				userQuery.WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id"}).AddRow(1, oldOrganization))
				organizationQuery := mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `organizations` WHERE id = ? AND status = ? ORDER BY `organizations`.`id` LIMIT ? FOR UPDATE")).WithArgs(uint64(20), "active", 1)
				if test.failAt == "organization missing" {
					organizationQuery.WillReturnRows(sqlmock.NewRows([]string{"id"}))
				} else if test.failAt == "organization" {
					organizationQuery.WillReturnError(failure)
				} else {
					organizationQuery.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(20))
					if !test.sameOrganization {
						userUpdate := mock.ExpectExec(regexp.QuoteMeta("UPDATE `users` SET `organization_id`=?,`updated_at`=? WHERE id = ?")).WithArgs(uint64(20), sqlmock.AnyArg(), uint64(1))
						if test.failAt == "user update" {
							userUpdate.WillReturnError(failure)
						} else {
							userUpdate.WillReturnResult(sqlmock.NewResult(0, 1))
							sessions := mock.ExpectExec(regexp.QuoteMeta("UPDATE `refresh_records` SET `revoked_at`=? WHERE user_id = ? AND revoked_at IS NULL")).WithArgs(sqlmock.AnyArg(), uint64(1))
							if test.failAt == "sessions" {
								sessions.WillReturnError(failure)
							} else {
								sessions.WillReturnResult(sqlmock.NewResult(0, 2))
							}
						}
					}
				}
			}
			if test.want != nil {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err = NewAdminUserRepository(database).UpdateOrganization(context.Background(), 1, 20)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestListAdminUsers(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewAdminUserRepository(database)

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `users`").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery("SELECT id, organization_id, email, role, status, totp_enabled, created_at, updated_at FROM `users` ORDER BY id ASC LIMIT \\?").
		WithArgs(20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "email", "role", "status", "totp_enabled", "created_at", "updated_at"}).
			AddRow(1, 1, "admin@example.com", "admin", "active", false, time.Now(), time.Now()))

	users, total, err := repo.ListAdminUsers(context.Background(), domain.AdminUserFilter{}, 1, 20)
	if err != nil {
		t.Fatalf("ListAdminUsers error: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
}

func TestUpdateRole(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewAdminUserRepository(database)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `organizations` WHERE id = \\? AND status = \\?").WithArgs(uint64(7), "active", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectExec("UPDATE `users` SET `organization_id`=\\?,`role`=\\?,`updated_at`=\\? WHERE id = \\?").
		WithArgs(uint64(7), "org_admin", sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `refresh_records` SET `revoked_at`=\\? WHERE user_id = \\? AND revoked_at IS NULL").
		WithArgs(sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = repo.UpdateRole(context.Background(), 1, "org_admin", 7)
	if err != nil {
		t.Fatalf("UpdateRole failed: %v", err)
	}
}

func TestUpdateStatus(t *testing.T) {
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDatabase.Close()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewAdminUserRepository(database)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `users` SET `status`=\\?,`updated_at`=\\? WHERE id = \\?").
		WithArgs("disabled", sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `refresh_records` SET `revoked_at`=\\? WHERE user_id = \\? AND revoked_at IS NULL").
		WithArgs(sqlmock.AnyArg(), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = repo.UpdateStatus(context.Background(), 1, "disabled")
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
}

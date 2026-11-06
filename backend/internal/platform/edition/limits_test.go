package edition

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCapacitySerializesCreatorsAndRejectsWholeOversizedBatches(t *testing.T) {
	for _, test := range []struct {
		name, resource     string
		existing, incoming int
		denied             bool
	}{
		{"second organization", "organizations", 1, 1, false},
		{"third organization", "organizations", 2, 1, true},
		{"last key", "api_keys", 9, 1, false},
		{"eleventh key", "api_keys", 10, 1, true},
		{"batch fills capacity", "api_keys", 8, 2, false},
		{"batch exceeds capacity", "api_keys", 8, 3, true},
		{"existing larger database", "api_keys", 12, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			orm, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT setting_key FROM settings WHERE setting_key = \\? FOR UPDATE").WithArgs(capacityLockKey).
				WillReturnRows(sqlmock.NewRows([]string{"setting_key"}).AddRow(capacityLockKey))
			mock.ExpectQuery("SELECT count.*FROM `" + test.resource + "` WHERE status <> \\?").WithArgs("deleted").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(test.existing))
			if test.denied {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err = orm.Transaction(func(tx *gorm.DB) error { return CheckCapacity(tx, test.resource, test.incoming) })
			if errors.Is(err, ErrLimitExceeded) != test.denied || !test.denied && err != nil {
				t.Fatalf("unexpected capacity result: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCapacityFailsClosedWhenLockRowIsUnavailable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	orm, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT setting_key FROM settings").WithArgs(capacityLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"setting_key"}))
	mock.ExpectRollback()
	if err := orm.Transaction(func(tx *gorm.DB) error { return CheckCapacity(tx, "api_keys", 1) }); err == nil {
		t.Fatal("missing migration lock must prevent creation")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

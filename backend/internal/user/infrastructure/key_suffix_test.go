package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"testing"
	"time"

	"bosapi/backend/internal/user/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestFindGatewayKeyBackfillsOnlyAuthenticatedMissingSuffix(t *testing.T) {
	for _, test := range []struct {
		name              string
		raw               string
		storedSuffix      string
		organizationState string
		missingKey        bool
		writeError        bool
		resetDuringLookup bool
		wantSuffix        string
	}{
		{name: "legacy key", wantSuffix: "ABCD"},
		{name: "already recovered", storedSuffix: "ABCD", wantSuffix: "ABCD"},
		{name: "unicode suffix", raw: "vend-甲乙丙丁戊己庚辛壬癸", wantSuffix: "庚辛壬癸"},
		{name: "wrong secret or disabled key", missingKey: true},
		{name: "inactive organization", organizationState: "disabled"},
		{name: "short key", raw: "vendABCD"},
		{name: "metadata write failure does not break auth", writeError: true},
		{name: "reset race cannot overwrite new suffix", resetDuringLookup: true},
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
			now := time.Now().UTC()
			raw := test.raw
			if raw == "" {
				raw = "vendor-legacy-secret-ABCD"
			}
			digest := sha256.Sum256([]byte(raw))
			rows := sqlmock.NewRows([]string{"id", "name", "key_hash", "key_prefix", "key_suffix", "user_id", "organization_id", "status"})
			if !test.missingKey {
				rows.AddRow(7, "example", digest[:], "vend", test.storedSuffix, 3, 2, "active")
			}
			mock.ExpectQuery("FROM `api_keys` WHERE key_hash = \\? AND status = \\?").WithArgs(digest[:], "active", now, 1).WillReturnRows(rows)
			organizationState := test.organizationState
			if organizationState == "" {
				organizationState = "active"
			}
			if !test.missingKey {
				mock.ExpectQuery("SELECT status FROM `organizations` WHERE id = \\?").WithArgs(uint64(2)).
					WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(organizationState))
			}
			if suffix := domain.SafeKeySuffix(raw, "vend"); !test.missingKey && organizationState == "active" && test.storedSuffix == "" && suffix != "" {
				update := mock.ExpectExec(regexp.QuoteMeta("UPDATE `api_keys` SET `key_suffix`=? WHERE id = ? AND key_hash = ? AND organization_id = ? AND status = ? AND key_suffix = ? AND (expires_at IS NULL OR expires_at > ?)")).
					WithArgs(suffix, uint64(7), digest[:], uint64(2), "active", "", now)
				if test.writeError {
					update.WillReturnError(errors.New("metadata unavailable"))
				} else if test.resetDuringLookup {
					update.WillReturnResult(sqlmock.NewResult(0, 0))
				} else {
					update.WillReturnResult(sqlmock.NewResult(0, 1))
				}
			}
			key, found, err := NewKeyRepository(database).FindGatewayKey(context.Background(), raw, now)
			wantFound := !test.missingKey && organizationState == "active"
			if err != nil || found != wantFound || key.KeySuffix != test.wantSuffix {
				t.Fatalf("authentication found=%v suffix=%q err=%v, want found=%v suffix=%q", found, key.KeySuffix, err, wantFound, test.wantSuffix)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAPIKeySuffixPersistsInBothRecordMappings(t *testing.T) {
	digest := sha256.Sum256([]byte("vendor-甲乙丙丁戊己庚辛壬癸"))
	key := domain.APIKey{ID: 7, Name: "example", KeyHash: hex.EncodeToString(digest[:]), KeyPrefix: "vend", KeySuffix: "庚辛壬癸", KeyEncrypted: "ciphertext", UserID: 3, OrganizationID: 2, Status: "active"}
	record, err := recordFromDomain(key)
	if err != nil {
		t.Fatal(err)
	}
	if record.KeySuffix != key.KeySuffix {
		t.Fatalf("stored suffix = %q, want %q", record.KeySuffix, key.KeySuffix)
	}
	roundTrip, err := recordToDomain(record)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.KeySuffix != key.KeySuffix || roundTrip.KeyHash != key.KeyHash || roundTrip.KeyEncrypted != key.KeyEncrypted {
		t.Fatal("key credential metadata changed during persistence mapping")
	}
}

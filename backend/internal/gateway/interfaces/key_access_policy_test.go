package interfaces

import (
	"context"
	"errors"
	"testing"

	"bosapi/backend/internal/platform/errorcode"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestKeyFiltersDenyBeforeAccessingChannels(t *testing.T) {
	for _, test := range []struct {
		name          string
		key           apiKey
		provider      int
		model, format string
		want          errorcode.Code
	}{
		{name: "channel denied", key: apiKey{OrgID: 3, AllowedProviderIDs: []uint64{7}}, provider: 8, want: errorcode.InvalidProvider},
		{name: "model denied", key: apiKey{OrgID: 3, AllowedModels: []string{"one"}}, provider: 7, model: "two", want: errorcode.ModelNotAllowed},
		{name: "Gemini exact normalized", key: apiKey{AllowedModels: []string{"models/one"}}, provider: 7, model: "one", format: "gemini"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := testApp(t)
			if got := a.databasePolicyDenial(context.Background(), test.key, test.provider, test.model, test.format); got != test.want {
				t.Fatalf("got=%s want=%s", got, test.want)
			}
		})
	}
}

func TestDatabaseSelectionNeverFallsBackToCachedProvider(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	a.database = db
	a.data.Providers = []provider{{ID: 99, Status: "active"}}
	mock.ExpectQuery("SELECT .* FROM `accounts`").WithArgs("active", true, 3, "active").WillReturnError(errors.New("database unavailable"))
	if p, ok := a.providerForKeyModel(context.Background(), apiKey{OrgID: 3}, "one"); ok {
		t.Fatalf("database failure selected a local provider: %+v", p)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

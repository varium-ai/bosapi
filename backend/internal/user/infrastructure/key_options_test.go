package infrastructure

import (
	admin "bosapi/backend/internal/admin/domain"
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"testing"
)

func TestKeyOptionsOnlyReadScopedMetadata(t *testing.T) {
	r, mock := usageRepository(t)
	mock.ExpectQuery("SELECT count.*FROM `api_keys` WHERE status <> \\? AND organization_id = \\?").WithArgs("deleted", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(144))
	mock.ExpectQuery("SELECT id, name, organization_id FROM `api_keys` WHERE status <> \\? AND organization_id = \\? ORDER BY created_at DESC, id DESC LIMIT \\? OFFSET \\?").WithArgs("deleted", uint64(7), 100, 100).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "organization_id"}).AddRow(8, "Option", 7))
	keys, total, err := r.ListAdminKeys(context.Background(), admin.AdminKeyFilter{OrganizationID: 7, OptionsOnly: true}, 2, 100)
	if err != nil || total != 144 || len(keys) != 1 || keys[0].Name != "Option" || keys[0].KeyHash != "" || keys[0].KeyEncrypted != "" || keys[0].UsageTodayAmount != nil {
		t.Fatalf("keys=%+v total=%d err=%v", keys, total, err)
	}
}

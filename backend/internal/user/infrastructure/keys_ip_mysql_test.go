package infrastructure

import (
	"context"
	"reflect"
	"testing"

	"bosapi/backend/internal/user/domain"
)

func TestUpdateKeyIPListsMySQL(t *testing.T) {
	database := newKeyUsageMySQL(t)
	if err := database.AutoMigrate(&APIKeyRecord{}); err != nil {
		t.Fatal(err)
	}
	repository := NewKeyRepository(database)
	ctx := context.Background()
	for _, mode := range []string{"admin", "owned", "owned with models"} {
		for _, field := range []string{"ip_allowlist", "ip_blocklist"} {
			if mode == "admin" && field == "ip_blocklist" {
				continue // The administrative edit allowlist exposes only ip_allowlist.
			}
			for _, test := range []struct {
				name   string
				values []string
			}{
				{"single CIDR", []string{"192.0.2.0/24"}},
				{"multiple IPv4 and IPv6 entries", []string{"192.0.2.1", "198.51.100.0/24", "2001:db8::/32"}},
				{"clear with empty list", []string{}},
				{"clear with nil list", nil},
			} {
				t.Run(mode+"/"+field+"/"+test.name, func(t *testing.T) {
					if err := database.Exec("DELETE FROM api_keys").Error; err != nil {
						t.Fatal(err)
					}
					original := APIKeyRecord{ID: 7, OrganizationID: 2, UserID: 3, Status: "active", AllowedProviderIDs: `[]`, AllowedModels: `[]`, IPAllowlist: `["203.0.113.1"]`, IPBlocklist: `["203.0.113.2"]`}
					if err := database.Create(&original).Error; err != nil {
						t.Fatal(err)
					}
					updates := map[string]any{field: test.values}
					var err error
					if mode == "admin" {
						err = repository.UpdateAdminKey(ctx, 7, 2, updates)
					} else {
						if mode == "owned with models" {
							updates["allowed_models"] = []string{"model-a"}
						}
						err = repository.UpdateOwned(ctx, 7, 3, 2, updates)
					}
					if err != nil {
						t.Fatal(err)
					}
					var record APIKeyRecord
					if err := database.First(&record, 7).Error; err != nil {
						t.Fatal(err)
					}
					key, err := recordToDomain(record)
					if err != nil {
						t.Fatal(err)
					}
					want := domain.APIKey{IPAllowlist: []string{"203.0.113.1"}, IPBlocklist: []string{"203.0.113.2"}}
					if field == "ip_allowlist" {
						want.IPAllowlist = test.values
					} else {
						want.IPBlocklist = test.values
					}
					if !reflect.DeepEqual(key.IPAllowlist, want.IPAllowlist) || !reflect.DeepEqual(key.IPBlocklist, want.IPBlocklist) {
						t.Fatalf("IP lists: got allow=%v block=%v; want allow=%v block=%v", key.IPAllowlist, key.IPBlocklist, want.IPAllowlist, want.IPBlocklist)
					}
					if mode == "owned with models" && !reflect.DeepEqual(key.AllowedModels, []string{"model-a"}) {
						t.Fatalf("models=%v", key.AllowedModels)
					}
				})
			}
		}
	}
}

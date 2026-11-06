package migrations

import "testing"

func TestAnnouncementOrganizationMigrationMySQL(t *testing.T) {
	conn, ctx := legacyBindingsTestDatabase(t)
	for _, migration := range []string{"022_announcements.sql", "023_announcement_organizations.sql"} {
		script, err := Files.ReadFile(migration)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
		if migration == "022_announcements.sql" {
			if _, err := conn.ExecContext(ctx, "INSERT INTO announcements (title, body) VALUES (?, ?)", "Legacy global", "Body"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var organizationID uint64
	if err := conn.QueryRowContext(ctx, "SELECT organization_id FROM announcements WHERE id = ?", 1).Scan(&organizationID); err != nil || organizationID != 0 {
		t.Fatalf("legacy publication changed scope: %d %v", organizationID, err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO announcements (organization_id, title, body) VALUES (?, ?, ?), (?, ?, ?)", 7, "Private 7", "Body", 99, "Private 99", "Body"); err != nil {
		t.Fatal(err)
	}
	rollback, err := Files.ReadFile("023_announcement_organizations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, string(rollback)); err != nil {
		t.Fatal(err)
	}
	var total int
	var title string
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*), MAX(title) FROM announcements").Scan(&total, &title); err != nil || total != 1 || title != "Legacy global" {
		t.Fatalf("rollback exposed private publications: total=%d title=%s err=%v", total, title, err)
	}
}

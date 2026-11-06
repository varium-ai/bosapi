package infrastructure

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/migrations"
)

func TestAdminIPSummaryMySQL(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	initial, _ := migrations.Files.ReadFile("001_initial_schema.sql")
	script := string(initial)
	start := strings.Index(script, "CREATE TABLE IF NOT EXISTS security_events (")
	end := start + strings.Index(script[start:], ";") + 1
	if err := db.Exec(script[start:end]).Error; err != nil {
		t.Fatal(err)
	}
	migration, _ := migrations.Files.ReadFile("024_usage_ip_covering_indexes.sql")
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	old := at.Add(-90 * 24 * time.Hour)
	// Shared IPv4 across both usage tables and security, IPv6, missing IPs,
	// security-only IPs, and a Key with historical ownership in two organizations.
	for i, f := range []struct {
		org, key int
		ip       string
		when     time.Time
	}{
		{1, 149, "192.0.2.1", old}, {1, 149, "192.0.2.1", at},
		{1, 149, "2001:db8::1", at}, {2, 149, "192.0.2.1", at},
		{1, 150, "192.0.2.99", at}, {1, 149, "", at},
		{1, 149, "192.0.2.250", at.Add(48 * time.Hour)},
	} {
		if err := db.Exec(`INSERT INTO usage_logs(id,organization_id,api_key_id,client_ip,total_tokens,cost_amount,created_at) VALUES (?,?,?,INET6_ATON(NULLIF(?,'')),0,0,?)`, i+1, f.org, f.key, f.ip, f.when).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO usage_logs_v2 SELECT * FROM usage_logs WHERE MOD(id,2)=0; DELETE FROM usage_logs WHERE MOD(id,2)=0`).Error; err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		org, key int
		ip       string
	}{{1, 149, "192.0.2.1"}, {1, 149, "192.0.2.2"}, {2, 149, "192.0.2.3"}, {1, 150, "192.0.2.99"}, {1, 149, ""}} {
		if err := db.Exec(`INSERT INTO security_events(organization_id,api_key_id,client_ip,created_at) VALUES (?,?,INET6_ATON(NULLIF(?,'')),?)`, f.org, f.key, f.ip, at).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := NewAdminUsageRepository(db)
	wanted := []domain.AdminUsageIP{
		{ClientIP: "192.0.2.1", Requests: 2, SecurityEvents: 1, LastSeen: at},
		{ClientIP: "192.0.2.2", SecurityEvents: 1, LastSeen: at},
		{ClientIP: "192.0.2.3", SecurityEvents: 1, LastSeen: at},
		{ClientIP: "2001:db8::1", Requests: 1, LastSeen: at},
	}
	filter := domain.AdminUsageFilter{APIKeyID: 149}
	items, total, err := r.GroupAdminUsageByIP(ctx, filter, 1, 20)
	if err != nil || total != 4 || !reflect.DeepEqual(items, wanted) {
		t.Fatalf("items=%+v total=%d err=%v", items, total, err)
	}
	for page := 1; page <= 5; page++ {
		rows, total, err := r.GroupAdminUsageByIP(ctx, filter, page, 1)
		want := []domain.AdminUsageIP{}
		if page <= 4 {
			want = wanted[page-1 : page]
		}
		if err != nil || total != 4 || !reflect.DeepEqual(rows, want) {
			t.Fatalf("page %d rows=%+v total=%d err=%v", page, rows, total, err)
		}
	}
	filter.OrganizationID = 1
	rows, total, err := r.GroupAdminUsageByIP(ctx, filter, 1, 20)
	if err != nil || total != 3 || rows[0].Requests != 1 {
		t.Fatalf("tenant: %+v %d %v", rows, total, err)
	}
	from := at.Add(-time.Hour)
	to := at.Add(time.Hour)
	filter.From, filter.To = &from, &to
	filter.ClientIP = "192.0.2.1"
	rows, total, err = r.GroupAdminUsageByIP(ctx, filter, 1, 20)
	if err != nil || total != 1 || rows[0].Requests != 1 || rows[0].SecurityEvents != 1 {
		t.Fatalf("range/IP: %+v %d %v", rows, total, err)
	}
	filter.To = &at // Exclusive end excludes both usage and security at the boundary.
	rows, total, err = r.GroupAdminUsageByIP(ctx, filter, 1, 20)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("exclusive end: %+v %d %v", rows, total, err)
	}
	// Every branch must seek by Key and time, rather than scan all Key IPs.
	for _, table := range []string{"usage_logs", "usage_logs_v2", "security_events"} {
		index := "idx_usage_key_time"
		if table == "security_events" {
			index = "idx_security_key_time"
		}
		var plan []struct {
			Key  string
			Type string
		}
		err := db.Raw("EXPLAIN SELECT client_ip, COUNT(*), MAX(created_at) FROM "+table+" FORCE INDEX ("+index+") WHERE api_key_id = 149 AND created_at >= ? AND created_at < ? AND client_ip IS NOT NULL AND LENGTH(client_ip)>0 GROUP BY client_ip", at.Add(-24*time.Hour), at.Add(time.Second)).Scan(&plan).Error
		if err != nil || len(plan) != 1 || plan[0].Key != index || plan[0].Type != "range" {
			t.Fatalf("%s plan=%+v err=%v", table, plan, err)
		}
	}
	down, _ := migrations.Files.ReadFile("024_usage_ip_covering_indexes.down.sql")
	if err := db.Exec(string(down)).Error; err != nil {
		t.Fatal(err)
	}
	// The query stays compatible during rollback; the indexes only affect speed.
	if _, _, err := r.GroupAdminUsageByIP(ctx, domain.AdminUsageFilter{APIKeyID: 149}, 1, 20); err != nil {
		t.Fatal(err)
	}
}

// Historical rows must not contribute to the rolling window even when the
// caller explicitly asks for an all-history range.
func TestAdminIPSummaryLargeHistoryMySQL(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	initial, _ := migrations.Files.ReadFile("001_initial_schema.sql")
	script := string(initial)
	start := strings.Index(script, "CREATE TABLE IF NOT EXISTS security_events (")
	if err := db.Exec(script[start : start+strings.Index(script[start:], ";")+1]).Error; err != nil {
		t.Fatal(err)
	}
	const digits = "(SELECT 0 AS n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := db.Exec(`INSERT INTO usage_logs (organization_id,api_key_id,client_ip,total_tokens,cost_amount,created_at)
 SELECT 1,149,INET6_ATON(CONCAT('192.0.2.', MOD(a.n+10*b.n,100)+1)),0,0,?
 FROM `+digits+` a CROSS JOIN `+digits+` b CROSS JOIN `+digits+` c CROSS JOIN `+digits+` d CROSS JOIN `+digits+` e`, old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO usage_logs_v2 SELECT * FROM usage_logs WHERE MOD(id,2)=0; DELETE FROM usage_logs WHERE MOD(id,2)=0`).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	if err := db.Exec(`INSERT INTO usage_logs(organization_id,api_key_id,client_ip,total_tokens,cost_amount,created_at) VALUES(1,149,INET6_ATON('2001:db8::1'),0,0,?)`, at).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO security_events(organization_id,api_key_id,client_ip,created_at) VALUES(1,149,INET6_ATON('192.0.2.250'),?),(1,149,INET6_ATON('192.0.2.251'),?),(1,149,INET6_ATON('192.0.2.252'),?)`, at, old, at.Add(48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	cached := NewCachedAdminUsageRepository(db)
	defer cached.Close()
	from, to := old.Add(-24*time.Hour), at.Add(48*time.Hour)
	f := domain.AdminUsageFilter{APIKeyID: 149, From: &from, To: &to, IPWindowMinutes: 1440}
	first, err := cached.GroupAdminUsageByIPSnapshot(context.Background(), f, 1, 1)
	if err != nil || first.Snapshot.Status != "ready" || first.Total != 2 || len(first.Items) != 1 || first.Items[0].ClientIP != "192.0.2.250" || first.Items[0].SecurityEvents != 1 {
		t.Fatalf("result=%+v err=%v", first, err)
	}
	f.SnapshotID = first.Snapshot.ID
	second, err := cached.GroupAdminUsageByIPSnapshot(context.Background(), f, 2, 1)
	if err != nil || second.Snapshot.ID != first.Snapshot.ID || len(second.Items) != 1 || second.Items[0].ClientIP != "2001:db8::1" || second.Items[0].Requests != 1 {
		t.Fatalf("page2=%+v err=%v", second, err)
	}
	// Explicitly requesting an old window returns an empty result, never history.
	to = old.Add(time.Hour)
	rows, total, err := NewAdminUsageRepository(db).GroupAdminUsageByIP(context.Background(), f, 1, 20)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("old window=%+v total=%d err=%v", rows, total, err)
	}
}

func TestAdminIPWindowPresetsMySQL(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	initial, _ := migrations.Files.ReadFile("001_initial_schema.sql")
	script := string(initial)
	start := strings.Index(script, "CREATE TABLE IF NOT EXISTS security_events (")
	if err := db.Exec(script[start : start+strings.Index(script[start:], ";")+1]).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i, minutes := range []int{15, 45, 180, 540, 1080, 1500} {
		at := now.Add(-time.Duration(minutes) * time.Minute)
		if err := db.Exec(`INSERT INTO usage_logs(id,organization_id,api_key_id,client_ip,total_tokens,cost_amount,created_at) VALUES(?,1,149,INET6_ATON('192.0.2.1'),0,0,?)`, i+1, at).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO security_events(organization_id,api_key_id,client_ip,created_at) VALUES(1,149,INET6_ATON('192.0.2.1'),?)`, at).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO usage_logs_v2 SELECT * FROM usage_logs WHERE MOD(id,2)=0; DELETE FROM usage_logs WHERE MOD(id,2)=0`).Error; err != nil {
		t.Fatal(err)
	}
	cached := NewCachedAdminUsageRepository(db)
	defer cached.Close()
	for i, minutes := range []int{30, 60, 360, 720, 1440} {
		result, err := cached.GroupAdminUsageByIPSnapshot(context.Background(), domain.AdminUsageFilter{OrganizationID: 1, APIKeyID: 149, IPWindowMinutes: minutes}, 1, 20)
		if err != nil || result.Snapshot.Status != "ready" || result.Total != 1 || len(result.Items) != 1 || result.Items[0].Requests != uint64(i+1) || result.Items[0].SecurityEvents != uint64(i+1) || result.Snapshot.ValidUntil.Sub(result.Snapshot.GeneratedAt) != 15*time.Minute {
			t.Fatalf("range=%d result=%+v err=%v", minutes, result, err)
		}
	}
}

package telemetry

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

func isolated(t *testing.T) *Monitor {
	t.Helper()
	t.Setenv("BOS_MONITOR_NODE_DIR", "")
	t.Setenv("BOS_MONITOR_TLS_TARGETS", "")
	return &Monitor{started: time.Now().Add(-time.Minute)}
}

func TestBoundedHistoryAndImmutableSnapshot(t *testing.T) {
	m := isolated(t)
	m.sample(context.Background())
	first := m.Snapshot()
	for i := 0; i < 140; i++ {
		m.sample(context.Background())
	}
	if len(m.Snapshot().History) != HistoryLimit || len(first.History) != 1 {
		t.Fatal("history is not bounded/immutable")
	}
	if m.Snapshot().Traffic["error_rate"].(*float64) != nil {
		t.Fatal("no traffic must not claim zero error rate")
	}
}

func TestConcurrentSnapshotReadsNeverProbe(t *testing.T) {
	m := isolated(t)
	calls := 0
	m.sources.Redis = func(context.Context) error { calls++; return nil }
	m.sample(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = m.Snapshot()
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("reads triggered %d probes", calls)
	}
}

func TestGatewayMetricsExcludeHealthAndAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := isolated(t)
	router := gin.New()
	router.Use(m.Middleware())
	router.GET("/v1/models", func(c *gin.Context) { c.Status(200) })
	router.GET("/v1/failure", func(c *gin.Context) { c.Status(503) })
	router.GET("/health/ready", func(c *gin.Context) { c.Status(200) })
	router.GET("/api/v1/admin/status", func(c *gin.Context) { c.Status(200) })
	for _, path := range []string{"/v1/models", "/v1/failure", "/health/ready", "/api/v1/admin/status"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	m.sample(context.Background())
	snapshot := m.Snapshot()
	if snapshot.Traffic["requests"] != uint64(2) || *snapshot.Traffic["error_rate"].(*float64) != 50 || m.active.Load() != 0 {
		t.Fatal(snapshot.Traffic)
	}
}

func TestMySQLTimeoutAndPoolSnapshot(t *testing.T) {
	m := isolated(t)
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m.sources.Database = db
	mock.ExpectPing()
	mock.ExpectQuery("SHOW GLOBAL STATUS").WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("Threads_connected", "3"))
	result := m.mysql(context.Background())
	if result["status"] != "ok" || result["metrics"].(Fields)["threads_connected"] != uint64(3) {
		t.Fatal(result)
	}
	mock.ExpectPing().WillDelayFor(2 * time.Second)
	started := time.Now()
	if m.mysql(context.Background())["status"] != "unavailable" || time.Since(started) > 1500*time.Millisecond {
		t.Fatal("probe was not bounded")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisAllowlist(t *testing.T) {
	result := ParseRedisInfo("redis_version:7.4\nused_memory:100\nsecret:password\nkeyspace_hits:NaN\nmaxmemory:Inf\naof_last_write_status:ok\n")
	if result["used_memory"] != float64(100) || result["secret"] != nil || result["keyspace_hits"] != nil || result["maxmemory"] != nil {
		t.Fatal(result)
	}
}

func TestNodeBoundsAndStaleness(t *testing.T) {
	directory := t.TempDir()
	data, _ := json.Marshal(Node{ID: "test-node", At: time.Now().Add(-2 * time.Minute), History: []Fields{}})
	os.WriteFile(filepath.Join(directory, "node.json"), data, 0600)
	os.WriteFile(filepath.Join(directory, "broken.json"), []byte("broken"), 0600)
	os.Symlink(filepath.Join(directory, "node.json"), filepath.Join(directory, "link.json"))
	nodes, state := readNodes(directory, time.Now())
	if state != "partial" || len(nodes) != 1 || !nodes[0].Stale {
		t.Fatalf("%s %+v", state, nodes)
	}
}

func TestCertificatesRejectURLsAndBoundTargets(t *testing.T) {
	certs := checkCertificates(context.Background(), "https://example.com,example.com:80,example.com:443/path,https://example.com,user@host:443,host:22")
	if len(certs) != 4 {
		t.Fatal(certs)
	}
	for _, cert := range certs {
		if cert.Status != "invalid_target" {
			t.Fatal(cert)
		}
	}
	result := Certificate{Status: "invalid"}
	certificateDetails(&result, &x509.Certificate{NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(-time.Hour)})
	if result.Status != "expired" {
		t.Fatal(result)
	}
}

func TestCollectorLifecycle(t *testing.T) {
	isolated(t)
	m := New(Sources{})
	m.Close()
	m.Close()
	select {
	case <-m.done:
	default:
		t.Fatal("collector leaked")
	}
}

func TestConcurrentSamplingRequestsAndJSONReaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := isolated(t)
	m.sample(context.Background())
	router := gin.New()
	router.Use(m.Middleware())
	router.GET("/v1/models", func(c *gin.Context) { c.Status(200) })
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 150; i++ {
			m.sample(context.Background())
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/models", nil))
				if _, err := json.Marshal(m.Snapshot()); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if m.active.Load() != 0 || len(m.Snapshot().History) > HistoryLimit {
		t.Fatal("invalid final state")
	}
}

func BenchmarkSnapshot(b *testing.B) {
	m := &Monitor{}
	m.cached.Store(&Snapshot{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.Snapshot()
	}
}

func BenchmarkRequestMetrics(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	m := &Monitor{}
	router := gin.New()
	router.Use(m.Middleware())
	router.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest("GET", "/v1/models", strings.NewReader(""))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func TestTrafficRollingWindow(t *testing.T) {
	m := isolated(t)
	m.started = time.Now().Add(-time.Hour)
	now := time.Now().Unix()
	for _, item := range []struct {
		age              int64
		requests, errors uint64
	}{
		{301, 100, 100}, {240, 10, 2}, {60, 30, 2},
	} {
		second := now - item.age
		m.traffic[second%int64(len(m.traffic))] = trafficBucket{second: second, requests: item.requests, errors: item.errors}
	}
	m.sample(context.Background())
	first := m.Snapshot()
	if first.Traffic["requests"] != uint64(40) || first.Traffic["errors"] != uint64(4) || *first.Traffic["error_rate"].(*float64) != 10 || first.Traffic["window_seconds"] != float64(300) {
		t.Fatal(first.Traffic)
	}
	// Reading a new sample must retain traffic from earlier intervals.
	m.sample(context.Background())
	if m.Snapshot().Traffic["requests"] != uint64(40) {
		t.Fatal("sample reset rolling counters")
	}
	// Once all traffic expires, an idle window has an unknown error rate.
	m.mu.Lock()
	for i := range m.traffic {
		m.traffic[i].second -= 301
	}
	m.mu.Unlock()
	m.sample(context.Background())
	if m.Snapshot().Traffic["requests"] != uint64(0) || m.Snapshot().Traffic["error_rate"].(*float64) != nil {
		t.Fatal(m.Snapshot().Traffic)
	}
	if first.Traffic["requests"] != uint64(40) {
		t.Fatal("snapshot mutated")
	}
}

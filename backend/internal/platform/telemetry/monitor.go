// Package telemetry provides bounded, cached operational telemetry, never request-time probes.
package telemetry

import (
	"context"
	"database/sql"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const Interval = 30 * time.Second
const HistoryLimit = 120

// TrafficWindow is aggregated at one-second resolution and refreshed every Interval.
const TrafficWindow = 5 * time.Minute

type trafficBucket struct {
	second           int64
	requests, errors uint64
	duration         float64
	buckets          [9]uint64
}

type Fields map[string]any

type Sources struct {
	Database  *sql.DB
	Redis     func(context.Context) error
	RedisInfo func(context.Context) (string, error)
	Queue     func() Fields
}

type Point struct {
	At           time.Time `json:"at"`
	Heap         uint64    `json:"heap_bytes"`
	Goroutines   uint64    `json:"goroutines"`
	RPS          float64   `json:"rps"`
	ErrorRate    *float64  `json:"error_rate"`
	CPU          *float64  `json:"cpu_percent"`
	RSS          *uint64   `json:"rss_bytes"`
	Memory       *float64  `json:"memory_percent"`
	Latency      *float64  `json:"latency_ms"`
	P95          *float64  `json:"p95_ms"`
	MySQLLatency *float64  `json:"mysql_latency_ms"`
	QueueDepth   int       `json:"queue_depth"`
}

type Snapshot struct {
	Version      string        `json:"version"`
	Uptime       int64         `json:"uptime_seconds"`
	At           time.Time     `json:"sampled_at"`
	Interval     int           `json:"sample_interval_seconds"`
	CollectionMS int64         `json:"collection_ms"`
	MySQL        Fields        `json:"mysql"`
	Redis        Fields        `json:"redis"`
	Queue        Fields        `json:"queue"`
	Draining     bool          `json:"draining"`
	Process      Fields        `json:"process"`
	Traffic      Fields        `json:"traffic"`
	Nodes        []Node        `json:"nodes"`
	NodeSource   string        `json:"node_source"`
	Certificates []Certificate `json:"certificates"`
	History      []Point       `json:"history"`
}

type Monitor struct {
	sources        Sources
	started        time.Time
	cached         atomic.Pointer[Snapshot]
	cancel         context.CancelFunc
	done           chan struct{}
	closeOnce      sync.Once
	active         atomic.Int64
	mu             sync.Mutex
	traffic        [301]trafficBucket
	history        []Point
	certificates   []Certificate
	certChecked    time.Time
	processSampler processSampler
}

var latencyBounds = [...]float64{10, 25, 50, 100, 250, 500, 1000, 5000}

func New(sources Sources) *Monitor {
	m := &Monitor{sources: sources, started: time.Now(), done: make(chan struct{})}
	m.cached.Store(&Snapshot{Version: version(), Interval: 30, MySQL: Fields{"status": "collecting"}, Redis: Fields{"status": "collecting"}, Queue: Fields{"state": "collecting"}, Nodes: []Node{}, Certificates: []Certificate{}, History: []Point{}})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go func() {
		defer close(m.done)
		m.sample(ctx)
		ticker := time.NewTicker(Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sample(ctx)
			}
		}
	}()
	return m
}

func (m *Monitor) Close()              { m.closeOnce.Do(func() { m.cancel(); <-m.done }) }
func (m *Monitor) Snapshot() *Snapshot { return m.cached.Load() }

// Middleware excludes UI, admin and health traffic; streaming response writers are untouched.
func (m *Monitor) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !(strings.HasPrefix(path, "/v1/") || path == "/v1" || path == "/responses" || path == "/chat/completions" || path == "/messages" || strings.HasPrefix(path, "/messages/") || path == "/embeddings" || path == "/models" || strings.HasPrefix(path, "/models/") || strings.HasPrefix(path, "/images/")) {
			c.Next()
			return
		}
		started := time.Now()
		m.active.Add(1)
		defer func() {
			m.active.Add(-1)
			completed := time.Now()
			ms := float64(completed.Sub(started).Microseconds()) / 1000
			m.mu.Lock()
			m.recordTraffic(completed, c.Writer.Status() >= 400, ms)
			m.mu.Unlock()
		}()
		c.Next()
	}
}

func (m *Monitor) sample(ctx context.Context) {
	start := time.Now()
	process := processMetrics()
	cpu, rss, memory := m.processSampler.sample(start, process)
	p := Point{At: start, Heap: process["heap_bytes"].(uint64), Goroutines: process["goroutines"].(uint64), CPU: cpu, RSS: rss, Memory: memory}
	m.mu.Lock()
	// Include every request accounted for before this lock in the same snapshot.
	trafficAt := time.Now()
	requests, errors, duration, buckets := m.trafficTotals(trafficAt)
	m.mu.Unlock()
	window := min(TrafficWindow.Seconds(), trafficAt.Sub(m.started).Seconds())
	if window > 0 {
		p.RPS = float64(requests) / window
	}
	if requests > 0 {
		rate := float64(errors) * 100 / float64(requests)
		p.ErrorRate = &rate
	}
	if requests > 0 {
		latency := duration / float64(requests)
		p.Latency = &latency
		var total uint64
		for i, n := range buckets {
			total += n
			if total*100 >= requests*95 {
				if i < len(latencyBounds) {
					v := latencyBounds[i]
					p.P95 = &v
				}
				break
			}
		}
	}
	mysql := m.mysql(ctx)
	if mysql["status"] == "ok" {
		v := mysql["latency_ms"].(float64)
		p.MySQLLatency = &v
	}
	redis := m.redis(ctx)
	queue := Fields{"state": "not_configured"}
	if m.sources.Queue != nil {
		queue = m.sources.Queue()
	}
	if depth, ok := queue["depth"].(int); ok {
		p.QueueDepth = depth
	}
	nodes, nodeSource := readNodes(os.Getenv("BOS_MONITOR_NODE_DIR"), start)
	if start.Sub(m.certChecked) >= 10*time.Minute {
		certCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		m.certificates = checkCertificates(certCtx, os.Getenv("BOS_MONITOR_TLS_TARGETS"))
		cancel()
		m.certChecked = start
	}
	m.history = append(m.history, p)
	if len(m.history) > HistoryLimit {
		m.history = m.history[len(m.history)-HistoryLimit:]
	}
	history := append([]Point(nil), m.history...)
	m.cached.Store(&Snapshot{
		Version: version(), Uptime: int64(start.Sub(m.started).Seconds()), At: start, Interval: 30, CollectionMS: time.Since(start).Milliseconds(),
		MySQL: mysql, Redis: redis, Queue: queue, Draining: queue["state"] == "draining", Process: process,
		Traffic: Fields{"in_flight": m.active.Load(), "requests": requests, "errors": errors, "window_seconds": window, "rps": p.RPS, "error_rate": p.ErrorRate, "error_window_seconds": TrafficWindow.Seconds(), "error_window_requests": requests, "error_window_errors": errors, "latency_ms": p.Latency, "p95_ms": p.P95, "p95_overflow": requests > 0 && p.P95 == nil},
		Nodes:   nodes, NodeSource: nodeSource, Certificates: m.certificates, History: history,
	})
}

func processMetrics() Fields {
	names := []string{"/memory/classes/heap/objects:bytes", "/memory/classes/total:bytes", "/memory/classes/heap/released:bytes", "/sched/goroutines:goroutines", "/gc/cycles/total:gc-cycles"}
	samples := make([]metrics.Sample, len(names))
	for i, name := range names {
		samples[i].Name = name
	}
	metrics.Read(samples)
	return Fields{"pid": os.Getpid(), "go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0), "heap_bytes": samples[0].Value.Uint64(), "runtime_bytes": samples[1].Value.Uint64() - samples[2].Value.Uint64(), "goroutines": samples[3].Value.Uint64(), "gc_cycles": samples[4].Value.Uint64()}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, item := range info.Settings {
			if item.Key == "vcs.revision" {
				return item.Value
			}
		}
	}
	if value := os.Getenv("BOS_BUILD_VERSION"); value != "" {
		return value
	}
	return "development"
}

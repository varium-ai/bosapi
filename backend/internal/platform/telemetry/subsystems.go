package telemetry

import (
	"context"
	"strconv"
	"strings"
	"time"
)

func (m *Monitor) mysql(parent context.Context) Fields {
	out := Fields{"status": "not_configured"}
	db := m.sources.Database
	if db == nil {
		return out
	}
	stats := db.Stats()
	out["pool"] = Fields{"open": stats.OpenConnections, "in_use": stats.InUse, "idle": stats.Idle, "max_open": stats.MaxOpenConnections, "wait_count": stats.WaitCount, "wait_ms": stats.WaitDuration.Milliseconds(), "idle_closed": stats.MaxIdleClosed, "lifetime_closed": stats.MaxLifetimeClosed}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	start := time.Now()
	err := db.PingContext(ctx)
	out["latency_ms"] = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		out["status"] = "unavailable"
		return out
	}
	out["status"] = "ok"
	// A fixed-size server-status query, never a table scan or PROCESSLIST.
	rows, err := db.QueryContext(ctx, "SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_connected','Threads_running','Questions','Slow_queries','Aborted_connects','Uptime')")
	if err != nil {
		out["details_status"] = "unavailable"
		return out
	}
	defer rows.Close()
	values := Fields{}
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			out["details_status"] = "unavailable"
			return out
		}
		if n, err := strconv.ParseUint(value, 10, 64); err == nil {
			values[strings.ToLower(key)] = n
		}
	}
	if rows.Err() != nil {
		out["details_status"] = "unavailable"
		return out
	}
	out["details_status"], out["metrics"] = "ok", values
	return out
}

func (m *Monitor) redis(parent context.Context) Fields {
	out := Fields{"status": "not_configured"}
	if m.sources.Redis == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	start := time.Now()
	err := m.sources.Redis(ctx)
	out["latency_ms"] = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		out["status"] = "unavailable"
		return out
	}
	out["status"] = "ok"
	if m.sources.RedisInfo == nil {
		out["details_status"] = "not_configured"
		return out
	}
	info, err := m.sources.RedisInfo(ctx)
	if err != nil || len(info) > 128*1024 {
		out["details_status"] = "unavailable"
		return out
	}
	out["details_status"], out["metrics"] = "ok", ParseRedisInfo(info)
	return out
}

// Only allowlisted metrics leave the process; Redis config, keys and clients are never exposed.
func ParseRedisInfo(info string) Fields {
	stringsAllowed := map[string]bool{"redis_version": true, "role": true, "rdb_last_bgsave_status": true, "aof_last_write_status": true, "master_link_status": true}
	numbers := strings.Fields("uptime_in_seconds connected_clients blocked_clients used_memory used_memory_rss used_memory_peak maxmemory mem_fragmentation_ratio instantaneous_ops_per_sec keyspace_hits keyspace_misses evicted_keys expired_keys rejected_connections total_net_input_bytes total_net_output_bytes connected_slaves aof_enabled rdb_changes_since_last_save rdb_last_save_time")
	allowed := map[string]bool{}
	for _, key := range numbers {
		allowed[key] = true
	}
	out := Fields{}
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if stringsAllowed[key] && len(value) <= 80 {
			out[key] = value
		}
		if allowed[key] {
			if n, err := strconv.ParseFloat(value, 64); err == nil && n >= 0 && n < 1e30 {
				out[key] = n
			}
		}
	}
	return out
}

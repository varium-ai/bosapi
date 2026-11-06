package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestRequestWindowWeightedCountsAndExpiry(t *testing.T) {
	m := &Monitor{}
	at := time.Unix(1000, 0)
	m.recordTraffic(at.Add(-TrafficWindow), true, 0)
	for i := 0; i < 100; i++ {
		m.recordTraffic(at.Add(-4*time.Minute), i < 10, 0)
	}
	m.recordTraffic(at, true, 0)
	requests, errors, _, _ := m.trafficTotals(at)
	if requests != 101 || errors != 11 {
		t.Fatalf("want weighted totals 101/11, got %d/%d", requests, errors)
	}
	requests, errors, _, _ = m.trafficTotals(at.Add(time.Minute))
	if requests != 1 || errors != 1 {
		t.Fatalf("old requests did not expire at boundary: %d/%d", requests, errors)
	}
	requests, errors, _, _ = m.trafficTotals(at.Add(TrafficWindow))
	if requests != 0 || errors != 0 {
		t.Fatalf("idle window did not expire: %d/%d", requests, errors)
	}
}

func TestRequestWindowBoundedAcrossWrapsAndSamplerGaps(t *testing.T) {
	m := &Monitor{}
	at := time.Unix(1000, 0)
	for i := 0; i < 2000; i++ {
		m.recordTraffic(at.Add(time.Duration(i)*time.Second), i%2 == 0, 0)
	}
	if requests, errors, _, _ := m.trafficTotals(at.Add(1999 * time.Second)); requests != 300 || errors != 150 {
		t.Fatalf("ring wrap: %d/%d", requests, errors)
	}
	at = at.Add(time.Hour)
	m.recordTraffic(at, true, 0)
	if requests, errors, _, _ := m.trafficTotals(at); requests != 1 || errors != 1 {
		t.Fatalf("gap retained expired traffic: %d/%d", requests, errors)
	}
	if requests, _, _, _ := m.trafficTotals(at.Add(-time.Second)); requests != 0 {
		t.Fatal("future traffic included")
	}
}

func TestSnapshotRollingTrafficWeightedLatency(t *testing.T) {
	m := isolated(t)
	at := time.Now()
	for i := 0; i < 100; i++ {
		m.recordTraffic(at.Add(-time.Minute), i < 10, 10)
	}
	m.recordTraffic(at, true, 40)
	m.sample(context.Background())
	traffic := m.Snapshot().Traffic
	if traffic["error_window_requests"] != uint64(101) || traffic["error_window_errors"] != uint64(11) || traffic["error_window_seconds"] != float64(300) {
		t.Fatal(traffic)
	}
	if got := *traffic["error_rate"].(*float64); got != float64(11)*100/101 {
		t.Fatalf("error percentages were not weighted: %v", got)
	}
	if traffic["requests"] != uint64(101) || *traffic["latency_ms"].(*float64) != float64(1040)/101 || *traffic["p95_ms"].(*float64) != 10 {
		t.Fatal("latency and histogram must cover the same rolling window", traffic)
	}
	first := m.Snapshot()
	m.sample(context.Background())
	traffic = m.Snapshot().Traffic
	if traffic["requests"] != uint64(101) || traffic["error_window_requests"] != uint64(101) || traffic["error_rate"].(*float64) == nil {
		t.Fatal("empty sample erased rolling traffic", traffic)
	}
	if first.Traffic["requests"] != uint64(101) {
		t.Fatal("previous snapshot mutated")
	}
	m.traffic = [301]trafficBucket{}
	m.recordTraffic(at.Add(-6*time.Minute), true, 10)
	m.sample(context.Background())
	if m.Snapshot().Traffic["error_rate"].(*float64) != nil {
		t.Fatal("expired traffic must yield null, not zero")
	}
}

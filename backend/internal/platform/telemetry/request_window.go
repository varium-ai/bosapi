package telemetry

import "time"

// One-second buckets bound memory independently of traffic and sampler delays.
// The oldest partial second is excluded (less than one second of quantization).
// The caller holds Monitor.mu for both recording and reading.
func (m *Monitor) recordTraffic(at time.Time, failed bool, duration float64) {
	second := at.Unix()
	b := &m.traffic[second%int64(len(m.traffic))]
	if b.second != second {
		*b = trafficBucket{second: second}
	}
	b.requests++
	if failed {
		b.errors++
	}
	b.duration += duration
	bucket := len(latencyBounds)
	for i, bound := range latencyBounds {
		if duration <= bound {
			bucket = i
			break
		}
	}
	b.buckets[bucket]++
}

func (m *Monitor) trafficTotals(at time.Time) (requests, errors uint64, duration float64, buckets [9]uint64) {
	second := at.Unix()
	cutoff := second - int64(TrafficWindow/time.Second)
	for _, b := range m.traffic {
		if b.second > cutoff && b.second <= second {
			requests += b.requests
			errors += b.errors
			duration += b.duration
			for i, n := range b.buckets {
				buckets[i] += n
			}
		}
	}
	return
}

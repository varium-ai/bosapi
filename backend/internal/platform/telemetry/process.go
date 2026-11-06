package telemetry

import "time"

type processReading struct {
	cpuSeconds *float64
	rss        *uint64
	limit      uint64
	source     string
}

type processSampler struct {
	previous time.Time
	cpu      float64
}

func (s *processSampler) sample(at time.Time, fields Fields) (*float64, *uint64, *float64) {
	return s.update(at, readProcess(), fields)
}

func (s *processSampler) update(at time.Time, reading processReading, fields Fields) (cpu *float64, rss *uint64, memory *float64) {
	if reading.cpuSeconds != nil {
		if elapsed := at.Sub(s.previous).Seconds(); !s.previous.IsZero() && elapsed > 0 && *reading.cpuSeconds >= s.cpu {
			value := (*reading.cpuSeconds - s.cpu) / elapsed * 100
			cpu = &value
		}
		s.previous, s.cpu = at, *reading.cpuSeconds
	} else {
		s.previous = time.Time{}
	}
	rss = reading.rss
	if rss != nil && reading.limit > 0 {
		value := float64(*rss) / float64(reading.limit) * 100
		memory = &value
	}
	fields["cpu_percent"], fields["rss_bytes"], fields["memory_percent"] = cpu, rss, memory
	if reading.limit > 0 {
		fields["memory_limit_bytes"], fields["memory_limit_source"] = reading.limit, reading.source
	}
	return
}

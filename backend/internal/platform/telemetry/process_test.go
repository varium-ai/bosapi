package telemetry

import (
	"encoding/json"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestProcessSamplerCPUAndMemorySemantics(t *testing.T) {
	var s processSampler
	at := time.Now()
	seconds, rss := float64(10), uint64(256)
	fields := Fields{}
	reading := processReading{cpuSeconds: &seconds, rss: &rss, limit: 1024, source: "cgroup"}
	cpu, gotRSS, memory := s.update(at, reading, fields)
	if cpu != nil || *gotRSS != 256 || *memory != 25 || fields["memory_limit_source"] != "cgroup" {
		t.Fatal(fields)
	}
	seconds = 70
	cpu, _, _ = s.update(at.Add(30*time.Second), reading, fields)
	if cpu == nil || *cpu != 200 {
		t.Fatal("multi-core CPU must not be clamped", fields)
	}
	seconds = 1
	cpu, _, _ = s.update(at.Add(time.Minute), reading, fields)
	if cpu != nil {
		t.Fatal("negative CPU delta should be unavailable")
	}
	fields = Fields{}
	cpu, gotRSS, memory = s.update(at.Add(90*time.Second), processReading{}, fields)
	if cpu != nil || gotRSS != nil || memory != nil || fields["memory_limit_bytes"] != nil {
		t.Fatal("missing values must not become zero", fields)
	}
	data, err := json.Marshal(fields)
	if err != nil || string(data) != `{"cpu_percent":null,"memory_percent":null,"rss_bytes":null}` {
		t.Fatalf("unknown metrics must serialize to null: %s, %v", data, err)
	}
	cpu, _, _ = s.update(at.Add(2*time.Minute), reading, fields)
	if cpu != nil {
		t.Fatal("CPU after failed read needs a new baseline")
	}
}

func TestProcessReadingCurrentProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("process metrics unsupported on this OS")
	}
	r := readProcess()
	if r.cpuSeconds == nil || *r.cpuSeconds < 0 || r.rss == nil || *r.rss == 0 || r.limit == 0 || r.source == "" {
		t.Fatalf("current process metrics unavailable: %+v", r)
	}
}

func TestProcessMemoryLimitCgroups(t *testing.T) {
	const host = uint64(8 << 30)
	tests := []struct {
		name, groups, mounts string
		files                map[string]string
		want                 uint64
		source               string
	}{
		{"v2 ancestor", "0::/parent/child\n", "1 0 0:1 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "max", "/cg/parent/memory.max": "1073741824", "/cg/parent/child/memory.max": "2147483648"}, 1 << 30, "cgroup"},
		{"v1", "5:cpu,cpuacct:/app\n4:memory:/app\n", "1 0 0:1 / /cg rw - cgroup cgroup rw,memory", map[string]string{"/cg/memory.limit_in_bytes": "9223372036854771712", "/cg/app/memory.limit_in_bytes": "536870912"}, 512 << 20, "cgroup"},
		{"namespaced root", "0::/\n", "1 0 0:1 /docker/container /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "268435456"}, 256 << 20, "cgroup"},
		{"subtree mount", "0::/docker/container/app", "1 0 0:1 /docker/container /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/app/memory.max": "536870912"}, 512 << 20, "cgroup"},
		{"unlimited", "0::/", "1 0 0:1 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "max"}, host, "host"},
		{"larger than host", "0::/", "1 0 0:1 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "17179869184"}, host, "host"},
		{"invalid or unreadable", "0::/", "1 0 0:1 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "broken"}, host, "host"},
		{"escaped mount", "0::/", `1 0 0:1 / /cgroup\040memory rw - cgroup2 cgroup rw`, map[string]string{"/cgroup memory/memory.max": "268435456"}, 256 << 20, "cgroup"},
		{"unrelated mount", "0::/actual", "1 0 0:1 /other /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "1"}, host, "host"},
		{"no memory controller", "1:cpu:/", "1 0 0:1 / /cg rw - cgroup cgroup rw,cpu", map[string]string{}, host, "host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := func(path string) ([]byte, error) {
				if value, ok := tt.files[path]; ok {
					return []byte(value), nil
				}
				return nil, os.ErrNotExist
			}
			limit, source := processMemoryLimit(host, tt.groups, tt.mounts, read)
			if limit != tt.want || source != tt.source {
				t.Fatalf("got %d (%s), want %d (%s)", limit, source, tt.want, tt.source)
			}
		})
	}
	if limit, source := processMemoryLimit(0, "", "", nil); limit != 0 || source != "" {
		t.Fatal("unknown denominator must stay unavailable")
	}
}

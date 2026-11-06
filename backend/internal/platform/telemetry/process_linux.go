package telemetry

import (
	"os"
	"strconv"
	"strings"
)

func readProcess() processReading {
	r := processReading{cpuSeconds: processCPUSeconds()}
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				bytes := pages * uint64(os.Getpagesize())
				r.rss = &bytes
			}
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					r.limit, r.source = kb*1024, "host"
				}
			}
		}
	}
	groups, groupErr := os.ReadFile("/proc/self/cgroup")
	mounts, mountErr := os.ReadFile("/proc/self/mountinfo")
	if groupErr == nil && mountErr == nil {
		r.limit, r.source = processMemoryLimit(r.limit, string(groups), string(mounts), os.ReadFile)
	}
	return r
}

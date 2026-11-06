package telemetry

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func readProcess() processReading {
	r := processReading{cpuSeconds: processCPUSeconds()}
	// ps reports current RSS in KiB; getrusage reports only the lifetime peak.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if data, err := exec.CommandContext(ctx, "/bin/ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output(); err == nil {
		if kb, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil {
			bytes := kb * 1024
			r.rss = &bytes
		}
	}
	if limit, err := unix.SysctlUint64("hw.memsize"); err == nil {
		r.limit, r.source = limit, "host"
	}
	return r
}

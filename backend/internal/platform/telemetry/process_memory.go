package telemetry

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Resolve the process's cgroup against visible mounts, including namespaced
// roots, and respect tighter ancestor limits in both cgroup v1 and v2.
func processMemoryLimit(host uint64, groups, mounts string, readFile func(string) ([]byte, error)) (uint64, string) {
	limit, source := host, "host"
	if host == 0 {
		source = ""
	}
	for _, group := range strings.Split(groups, "\n") {
		parts := strings.SplitN(group, ":", 3)
		if len(parts) != 3 {
			continue
		}
		v2 := parts[0] == "0" && parts[1] == ""
		if !v2 && !containsController(parts[1], "memory") {
			continue
		}
		for _, mount := range strings.Split(mounts, "\n") {
			before, after, ok := strings.Cut(mount, " - ")
			fields, fs := strings.Fields(before), strings.Fields(after)
			if !ok || len(fields) < 6 || len(fs) < 3 || (v2 && fs[0] != "cgroup2") || (!v2 && (fs[0] != "cgroup" || !containsController(fs[2], "memory"))) {
				continue
			}
			root, point := filepath.Clean(unescapeMount(fields[3])), filepath.Clean(unescapeMount(fields[4]))
			if !filepath.IsAbs(root) || !filepath.IsAbs(point) || !filepath.IsAbs(parts[2]) {
				continue
			}
			rel, err := filepath.Rel(root, parts[2])
			if parts[2] == "/" {
				// A private cgroup namespace reports its own root as '/'.
				rel, err = ".", nil
			}
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			name := "memory.limit_in_bytes"
			if v2 {
				name = "memory.max"
			}
			for dir := filepath.Join(point, rel); ; dir = filepath.Dir(dir) {
				if data, err := readFile(filepath.Join(dir, name)); err == nil {
					value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
					// v1 represents unlimited memory with a page-aligned MaxInt64.
					if err == nil && value > 0 && value < 1<<60 && (limit == 0 || value < limit) {
						limit, source = value, "cgroup"
					}
				}
				if dir == point {
					break
				}
			}
		}
	}
	return limit, source
}

func containsController(list, controller string) bool {
	for _, item := range strings.Split(list, ",") {
		if item == controller {
			return true
		}
	}
	return false
}

func unescapeMount(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

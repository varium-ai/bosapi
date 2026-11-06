package telemetry

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Node struct {
	ID                 string    `json:"id"`
	At                 time.Time `json:"sampled_at"`
	Stale              bool      `json:"stale"`
	Status             string    `json:"status"`
	CPUCount           int       `json:"cpu_count"`
	CPU                *float64  `json:"cpu_percent"`
	Load               []float64 `json:"load"`
	Memory             Fields    `json:"memory"`
	Swap               Fields    `json:"swap"`
	Disks              []Fields  `json:"disks"`
	Network            Fields    `json:"network"`
	Processes          []Fields  `json:"processes"`
	ProcessCount       int       `json:"process_count"`
	ProcessesTruncated bool      `json:"processes_truncated"`
	Uptime             float64   `json:"uptime_seconds"`
	CollectionMS       float64   `json:"collection_ms"`
	History            []Fields  `json:"history"`
	Errors             []string  `json:"errors"`
}

func readNodes(directory string, now time.Time) ([]Node, string) {
	nodes := []Node{}
	if directory == "" {
		return nodes, "not_configured"
	}
	// Bound directory reads as well as decoded payloads; only local regular snapshots are accepted.
	dir, err := os.Open(directory)
	if err != nil {
		return nodes, "unavailable"
	}
	defer dir.Close()
	entries, err := dir.ReadDir(64)
	if err != nil && err != io.EOF {
		return nodes, "unavailable"
	}
	state := "ok"
	if len(entries) == 64 {
		state = "truncated"
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if len(nodes) == 16 {
			state = "truncated"
			break
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 256*1024 {
			state = "partial"
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			state = "partial"
			continue
		}
		var node Node
		err = json.NewDecoder(io.LimitReader(file, 256*1024)).Decode(&node)
		file.Close()
		if err != nil || node.ID == "" || len(node.ID) > 128 || node.At.IsZero() || len(node.Disks) > 16 || len(node.Processes) > 10 || len(node.History) > HistoryLimit {
			state = "partial"
			continue
		}
		if seen[node.ID] {
			state = "partial"
			continue
		}
		seen[node.ID] = true
		node.Stale = now.Sub(node.At) > 90*time.Second || node.At.After(now.Add(30*time.Second))
		if node.Stale {
			node.Status = "stale"
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 && state == "ok" {
		state = "not_configured"
	}
	return nodes, state
}

package domain

import (
	"context"
	"time"
)

// AdminUsageFilter constrains administrative usage queries.
type AdminUsageFilter struct {
	OrganizationID  uint64
	APIKeyID        uint64
	ClientIP        string
	Model           string
	From            *time.Time
	To              *time.Time
	IPWindowMinutes int `json:",omitempty"`
	// Refresh bypasses cached aggregates without changing the query's scope.
	Refresh    bool
	SkipTotal  bool
	Before     *time.Time
	BeforeID   uint64
	SnapshotID string
}

// IPUsageWindow permits only the supported rolling windows. Zero preserves
// the 30-minute default for internal callers that omit the option.
func IPUsageWindow(minutes int) (time.Duration, bool) {
	switch minutes {
	case 0:
		return 30 * time.Minute, true
	case 30, 60, 360, 720, 1440:
		return time.Duration(minutes) * time.Minute, true
	default:
		return 0, false
	}
}

// AdminUsageRecord contains body-free request metadata for administrators.
type AdminUsageRecord struct {
	ID                   uint64    `json:"id"`
	RequestID            string    `json:"request_id"`
	OrganizationID       uint64    `json:"organization_id"`
	OrganizationName     string    `json:"organization_name,omitempty"`
	APIKeyID             uint64    `json:"api_key_id"`
	APIKeyName           string    `json:"api_key_name,omitempty"`
	Model                string    `json:"model"`
	Endpoint             string    `json:"endpoint"`
	StatusCode           uint16    `json:"status_code"`
	InputTokens          uint64    `json:"input_tokens"`
	CacheReadTokens      uint64    `json:"cache_read_tokens"`
	CacheWriteTokens     uint64    `json:"cache_write_tokens"`
	CacheReadCostAmount  float64   `json:"cache_read_cost_amount"`
	CacheWriteCostAmount float64   `json:"cache_write_cost_amount"`
	OutputTokens         uint64    `json:"output_tokens"`
	TotalTokens          uint64    `json:"total_tokens"`
	CostAmount           float64   `json:"cost_amount"`
	FirstTokenMillis     *uint32   `json:"first_token_ms"`
	CreatedAt            time.Time `json:"created_at"`
	ClientIP             string    `json:"client_ip"`
}

type AdminUsageIP struct {
	ClientIP       string    `json:"client_ip"`
	Requests       uint64    `json:"requests"`
	SecurityEvents uint64    `json:"security_events"`
	LastSeen       time.Time `json:"last_seen"`
}

type AdminUsageIPSnapshot struct {
	Items    []AdminUsageIP         `json:"items"`
	Total    int64                  `json:"total"`
	Snapshot *UsageSnapshotMetadata `json:"snapshot,omitempty"`
}

// AdminUsagePoint contains grouped request, error, token, and cost counters.
type AdminUsagePoint struct {
	Period               string  `json:"period"`
	Requests             uint64  `json:"requests"`
	Errors               uint64  `json:"errors"`
	InputTokens          uint64  `json:"input_tokens"`
	CacheReadTokens      uint64  `json:"cache_read_tokens"`
	CacheWriteTokens     uint64  `json:"cache_write_tokens"`
	CacheReadCostAmount  float64 `json:"cache_read_cost_amount"`
	CacheWriteCostAmount float64 `json:"cache_write_cost_amount"`
	OutputTokens         uint64  `json:"output_tokens"`
	TotalTokens          uint64  `json:"total_tokens"`
	CostAmount           float64 `json:"cost_amount"`
	CacheHitRate         float64 `json:"cache_hit_rate"`
}

// AdminUsageRepository defines administrative usage reads.
type AdminUsageRepository interface {
	DailyAdminOrganizationUsage(context.Context, AdminOrganizationUsageFilter, int, int) (AdminOrganizationUsageDashboard, error)
	ListAdminUsage(context.Context, AdminUsageFilter, int, int) ([]AdminUsageRecord, int64, error)
	AggregateAdminUsage(context.Context, AdminUsageFilter, string) ([]AdminUsagePoint, error)
	GroupAdminUsageByIP(context.Context, AdminUsageFilter, int, int) ([]AdminUsageIP, int64, error)
}

// AdminOrganizationUsageFilter limits the organization dashboard without exposing key material.
type AdminOrganizationUsageFilter struct {
	AdminUsageFilter
	Search string
}

// AdminOrganizationUsagePoint combines all keys recorded for one user and business day.
type AdminOrganizationUsagePoint struct {
	AdminUsagePoint
	OrganizationID   uint64 `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	KeyCount         uint64 `json:"key_count"`
}

// AdminOrganizationUsageDashboard keeps totals independent of the selected results page.
type AdminOrganizationUsageDashboard struct {
	Snapshot         *UsageSnapshotMetadata        `json:"snapshot,omitempty"`
	Items            []AdminOrganizationUsagePoint `json:"items"`
	Total            int64                         `json:"total"`
	Summary          AdminUsagePoint               `json:"summary"`
	TopOrganizations []AdminOrganizationTokenRank  `json:"top_organizations"`
	TopKeys          []AdminKeyTokenRank           `json:"top_keys"`
	TopModels        []AdminModelTokenRank         `json:"top_models"`
}

// AdminModelTokenRank combines recorded usage for a model across the filtered keys and organizations.
type AdminModelTokenRank struct {
	Model        string `json:"model"`
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
	TotalTokens  uint64 `json:"total_tokens"`
}

// AdminKeyTokenRank keeps separately issued keys distinct even when their owners match.
type AdminKeyTokenRank struct {
	APIKeyID          uint64 `json:"api_key_id"`
	APIKeyName        string `json:"api_key_name"`
	OrganizationName  string `json:"organization_name"`
	OrganizationCount uint64 `json:"organization_count"`
	InputTokens       uint64 `json:"input_tokens"`
	OutputTokens      uint64 `json:"output_tokens"`
	TotalTokens       uint64 `json:"total_tokens"`
}

// AdminOrganizationTokenRank compares a person's combined token usage across the filtered dates.
type AdminOrganizationTokenRank struct {
	OrganizationID   uint64 `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	InputTokens      uint64 `json:"input_tokens"`
	OutputTokens     uint64 `json:"output_tokens"`
	TotalTokens      uint64 `json:"total_tokens"`
}

// UsageSnapshotMetadata describes display freshness, never accounting state.
type UsageSnapshotMetadata struct {
	ID          string    `json:"snapshot_id,omitempty"`
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	ValidUntil  time.Time `json:"valid_until,omitempty"`
	Status      string    `json:"status"`
}
type AdminUsageAggregateSnapshot struct {
	Items    []AdminUsagePoint      `json:"items"`
	Snapshot *UsageSnapshotMetadata `json:"snapshot,omitempty"`
}

package domain

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidUsageFilter = errors.New("invalid usage filter")

// UsageRecord contains safe, body-free metadata for one completed gateway request.
type UsageRecord struct {
	ID                   uint64    `json:"id"`
	RequestID            string    `json:"request_id"`
	APIKeyID             uint64    `json:"api_key_id"`
	Model                string    `json:"model"`
	UpstreamModel        *string   `json:"upstream_model"`
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
	IsStream             bool      `json:"is_stream"`
	DurationMillis       uint32    `json:"duration_ms"`
	FirstTokenMillis     *uint32   `json:"first_token_ms"`
	CreatedAt            time.Time `json:"created_at"`
}

// UsageFilter limits usage queries to an authenticated owner and bounded filters.
type UsageFilter struct {
	UserID         uint64
	OrganizationID uint64
	APIKeyID       uint64
	Model          string
	From           *time.Time
	To             *time.Time
}

// UsageTotals summarizes request and billing counters without request content.
type UsageTotals struct {
	Requests     uint64  `json:"requests"`
	Errors       uint64  `json:"errors"`
	InputTokens  uint64  `json:"input_tokens"`
	OutputTokens uint64  `json:"output_tokens"`
	TotalTokens  uint64  `json:"total_tokens"`
	CostAmount   float64 `json:"cost_amount"`
	Incomplete   bool    `json:"incomplete"`
}

// UsageTrendPoint contains daily counters for the authenticated user's dashboard.
type UsageTrendPoint struct {
	InputTokens  uint64  `json:"input_tokens"`
	OutputTokens uint64  `json:"output_tokens"`
	Date         string  `json:"date"`
	Requests     uint64  `json:"requests"`
	Errors       uint64  `json:"errors"`
	TotalTokens  uint64  `json:"total_tokens"`
	CostAmount   float64 `json:"cost_amount"`
}

// UsageModelTotal contains aggregated counters for one requested model.
type UsageModelTotal struct {
	InputTokens  uint64  `json:"input_tokens"`
	OutputTokens uint64  `json:"output_tokens"`
	Model        string  `json:"model"`
	Requests     uint64  `json:"requests"`
	TotalTokens  uint64  `json:"total_tokens"`
	CostAmount   float64 `json:"cost_amount"`
}

// UsageDailyPoint contains owner-scoped daily counters for one API key.
type UsageDailyPoint struct {
	InputTokens  uint64  `json:"input_tokens"`
	OutputTokens uint64  `json:"output_tokens"`
	Date         string  `json:"date"`
	Requests     uint64  `json:"requests"`
	Errors       uint64  `json:"errors"`
	TotalTokens  uint64  `json:"total_tokens"`
	CostAmount   float64 `json:"cost_amount"`
}

// APIKeyUsagePoint contains daily counters for one owned API key.
type APIKeyUsagePoint struct {
	InputTokens  uint64  `json:"input_tokens"`
	OutputTokens uint64  `json:"output_tokens"`
	APIKeyID     uint64  `json:"api_key_id"`
	Date         string  `json:"date"`
	Requests     uint64  `json:"requests"`
	Errors       uint64  `json:"errors"`
	TotalTokens  uint64  `json:"total_tokens"`
	CostAmount   float64 `json:"cost_amount"`
}

// UsageError contains sanitized metadata for one failed request.
type UsageError struct {
	ID             uint64    `json:"id"`
	Time           time.Time `json:"time"`
	RequestID      string    `json:"request_id"`
	APIKeyID       uint64    `json:"api_key_id"`
	Model          string    `json:"model"`
	HTTPStatus     uint16    `json:"http_status"`
	Outcome        string    `json:"outcome"`
	ErrorCode      string    `json:"error_code"`
	DurationMillis uint32    `json:"duration_ms"`
}

// UsageRepository defines owner-scoped usage reads.
type UsageRepository interface {
	ListUsage(context.Context, UsageFilter, int, int) ([]UsageRecord, int64, error)
	FindUsage(context.Context, UsageFilter, uint64) (UsageRecord, error)
	SummarizeUsage(context.Context, UsageFilter) (UsageTotals, error)
	TrendUsage(context.Context, UsageFilter) ([]UsageTrendPoint, error)
	UsageByModel(context.Context, UsageFilter, int) ([]UsageModelTotal, error)
	DailyUsageByKey(context.Context, UsageFilter, int) ([]UsageDailyPoint, error)
	OwnsAPIKey(context.Context, uint64, uint64) (bool, error)
	OwnsAPIKeys(context.Context, []uint64, uint64) (bool, error)
	UsageByKeys(context.Context, UsageFilter, []uint64) ([]APIKeyUsagePoint, error)
	ListUsageErrors(context.Context, UsageFilter, string, int, int) ([]UsageError, int64, error)
	FindUsageError(context.Context, UsageFilter, uint64) (UsageError, error)
}

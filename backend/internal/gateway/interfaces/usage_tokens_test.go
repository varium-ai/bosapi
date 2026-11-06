package interfaces

import "testing"

func TestExtractUsageSplitTokensDetermineTotal(t *testing.T) {
	for _, test := range []struct {
		name  string
		usage map[string]any
		total int64
	}{
		{"responses", map[string]any{"input_tokens": float64(11), "output_tokens": float64(7), "total_tokens": float64(99)}, 18},
		{"chat", map[string]any{"prompt_tokens": float64(11), "completion_tokens": float64(7), "total_tokens": float64(99)}, 18},
		{"embeddings", map[string]any{"prompt_tokens": float64(11), "total_tokens": float64(99)}, 11},
		{"explicit zero split", map[string]any{"input_tokens": float64(0), "output_tokens": float64(0), "total_tokens": float64(99)}, 0},
		{"total only", map[string]any{"total_tokens": float64(99)}, 99},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := extractUsageFields(test.usage); got.TotalTokens != test.total {
				t.Fatalf("usage=%+v total=%d want=%d", got, got.TotalTokens, test.total)
			}
		})
	}
}

func TestExtractUsageCapturesCacheBuckets(t *testing.T) {
	testCases := []struct {
		name            string
		usage           map[string]any
		cacheReadTokens int64
	}{
		{"OpenAI chat details", map[string]any{"prompt_tokens": float64(100), "prompt_tokens_details": map[string]any{"cached_tokens": float64(60)}}, 60},
		{"OpenAI responses details", map[string]any{"input_tokens": float64(100), "input_tokens_details": map[string]any{"cached_tokens": float64(60)}}, 60},
		{"Anthropic split", map[string]any{"input_tokens": float64(10), "cache_read_input_tokens": float64(60), "cache_creation_input_tokens": float64(20)}, 60},
		{"DeepSeek split", map[string]any{"prompt_cache_hit_tokens": float64(40), "prompt_cache_miss_tokens": float64(60)}, 40},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			got := extractUsageFields(test.usage)
			if got.CacheReadTokens != test.cacheReadTokens {
				t.Fatalf("cache read=%d want=%d", got.CacheReadTokens, test.cacheReadTokens)
			}
			if got.TotalTokens <= 0 {
				t.Fatalf("total must be positive: %+v", got)
			}
		})
	}
}

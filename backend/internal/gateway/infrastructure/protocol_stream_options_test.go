package infrastructure

import (
	"errors"
	"testing"
)

func TestRelayContinuousUsageStatsOptions(t *testing.T) {
	for _, target := range []string{"anthropic_messages", "gemini", "openai_responses"} {
		for _, tc := range []struct {
			name, options, field        string
			stream, include, continuous bool
		}{
			{name: "enabled", options: `{"include_usage":true,"continuous_usage_stats":true}`, stream: true, include: true, continuous: true},
			{name: "disabled", options: `{"include_usage":true,"continuous_usage_stats":false}`, stream: true, include: true},
			{name: "omitted", options: `{"include_usage":true}`, stream: true, include: true},
			{name: "include disabled", options: `{"include_usage":false,"continuous_usage_stats":true}`, stream: true, continuous: true},
			{name: "include omitted", options: `{"continuous_usage_stats":true}`, stream: true, continuous: true},
			{name: "empty", options: `{}`, stream: true},
			{name: "string", options: `{"continuous_usage_stats":"true"}`, stream: true, field: "stream_options.continuous_usage_stats"},
			{name: "number", options: `{"continuous_usage_stats":1}`, stream: true, field: "stream_options.continuous_usage_stats"},
			{name: "null", options: `{"continuous_usage_stats":null}`, stream: true, field: "stream_options.continuous_usage_stats"},
			{name: "object", options: `{"continuous_usage_stats":{}}`, stream: true, field: "stream_options.continuous_usage_stats"},
			{name: "array", options: `{"continuous_usage_stats":[]}`, stream: true, field: "stream_options.continuous_usage_stats"},
			{name: "unknown option", options: `{"continuous_usage_stats":true,"unknown":true}`, stream: true, field: "stream_options.unknown"},
			{name: "non streaming", options: `{"continuous_usage_stats":true}`, field: "stream_options"},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stream := "false"
				if tc.stream {
					stream = "true"
				}
				raw := `{"model":"alias","messages":[{"role":"user","content":"hello"}],"stream":` + stream + `,"stream_options":` + tc.options + `}`
				q, err := ParseRelayRequest([]byte(raw), "chat", target)
				if tc.field != "" {
					var failure *ConversionError
					if !errors.As(err, &failure) || failure.Field != tc.field {
						t.Fatalf("error=%v, want field=%s", err, tc.field)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if q.Stream != tc.stream || q.IncludeUsage != tc.include || q.ContinuousUsageStats != tc.continuous {
					t.Fatalf("unexpected stream options: stream=%v include=%v continuous=%v", q.Stream, q.IncludeUsage, q.ContinuousUsageStats)
				}
			})
		}
	}
}

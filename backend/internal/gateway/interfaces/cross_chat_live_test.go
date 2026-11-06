package interfaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMessagesChatLiveClaudeCode(t *testing.T) {
	endpoint, secret := os.Getenv("BOS_CHAT_LIVE_URL"), os.Getenv("BOS_CHAT_LIVE_KEY")
	if endpoint == "" || secret == "" {
		t.Skip("set BOS_CHAT_LIVE_URL and BOS_CHAT_LIVE_KEY to opt into authenticated inference")
	}
	model := os.Getenv("BOS_CHAT_LIVE_MODEL")
	if model == "" {
		t.Fatal("BOS_CHAT_LIVE_MODEL is required")
	}
	a, key := crossTestApp(t, "openai_chat")
	a.data.Providers[0].BaseURL = endpoint
	a.data.Providers[0].APIKey = secret
	a.data.Providers[0].Models = []string{model}
	a.data.Providers[0].ModelMapping = nil
	a.client = &http.Client{Timeout: 60 * time.Second}
	server := httptest.NewServer(a.routes())
	defer server.Close()
	for _, tools := range []string{"", "Read"} {
		t.Run("tools="+tools, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			directory := t.TempDir()
			prompt, expected := "Reply exactly BOS_OK", "BOS_OK"
			if tools != "" {
				expected = "BOS_TOOL_" + random(8)
				path := filepath.Join(directory, "probe.txt")
				if err := os.WriteFile(path, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
				prompt = "Use the Read tool to read " + path + ". Reply only with the file contents."
			}
			args := []string{"-p", prompt, "--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--tools", tools, "--disable-slash-commands", "--system-prompt", "Follow the user's instructions. Reply briefly.", "--no-session-persistence", "--max-turns", "3"}
			if tools != "" {
				args = append(args, "--allowedTools", tools)
			}
			cmd := exec.CommandContext(ctx, "claude", args...)
			cmd.Dir = directory
			cmd.Env = claudeBridgeTestEnvironment(server.URL, key, model)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), expected) {
				t.Fatalf("real Claude Code bridge failed: %v %s", err, output)
			}
		})
	}
}

func claudeBridgeTestEnvironment(endpoint, key, model string) []string {
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "ANTHROPIC_") && !strings.HasPrefix(value, "CLAUDE_") && !strings.HasPrefix(value, "BOS_CHAT_LIVE_") && !strings.HasPrefix(value, "MAX_THINKING_TOKENS=") && !strings.HasPrefix(value, "DISABLE_INTERLEAVED_THINKING=") {
			env = append(env, value)
		}
	}
	return append(env, "ANTHROPIC_BASE_URL="+endpoint, "ANTHROPIC_API_KEY="+key, "ANTHROPIC_AUTH_TOKEN="+key, "ANTHROPIC_MODEL="+model,
		"CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=1024")
}

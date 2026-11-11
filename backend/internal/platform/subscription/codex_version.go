package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"bosapi/backend/internal/platform/logging"
)

// DefaultCodexClientVersion is the bundled fallback when release discovery is unavailable.
const DefaultCodexClientVersion = "0.166.0"

const codexStableReleaseURL = "https://api.github.com/repos/openai/codex/releases/latest"

var codexStableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type codexVersionState struct{ version atomic.Value }

func newCodexVersionState(version string) *codexVersionState {
	s := &codexVersionState{}
	s.version.Store(version)
	return s
}

var activeCodexVersion atomic.Pointer[codexVersionState]

func init() { activeCodexVersion.Store(newCodexVersionState(DefaultCodexClientVersion)) }

// CodexClientVersion is shared by catalogs and every Codex inference transport.
func CodexClientVersion() string { return activeCodexVersion.Load().version.Load().(string) }

// ApplyCodexClientHeaders takes one snapshot, so a background update cannot split
// the version declarations within an outgoing request. The return value must also
// be used for the catalog's client_version query parameter.
func ApplyCodexClientHeaders(headers http.Header) string {
	version := CodexClientVersion()
	headers.Set("Version", version)
	headers.Set("Originator", "codex-tui")
	headers.Set("User-Agent", "codex-tui/"+version+" (Ubuntu 22.4.0; x86_64) xterm-256color")
	return version
}

func validCodexVersion(version string) bool {
	if len(version) > 32 || !codexStableVersion.MatchString(version) {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}

func newerCodexVersion(candidate, current string) bool {
	c, v := strings.Split(candidate, "."), strings.Split(current, ".")
	for i := range c {
		a, _ := strconv.ParseUint(c[i], 10, 32)
		b, _ := strconv.ParseUint(v[i], 10, 32)
		if a != b {
			return a > b
		}
	}
	return false
}

// StartCodexVersionSync starts one process-scoped worker. A manual pin disables
// synchronization. Failed refreshes retain the last successful in-memory value;
// restarts fall back to the bundled version until a release is fetched.
func StartCodexVersionSync(ctx context.Context) (func(), error) {
	version := strings.TrimSpace(os.Getenv("BOS_CODEX_CLIENT_VERSION"))
	pinned := version != ""
	if pinned && !validCodexVersion(version) {
		return nil, errors.New("BOS_CODEX_CLIENT_VERSION must be a stable major.minor.patch version")
	}
	auto := true
	if value := strings.TrimSpace(os.Getenv("BOS_CODEX_VERSION_AUTO_UPDATE")); value != "" {
		var err error
		auto, err = strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("BOS_CODEX_VERSION_AUTO_UPDATE must be a boolean")
		}
	}
	if !pinned {
		version = DefaultCodexClientVersion
	}
	state := newCodexVersionState(version)
	activeCodexVersion.Store(state)
	logging.Printf(ctx, "INFO", "codex.version.configured", "version=%s pinned=%t auto_update=%t", version, pinned, auto && !pinned)
	if pinned || !auto {
		return func() {}, nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		refresh := func() {
			before := state.version.Load().(string)
			if err := state.refresh(workerCtx, client, codexStableReleaseURL); err != nil {
				if workerCtx.Err() == nil {
					logging.Printf(workerCtx, "WARN", "codex.version.sync_failed", "retained_version=%s", before)
				}
				return
			}
			if after := state.version.Load().(string); after != before {
				logging.Printf(workerCtx, "INFO", "codex.version.updated", "previous_version=%s version=%s", before, after)
			}
		}
		refresh()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}

func (s *codexVersionState) refresh(ctx context.Context, client *http.Client, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bosapi-codex-version-sync")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("Codex release discovery failed")
	}
	// Codex releases include many platform assets, so the GitHub JSON can be
	// substantially larger than the few metadata fields we consume.
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return errors.New("invalid Codex release response")
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(raw, &release) != nil || release.Draft || release.Prerelease || !strings.HasPrefix(release.Tag, "rust-v") {
		return errors.New("invalid stable Codex release")
	}
	version := strings.TrimPrefix(release.Tag, "rust-v")
	if !validCodexVersion(version) {
		return errors.New("invalid stable Codex version")
	}
	if newerCodexVersion(version, s.version.Load().(string)) {
		s.version.Store(version)
	}
	return nil
}

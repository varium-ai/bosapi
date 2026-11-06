//go:build darwin

package interfaces

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed live_attestation.js
var liveAttestationScript string

func generateLiveAttestation(ctx context.Context) (string, error) {
	app := "/Applications/ChatGPT.app"
	node := filepath.Join(app, "Contents/Resources/cua_node/bin/node")
	addon := filepath.Join(app, "Contents/Resources/native/devicecheck.node")
	if _, err := os.Stat(node); err != nil {
		return "", errors.New("chatgpt app runtime unavailable")
	}
	if _, err := os.Stat(addon); err != nil {
		return "", errors.New("devicecheck unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	bundle, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", filepath.Join(app, "Contents/Info.plist")).Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(bundle)), "com.openai.") {
		return "", errors.New("invalid chatgpt app bundle")
	}
	const script = `ObjC.import("Foundation");ObjC.import("AppKit");const s=$.NSScreen.mainScreen;const f=s.frame;JSON.stringify({locale:ObjC.unwrap($.NSLocale.currentLocale.localeIdentifier),languages:ObjC.deepUnwrap($.NSLocale.preferredLanguages),timezone:ObjC.unwrap($.NSTimeZone.localTimeZone.name),width:Number(f.size.width),height:Number(f.size.height),scale:Number(s.backingScaleFactor)})`
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return "", errors.New("macos signals unavailable")
	}
	var values struct {
		Locale    string   `json:"locale"`
		Languages []string `json:"languages"`
		Timezone  string   `json:"timezone"`
		Width     float64  `json:"width"`
		Height    float64  `json:"height"`
		Scale     float64  `json:"scale"`
	}
	if json.Unmarshal(output, &values) != nil {
		return "", errors.New("invalid macos signals")
	}
	if values.Scale <= 0 {
		values.Scale = 1
	}
	if len(values.Languages) == 0 {
		values.Languages = []string{values.Locale}
	}
	signals := map[string]any{"schemaVersion": 1, "preferredLanguages": values.Languages, "locale": values.Locale, "timezone": values.Timezone, "screenSizeSum": int(values.Width + values.Height + 0.5), "screenScale": values.Scale, "appSessionId": random(16)}
	encoded, _ := json.Marshal(signals)
	command := exec.CommandContext(ctx, node, "-e", liveAttestationScript)
	command.Env = []string{"PATH=/usr/bin:/bin", "BOS_DEVICECHECK_ADDON=" + addon, "BOS_DEVICECHECK_BUNDLE=" + strings.TrimSpace(string(bundle)), "BOS_DEVICECHECK_SIGNALS=" + string(encoded)}
	value, err := command.Output()
	if err != nil || len(value) < 20 || len(value) > 16<<10 || !json.Valid(value) {
		return "", errors.New("devicecheck attestation failed")
	}
	return strings.TrimSpace(string(value)), nil
}

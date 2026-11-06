package securityevidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// credentialManifest records the public credential corpus coverage without
// depending on the private advanced-detector fixtures.
type credentialManifest struct {
	CorpusVersion string `json:"corpus_version"`
	Coverage      struct {
		CredentialsPositive int `json:"credentials_positive"`
		CredentialsNegative int `json:"credentials_negative"`
	} `json:"coverage"`
}

type credentialFixture struct {
	ID        string `json:"id"`
	Group     string `json:"group"`
	Input     string `json:"input"`
	ExpectHit bool   `json:"expect_hit"`
}

// readCredentialCorpus loads the public credential fixtures and validates the
// manifest counts so the open-source corpus cannot silently drift.
func readCredentialCorpus(t *testing.T) (credentialManifest, []credentialFixture) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "security", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest credentialManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if strings.TrimSpace(manifest.CorpusVersion) == "" {
		t.Fatal("manifest corpus_version is empty")
	}

	files, err := filepath.Glob(filepath.Join("testdata", "security", "credentials.*.jsonl"))
	if err != nil {
		t.Fatalf("glob credential corpus: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no credential corpus files found")
	}

	seen := make(map[string]string)
	all := make([]credentialFixture, 0, 32)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for lineNo, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var fixture credentialFixture
			if err := json.Unmarshal([]byte(line), &fixture); err != nil {
				t.Fatalf("%s:%d decode: %v", path, lineNo+1, err)
			}
			if err := validateCredentialFixture(fixture); err != nil {
				t.Fatalf("%s:%d %v", path, lineNo+1, err)
			}
			if previous, exists := seen[fixture.ID]; exists {
				t.Fatalf("duplicate id %q in %s and %s", fixture.ID, previous, path)
			}
			seen[fixture.ID] = path
			all = append(all, fixture)
		}
	}
	return manifest, all
}

func validateCredentialFixture(fixture credentialFixture) error {
	if strings.TrimSpace(fixture.ID) == "" {
		return fmt.Errorf("id is empty")
	}
	if fixture.Group != "credentials" {
		return fmt.Errorf("unsupported group %q", fixture.Group)
	}
	if strings.TrimSpace(fixture.Input) == "" {
		return fmt.Errorf("input is empty")
	}
	return nil
}

// TestCredentialCorpusAgainstCurrentRules proves the public credential fixtures
// match the rules already implemented in the open-source core.
func TestCredentialCorpusAgainstCurrentRules(t *testing.T) {
	manifest, fixtures := readCredentialCorpus(t)

	positive, negative := 0, 0
	for _, fixture := range fixtures {
		matched := false
		for _, pattern := range CredentialPatterns {
			if pattern.Regex.MatchString(fixture.Input) {
				matched = true
				break
			}
		}
		if !matched && len(CredentialAssignments([]byte(fixture.Input), -1)) > 0 {
			matched = true
		}
		if fixture.ExpectHit && !matched {
			t.Fatalf("%s: expected current credential rules to match %q", fixture.ID, fixture.Input)
		}
		if !fixture.ExpectHit && matched {
			t.Fatalf("%s: expected current credential rules not to match %q", fixture.ID, fixture.Input)
		}
		if fixture.ExpectHit {
			positive++
		} else {
			negative++
		}
	}
	if positive != manifest.Coverage.CredentialsPositive || negative != manifest.Coverage.CredentialsNegative {
		t.Fatalf("credential coverage: positive=%d/%d negative=%d/%d", positive, manifest.Coverage.CredentialsPositive, negative, manifest.Coverage.CredentialsNegative)
	}
}

package migrations

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// These files have already been applied to existing databases. Changing them
// prevents startup even if a later migration reaches the same final schema.
func TestDeployedMigrationChecksums(t *testing.T) {
	for name, expected := range map[string]string{
		"001_initial_schema.sql":       "f69f5bcd8ad1d2b6012f003422d4cd9db41535d89abab0a6c84415049e86cd56",
		"023_usage_log_transition.sql": "2995e83aea1154455279ea2984e274122722098ec76d380acb46aa48d690e870",
		"028_usage_partitions_utc.sql": "16474f523f8db8a946fb890427fc51eeec52062b9dca0c2d7f199ef83abb03b7",
	} {
		body, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != expected {
			t.Errorf("%s checksum=%s; preserve deployed history and add a new migration", name, got)
		}
	}
}

package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/securityevidence"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestEventDetailEvidenceScopeExpiryAndHistoricalCompatibility(t *testing.T) {
	for _, test := range []struct {
		name    string
		count   uint8
		age     time.Duration
		found   bool
		context bool
	}{
		{"new evidence", 1, time.Hour, true, false},
		{"redacted context", 1, time.Hour, true, true},
		{"historical event", 0, time.Hour, true, false},
		{"expired evidence", 1, securityevidence.Retention + time.Hour, true, false},
		{"foreign event", 1, time.Hour, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			created := time.Now().UTC().Add(-test.age)
			rows := sqlmock.NewRows([]string{"id", "organization_id", "rule_id", "rule_version", "rule_scope", "rule_name_snapshot", "processing_result", "evidence_count", "created_at"})
			if test.found {
				rows.AddRow(42, 9, "12", 7, "organization", "original rule name", "redacted", test.count, created)
			}
			mock.ExpectQuery("SELECT security_events\\.\\* FROM `security_events` WHERE id = \\? AND organization_id = \\? LIMIT \\?").WithArgs(42, 9, 1).WillReturnRows(rows)
			snippet, visibility := "PRIVATE_ORIGINAL", "redacted"
			expectedSnippet := securityevidence.CredentialHidden
			if test.context {
				snippet, visibility = "配置 api_key=[Credential hidden] 后重试", securityevidence.ContextVisibility
				expectedSnippet = snippet
			}
			if test.found && test.count > 0 && test.age < securityevidence.Retention {
				// A corrupted/custom snippet must also be normalized on reads.
				mock.ExpectQuery("SELECT \\* FROM `security_event_evidence` WHERE event_id = \\? AND expires_at > \\? ORDER BY ordinal ASC LIMIT \\?").WithArgs(42, sqlmock.AnyArg(), securityevidence.MaxItems).
					WillReturnRows(sqlmock.NewRows([]string{"source", "path", "reason_code", "snippet", "visibility", "start_line", "start_column", "end_line", "end_column"}).AddRow("prompt", "messages[3].content", "credential_format", snippet, visibility, 2, 4, 2, 20))
			}
			detail, err := NewSecurityEventRepository(database).GetSecurityEventDetail(context.Background(), 42, 9)
			if !test.found {
				if !errors.Is(err, domain.ErrSecurityEventNotFound) {
					t.Fatalf("error=%v", err)
				}
			} else {
				if err != nil || detail.RuleName != "original rule name" || detail.ProcessingResult != "redacted" {
					t.Fatalf("detail=%+v err=%v", detail, err)
				}
				status := "available"
				if test.count == 0 {
					status = "not_collected"
				} else if test.age >= securityevidence.Retention {
					status = "expired"
				}
				if detail.EvidenceStatus != status {
					t.Fatalf("status=%s want=%s", detail.EvidenceStatus, status)
				}
				encoded, _ := json.Marshal(detail)
				if strings.Contains(string(encoded), "PRIVATE_") {
					t.Fatalf("unsafe response: %s", encoded)
				}
				if status == "available" && (len(detail.Evidence) != 1 || detail.Evidence[0].Snippet != expectedSnippet || detail.Evidence[0].Reason == "") {
					t.Fatalf("evidence=%+v", detail.Evidence)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

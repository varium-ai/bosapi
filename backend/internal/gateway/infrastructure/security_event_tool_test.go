package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bosapi/backend/internal/gateway/domain"
	"bosapi/backend/internal/platform/securityevidence"
)

func TestToolSecurityEventPersistsOnlySafeMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	call := domain.ToolCall{ID: "PRIVATE_CALL_ID", Name: "shell", Arguments: json.RawMessage(`{"password":"PRIVATE_PARAMETER"}`), Protocol: domain.ToolProtocolOpenAI, ToolCallContext: domain.ToolCallContext{Direction: domain.ToolDirectionResponse}}
	metadata := call.Metadata()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `security_events`").WithArgs("tool_call", "warning", "request", 9, 7, []byte(nil), "123", "block", 2, 200, now, "shell", "openai", "response", metadata.CallIDHash, 4, "global", "normal", "", "blocked", 0).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	sink := NewDatabaseSecurityEventSink(database, 1)
	if err := sink.Publish(context.Background(), domain.SecurityEvent{RequestID: "request", OrganizationID: 9, APIKeyID: 7, RuleID: "123", Action: "block", HitCount: 2, HTTPStatus: 200, CreatedAt: now, ToolCall: &metadata, RuleVersion: 4, RuleScope: "global"}); err != nil {
		t.Fatal(err)
	}
	sink.Close()
	mock.ExpectClose()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceAndEventPersistAtomically(t *testing.T) {
	for _, failEvidence := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		database, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO `security_events`").WillReturnResult(sqlmock.NewResult(42, 1))
		insert := mock.ExpectExec("INSERT INTO `security_event_evidence`").WithArgs(42, 0, "tool_arguments", "arguments.command", 1, 1, 1, 7, "regex_match", "configured_pattern", "", "", "hidden", now, now.Add(securityevidence.Retention))
		if failEvidence {
			insert.WillReturnError(errors.New("evidence unavailable"))
			mock.ExpectRollback()
		} else {
			insert.WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
		}
		sink := NewDatabaseSecurityEventSink(database, 1)
		_ = sink.Publish(context.Background(), domain.SecurityEvent{OrganizationID: 9, RuleID: "12", Action: "block", CreatedAt: now, RuleName: "rule snapshot", Evidence: []securityevidence.Evidence{{Source: "tool_arguments", Path: "arguments.command", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 7, ReasonCode: "regex_match", Snippet: "PRIVATE_CONTENT"}}})
		sink.Close()

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
}

func TestEventQueueOwnsNormalizedEvidenceAndMetadata(t *testing.T) {
	sink := &DatabaseSecurityEventSink{queue: make(chan domain.SecurityEvent, 1)}
	evidence := []securityevidence.Evidence{{Source: "prompt", Path: "messages[0].content", ReasonCode: "credential_format", Snippet: "PRIVATE_VALUE"}}
	metadata := &domain.ToolCallMetadata{Name: "shell"}
	ip := []byte{192, 0, 2, 1}
	_ = sink.Publish(context.Background(), domain.SecurityEvent{Evidence: evidence, ToolCall: metadata, ClientIP: ip})
	evidence[0].Path, metadata.Name, ip[0] = "PRIVATE_PATH", "PRIVATE_NAME", 0
	queued := <-sink.queue
	if queued.Evidence[0].Path != "messages[0].content" || queued.Evidence[0].Snippet != "[Credential hidden]" || queued.ToolCall.Name != "shell" || queued.ClientIP[0] != 192 {
		t.Fatalf("queue changed or leaked: %+v", queued)
	}
}

func TestExpiredEvidenceCleanupKeepsEventMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM security_event_evidence WHERE expires_at <= \\? ORDER BY expires_at LIMIT 10000").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 10000))
	mock.ExpectExec("DELETE FROM security_event_evidence WHERE expires_at <= \\? ORDER BY expires_at LIMIT 10000").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 2))
	(&DatabaseSecurityEventSink{database: database}).cleanupEvidence()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

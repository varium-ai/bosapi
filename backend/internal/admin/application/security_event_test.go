package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

type securityEventTestRepository struct {
	filter domain.SecurityEventFilter
	calls  int
	err    error
}

func (repository *securityEventTestRepository) ListSecurityEvents(_ context.Context, filter domain.SecurityEventFilter, _, _ int) ([]domain.SecurityEvent, int64, error) {
	repository.filter = filter
	repository.calls++
	return []domain.SecurityEvent{{ID: 7}}, 1, repository.err
}

func (repository *securityEventTestRepository) GetSecurityEventDetail(context.Context, uint64, uint64) (domain.SecurityEventDetail, error) {
	repository.calls++
	return domain.SecurityEventDetail{}, repository.err
}

type securityEventReadTestRepository struct {
	securityEventTestRepository
	scope domain.SecurityEventReadScope
	count int64
}

func (repository *securityEventReadTestRepository) CountUnreadSecurityEvents(_ context.Context, scope domain.SecurityEventReadScope) (int64, error) {
	repository.calls++
	repository.scope = scope
	return repository.count, repository.err
}

func (repository *securityEventReadTestRepository) MarkSecurityEventsRead(_ context.Context, scope domain.SecurityEventReadScope) error {
	repository.calls++
	repository.scope = scope
	return repository.err
}

func TestValidateSecurityEventFilter(t *testing.T) {
	for _, test := range []struct {
		name    string
		filter  domain.SecurityEventFilter
		page    int
		size    int
		invalid bool
	}{
		{name: "empty filters", page: 1, size: 20},
		{name: "exact and minimum", filter: domain.SecurityEventFilter{Severity: "high", MinSeverity: "warning"}, page: 1, size: 100},
		{name: "contradicting levels remain an empty query", filter: domain.SecurityEventFilter{Severity: "info", MinSeverity: "warning"}, page: 1, size: 1},
		{name: "minimum info", filter: domain.SecurityEventFilter{MinSeverity: "info"}, page: 1, size: 1},
		{name: "minimum high", filter: domain.SecurityEventFilter{MinSeverity: "high"}, page: 1, size: 1},
		{name: "minimum critical", filter: domain.SecurityEventFilter{MinSeverity: "critical"}, page: 1, size: 1},
		{name: "invalid minimum", filter: domain.SecurityEventFilter{MinSeverity: "unknown"}, page: 1, size: 20, invalid: true},
		{name: "injected minimum", filter: domain.SecurityEventFilter{MinSeverity: "warning' OR 1=1 --"}, page: 1, size: 20, invalid: true},
		{name: "invalid exact", filter: domain.SecurityEventFilter{Severity: "unknown"}, page: 1, size: 20, invalid: true},
		{name: "invalid event type", filter: domain.SecurityEventFilter{EventType: "unknown"}, page: 1, size: 20, invalid: true},
		{name: "zero page", page: 0, size: 20, invalid: true},
		{name: "zero size", page: 1, size: 0, invalid: true},
		{name: "large size", page: 1, size: 101, invalid: true},
		{name: "long rule", filter: domain.SecurityEventFilter{RuleID: strings.Repeat("x", 65)}, page: 1, size: 20, invalid: true},
		{name: "long action", filter: domain.SecurityEventFilter{Action: strings.Repeat("x", 33)}, page: 1, size: 20, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateSecurityEventFilter(test.filter, test.page, test.size)
			if errors.Is(err, ErrInvalidAdminUsageFilter) != test.invalid {
				t.Fatalf("validation error=%v invalid=%v", err, test.invalid)
			}
		})
	}
}

func TestSecurityEventActorReadScope(t *testing.T) {
	for _, test := range []struct {
		name      string
		actor     SecurityEventActor
		want      domain.SecurityEventReadScope
		forbidden bool
	}{
		{name: "organization", actor: SecurityEventActor{UserID: 7, OrganizationID: 9, Role: "org_admin"}, want: domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9}},
		{name: "platform", actor: SecurityEventActor{UserID: 7, OrganizationID: 9, Role: "super_admin"}, want: domain.SecurityEventReadScope{UserID: 7}},
		{name: "platform without organization", actor: SecurityEventActor{UserID: 7, Role: "super_admin"}, want: domain.SecurityEventReadScope{UserID: 7}},
		{name: "missing organization", actor: SecurityEventActor{UserID: 7, Role: "org_admin"}, forbidden: true},
		{name: "missing user", actor: SecurityEventActor{Role: "super_admin"}, forbidden: true},
		{name: "viewer", actor: SecurityEventActor{UserID: 7, OrganizationID: 9, Role: "key_viewer"}, forbidden: true},
		{name: "unknown role", actor: SecurityEventActor{UserID: 7, OrganizationID: 9, Role: "admin"}, forbidden: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.actor.readScope()
			if errors.Is(err, ErrSecurityEventForbidden) != test.forbidden || got != test.want {
				t.Fatalf("scope=%+v error=%v want=%+v forbidden=%v", got, err, test.want, test.forbidden)
			}
		})
	}
}

func TestSecurityEventReadService(t *testing.T) {
	for _, mark := range []bool{false, true} {
		for _, outcome := range []string{"success", "repository failure", "forbidden", "repository unavailable"} {
			t.Run(map[bool]string{false: "unread ", true: "mark read "}[mark]+outcome, func(t *testing.T) {
				repository := &securityEventReadTestRepository{count: 3}
				actor := SecurityEventActor{UserID: 7, OrganizationID: 9, Role: "org_admin"}
				if outcome == "repository failure" {
					repository.err = context.Canceled
				}
				if outcome == "forbidden" {
					actor.OrganizationID = 0
				}
				service := NewSecurityEventService(repository)
				if outcome == "repository unavailable" {
					service = NewSecurityEventService(&securityEventTestRepository{})
				}
				var state domain.SecurityEventReadState
				var err error
				if mark {
					state, err = service.MarkRead(context.Background(), actor)
				} else {
					state, err = service.Unread(context.Background(), actor)
				}
				if outcome != "success" {
					if err == nil {
						t.Fatal("expected error")
					}
					if outcome == "repository failure" && !errors.Is(err, context.Canceled) {
						t.Fatalf("repository error was not wrapped: %v", err)
					}
					if outcome == "forbidden" && (!errors.Is(err, ErrSecurityEventForbidden) || repository.calls != 0) {
						t.Fatalf("forbidden error=%v calls=%d", err, repository.calls)
					}
					return
				}
				want := domain.SecurityEventReadState{UnreadCount: 3}
				if mark {
					want = domain.SecurityEventReadState{}
				}
				if err != nil || state != want || repository.scope != (domain.SecurityEventReadScope{UserID: 7, OrganizationID: 9}) {
					t.Fatalf("state=%+v error=%v scope=%+v", state, err, repository.scope)
				}
			})
		}
	}
}

func TestSecurityEventListService(t *testing.T) {
	for _, outcome := range []string{"success", "invalid", "repository error"} {
		t.Run(outcome, func(t *testing.T) {
			repository := &securityEventTestRepository{}
			filter := domain.SecurityEventFilter{MinSeverity: "warning", Severity: "high"}
			if outcome == "invalid" {
				filter.MinSeverity = "unknown"
			}
			if outcome == "repository error" {
				repository.err = context.Canceled
			}
			items, count, err := NewSecurityEventService(repository).List(context.Background(), filter, 1, 20)
			switch outcome {
			case "success":
				if err != nil || count != 1 || len(items) != 1 || repository.filter != filter {
					t.Fatalf("items=%v count=%d error=%v filter=%+v", items, count, err, repository.filter)
				}
			case "invalid":
				if !errors.Is(err, ErrInvalidAdminUsageFilter) || repository.calls != 0 {
					t.Fatalf("error=%v calls=%d", err, repository.calls)
				}
			case "repository error":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			}
		})
	}
}

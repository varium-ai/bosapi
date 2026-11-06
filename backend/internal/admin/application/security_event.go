package application

import (
	"context"
	"errors"
	"fmt"

	"bosapi/backend/internal/admin/domain"
)

// ErrSecurityEventForbidden prevents non-administrators or unscoped tenants from reading events.
var ErrSecurityEventForbidden = errors.New("security event access forbidden")

// SecurityEventActor contains privileges obtained from the authenticated session.
type SecurityEventActor struct {
	UserID         uint64
	OrganizationID uint64
	Role           string
}

func (actor SecurityEventActor) readScope() (domain.SecurityEventReadScope, error) {
	if actor.UserID == 0 {
		return domain.SecurityEventReadScope{}, ErrSecurityEventForbidden
	}
	switch actor.Role {
	case "super_admin":
		return domain.SecurityEventReadScope{UserID: actor.UserID}, nil
	case "org_admin":
		if actor.OrganizationID > 0 {
			return domain.SecurityEventReadScope{UserID: actor.UserID, OrganizationID: actor.OrganizationID}, nil
		}
	}
	return domain.SecurityEventReadScope{}, ErrSecurityEventForbidden
}

// SecurityEventService provides bounded security event queries.
type SecurityEventService struct {
	repository domain.SecurityEventRepository
}

// NewSecurityEventService constructs the security event query service.
func NewSecurityEventService(repository domain.SecurityEventRepository) *SecurityEventService {
	return &SecurityEventService{repository: repository}
}

// List returns a page of metadata-only security events.
func (service *SecurityEventService) List(ctx context.Context, filter domain.SecurityEventFilter, page, size int) ([]domain.SecurityEvent, int64, error) {
	if err := validateSecurityEventFilter(filter, page, size); err != nil {
		return nil, 0, err
	}
	items, total, err := service.repository.ListSecurityEvents(ctx, filter, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("list security events: %w", err)
	}
	return items, total, nil
}

func validateSecurityEventFilter(filter domain.SecurityEventFilter, page, size int) error {
	if page < 1 || size < 1 || size > 100 || len(filter.RuleID) > 64 || len(filter.Action) > 32 || !domain.ValidSecurityEventType(filter.EventType) || !domain.ValidSecuritySeverity(filter.Severity) || !domain.ValidSecuritySeverity(filter.MinSeverity) {
		return ErrInvalidAdminUsageFilter
	}
	return nil
}

// Unread counts warning and more severe events within the authenticated administrator's scope.
func (service *SecurityEventService) Unread(ctx context.Context, actor SecurityEventActor) (domain.SecurityEventReadState, error) {
	scope, err := actor.readScope()
	if err != nil {
		return domain.SecurityEventReadState{}, err
	}
	repository, ok := service.repository.(domain.SecurityEventReadRepository)
	if !ok {
		return domain.SecurityEventReadState{}, errors.New("security event read repository is unavailable")
	}
	count, err := repository.CountUnreadSecurityEvents(ctx, scope)
	if err != nil {
		return domain.SecurityEventReadState{}, fmt.Errorf("count unread security events: %w", err)
	}
	return domain.SecurityEventReadState{UnreadCount: count}, nil
}

// MarkRead acknowledges the current warning and more severe events within the administrator's scope.
func (service *SecurityEventService) MarkRead(ctx context.Context, actor SecurityEventActor) (domain.SecurityEventReadState, error) {
	scope, err := actor.readScope()
	if err != nil {
		return domain.SecurityEventReadState{}, err
	}
	repository, ok := service.repository.(domain.SecurityEventReadRepository)
	if !ok {
		return domain.SecurityEventReadState{}, errors.New("security event read repository is unavailable")
	}
	if err := repository.MarkSecurityEventsRead(ctx, scope); err != nil {
		return domain.SecurityEventReadState{}, fmt.Errorf("mark security events read: %w", err)
	}
	return domain.SecurityEventReadState{}, nil
}

// Detail returns evidence only after the HTTP boundary has established the administrator's scope.
func (service *SecurityEventService) Detail(ctx context.Context, id, organizationID uint64) (domain.SecurityEventDetail, error) {
	if id == 0 {
		return domain.SecurityEventDetail{}, ErrInvalidAdminUsageFilter
	}
	return service.repository.GetSecurityEventDetail(ctx, id, organizationID)
}

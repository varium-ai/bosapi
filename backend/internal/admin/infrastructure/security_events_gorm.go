package infrastructure

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/geoip"
	"bosapi/backend/internal/platform/securityevidence"

	"gorm.io/gorm"
)

type securityEventRecord struct {
	EventType        string
	Severity         string
	ID               uint64
	RequestID        string
	OrganizationID   *uint64
	APIKeyID         *uint64
	ClientIP         []byte
	RuleID           string
	Action           string
	HitCount         uint32
	HTTPStatus       *uint16
	ToolName         string
	ToolProtocol     string
	ToolDirection    string
	CallIDHash       string
	RuleVersion      uint64
	RuleScope        string
	RuleSensitivity  string
	RuleNameSnapshot string
	ProcessingResult string
	EvidenceCount    uint8
	CreatedAt        time.Time
}

func (securityEventRecord) TableName() string { return "security_events" }

const securityEventSelect = "security_events.*"

// SecurityEventRepository reads security metadata without exposing scanned content.
type SecurityEventRepository struct {
	database    *gorm.DB
	geoip       *geoip.Resolver
	unreadCache *securityUnreadCache
}

// NewSecurityEventRepository constructs the Gorm security event repository.
func NewSecurityEventRepository(database *gorm.DB, resolver ...*geoip.Resolver) *SecurityEventRepository {
	var lookup *geoip.Resolver
	if len(resolver) > 0 {
		lookup = resolver[0]
	}
	return &SecurityEventRepository{database: database, geoip: lookup}
}

// ListSecurityEvents returns paginated, filtered event metadata with IP addresses masked.
func (repository *SecurityEventRepository) ListSecurityEvents(ctx context.Context, filter domain.SecurityEventFilter, page, size int) ([]domain.SecurityEvent, int64, error) {
	query := repository.database.WithContext(ctx).Model(&securityEventRecord{})
	if filter.OrganizationID > 0 {
		query = query.Where("organization_id = ?", filter.OrganizationID)
	}
	if filter.APIKeyID > 0 {
		query = query.Where("api_key_id = ?", filter.APIKeyID)
	}
	if filter.RuleID != "" {
		query = query.Where("rule_id = ?", filter.RuleID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.ToolName != "" {
		query = query.Where("tool_name = ?", filter.ToolName)
	}
	if filter.ClientIP != "" {
		query = query.Where("client_ip = INET6_ATON(?)", filter.ClientIP)
	}
	if filter.EventType != "" {
		query = query.Where("event_type = ?", filter.EventType)
	}
	if filter.Severity != "" {
		query = query.Where("severity = ?", filter.Severity)
	}
	if filter.MinSeverity != "" {
		query = query.Where("severity IN ?", securitySeveritiesAtLeast(filter.MinSeverity))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []securityEventRecord
	if err := query.Select(securityEventSelect).Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.SecurityEvent, 0, len(records))
	for _, record := range records {
		items = append(items, repository.eventView(record))
	}
	return items, total, nil
}

func securitySeveritiesAtLeast(minimum string) []string {
	levels := []string{"info", "warning", "high", "critical"}
	for index, severity := range levels {
		if severity == minimum {
			return levels[index:]
		}
	}
	return []string{}
}

func (repository *SecurityEventRepository) eventView(record securityEventRecord) domain.SecurityEvent {
	masked := formatClientIP(net.IP(record.ClientIP).String(), repository.geoip)
	return domain.SecurityEvent{EventType: record.EventType, Severity: record.Severity, ID: record.ID, RequestID: record.RequestID, OrganizationID: record.OrganizationID, APIKeyID: record.APIKeyID, ClientIP: masked, RuleID: record.RuleID, RuleName: record.RuleNameSnapshot, Action: record.Action, HitCount: record.HitCount, HTTPStatus: record.HTTPStatus, CreatedAt: record.CreatedAt, ToolName: record.ToolName, ToolProtocol: record.ToolProtocol, ToolDirection: record.ToolDirection, CallIDHash: record.CallIDHash, RuleVersion: record.RuleVersion, RuleScope: record.RuleScope, RuleSensitivity: record.RuleSensitivity}
}

func (repository *SecurityEventRepository) GetSecurityEventDetail(ctx context.Context, id, organizationID uint64) (domain.SecurityEventDetail, error) {
	query := repository.database.WithContext(ctx).Model(&securityEventRecord{}).Select(securityEventSelect).Where("id = ?", id)
	if organizationID > 0 {
		query = query.Where("organization_id = ?", organizationID)
	}
	var record securityEventRecord
	if err := query.Take(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.SecurityEventDetail{}, domain.ErrSecurityEventNotFound
		}
		return domain.SecurityEventDetail{}, err
	}
	events := []domain.SecurityEvent{repository.eventView(record)}
	detail := domain.SecurityEventDetail{SecurityEvent: events[0], ProcessingResult: record.ProcessingResult, EvidenceStatus: "not_collected", EvidenceCount: record.EvidenceCount, EvidenceLimit: securityevidence.MaxItems, Evidence: []securityevidence.View{}}
	if record.EvidenceCount == 0 {
		return detail, nil
	}
	expires := record.CreatedAt.Add(securityevidence.Retention)
	detail.EvidenceExpiresAt = &expires
	now := time.Now().UTC()
	if !expires.After(now) {
		detail.EvidenceStatus = "expired"
		return detail, nil
	}
	var records []securityevidence.Record
	if err := repository.database.WithContext(ctx).Where("event_id = ? AND expires_at > ?", id, now).Order("ordinal ASC").Limit(securityevidence.MaxItems).Find(&records).Error; err != nil {
		return domain.SecurityEventDetail{}, err
	}
	for _, item := range records {
		for _, evidence := range securityevidence.Normalize([]securityevidence.Evidence{item.Evidence}) {
			detail.Evidence = append(detail.Evidence, securityevidence.View{Evidence: evidence, Reason: securityevidence.Explanation(evidence.ReasonCode)})
		}
	}
	detail.EvidenceStatus = "available"
	if len(detail.Evidence) == 0 {
		detail.EvidenceStatus = "unavailable"
	}
	return detail, nil
}

func maskIP(value string) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return strconv.Itoa(int(ipv4[0])) + "." + strconv.Itoa(int(ipv4[1])) + "." + strconv.Itoa(int(ipv4[2])) + ".0"
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}

func formatClientIP(value string, resolver *geoip.Resolver) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	masked := maskIP(value)
	if resolver != nil {
		return resolver.Format(ip, masked)
	}
	return masked
}

var _ domain.SecurityEventRepository = (*SecurityEventRepository)(nil)

// MaskSecurityEventIP masks webhook source addresses in the same way as event reads.
func MaskSecurityEventIP(value string) string { return maskIP(value) }

package administration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

// The administrator's view of every person's personal access keys and of the
// service's overall switch (#332). The administrator never sees a key's
// plaintext or digest: only its ID, visible prefix, owner and usage.

var (
	// ErrAccessKeyNotFound is returned for a key ID no person holds.
	ErrAccessKeyNotFound = errors.New("administration: access key not found")
)

// AccessKeyRecord is one key as the administrator sees it. OwnerUsername and
// OwnerName come from the owner's account; an owner that was deleted leaves
// them empty.
type AccessKeyRecord struct {
	ID            string
	OwnerID       string
	OwnerUsername string
	OwnerName     string
	Name          string
	Prefix        string
	Scopes        []string
	CreatedAt     time.Time
	LastUsedAt    time.Time
	ExpiresAt     time.Time
	RevokedAt     time.Time
	RequestCount  int64
}

// Active reports whether the key can still authenticate: not revoked and not
// past its expiry.
func (r AccessKeyRecord) Active(now time.Time) bool {
	if !r.RevokedAt.IsZero() {
		return false
	}
	return r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)
}

// AccessKeyQuery is one page of the administrator's list. Search matches the
// key's name and prefix and the owner's username and name; OwnerID narrows
// the list to one person. Page and PageSize are clamped by the service.
type AccessKeyQuery struct {
	Page     int
	PageSize int
	Search   string
	OwnerID  string
}

// AccessKeyPage is one page of keys, newest first.
type AccessKeyPage struct {
	Records  []AccessKeyRecord
	Total    int
	Page     int
	PageSize int
}

// AccessKeyAdminRepository is the persistence the administrator's access key
// view needs.
type AccessKeyAdminRepository interface {
	// ListAdministrationAccessKeys returns every key matching the query's
	// Search and OwnerID, ignoring Page and PageSize: the service orders and
	// slices them, because the stored time text is not time-ordered.
	ListAdministrationAccessKeys(ctx context.Context, query AccessKeyQuery) ([]AccessKeyRecord, error)
	// RevokeAdministrationAccessKey revokes any person's key. The second
	// result is false when it was already revoked; ErrAccessKeyNotFound when
	// the key does not exist.
	RevokeAdministrationAccessKey(ctx context.Context, keyID string, now time.Time) (AccessKeyRecord, bool, error)
	// AccessKeySwitch reports the overall switch and whether it was ever set.
	AccessKeySwitch(ctx context.Context) (enabled bool, set bool, err error)
	// SetAccessKeySwitch persists the overall switch.
	SetAccessKeySwitch(ctx context.Context, enabled bool, now time.Time) error
}

// AccessKeyAdminAuditActions are the audit actions and the resource type the
// Product Schema declares for this view.
type AccessKeyAdminAuditActions struct {
	Read    string
	Revoke  string
	Enable  string
	Disable string
	Type    string
}

// AccessKeyAdminConfig is the Schema's administration.access_keys in the form
// the service uses.
type AccessKeyAdminConfig struct {
	Repository      AccessKeyAdminRepository
	Audit           AuditRecorder
	DefaultEnabled  bool
	DefaultPageSize int
	MaxPageSize     int
	AuditActions    AccessKeyAdminAuditActions
	Clock           func() time.Time
}

// AccessKeyAdminService is the administrator's access key use cases.
type AccessKeyAdminService struct {
	repository      AccessKeyAdminRepository
	audit           AuditRecorder
	defaultEnabled  bool
	defaultPageSize int
	maxPageSize     int
	auditActions    AccessKeyAdminAuditActions
	clock           func() time.Time
}

// NewAccessKeyAdminService builds the service. Every bound must be set; the
// Schema validates them before a Catalog loads.
func NewAccessKeyAdminService(config AccessKeyAdminConfig) (*AccessKeyAdminService, error) {
	if config.Repository == nil {
		return nil, errors.New("administration: 缺少 AccessKeyAdminRepository")
	}
	if config.DefaultPageSize <= 0 || config.MaxPageSize < config.DefaultPageSize {
		return nil, errors.New("administration: 访问 Key 分页上限无效")
	}
	for name, value := range map[string]string{
		"audit read":     config.AuditActions.Read,
		"audit revoke":   config.AuditActions.Revoke,
		"audit enable":   config.AuditActions.Enable,
		"audit disable":  config.AuditActions.Disable,
		"audit resource": config.AuditActions.Type,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, errors.New("administration: 访问 Key 审计动作 " + name + " 不能为空")
		}
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &AccessKeyAdminService{
		repository: config.Repository, audit: config.Audit,
		defaultEnabled: config.DefaultEnabled, defaultPageSize: config.DefaultPageSize,
		maxPageSize: config.MaxPageSize, auditActions: config.AuditActions, clock: config.Clock,
	}, nil
}

// List returns one page of keys, newest first. The read is audited.
func (s *AccessKeyAdminService) List(ctx context.Context, actor Actor, query AccessKeyQuery) (AccessKeyPage, error) {
	query.Search = strings.TrimSpace(query.Search)
	query.OwnerID = strings.TrimSpace(query.OwnerID)
	query.Page = max(1, query.Page)
	if query.PageSize < 1 {
		query.PageSize = s.defaultPageSize
	}
	query.PageSize = min(query.PageSize, s.maxPageSize)
	records, err := s.repository.ListAdministrationAccessKeys(ctx, query)
	if err != nil {
		return AccessKeyPage{}, err
	}
	sortAccessKeysNewest(records)
	total := len(records)
	start := pageStart(query.Page, query.PageSize, total)
	end := min(start+query.PageSize, total)
	page := make([]AccessKeyRecord, end-start)
	copy(page, records[start:end])
	s.recordAudit(ctx, actor, s.auditActions.Read, "", nil)
	return AccessKeyPage{Records: page, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

// pageStart is (page-1)*pageSize without overflowing: a page past the last one
// of total rows starts at the end. A huge page number cannot wrap into the
// first page, and the multiplication only runs when its result is <= total.
func pageStart(page, pageSize, total int) int {
	if page <= 1 {
		return 0
	}
	if page-1 > total/pageSize {
		return total
	}
	return (page - 1) * pageSize
}

// sortAccessKeysNewest orders the keys by creation time, newest first, with
// the ID as the tie-break so pages are stable.
func sortAccessKeysNewest(records []AccessKeyRecord) {
	slices.SortStableFunc(records, func(a, b AccessKeyRecord) int {
		switch {
		case a.CreatedAt.After(b.CreatedAt):
			return -1
		case a.CreatedAt.Before(b.CreatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

// Revoke ends any person's key at once. A key that was already revoked is not
// a change and writes no audit entry.
func (s *AccessKeyAdminService) Revoke(ctx context.Context, actor Actor, keyID string) (AccessKeyRecord, bool, error) {
	if strings.TrimSpace(keyID) == "" {
		return AccessKeyRecord{}, false, ErrAccessKeyNotFound
	}
	record, changed, err := s.repository.RevokeAdministrationAccessKey(ctx, keyID, s.clock().UTC())
	if err != nil {
		return AccessKeyRecord{}, false, err
	}
	if changed {
		s.recordAudit(ctx, actor, s.auditActions.Revoke, record.ID, map[string]any{
			"owner_id": record.OwnerID, "name": record.Name, "prefix": record.Prefix,
		})
	}
	return record, changed, nil
}

// Enabled reports the overall switch. Until an administrator sets it, the
// Product Schema's application.access_keys.enabled decides.
func (s *AccessKeyAdminService) Enabled(ctx context.Context) (bool, error) {
	enabled, set, err := s.repository.AccessKeySwitch(ctx)
	if err != nil {
		return false, err
	}
	if !set {
		return s.defaultEnabled, nil
	}
	return enabled, nil
}

// SetEnabled persists the overall switch and audits the change. Setting the
// value it already has changes nothing and writes no audit entry.
func (s *AccessKeyAdminService) SetEnabled(ctx context.Context, actor Actor, enabled bool) (bool, error) {
	current, err := s.Enabled(ctx)
	if err != nil {
		return false, err
	}
	if current == enabled {
		return enabled, nil
	}
	if err := s.repository.SetAccessKeySwitch(ctx, enabled, s.clock().UTC()); err != nil {
		return false, err
	}
	action := s.auditActions.Disable
	if enabled {
		action = s.auditActions.Enable
	}
	s.recordAudit(ctx, actor, action, "", map[string]any{"enabled": enabled})
	return enabled, nil
}

func (s *AccessKeyAdminService) recordAudit(ctx context.Context, actor Actor, action, resourceID string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	// Best effort, like every product audit: an audit outage never undoes the
	// change it describes.
	_ = s.audit.RecordAudit(ctx, AuditEvent{
		Action:       action,
		ActorID:      actor.ID,
		ActorName:    actor.Username,
		ResourceType: s.auditActions.Type,
		ResourceID:   resourceID,
		Detail:       detail,
		IPAddress:    actor.IPAddress,
		UserAgent:    actor.UserAgent,
		CreatedAt:    s.clock().UTC(),
	})
}

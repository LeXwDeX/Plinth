package administration_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

type accessKeyAdminStore struct {
	records  []administration.AccessKeyRecord
	queries  []administration.AccessKeyQuery
	enabled  bool
	set      bool
	revokes  []string
	released int
}

func (s *accessKeyAdminStore) ListAdministrationAccessKeys(_ context.Context, query administration.AccessKeyQuery) ([]administration.AccessKeyRecord, error) {
	s.queries = append(s.queries, query)
	return append([]administration.AccessKeyRecord(nil), s.records...), nil
}

func (s *accessKeyAdminStore) RevokeAdministrationAccessKey(_ context.Context, keyID string, now time.Time) (administration.AccessKeyRecord, bool, error) {
	for index := range s.records {
		if s.records[index].ID != keyID {
			continue
		}
		if !s.records[index].RevokedAt.IsZero() {
			return s.records[index], false, nil
		}
		s.records[index].RevokedAt = now
		s.revokes = append(s.revokes, keyID)
		return s.records[index], true, nil
	}
	return administration.AccessKeyRecord{}, false, administration.ErrAccessKeyNotFound
}

func (s *accessKeyAdminStore) AccessKeySwitch(context.Context) (bool, bool, error) {
	return s.enabled, s.set, nil
}

func (s *accessKeyAdminStore) SetAccessKeySwitch(_ context.Context, enabled bool, _ time.Time) error {
	s.enabled, s.set = enabled, true
	return nil
}

type accessKeyAdminAudit struct{ events []administration.AuditEvent }

func (a *accessKeyAdminAudit) RecordAudit(_ context.Context, event administration.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

func accessKeyAdminConfig(store *accessKeyAdminStore, audit administration.AuditRecorder) administration.AccessKeyAdminConfig {
	return administration.AccessKeyAdminConfig{
		Repository: store, Audit: audit, DefaultEnabled: true, DefaultPageSize: 2, MaxPageSize: 3,
		AuditActions: administration.AccessKeyAdminAuditActions{
			Read: "admin_read_access_keys", Revoke: "admin_revoke_access_key",
			Enable: "admin_enable_access_keys", Disable: "admin_disable_access_keys", Type: "access_key",
		},
		Clock: func() time.Time { return time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC) },
	}
}

func TestAccessKeyAdminListPagesFiltersAndAuditsTheRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	store := &accessKeyAdminStore{records: []administration.AccessKeyRecord{
		{ID: "a", CreatedAt: now}, {ID: "b", CreatedAt: now.Add(time.Minute)}, {ID: "c", CreatedAt: now.Add(2 * time.Minute)},
	}}
	audit := &accessKeyAdminAudit{}
	service, err := administration.NewAccessKeyAdminService(accessKeyAdminConfig(store, audit))
	if err != nil {
		t.Fatal(err)
	}
	actor := administration.Actor{ID: "admin", Username: "admin"}
	page, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Page: 2, PageSize: 2, Search: "  bot ", OwnerID: " owner "})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Page != 2 || page.PageSize != 2 || len(page.Records) != 1 || page.Records[0].ID != "a" {
		t.Fatalf("page = %+v", page)
	}
	if len(store.queries) != 1 || store.queries[0].Search != "bot" || store.queries[0].OwnerID != "owner" {
		t.Fatalf("query = %+v", store.queries)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "admin_read_access_keys" ||
		audit.events[0].ActorID != "admin" || audit.events[0].ResourceType != "access_key" {
		t.Fatalf("read audit = %+v", audit.events)
	}
	// Page and size are clamped to the declared bounds.
	clamped, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Page: 0, PageSize: 99})
	if err != nil || clamped.Page != 1 || clamped.PageSize != 3 || len(clamped.Records) != 3 {
		t.Fatalf("clamped = %+v err=%v", clamped, err)
	}
}

// A page past the end is empty: (page-1)*pageSize must not overflow into the
// first page for a page number no data could ever reach (#332 review).
func TestAccessKeyAdminListHugePageIsEmpty(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	store := &accessKeyAdminStore{records: []administration.AccessKeyRecord{
		{ID: "a", CreatedAt: now}, {ID: "b", CreatedAt: now.Add(time.Minute)},
	}}
	service, err := administration.NewAccessKeyAdminService(accessKeyAdminConfig(store, nil))
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.List(context.Background(), administration.Actor{ID: "admin"},
		administration.AccessKeyQuery{Page: math.MaxInt, PageSize: math.MaxInt})
	if err != nil || page.Total != 2 || len(page.Records) != 0 || page.Page != math.MaxInt {
		t.Fatalf("page = %+v err=%v; a huge page is past the end and empty", page, err)
	}
}

func TestAccessKeyAdminRevokeAuditsOnlyAChange(t *testing.T) {
	t.Parallel()
	store := &accessKeyAdminStore{records: []administration.AccessKeyRecord{{ID: "key", OwnerID: "owner-a", Name: "bot"}}}
	audit := &accessKeyAdminAudit{}
	service, err := administration.NewAccessKeyAdminService(accessKeyAdminConfig(store, audit))
	if err != nil {
		t.Fatal(err)
	}
	actor := administration.Actor{ID: "admin", Username: "admin"}
	record, changed, err := service.Revoke(context.Background(), actor, "key")
	if err != nil || !changed || record.ID != "key" || record.RevokedAt.IsZero() {
		t.Fatalf("revoke = %+v changed=%v err=%v", record, changed, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "admin_revoke_access_key" ||
		audit.events[0].Detail["owner_id"] != "owner-a" {
		t.Fatalf("revoke audit = %+v", audit.events)
	}
	if _, changed, err := service.Revoke(context.Background(), actor, "key"); err != nil || changed {
		t.Fatalf("repeat = %v err=%v", changed, err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("a repeat wrote an audit entry: %+v", audit.events)
	}
	if _, _, err := service.Revoke(context.Background(), actor, "missing"); !errors.Is(err, administration.ErrAccessKeyNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestAccessKeyAdminSwitchDefaultsAndAudits(t *testing.T) {
	t.Parallel()
	store := &accessKeyAdminStore{}
	audit := &accessKeyAdminAudit{}
	service, err := administration.NewAccessKeyAdminService(accessKeyAdminConfig(store, audit))
	if err != nil {
		t.Fatal(err)
	}
	ctx, actor := context.Background(), administration.Actor{ID: "admin", Username: "admin"}
	if enabled, err := service.Enabled(ctx); err != nil || !enabled {
		t.Fatalf("default = %v err=%v", enabled, err)
	}
	if enabled, err := service.SetEnabled(ctx, actor, false); err != nil || enabled {
		t.Fatalf("off = %v err=%v", enabled, err)
	}
	if enabled, err := service.Enabled(ctx); err != nil || enabled {
		t.Fatalf("read off = %v err=%v", enabled, err)
	}
	if _, err := service.SetEnabled(ctx, actor, false); err != nil || len(audit.events) != 1 {
		t.Fatalf("repeat wrote an audit entry: %v", err)
	}
	if enabled, err := service.SetEnabled(ctx, actor, true); err != nil || !enabled {
		t.Fatalf("on = %v err=%v", enabled, err)
	}
	if len(audit.events) != 2 || audit.events[0].Action != "admin_disable_access_keys" ||
		audit.events[1].Action != "admin_enable_access_keys" ||
		audit.events[0].Detail["enabled"] != false || audit.events[1].Detail["enabled"] != true {
		t.Fatalf("switch audits = %+v", audit.events)
	}
}

func TestAccessKeyAdminConfigRequiresItsBounds(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*administration.AccessKeyAdminConfig){
		"no repository":  func(config *administration.AccessKeyAdminConfig) { config.Repository = nil },
		"zero page":      func(config *administration.AccessKeyAdminConfig) { config.DefaultPageSize = 0 },
		"small maximum":  func(config *administration.AccessKeyAdminConfig) { config.MaxPageSize = 1 },
		"missing action": func(config *administration.AccessKeyAdminConfig) { config.AuditActions.Enable = "" },
	} {
		config := accessKeyAdminConfig(&accessKeyAdminStore{}, nil)
		mutate(&config)
		if _, err := administration.NewAccessKeyAdminService(config); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
)

type recordingAudit struct{ events []administration.AuditEvent }

func (r *recordingAudit) RecordAudit(_ context.Context, event administration.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func seedAccessKeyOwner(t *testing.T, r *Repository, id, username, nickname string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO authentication_users
		(id, username, nickname, name, email, roles_json, is_admin, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', '["user"]', 0, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		id, username, nickname, nickname); err != nil {
		t.Fatal(err)
	}
}

func adminAccessKeys(t *testing.T) (*Repository, time.Time) {
	t.Helper()
	r := openAccessKeyRepo(t)
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	seedAccessKeyOwner(t, r, "owner-a", "alice", "Alice")
	seedAccessKeyOwner(t, r, "owner-b", "bob", "Bob")
	for index, key := range []struct {
		id, owner, name string
		at              time.Time
	}{
		{"key-a1", "owner-a", "deploy bot", now},
		{"key-a2", "owner-a", "laptop", now.Add(time.Minute)},
		{"key-b1", "owner-b", "bob key", now.Add(2 * time.Minute)},
	} {
		record := keyRecord(key.id, key.owner, key.at, time.Hour)
		record.Name = key.name
		if err := r.CreateAccessKey(context.Background(), record, 10, key.at); err != nil {
			t.Fatalf("create %s: %v", key.id, err)
		}
		if index == 0 {
			if err := r.RecordAccessKeyUse(context.Background(), key.id, now.Add(3*time.Minute), 7); err != nil {
				t.Fatal(err)
			}
		}
	}
	return r, now
}

func TestAdministrationAccessKeyRepositoryFiltersByOwnerAndSearch(t *testing.T) {
	r, _ := adminAccessKeys(t)
	ctx := context.Background()
	all, err := r.ListAdministrationAccessKeys(ctx, administration.AccessKeyQuery{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d err=%v", len(all), err)
	}
	// The repository returns every match; ordering and paging are the
	// service's job. Only the joined owner and count are checked here.
	byOwner, err := r.ListAdministrationAccessKeys(ctx, administration.AccessKeyQuery{OwnerID: "owner-a"})
	if err != nil || len(byOwner) != 2 {
		t.Fatalf("owner-a = %d err=%v", len(byOwner), err)
	}
	if byOwner[0].OwnerUsername != "alice" || byOwner[0].OwnerName != "Alice" {
		t.Fatalf("owner projection = %+v", byOwner[0])
	}
	for query, want := range map[administration.AccessKeyQuery]int{
		{Search: "laptop"}: 1, {Search: "alice"}: 2, {Search: "key-b"}: 1, {Search: "BOB"}: 1,
	} {
		found, err := r.ListAdministrationAccessKeys(ctx, query)
		if err != nil || len(found) != want {
			t.Fatalf("search %+v = %d err=%v; want %d", query, len(found), err, want)
		}
	}
	if found, err := r.ListAdministrationAccessKeys(ctx, administration.AccessKeyQuery{Search: "%"}); err != nil || len(found) != 0 {
		t.Fatalf("literal wildcard = %d err=%v; want none", len(found), err)
	}
	record, found, err := r.AccessKey(ctx, "key-a1")
	if err != nil || !found || record.RequestCount != 7 {
		t.Fatalf("request count = %+v found=%v err=%v", record, found, err)
	}
}

func TestAdministrationAccessKeyServicePagesAndAudits(t *testing.T) {
	r, now := adminAccessKeys(t)
	audit := &recordingAudit{}
	service, err := administration.NewAccessKeyAdminService(administration.AccessKeyAdminConfig{
		Repository: r, Audit: audit, DefaultEnabled: true, DefaultPageSize: 2, MaxPageSize: 2,
		AuditActions: administration.AccessKeyAdminAuditActions{
			Read: "admin_read_access_keys", Revoke: "admin_revoke_access_key",
			Enable: "admin_enable_access_keys", Disable: "admin_disable_access_keys", Type: "access_key",
		},
		Clock: func() time.Time { return now.Add(time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := administration.Actor{ID: "admin", Username: "admin"}
	first, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Page: 1, PageSize: 2})
	if err != nil || first.Total != 3 || first.Page != 1 || first.PageSize != 2 || len(first.Records) != 2 {
		t.Fatalf("page 1 = %+v err=%v", first, err)
	}
	if first.Records[0].ID != "key-b1" || first.Records[1].ID != "key-a2" {
		t.Fatalf("newest first = %+v", first.Records)
	}
	second, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Page: 2, PageSize: 2})
	if err != nil || second.Total != 3 || len(second.Records) != 1 || second.Records[0].ID != "key-a1" {
		t.Fatalf("page 2 = %+v err=%v", second, err)
	}
	// Page and size are clamped, not trusted.
	clamped, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Page: 0, PageSize: 99})
	if err != nil || clamped.Page != 1 || clamped.PageSize != 2 || len(clamped.Records) != 2 {
		t.Fatalf("clamped = %+v err=%v", clamped, err)
	}
	searched, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Search: "bob"})
	if err != nil || searched.Total != 1 || searched.Records[0].ID != "key-b1" {
		t.Fatalf("search = %+v err=%v", searched, err)
	}
	owner, err := service.List(context.Background(), actor, administration.AccessKeyQuery{OwnerID: "owner-a"})
	if err != nil || owner.Total != 2 {
		t.Fatalf("owner = %+v err=%v", owner, err)
	}
	if len(audit.events) != 5 || audit.events[0].Action != "admin_read_access_keys" ||
		audit.events[0].ActorID != "admin" || audit.events[0].ResourceType != "access_key" {
		t.Fatalf("read audits = %+v", audit.events)
	}

	record, changed, err := service.Revoke(context.Background(), actor, "key-a1")
	if err != nil || !changed || record.ID != "key-a1" || record.RevokedAt.IsZero() {
		t.Fatalf("revoke = %+v changed=%v err=%v", record, changed, err)
	}
	_, changed, err = service.Revoke(context.Background(), actor, "key-a1")
	if err != nil || changed {
		t.Fatalf("repeat revoke changed=%v err=%v", changed, err)
	}
	if _, _, err := service.Revoke(context.Background(), actor, "missing"); !errors.Is(err, administration.ErrAccessKeyNotFound) {
		t.Fatalf("missing revoke = %v", err)
	}
	if last := audit.events[len(audit.events)-1]; last.Action != "admin_revoke_access_key" || last.ResourceID != "key-a1" {
		t.Fatalf("revoke audit = %+v", last)
	}
	// The key list now reports the state.
	after, err := service.List(context.Background(), actor, administration.AccessKeyQuery{Search: "deploy"})
	if err != nil || after.Total != 1 || after.Records[0].Active(now.Add(time.Hour)) {
		t.Fatalf("revoked listing = %+v err=%v", after, err)
	}
}

func TestAdministrationAccessKeySwitchPersistsAndAudits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switch.db")
	repository, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	audit := &recordingAudit{}
	service, err := administration.NewAccessKeyAdminService(administration.AccessKeyAdminConfig{
		Repository: repository, Audit: audit, DefaultEnabled: true, DefaultPageSize: 20, MaxPageSize: 100,
		AuditActions: administration.AccessKeyAdminAuditActions{
			Read: "admin_read_access_keys", Revoke: "admin_revoke_access_key",
			Enable: "admin_enable_access_keys", Disable: "admin_disable_access_keys", Type: "access_key",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actor := administration.Actor{ID: "admin", Username: "admin"}
	if enabled, err := service.Enabled(ctx); err != nil || !enabled {
		t.Fatalf("default = %v err=%v; want the Schema default true", enabled, err)
	}
	if enabled, err := service.SetEnabled(ctx, actor, false); err != nil || enabled {
		t.Fatalf("off = %v err=%v", enabled, err)
	}
	if enabled, err := service.Enabled(ctx); err != nil || enabled {
		t.Fatalf("read off = %v err=%v", enabled, err)
	}
	// Setting the value it already has is not a change and writes no audit.
	events := len(audit.events)
	if _, err := service.SetEnabled(ctx, actor, false); err != nil || len(audit.events) != events {
		t.Fatalf("repeat off wrote an audit entry: %v", err)
	}
	if enabled, err := service.SetEnabled(ctx, actor, true); err != nil || !enabled {
		t.Fatalf("on = %v err=%v", enabled, err)
	}
	if len(audit.events) != events+1 || audit.events[len(audit.events)-1].Action != "admin_enable_access_keys" {
		t.Fatalf("switch audits = %+v", audit.events)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openTestRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if enabled, set, err := reopened.AccessKeySwitch(ctx); err != nil || !enabled || !set {
		t.Fatalf("persisted switch = %v set=%v err=%v", enabled, set, err)
	}
}

// The 0105 migration appends the usage column and the settings table.
func TestMigration0105AddsUsageColumnAndSwitchTable(t *testing.T) {
	r := openAccessKeyRepo(t)
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('access_keys') WHERE name = 'request_count'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("request_count column = %d err=%v", count, err)
	}
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	if err := r.CreateAccessKey(context.Background(), keyRecord("fresh", "owner-a", now, time.Hour), 10, now); err != nil {
		t.Fatal(err)
	}
	record, _, err := r.AccessKey(context.Background(), "fresh")
	if err != nil || record.RequestCount != 0 || !record.LastUsedAt.IsZero() {
		t.Fatalf("fresh record = %+v err=%v", record, err)
	}
	if _, set, err := r.AccessKeySwitch(context.Background()); err != nil || set {
		t.Fatalf("switch before any set = %v err=%v", set, err)
	}
	if err := r.SetAccessKeySwitch(context.Background(), true, now); err != nil {
		t.Fatal(err)
	}
	if enabled, set, err := r.AccessKeySwitch(context.Background()); err != nil || !enabled || !set {
		t.Fatalf("switch after set = %v set=%v err=%v", enabled, set, err)
	}
}

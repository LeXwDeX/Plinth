package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

func TestAdministrationAuditQueryCombinesLegacyAndGoRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	ctx := context.Background()
	firstAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	for _, event := range []administration.AuditEvent{
		{
			ID:           "go-first",
			Action:       "login",
			ActorID:      "user-1",
			ActorName:    "Alice",
			ResourceType: "session",
			ResourceID:   "session-1",
			Detail:       map[string]any{"source": "go"},
			IPAddress:    "192.0.2.1",
			UserAgent:    "go-test",
			CreatedAt:    firstAt,
		},
		{
			ID:           "go-latest",
			Action:       "admin_update_user",
			ActorID:      "user-1",
			ActorName:    "Alice",
			ResourceType: "user",
			ResourceID:   "user-2",
			Detail:       map[string]any{"enabled": false},
			IPAddress:    "192.0.2.2",
			UserAgent:    "go-test",
			CreatedAt:    firstAt.Add(2 * time.Hour),
		},
	} {
		if err := repository.RecordAudit(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	insertLegacyAuditLog(t, path, firstAt.Add(time.Hour))

	page, total, err := repository.QueryAdministrationAuditEvents(
		ctx,
		administration.AuditQuery{Page: 1, PageSize: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 ||
		page[0].ID != "go-latest" || page[1].ID != "legacy-middle" {
		t.Fatalf("combined page total=%d events=%#v", total, page)
	}
	if page[1].Detail["source"] != "next" ||
		page[1].ActorID != "user-2" ||
		page[1].ActorName != "Bob" {
		t.Fatalf("legacy projection = %#v", page[1])
	}

	page, total, err = repository.QueryAdministrationAuditEvents(
		ctx,
		administration.AuditQuery{
			Page:     1,
			PageSize: 10,
			UserID:   "user-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page) != 2 {
		t.Fatalf("user filter total=%d events=%#v", total, page)
	}

	page, total, err = repository.QueryAdministrationAuditEvents(
		ctx,
		administration.AuditQuery{
			Page:     1,
			PageSize: 10,
			Action:   "login",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page) != 2 {
		t.Fatalf("action filter total=%d events=%#v", total, page)
	}

	from := firstAt.Add(30 * time.Minute)
	to := firstAt.Add(90 * time.Minute)
	page, total, err = repository.QueryAdministrationAuditEvents(
		ctx,
		administration.AuditQuery{
			Page:     1,
			PageSize: 10,
			From:     &from,
			To:       &to,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page) != 1 || page[0].ID != "legacy-middle" {
		t.Fatalf("time filter total=%d events=%#v", total, page)
	}

	page, total, err = repository.QueryAdministrationAuditEvents(
		ctx,
		administration.AuditQuery{Page: 2, PageSize: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 1 || page[0].ID != "go-first" {
		t.Fatalf("second page total=%d events=%#v", total, page)
	}
}

func insertLegacyAuditLog(t *testing.T, path string, createdAt time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			userId TEXT NOT NULL,
			userName TEXT NOT NULL DEFAULT '',
			resourceType TEXT NOT NULL DEFAULT '',
			resourceId TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '{}',
			ipAddress TEXT NOT NULL DEFAULT '',
			userAgent TEXT NOT NULL DEFAULT '',
			createdAt TEXT NOT NULL
		);
		INSERT INTO audit_logs (
			id, action, userId, userName, resourceType, resourceId,
			detail, ipAddress, userAgent, createdAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-middle",
		"login",
		"user-2",
		"Bob",
		"session",
		"session-2",
		`{"source":"next"}`,
		"192.0.2.3",
		"next-test",
		createdAt.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
}

package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

func TestAdministrationUsersShareDurableAuthenticationState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "administration.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Hour)
	first := administration.StoredUser{
		User: administration.User{
			ID:        "user-1",
			Username:  "alice",
			Nickname:  "Alice",
			Name:      "Alice",
			Email:     "alice@example.test",
			Active:    true,
			CreatedAt: firstAt,
		},
		PasswordHash: "hash-one",
		Roles:        []string{"user"},
		UpdatedAt:    firstAt,
	}
	second := administration.StoredUser{
		User: administration.User{
			ID:        "user-2",
			Username:  "bob",
			Nickname:  "Bob",
			Name:      "Bob",
			Admin:     true,
			Active:    true,
			CreatedAt: secondAt,
		},
		PasswordHash: "hash-two",
		Roles:        []string{"user", "admin"},
		UpdatedAt:    secondAt,
	}
	if err := repository.CreateAdministrationUser(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAdministrationUser(
		context.Background(),
		second,
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAdministrationUser(
		context.Background(),
		first,
	); !errors.Is(err, administration.ErrDuplicateUsername) {
		t.Fatalf("duplicate insert error = %v", err)
	}

	users, err := repository.ListAdministrationUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].ID != second.ID || users[1].ID != first.ID {
		t.Fatalf("newest-first users = %#v", users)
	}
	stored, exists, err := repository.FindAdministrationUserByUsername(
		context.Background(),
		"bob",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || stored.PasswordHash != second.PasswordHash ||
		len(stored.Roles) != 2 || !stored.Admin {
		t.Fatalf("stored user = %#v, exists = %v", stored, exists)
	}

	second.Active = false
	second.Name = "Bob Updated"
	second.Nickname = second.Name
	second.UpdatedAt = secondAt.Add(time.Hour)
	if err := repository.UpdateAdministrationUser(
		context.Background(),
		second,
		administration.UserGuards{},
	); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := repository.FindActiveByUsername(
		context.Background(),
		"bob",
	); err != nil || exists {
		t.Fatalf("disabled authentication lookup exists=%v err=%v", exists, err)
	}
	if err := repository.RecordAudit(context.Background(), administration.AuditEvent{
		Action:       "admin_update_user",
		ActorID:      "admin-1",
		ActorName:    "root",
		ResourceType: "user",
		ResourceID:   second.ID,
		Detail:       map[string]any{"isActive": false},
		CreatedAt:    second.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	repository, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	reloaded, exists, err := repository.FindAdministrationUser(
		context.Background(),
		second.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || reloaded.Active || reloaded.Name != second.Name {
		t.Fatalf("reloaded user = %#v, exists = %v", reloaded, exists)
	}
	audits, err := repository.ListAdministrationAuditEvents(
		context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].ResourceID != second.ID ||
		audits[0].Detail["isActive"] != false {
		t.Fatalf("reloaded audit events = %#v", audits)
	}

	if err := repository.SoftDeleteAdministrationUser(
		context.Background(),
		first.ID,
		secondAt.Add(3*time.Hour),
		administration.UserGuards{},
	); err != nil {
		t.Fatal(err)
	}
	authRecord, exists, err := repository.FindByID(
		context.Background(),
		first.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || !authRecord.Deleted {
		t.Fatalf("authentication tombstone = %#v, exists = %v", authRecord, exists)
	}
	users, err = repository.ListAdministrationUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != second.ID {
		t.Fatalf("users after soft delete = %#v", users)
	}
}

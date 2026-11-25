package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

// #599: the project group key round-trips through create, update, list and
// find; "" is stored as NULL (never as an empty string), and a soft delete
// keeps the group so a departed user's history still counts for it.
func TestAdministrationUserProjectGroupRoundTripsAsNullableColumn599(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "project-group.db")
	repository, err := sqlitetest.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	newUser := func(id, username, group string) administration.StoredUser {
		return administration.StoredUser{
			User: administration.User{
				ID: id, Username: username, Nickname: username, Name: username,
				Email: username + "@example.test", Active: true, CreatedAt: at,
				ProjectGroupKey: group,
			},
			PasswordHash: "hash", Roles: []string{"user"}, UpdatedAt: at,
		}
	}
	grouped := newUser("user-grouped", "alice", "studio-a")
	ungrouped := newUser("user-ungrouped", "bob", "")
	for _, user := range []administration.StoredUser{grouped, ungrouped} {
		if err := repository.CreateAdministrationUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	column := func(id string) sql.NullString {
		t.Helper()
		var value sql.NullString
		if err := raw.QueryRow(`SELECT project_group_key FROM authentication_users WHERE id = ?`, id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := column(grouped.ID); !got.Valid || got.String != "studio-a" {
		t.Fatalf("grouped column = %#v", got)
	}
	if got := column(ungrouped.ID); got.Valid {
		t.Fatalf("ungrouped user must be stored as NULL, got %#v", got)
	}

	users, err := repository.ListAdministrationUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]string{}
	for _, user := range users {
		groups[user.ID] = user.ProjectGroupKey
	}
	if groups[grouped.ID] != "studio-a" || groups[ungrouped.ID] != "" || len(groups) != 2 {
		t.Fatalf("listed groups = %#v", groups)
	}

	// Set the ungrouped user's group, then clear the grouped user's group.
	ungrouped.ProjectGroupKey = "studio-b"
	grouped.ProjectGroupKey = ""
	guards := administration.UserGuards{}
	for _, user := range []administration.StoredUser{ungrouped, grouped} {
		if err := repository.UpdateAdministrationUser(ctx, user, guards); err != nil {
			t.Fatal(err)
		}
	}
	if got := column(ungrouped.ID); !got.Valid || got.String != "studio-b" {
		t.Fatalf("updated column = %#v", got)
	}
	if got := column(grouped.ID); got.Valid {
		t.Fatalf("cleared group must be stored as NULL, got %#v", got)
	}
	found, exists, err := repository.FindAdministrationUserByUsername(ctx, "bob")
	if err != nil || !exists || found.ProjectGroupKey != "studio-b" {
		t.Fatalf("find by username = %+v exists=%v err=%v", found, exists, err)
	}

	if err := repository.SoftDeleteAdministrationUser(ctx, ungrouped.ID, at.Add(time.Hour), guards); err != nil {
		t.Fatal(err)
	}
	deleted, exists, err := repository.FindAdministrationUser(ctx, ungrouped.ID)
	if err != nil || !exists || !deleted.Deleted || deleted.ProjectGroupKey != "studio-b" {
		t.Fatalf("soft delete must keep the group: %+v exists=%v err=%v", deleted, exists, err)
	}
}

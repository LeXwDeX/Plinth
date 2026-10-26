package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
)

func TestAuthenticationDirectoryPersistsUsersAndLoginState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authentication.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 26, 17, 0, 0, 0, time.UTC)
	record := authentication.UserRecord{
		ID:           "auth-user-1",
		Username:     "schema-admin",
		PasswordHash: "hash-one",
		Nickname:     "Schema Admin",
		Name:         "Admin",
		Email:        "admin@example.test",
		Roles:        []string{"admin", "operator"},
		Admin:        true,
		Active:       true,
	}
	if err := repository.CreateBootstrapAdmin(
		context.Background(),
		record,
		createdAt,
	); err != nil {
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
	byUsername, exists, err := repository.FindActiveByUsername(
		context.Background(),
		"schema-admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || byUsername.ID != record.ID ||
		len(byUsername.Roles) != 2 ||
		byUsername.Roles[1] != "operator" ||
		!byUsername.Admin {
		t.Fatalf("user by username = %#v, exists = %v", byUsername, exists)
	}
	byID, exists, err := repository.FindByID(
		context.Background(),
		record.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || byID.Username != record.Username {
		t.Fatalf("user by ID = %#v, exists = %v", byID, exists)
	}
	byAnyUsername, exists, err := repository.FindByUsername(
		context.Background(),
		record.Username,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || byAnyUsername.ID != record.ID {
		t.Fatalf(
			"user by any username = %#v, exists = %v",
			byAnyUsername,
			exists,
		)
	}

	lastLogin := createdAt.Add(time.Hour)
	if err := repository.UpdateLastLogin(
		context.Background(),
		record.ID,
		lastLogin,
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdatePassword(
		context.Background(),
		record.ID,
		"hash-two",
		lastLogin,
	); err != nil {
		t.Fatal(err)
	}
	updated, exists, err := repository.FindByID(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || updated.PasswordHash != "hash-two" {
		t.Fatalf("updated user = %#v, exists = %v", updated, exists)
	}
}

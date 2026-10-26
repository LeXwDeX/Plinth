package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/administration"
)

func TestAdministrationFloorIsAtomicAcrossDatabaseConnections(t *testing.T) {
	for _, test := range []struct {
		name       string
		operations [2]string
		minimum    int
	}{
		{"demote", [2]string{"demote", "demote"}, 1},
		{"deactivate", [2]string{"deactivate", "deactivate"}, 1},
		{"delete", [2]string{"delete", "delete"}, 1},
		{"mixed", [2]string{"demote", "delete"}, 1},
		{"schema minimum", [2]string{"deactivate", "delete"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "administrators.db")
			first, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			second, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = second.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			users := make([]administration.StoredUser, test.minimum+1)
			for index := range users {
				users[index] = durableAdministrator(fmt.Sprintf("admin-%d", index))
				if err := first.CreateAdministrationUser(ctx, users[index]); err != nil {
					t.Fatal(err)
				}
			}
			guards := administration.UserGuards{MinimumActiveAdministrators: test.minimum}
			start := make(chan struct{})
			results := make(chan error, 2)
			for index, repository := range []*sqlite.Repository{first, second} {
				go func() {
					<-start
					user := users[index]
					switch test.operations[index] {
					case "delete":
						results <- repository.SoftDeleteAdministrationUser(ctx, user.ID, user.UpdatedAt.Add(time.Minute), guards)
					case "demote":
						user.Admin = false
						user.Roles = []string{"user"}
						results <- repository.UpdateAdministrationUser(ctx, user, guards)
					case "deactivate":
						user.Active = false
						results <- repository.UpdateAdministrationUser(ctx, user, guards)
					}
				}()
			}
			close(start)
			succeeded, rejected := 0, 0
			for range 2 {
				switch err := <-results; {
				case err == nil:
					succeeded++
				case errors.Is(err, administration.ErrLastAdministrator):
					rejected++
				default:
					t.Fatalf("mutation error = %v", err)
				}
			}
			if succeeded != 1 || rejected != 1 {
				t.Fatalf("successful=%d rejected=%d", succeeded, rejected)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			remaining, err := reopened.ListAdministrationUsers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			active := 0
			for _, user := range remaining {
				if user.Admin && user.Active {
					active++
					if err := reopened.SoftDeleteAdministrationUser(ctx, user.ID, time.Now(), guards); !errors.Is(err, administration.ErrLastAdministrator) {
						t.Fatalf("floor after reopen = %v", err)
					}
				}
			}
			if active != test.minimum {
				t.Fatalf("active administrators=%d, want %d", active, test.minimum)
			}
		})
	}
}

func TestAdministrationFloorUsesCurrentDurableUserState(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "administrators.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	stale := durableAdministrator("admin-only")
	stale.Admin = false
	stale.Roles = []string{"user"}
	if err := repository.CreateAdministrationUser(ctx, stale); err != nil {
		t.Fatal(err)
	}
	promoted := stale
	promoted.Admin = true
	promoted.Roles = []string{"user", "admin"}
	guards := administration.UserGuards{MinimumActiveAdministrators: 1}
	if err := repository.UpdateAdministrationUser(ctx, promoted, guards); err != nil {
		t.Fatal(err)
	}
	stale.Name = "Concurrent profile edit"
	if err := repository.UpdateAdministrationUser(ctx, stale, guards); !errors.Is(err, administration.ErrLastAdministrator) {
		t.Fatalf("stale pre-promotion update = %v", err)
	}
	stored, exists, err := repository.FindAdministrationUser(ctx, stale.ID)
	if err != nil || !exists || !stored.Admin || stored.Name == stale.Name {
		t.Fatalf("rejected update changed user: %#v, exists=%v err=%v", stored.User, exists, err)
	}
	ordinary := stale
	ordinary.ID, ordinary.Username = "ordinary", "ordinary"
	if err := repository.CreateAdministrationUser(ctx, ordinary); err != nil {
		t.Fatal(err)
	}
	if err := repository.SoftDeleteAdministrationUser(ctx, ordinary.ID, time.Now(), guards); err != nil {
		t.Fatalf("ordinary user deletion = %v", err)
	}
}

func durableAdministrator(id string) administration.StoredUser {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	return administration.StoredUser{
		User:         administration.User{ID: id, Username: id, Name: id, Admin: true, Active: true, CreatedAt: at},
		PasswordHash: "test-hash", Roles: []string{"user", "admin"}, UpdatedAt: at,
	}
}

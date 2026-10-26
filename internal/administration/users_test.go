package administration

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestUserServiceCreatesSchemaConfiguredCredentialAndAudit(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newUserService(t, repository, audit)

	user, err := service.CreateUser(context.Background(), Actor{
		ID:       "admin-1",
		Username: "root",
	}, CreateUser{
		Username: "  alice  ",
		Password: "secret12",
		Name:     "  ",
		Email:    " alice@example.test ",
		Admin:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "alice" || user.Name != "alice" ||
		user.Nickname != "alice" || user.Email != "alice@example.test" {
		t.Fatalf("unexpected normalized user: %+v", user)
	}
	if !user.Admin || !user.Active {
		t.Fatalf("unexpected flags: %+v", user)
	}
	if user.ID == "" || user.ID[:5] != "acct_" {
		t.Fatalf("schema ID prefix not applied: %q", user.ID)
	}
	stored := repository.users[user.ID]
	if err := bcrypt.CompareHashAndPassword(
		[]byte(stored.PasswordHash),
		[]byte("secret12"),
	); err != nil {
		t.Fatalf("stored credential is not bcrypt: %v", err)
	}
	if !slices.Contains(stored.Roles, "superuser") {
		t.Fatalf("admin role not persisted: %#v", stored.Roles)
	}
	if len(audit.events) != 1 ||
		audit.events[0].Action != "user-created" ||
		audit.events[0].ResourceType != "account" ||
		audit.events[0].ResourceID != user.ID {
		t.Fatalf("unexpected create audit: %#v", audit.events)
	}
}

func TestUserServiceEnforcesConfiguredLimitsAndDuplicateUsername(t *testing.T) {
	repository := newMemoryUserRepository()
	service := newUserService(t, repository, &memoryAuditRecorder{})
	actor := Actor{ID: "admin-1", Username: "root"}

	_, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "a",
		Password: "secret12",
	})
	if !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("short username error = %v", err)
	}
	_, err = service.CreateUser(context.Background(), actor, CreateUser{
		Username: "alice",
		Password: "123",
	})
	if !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password error = %v", err)
	}
	if _, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "alice",
		Password: "secret12",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateUser(context.Background(), actor, CreateUser{
		Username: " alice ",
		Password: "different",
	})
	if !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("duplicate username error = %v", err)
	}
}

func TestUserServiceUpdatesRolesPasswordAndDeactivationAudit(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newUserService(t, repository, audit)
	user, err := service.CreateUser(context.Background(), Actor{
		ID: "admin-1",
	}, CreateUser{
		Username: "alice",
		Password: "secret12",
		Name:     "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	audit.events = nil
	name := " Alice Updated "
	email := " updated@example.test "
	admin := true
	active := false
	password := "new-secret"

	updated, err := service.UpdateUser(context.Background(), Actor{
		ID:       "admin-1",
		Username: "root",
	}, user.ID, UpdateUser{
		Name:     &name,
		Email:    &email,
		Admin:    &admin,
		Active:   &active,
		Password: &password,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Alice Updated" || updated.Nickname != "Alice Updated" ||
		updated.Email != "updated@example.test" || !updated.Admin ||
		updated.Active {
		t.Fatalf("unexpected update result: %+v", updated)
	}
	stored := repository.users[user.ID]
	if !slices.Contains(stored.Roles, "superuser") {
		t.Fatalf("admin role not stored: %#v", stored.Roles)
	}
	if err := bcrypt.CompareHashAndPassword(
		[]byte(stored.PasswordHash),
		[]byte(password),
	); err != nil {
		t.Fatalf("password not rotated: %v", err)
	}
	if len(audit.events) != 2 ||
		audit.events[0].Action != "user-updated" ||
		audit.events[1].Action != "user-disabled" {
		t.Fatalf("unexpected update audit sequence: %#v", audit.events)
	}
	if got := audit.events[0].Detail["login"]; got != "alice" {
		t.Fatalf("schema-configured username missing from update audit: %#v", audit.events[0].Detail)
	}
}

func TestUserServiceRejectsSelfDeactivationAndDeletion(t *testing.T) {
	repository := newMemoryUserRepository()
	service := newUserService(t, repository, &memoryAuditRecorder{})
	user, err := service.CreateUser(context.Background(), Actor{
		ID: "admin-1",
	}, CreateUser{
		Username: "alice",
		Password: "secret12",
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{ID: user.ID, Username: user.Username}
	inactive := false

	_, err = service.UpdateUser(
		context.Background(),
		actor,
		user.ID,
		UpdateUser{Active: &inactive},
	)
	if !errors.Is(err, ErrSelfDeactivate) {
		t.Fatalf("self deactivate error = %v", err)
	}
	err = service.DeleteUser(context.Background(), actor, user.ID)
	if !errors.Is(err, ErrSelfDelete) {
		t.Fatalf("self delete error = %v", err)
	}
}

func TestUserServiceListsOnlyLiveUsersAndSoftDeletes(t *testing.T) {
	repository := newMemoryUserRepository()
	audit := &memoryAuditRecorder{}
	service := newUserService(t, repository, audit)
	actor := Actor{ID: "admin-1", Username: "root"}
	first, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "alice",
		Password: "secret12",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateUser(context.Background(), actor, CreateUser{
		Username: "bob",
		Password: "secret12",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteUser(context.Background(), actor, first.ID); err != nil {
		t.Fatal(err)
	}
	users, err := service.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != second.ID {
		t.Fatalf("live users = %#v", users)
	}
	if !repository.users[first.ID].Deleted {
		t.Fatalf("user was not soft deleted: %+v", repository.users[first.ID])
	}
	last := audit.events[len(audit.events)-1]
	if last.Action != "user-deleted" || last.ResourceID != first.ID {
		t.Fatalf("unexpected delete audit: %+v", last)
	}
}

func newUserService(
	t *testing.T,
	repository UserRepository,
	audit AuditRecorder,
) *UserService {
	t.Helper()
	service, err := NewUserService(UserServiceConfig{
		Repository:                repository,
		Audit:                     audit,
		PasswordHashCost:          bcrypt.MinCost,
		UsernameMinimumCharacters: 2,
		PasswordMinimumCharacters: 6,
		IDPrefix:                  "acct_",
		DefaultRole:               "member",
		AdminRole:                 "superuser",
		DefaultActive:             true,
		AuditActions: UserAuditActions{
			Create:     "user-created",
			Update:     "user-updated",
			Deactivate: "user-disabled",
			Delete:     "user-deleted",
			Resource:   "account",
		},
		AuditFields: UserAuditFields{
			Username: "login",
			Admin:    "privileged",
			Active:   "enabled",
		},
		Clock: func() time.Time {
			return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type memoryUserRepository struct {
	users map[string]StoredUser
}

func newMemoryUserRepository() *memoryUserRepository {
	return &memoryUserRepository{users: map[string]StoredUser{}}
}

func (r *memoryUserRepository) ListAdministrationUsers(
	context.Context,
) ([]User, error) {
	result := make([]User, 0, len(r.users))
	for _, stored := range r.users {
		if !stored.Deleted {
			result = append(result, stored.User)
		}
	}
	return result, nil
}

func (r *memoryUserRepository) FindAdministrationUser(
	_ context.Context,
	id string,
) (StoredUser, bool, error) {
	user, exists := r.users[id]
	return user, exists, nil
}

func (r *memoryUserRepository) FindAdministrationUserByUsername(
	_ context.Context,
	username string,
) (StoredUser, bool, error) {
	for _, user := range r.users {
		if user.Username == username {
			return user, true, nil
		}
	}
	return StoredUser{}, false, nil
}

func (r *memoryUserRepository) CreateAdministrationUser(
	_ context.Context,
	user StoredUser,
) error {
	if _, exists, _ := r.FindAdministrationUserByUsername(
		context.Background(),
		user.Username,
	); exists {
		return ErrDuplicateUsername
	}
	r.users[user.ID] = user
	return nil
}

func (r *memoryUserRepository) UpdateAdministrationUser(
	_ context.Context,
	user StoredUser,
	guards UserGuards,
) error {
	if err := r.guardAdministratorRemoval(user.ID, user.Admin && user.Active, guards); err != nil {
		return err
	}
	r.users[user.ID] = user
	return nil
}

func (r *memoryUserRepository) SoftDeleteAdministrationUser(
	_ context.Context,
	id string,
	at time.Time,
	guards UserGuards,
) error {
	if err := r.guardAdministratorRemoval(id, false, guards); err != nil {
		return err
	}
	user, exists := r.users[id]
	if !exists || user.Deleted {
		return ErrUserNotFound
	}
	user.Deleted = true
	user.Active = false
	user.UpdatedAt = at
	r.users[id] = user
	return nil
}

func (r *memoryUserRepository) guardAdministratorRemoval(id string, retainsAdministrator bool, guards UserGuards) error {
	user, exists := r.users[id]
	if !exists || user.Deleted {
		return ErrUserNotFound
	}
	if !user.Admin || !user.Active || retainsAdministrator {
		return nil
	}
	remaining := 0
	for otherID, other := range r.users {
		if otherID != id && other.Admin && other.Active && !other.Deleted {
			remaining++
		}
	}
	if remaining < guards.MinimumActiveAdministrators {
		return ErrLastAdministrator
	}
	return nil
}

type memoryAuditRecorder struct {
	events []AuditEvent
}

func (r *memoryAuditRecorder) RecordAudit(
	_ context.Context,
	event AuditEvent,
) error {
	r.events = append(r.events, event)
	return nil
}

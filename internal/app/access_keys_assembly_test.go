package app

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type keyDirectory map[string]authentication.UserRecord

func (d keyDirectory) FindByID(_ context.Context, id string) (authentication.UserRecord, bool, error) {
	record, found := d[id]
	return record, found, nil
}

// #332: the assembly turns application.access_keys into the key service and the
// authenticator a separate listener uses. The authenticator resolves a key to
// the owner's scope and nothing else, and a Catalog without the section has
// neither.
func TestDefaultSchemaAssemblesAccessKeysAndAnOwnerScopedAuthenticator(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	policy, err := buildAccessPolicy(application.Authentication)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	users := keyDirectory{
		"admin-a": {ID: "admin-a", Username: "root", Roles: []string{application.Authentication.AdminRole}, Admin: true, Active: true},
	}
	service, authenticator, err := accessKeysFromSchema(repository, users, policy, application.AccessKeys, slog.New(slog.DiscardHandler))
	if err != nil || service == nil || authenticator == nil {
		t.Fatalf("service = %v authenticator = %v err=%v", service, authenticator, err)
	}
	created, err := service.Create(context.Background(), accesskeys.CreateRequest{
		OwnerID: "admin-a", Name: "admin key", Scopes: []string{"read", "generate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "http://mcp.test/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+created.Key)
	principal, err := authenticator.Identify(request)
	if err != nil {
		t.Fatal(err)
	}
	credential := principal.Credential()
	if principal.ID() != "admin-a" || credential.Kind != access.CredentialAccessKey || credential.KeyID != created.Record.ID ||
		!credential.HasScope("read") || !credential.HasScope("generate") {
		t.Fatalf("principal = %+v credential = %+v", principal, credential)
	}
	// The user is an administrator; the key is not.
	if principal.IsAdmin() || principal.Can(access.PermissionTasksReadAny) || !principal.Can(access.PermissionTasksReadOwn) {
		t.Fatal("an administrator's access key carries administrator permissions")
	}
	// The schema bounds reach the service: a ninety-day default, a ten-key limit.
	if created.Record.ExpiresAt.Sub(created.Record.CreatedAt).Hours() != 90*24 {
		t.Fatalf("default lifetime = %v", created.Record.ExpiresAt.Sub(created.Record.CreatedAt))
	}

	// A Catalog without the section has no key service and no authenticator.
	service, authenticator, err = accessKeysFromSchema(repository, users, policy, nil, nil)
	if err != nil || service != nil || authenticator != nil {
		t.Fatalf("without the section: %v %v err=%v", service, authenticator, err)
	}
	var unassembled Application
	if unassembled.AccessKeyAuthenticator() != nil {
		t.Fatal("an application without keys exposes an authenticator")
	}
}

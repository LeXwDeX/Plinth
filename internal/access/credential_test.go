package access_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
)

func credentialPolicy(t *testing.T) *access.Policy {
	t.Helper()
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "user", AdminRole: "admin",
		Roles: []access.RoleDefinition{
			{Key: "user", Permissions: []access.Permission{"tasks.read.own"}},
			{Key: "admin", Permissions: []access.Permission{"tasks.read.own", "tasks.read.any", "users.manage"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// A Principal no identity resolver marked has no credential kind: nothing may
// take it for a person at a browser.
func TestPrincipalWithoutAResolverHasNoCredentialKind(t *testing.T) {
	t.Parallel()
	principal := credentialPolicy(t).Principal(access.Claims{ID: "user-a"})
	if got := principal.Credential(); got.Kind != access.CredentialUnknown || got.KeyID != "" || len(got.Scopes) != 0 || got.HasScope("read") {
		t.Fatalf("credential = %+v", got)
	}
}

func TestWithCredentialMarksACopyAndKeepsThePermissions(t *testing.T) {
	t.Parallel()
	admin := credentialPolicy(t).Principal(access.Claims{ID: "admin-a", Roles: []string{"admin"}})
	marked := admin.WithCredential(access.Credential{Kind: access.CredentialBrowserSession})
	if marked.Credential().Kind != access.CredentialBrowserSession || admin.Credential().Kind != access.CredentialUnknown {
		t.Fatal("WithCredential changed the receiver or did not mark the copy")
	}
	if !marked.IsAdmin() || !marked.Can("users.manage") || marked.ID() != "admin-a" {
		t.Fatalf("marking a principal changed what it may do: %+v", marked)
	}
	// Scopes are copied in and out.
	scopes := []string{"read"}
	keyed := admin.WithCredential(access.Credential{Kind: access.CredentialAccessKey, KeyID: "k1", Scopes: scopes})
	scopes[0] = "mutated"
	if got := keyed.Credential(); got.Scopes[0] != "read" {
		t.Fatal("WithCredential kept the caller's slice")
	}
	got := keyed.Credential()
	got.Scopes[0] = "mutated"
	if keyed.Credential().Scopes[0] != "read" {
		t.Fatal("Credential returned the principal's own slice")
	}
}

// Only an access key has scopes.
func TestOnlyAnAccessKeyHasScopes(t *testing.T) {
	t.Parallel()
	for _, kind := range []access.CredentialKind{access.CredentialBrowserSession, access.CredentialDeploymentToken, access.CredentialDevelopment} {
		if (access.Credential{Kind: kind, Scopes: []string{"read"}}).HasScope("read") {
			t.Errorf("%s has a scope", kind)
		}
	}
	key := access.Credential{Kind: access.CredentialAccessKey, Scopes: []string{"read"}}
	if !key.HasScope("read") || key.HasScope("generate") {
		t.Fatalf("key scopes = %+v", key)
	}
}

// The principal of an access key is the owner's scope and nothing more: the
// roles of the claims never travel with a key, so an administrator's key has no
// administrator permissions, and the identity is still the owner's.
func TestAccessKeyPrincipalCarriesOnlyTheOwnersScope(t *testing.T) {
	t.Parallel()
	policy := credentialPolicy(t)
	adminClaims := access.Claims{ID: "admin-a", Name: "Root", Email: "root@example.test", Roles: []string{"admin", "user"}}
	principal := policy.AccessKeyPrincipal(adminClaims, "key-1", []string{"read", "generate"})
	if principal.ID() != "admin-a" || principal.Claims.Name != "Root" || principal.Claims.Email != "root@example.test" {
		t.Fatalf("identity = %+v", principal.Claims)
	}
	if principal.IsAdmin() || principal.Can("tasks.read.any") || principal.Can("users.manage") || !principal.Can("tasks.read.own") {
		t.Fatalf("an administrator's key has administrator permissions: admin=%v", principal.IsAdmin())
	}
	for _, role := range principal.Claims.Roles {
		if role == "admin" {
			t.Fatalf("roles = %v", principal.Claims.Roles)
		}
	}
	credential := principal.Credential()
	if credential.Kind != access.CredentialAccessKey || credential.KeyID != "key-1" || !credential.HasScope("read") || !credential.HasScope("generate") {
		t.Fatalf("credential = %+v", credential)
	}
	// The claims passed in were not edited.
	if len(adminClaims.Roles) != 2 || adminClaims.Roles[0] != "admin" {
		t.Fatalf("the caller's claims were changed: %v", adminClaims.Roles)
	}
	// A policy whose default role is the administrator role (a misconfiguration)
	// still never makes a key an administrator or lets it read across owners.
	odd, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "admin", AdminRole: "admin",
		Roles: []access.RoleDefinition{{Key: "admin", Permissions: []access.Permission{"tasks.read.own", "tasks.read.any"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if key := odd.AccessKeyPrincipal(access.Claims{ID: "u"}, "key-2", []string{"read"}); key.IsAdmin() || key.Can("tasks.read.any") {
		t.Fatal("a key became an administrator under a policy whose default role is the admin role")
	}
	// A plain principal of the same policy is one.
	if plain := odd.Principal(access.Claims{ID: "u"}); !plain.IsAdmin() || !plain.Can("tasks.read.any") {
		t.Fatal("the fixture is wrong: a plain principal should be an administrator here")
	}
}

package httpapi_test

import (
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

// #332: each identity resolver of the main server says what it authenticated
// with, and none of them is an access key.
func TestMainServerIdentityResolversMarkTheirCredentialKind(t *testing.T) {
	t.Parallel()
	policy, err := access.NewPolicy(access.PolicyConfig{
		DefaultRole: "user", AdminRole: "admin",
		Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	development := httpapi.DevelopmentIdentity{DefaultClaims: access.Claims{ID: "dev-user"}, Policy: policy}
	principal, err := development.Identify(httptest.NewRequest("GET", "/", nil))
	if err != nil || principal.Credential().Kind != access.CredentialDevelopment {
		t.Fatalf("development = %+v err=%v", principal.Credential(), err)
	}

	bearer, err := httpapi.NewBearerIdentity(map[string]access.Claims{deployToken: {ID: "deployment-owner"}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Authorization", "Bearer "+deployToken)
	principal, err = bearer.Identify(request)
	if err != nil || principal.ID() != "deployment-owner" || principal.Credential().Kind != access.CredentialDeploymentToken {
		t.Fatalf("deployment token = %+v err=%v", principal.Credential(), err)
	}
	// A string shaped like an access key is only an unknown Bearer token here.
	request.Header.Set("Authorization", "Bearer plk_aaaaaaaaaaaaaaaa_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := bearer.Identify(request); err == nil {
		t.Fatal("the deployment identity accepted an access key shaped token")
	}
}

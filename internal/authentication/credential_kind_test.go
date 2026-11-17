package authentication_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/authentication"
)

type fixedFallback struct{ principal access.Principal }

func (f fixedFallback) Identify(*http.Request) (access.Principal, error) { return f.principal, nil }

// #332: the principal says how it was authenticated. A signed-in cookie is a
// browser session; the non-production mock user, which needs no cookie, is the
// development identity; whatever the fallback returns keeps its own kind.
func TestIdentifyMarksTheCredentialKind(t *testing.T) {
	schema := authenticationSchema(t)
	record := authentication.UserRecord{ID: "user-a", Name: "User A", Email: "a@example.test", Active: true}
	directory := &memoryDirectory{byID: map[string]authentication.UserRecord{record.ID: record}}
	policy := testPolicy(t)
	fallback := fixedFallback{principal: policy.Principal(access.Claims{ID: "deployment-owner"}).
		WithCredential(access.Credential{Kind: access.CredentialDeploymentToken})}

	service := newService(t, authentication.Config{
		Schema: schema, SessionSecret: strings.Repeat("k", 32), Environment: "test",
		Policy: policy, Directory: directory, Fallback: fallback,
		MockEnabled: true, MockUserJSON: `{"id":"mock-user","name":"Mock","email":"mock@example.test"}`,
	})
	writer := httptest.NewRecorder()
	if err := service.IssueSession(writer, httptest.NewRequest(http.MethodPost, "/login", nil),
		authentication.User{ID: record.ID, Name: record.Name, Email: record.Email}); err != nil {
		t.Fatal(err)
	}
	withCookie := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	withCookie.AddCookie(writer.Result().Cookies()[0])
	principal, err := service.Identify(withCookie)
	if err != nil || principal.ID() != "user-a" || principal.Credential().Kind != access.CredentialBrowserSession {
		t.Fatalf("session principal = %+v credential = %+v err=%v", principal, principal.Credential(), err)
	}

	// No cookie, mock user enabled: a development convenience, not a browser.
	mock, err := service.Identify(httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if err != nil || mock.ID() != "mock-user" || mock.Credential().Kind != access.CredentialDevelopment {
		t.Fatalf("mock principal = %+v credential = %+v err=%v", mock, mock.Credential(), err)
	}

	// Without the mock user the request falls to the fallback and keeps its kind.
	plain := newService(t, authentication.Config{
		Schema: schema, SessionSecret: strings.Repeat("k", 32), Environment: "test",
		Policy: policy, Directory: directory, Fallback: fallback,
	})
	token, err := plain.Identify(httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if err != nil || token.ID() != "deployment-owner" || token.Credential().Kind != access.CredentialDeploymentToken {
		t.Fatalf("fallback principal = %+v credential = %+v err=%v", token, token.Credential(), err)
	}
}

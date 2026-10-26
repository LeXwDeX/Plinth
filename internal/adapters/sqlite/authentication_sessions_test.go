package sqlite_test

import (
	"context"
	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionRevocationAndExpirySurviveRepositoryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	record := authentication.UserRecord{ID: "user", Name: "User", Username: "user", Email: "user@example.test", Active: true}
	if err := repo.CreateBootstrapAdmin(context.Background(), record, now); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := cat.Application().Authentication
	schema.Cookie.TTLSeconds = 60
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	newService := func() *authentication.Service {
		s, err := authentication.New(authentication.Config{Schema: schema, SessionSecret: strings.Repeat("s", 32), Environment: "production", Directory: repo, Policy: policy, Clock: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	service := newService()
	issue := func() *http.Request {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		if err := service.IssueSession(w, r, authentication.User{ID: record.ID, Name: record.Name, Email: record.Email}); err != nil {
			t.Fatal(err)
		}
		r.AddCookie(w.Result().Cookies()[0])
		return r
	}
	first, other := issue(), issue()
	if err := service.RevokeSession(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service = newService()
	if _, ok, err := service.CurrentUser(context.Background(), first); err != nil || ok {
		t.Fatalf("revoked session survived reopen: %v %v", ok, err)
	}
	if _, ok, err := service.CurrentUser(context.Background(), other); err != nil || !ok {
		t.Fatalf("unrelated session lost: %v %v", ok, err)
	}
	now = now.Add(time.Minute)
	if _, ok, err := service.CurrentUser(context.Background(), other); err != nil || ok {
		t.Fatalf("expired session accepted: %v %v", ok, err)
	}
}

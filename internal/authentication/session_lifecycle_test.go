package authentication_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSessionRejectsExpiredCookie(t *testing.T) {
	schema := authenticationSchema(t)
	schema.Cookie.TTLSeconds = 1
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	record := authentication.UserRecord{ID: "probe-user", Username: "probe", Name: "Probe", Email: "probe@example.test", Roles: []string{"admin"}, Admin: true, Active: true}
	directory := &memoryDirectory{byID: map[string]authentication.UserRecord{record.ID: record}}
	service := newService(t, authentication.Config{Schema: schema, SessionSecret: strings.Repeat("s", 32), Environment: "test", Policy: testPolicy(t), Directory: directory, Clock: func() time.Time { return now }})
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	e := service.IssueSession(writer, request, authentication.User{ID: record.ID, Name: record.Name, Email: record.Email, Roles: record.Roles})
	if e != nil {
		t.Fatal(e)
	}
	request = httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(writer.Result().Cookies()[0])
	now = now.AddDate(5, 0, 0)
	service.ClearSession(httptest.NewRecorder())
	delete(directory.byID, record.ID)
	_, exists, e := service.CurrentUser(context.Background(), request)
	if e != nil || exists {
		t.Fatalf("expired/removed identity accepted: exists=%v err=%v", exists, e)
	}
}

func TestSessionRevokesOnlyOneCookieAndReloadsDirectoryIdentity(t *testing.T) {
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	record := authentication.UserRecord{ID: "user", Name: "User", Email: "user@example.test", Roles: []string{"admin"}, Active: true}
	directory := &memoryDirectory{byID: map[string]authentication.UserRecord{record.ID: record}}
	service := newService(t, authentication.Config{Schema: authenticationSchema(t), SessionSecret: strings.Repeat("x", 32), Environment: "test", Policy: testPolicy(t), Directory: directory, Clock: func() time.Time { return now }})
	issue := func() *http.Request {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		if err := service.IssueSession(w, r, authentication.User{ID: record.ID, Name: record.Name, Email: record.Email, Roles: record.Roles}); err != nil {
			t.Fatal(err)
		}
		r.AddCookie(w.Result().Cookies()[0])
		return r
	}
	a, b := issue(), issue()
	if err := service.RevokeSession(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeSession(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.CurrentUser(context.Background(), a); err != nil || ok {
		t.Fatalf("revoked cookie: %v %v", ok, err)
	}
	if _, ok, err := service.CurrentUser(context.Background(), b); err != nil || !ok {
		t.Fatalf("other cookie: %v %v", ok, err)
	}
	record.Roles = []string{"user"}
	directory.byID[record.ID] = record
	user, ok, err := service.CurrentUser(context.Background(), b)
	if err != nil || !ok || len(user.Roles) != 1 || user.Roles[0] != "user" {
		t.Fatalf("stale role: %+v %v %v", user, ok, err)
	}
	if admin, err := service.IsAdmin(context.Background(), authentication.User{ID: record.ID, Roles: []string{"admin"}}); err != nil || admin {
		t.Fatalf("stale admin: %v %v", admin, err)
	}
	delete(directory.byID, record.ID)
	if _, ok, err := service.CurrentUser(context.Background(), b); err != nil || ok {
		t.Fatalf("removed identity: %v %v", ok, err)
	}
}

type unavailableDirectory struct{ memoryDirectory }

func (*unavailableDirectory) FindByID(context.Context, string) (authentication.UserRecord, bool, error) {
	return authentication.UserRecord{}, false, errors.New("directory unavailable")
}
func TestAdministratorFailsClosedWhenDirectoryUnavailable(t *testing.T) {
	service := newService(t, authentication.Config{Schema: authenticationSchema(t), SessionSecret: strings.Repeat("x", 32), Environment: "test", Policy: testPolicy(t), Directory: &unavailableDirectory{}, AdminAllowlist: []string{"admin@example.test"}})
	if admin, err := service.IsAdmin(context.Background(), authentication.User{ID: "admin", Email: "admin@example.test", Roles: []string{"admin"}}); err == nil || admin {
		t.Fatalf("directory outage grants admin: %v %v", admin, err)
	}
}

func TestLegacySessionHasFixedCutoffAndDurableRevocation(t *testing.T) {
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	cutoff := now.Add(time.Hour)
	schema := authenticationSchema(t)
	schema.Session.LegacyAcceptUntil = cutoff.Format(time.RFC3339)
	directory := &memoryDirectory{byID: map[string]authentication.UserRecord{"old": {ID: "old", Name: "Old", Email: "old@example.test", Active: true}}}
	secret := strings.Repeat("l", 32)
	service := newService(t, authentication.Config{Schema: schema, SessionSecret: secret, Environment: "test", Policy: testPolicy(t), Directory: directory, Clock: func() time.Time { return now }})
	payload := `{"id":"old","name":"Old","email":"old@example.test"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signed := payload + "." + hex.EncodeToString(mac.Sum(nil))
	request := httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(&http.Cookie{Name: schema.Cookie.Name, Value: url.QueryEscape(signed)})
	if _, ok, err := service.CurrentUser(context.Background(), request); err != nil || !ok {
		t.Fatalf("legacy before cutoff: %v %v", ok, err)
	}
	if err := service.RevokeSession(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := service.CurrentUser(context.Background(), request); err != nil || ok {
		t.Fatalf("legacy revoked: %v %v", ok, err)
	}
	now = cutoff
	restarted := newService(t, authentication.Config{Schema: schema, SessionSecret: secret, Environment: "test", Policy: testPolicy(t), Directory: directory, Clock: func() time.Time { return now }})
	if _, ok, err := restarted.CurrentUser(context.Background(), request); err != nil || ok {
		t.Fatalf("legacy cutoff reset after restart: %v %v", ok, err)
	}
}

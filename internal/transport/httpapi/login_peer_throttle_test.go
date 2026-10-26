package httpapi_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginThrottleBindsToPeerAcrossConnectionsAndForwardingHeaders(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	threshold := taskCatalog.Application().Authentication.API.LoginMaxAttemptsPerMinute
	for attempt := 0; attempt <= threshold; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(
			`{"username":"missing-account","password":"wrong"}`,
		))
		request.RemoteAddr = fmt.Sprintf("203.0.113.7:%d", 40000+attempt)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d, 10.0.0.5", attempt+1))
		request.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", attempt+1))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == threshold {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status=%d want=%d body=%s", attempt, response.Code, want, response.Body)
		}
	}
	// Other peer IPs and account names retain independent budgets.
	for _, tc := range []struct{ peer, account string }{
		{"203.0.113.8:40000", "missing-account"},
		{"203.0.113.7:50000", "another-account"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(
			fmt.Sprintf(`{"username":%q,"password":"wrong"}`, tc.account),
		))
		request.RemoteAddr = tc.peer
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("independent peer/account status=%d body=%s", response.Code, response.Body)
		}
	}
}

func TestCurrentPasswordThrottleUsesStableAccountIdentity(t *testing.T) {
	t.Parallel()
	hash, err := bcrypt.GenerateFromPassword([]byte("known-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	record := authentication.UserRecord{
		ID: "password-throttle-user", Username: "password-throttle-user", PasswordHash: string(hash),
		Nickname: "original display name", Email: "password-throttle@example.test", Active: true,
	}
	directory := &passwordDirectory{
		authDirectory: &authDirectory{
			byUsername: map[string]authentication.UserRecord{record.Username: record},
			byID:       map[string]authentication.UserRecord{record.ID: record},
		},
		passwords: map[string]string{},
	}
	application, handler := newPasswordHarness(t, directory)
	cookie := loginForPasswordTest(t, handler, record.Username, "known-test-password")
	threshold := application.Authentication.API.LoginMaxAttemptsPerMinute
	for attempt := 0; attempt <= threshold; attempt++ {
		if attempt == threshold {
			record.Nickname = "changed display name"
			directory.byID[record.ID] = record
			directory.byUsername[record.Username] = record
		}
		request := httptest.NewRequest(http.MethodPost, application.Runtime.HTTP.LegacyRoutes.AuthPassword,
			bytes.NewBufferString(`{"currentPassword":"wrong","newPassword":"new-test-password"}`))
		request.RemoteAddr = fmt.Sprintf("203.0.113.31:%d", 40000+attempt)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://example.com")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", attempt+1))
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == threshold {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("password guess %d status=%d want=%d body=%s", attempt, response.Code, want, response.Body)
		}
	}
	// The login bucket remains independent of rejected password-change guesses.
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(
		fmt.Sprintf(`{"username":%q,"password":"known-test-password"}`, record.Username),
	))
	request.RemoteAddr = "203.0.113.31:51000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login after password-change lockout status=%d body=%s", response.Code, response.Body)
	}
}

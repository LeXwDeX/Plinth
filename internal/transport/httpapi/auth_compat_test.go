package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type authDirectory struct {
	byUsername map[string]authentication.UserRecord
	byID       map[string]authentication.UserRecord
	lastLogin  time.Time
}

func (a *authDirectory) FindActiveByUsername(
	_ context.Context,
	username string,
) (authentication.UserRecord, bool, error) {
	record, exists := a.byUsername[username]
	if !exists || !record.Active || record.Deleted {
		return authentication.UserRecord{}, false, nil
	}
	return record, true, nil
}

func (a *authDirectory) FindByID(
	_ context.Context,
	id string,
) (authentication.UserRecord, bool, error) {
	record, exists := a.byID[id]
	return record, exists, nil
}

func (a *authDirectory) UpdateLastLogin(
	_ context.Context,
	_ string,
	at time.Time,
) error {
	a.lastLogin = at
	return nil
}

func TestLegacyLoginAndSessionMatchNextContract(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 18, 0, 0, 0, time.UTC)
	record := authentication.UserRecord{
		ID: "login-user", Username: "schema-admin",
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "登录用户", Email: "login@example.test",
		Admin: true, Active: true,
	}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{
			record.Username: record,
		},
		byID: map[string]authentication.UserRecord{record.ID: record},
	}
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.Directory = directory
			config.SessionSecret = strings.Repeat("l", 32)
			config.Clock = func() time.Time { return now }
		},
	)

	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{}`,
		http.StatusBadRequest,
		map[string]any{"error": "请输入用户名和密码"},
	)
	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/login",
		`{"username":"schema-admin","password":"wrong"}`,
		http.StatusUnauthorized,
		map[string]any{"error": "用户名或密码错误"},
	)

	login := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		bytes.NewBufferString(
			`{"username":"schema-admin","password":"correct-horse"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
	request.Header.Set("User-Agent", "schema-auth-test")
	handler.ServeHTTP(login, request)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body)
	}
	// #134: this request is plain HTTP on a non-loopback host, where browsers
	// reject Clear-Site-Data; see TestSessionChangeClearSiteDataNeedsSecureContext.
	if got := login.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("login Clear-Site-Data over plain HTTP = %q", got)
	}

	assertJSONBody(t, login.Body.Bytes(), map[string]any{
		"success": true,
		"user": map[string]any{
			"id":    "login-user",
			"name":  "登录用户",
			"email": "login@example.test",
		},
	})
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "meshyai_session" {
		t.Fatalf("login cookies = %#v", cookies)
	}
	if !directory.lastLogin.Equal(now) {
		t.Fatalf("last login = %s", directory.lastLogin)
	}
	assertPersistedAuthenticationAudit(
		t,
		handler,
		cookies[0],
		"login",
		"login-user",
		"schema-admin",
		"203.0.113.9, 10.0.0.5",
		"schema-auth-test",
	)

	session := httptest.NewRecorder()
	sessionRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/auth/session",
		nil,
	)
	sessionRequest.AddCookie(cookies[0])
	handler.ServeHTTP(session, sessionRequest)
	if session.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", session.Code, session.Body)
	}
	assertJSONBody(t, session.Body.Bytes(), map[string]any{
		"user": map[string]any{
			"id":    "login-user",
			"name":  "登录用户",
			"email": "login@example.test",
		},
		"isAdmin": true,
	})

	logout := httptest.NewRecorder()
	logoutRequest := httptest.NewRequest(
		http.MethodDelete,
		"/api/auth/session",
		nil,
	)
	logoutRequest.AddCookie(cookies[0])
	logoutRequest.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", logout.Code, logout.Body)
	}
	if got := logout.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("logout Clear-Site-Data over plain HTTP = %q", got)
	}
	assertJSONBody(t, logout.Body.Bytes(), map[string]any{"success": true})
	cleared := logout.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != "meshyai_session" ||
		cleared[0].MaxAge >= 0 {
		t.Fatalf("logout cookies = %#v", cleared)
	}
	// A saved cookie cannot authenticate after logout. Inspect the audit using
	// a separate login, as an administrator would after revoking this session.
	replay := administrationRequest(handler, http.MethodGet, "/api/admin/audit-logs", "", cookies[0])
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie accepted: %d", replay.Code)
	}
	relogin := administrationRequest(handler, http.MethodPost, "/api/auth/login", `{"username":"schema-admin","password":"correct-horse"}`, nil)
	if relogin.Code != http.StatusOK {
		t.Fatalf("relogin: %d %s", relogin.Code, relogin.Body)
	}
	assertPersistedAuthenticationAudit(
		t,
		handler,
		relogin.Result().Cookies()[0],
		"logout",
		"login-user",
		"登录用户",
		"",
		"",
	)
}

func assertPersistedAuthenticationAudit(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	action string,
	resourceID string,
	userName string,
	ipAddress string,
	userAgent string,
) {
	t.Helper()
	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action="+action,
		"",
		cookie,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["total"] != float64(1) {
		t.Fatalf("%s audit body=%#v", action, body)
	}
	events := body["data"].([]any)
	event := events[0].(map[string]any)
	if event["action"] != action ||
		event["userId"] != "login-user" ||
		event["userName"] != userName ||
		event["resourceType"] != "user" ||
		event["resourceId"] != resourceID ||
		event["ipAddress"] != ipAddress ||
		event["userAgent"] != userAgent {
		t.Fatalf("%s audit event=%#v", action, event)
	}
}

func TestLegacyMockAuthMatchesNextContract(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1785061800123)
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.SessionSecret = strings.Repeat("m", 32)
			config.MockEnabled = true
			config.MockUserJSON =
				`{"id":"env-user","name":"Env User","email":"env@example.test"}`
			config.Clock = func() time.Time { return now }
		},
	)
	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/mock",
		`{"name":"Body User","email":"body@example.test"}`,
		http.StatusOK,
		map[string]any{"user": map[string]any{
			"id":    "mock_1785061800123",
			"name":  "Body User",
			"email": "body@example.test",
		}},
	)
	// #225: a mock login is a session change too (header rules: #134, see
	// TestSessionChangeClearSiteDataNeedsSecureContext).
	mockLogin := httptest.NewRecorder()
	handler.ServeHTTP(mockLogin, httptest.NewRequest(http.MethodPost, "https://example.com/api/auth/mock", strings.NewReader(`{"name":"Body User","email":"body@example.test"}`)))
	if got := mockLogin.Header().Get("Clear-Site-Data"); mockLogin.Code != http.StatusOK || got != `"cache"` {
		t.Fatalf("mock login status = %d, Clear-Site-Data = %q", mockLogin.Code, got)
	}
	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/auth/mock",
		`not-json`,
		http.StatusOK,
		map[string]any{"user": map[string]any{
			"id":    "env-user",
			"name":  "Env User",
			"email": "env@example.test",
		}},
	)

	_, disabled, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.SessionSecret = strings.Repeat("d", 32)
			config.MockEnabled = false
		},
	)
	assertJSONResponse(
		t,
		disabled,
		http.MethodPost,
		"/api/auth/mock",
		`{}`,
		http.StatusForbidden,
		map[string]any{"error": "Mock auth is disabled"},
	)
	for _, test := range []struct {
		name    string
		rawUser string
		status  int
		message string
	}{
		{
			name:    "missing",
			status:  http.StatusBadRequest,
			message: "No user info provided and DEV_MOCK_USER is not set",
		},
		{
			name:    "invalid",
			rawUser: `not-json`,
			status:  http.StatusInternalServerError,
			message: "DEV_MOCK_USER is not valid JSON",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, fallbackFailure, _ := newHTTPHarnessWithAuthentication(
				t,
				[]string{"user"},
				nil,
				func(config *authentication.Config) {
					config.SessionSecret = strings.Repeat("f", 32)
					config.MockEnabled = true
					config.MockUserJSON = test.rawUser
				},
			)
			assertJSONResponse(
				t,
				fallbackFailure,
				http.MethodPost,
				"/api/auth/mock",
				`{}`,
				test.status,
				map[string]any{"error": test.message},
			)
		})
	}
}

func TestAuthenticationHTTPWireChangesWithProductSchema(t *testing.T) {
	t.Parallel()
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{},
		byID:       map[string]authentication.UserRecord{},
	}
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		func(schema *catalog.ApplicationSchema) {
			schema.Runtime.HTTP.LegacyRoutes.AuthLogin = "/api/schema/login"
			schema.Authentication.API.UsernameField = "account"
			schema.Authentication.API.PasswordField = "credential"
			schema.Authentication.API.ErrorField = "problem"
			schema.Authentication.Messages.MissingCredentials = "schema-missing"
		},
		func(config *authentication.Config) {
			config.Directory = directory
			config.SessionSecret = strings.Repeat("s", 32)
		},
	)
	assertJSONResponse(
		t,
		handler,
		http.MethodPost,
		"/api/schema/login",
		`{"account":"","credential":""}`,
		http.StatusBadRequest,
		map[string]any{"problem": "schema-missing"},
	)
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(
		notFound,
		httptest.NewRequest(http.MethodPost, "/api/auth/login", nil),
	)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("old route status = %d", notFound.Code)
	}
}

func assertJSONResponse(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body string,
	status int,
	expected map[string]any,
) {
	t.Helper()
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(writer, request)
	if writer.Code != status {
		t.Fatalf(
			"%s %s status = %d, body = %s",
			method,
			path,
			writer.Code,
			writer.Body,
		)
	}
	assertJSONBody(t, writer.Body.Bytes(), expected)
}

func assertJSONBody(t *testing.T, body []byte, expected map[string]any) {
	t.Helper()
	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	// #316: error bodies also name the access log's request ID; its value is
	// per request, so the wire comparison leaves it out (see
	// TestCompatibilityErrorBodiesCarryTheRequestID).
	if id, ok := actual["request_id"].(string); ok && strings.HasPrefix(id, "req_") {
		if _, wanted := expected["request_id"]; !wanted {
			delete(actual, "request_id")
		}
	}
	expectedJSON, _ := json.Marshal(expected)
	actualJSON, _ := json.Marshal(actual)
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("body = %s, expected %s", actualJSON, expectedJSON)
	}
}

// #134: browsers act on Clear-Site-Data only in a secure context and log an error
// otherwise, so the header goes out for HTTPS and loopback hosts and not for plain
// HTTP on an IP address (a dev-test style deployment).
func TestSessionChangeClearSiteDataNeedsSecureContext(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t,
		[]string{"user"},
		nil,
		func(config *authentication.Config) {
			config.SessionSecret = strings.Repeat("s", 32)
			config.MockEnabled = true
			config.MockUserJSON =
				`{"id":"env-user","name":"Env User","email":"env@example.test"}`
		},
	)
	for _, test := range []struct {
		name, target, want string
	}{
		{"HTTPS", "https://203.0.113.5/api/auth/mock", `"cache"`},
		{"plain HTTP on an IP address", "http://203.0.113.5:8080/api/auth/mock", ""},
		{"localhost", "http://localhost:3000/api/auth/mock", `"cache"`},
		{"127.0.0.1", "http://127.0.0.1:8080/api/auth/mock", `"cache"`},
		{"[::1]", "http://[::1]:8080/api/auth/mock", `"cache"`},
	} {
		t.Run("login "+test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(`{}`)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
			}
			if got := recorder.Header().Get("Clear-Site-Data"); got != test.want {
				t.Fatalf("Clear-Site-Data = %q, want %q", got, test.want)
			}
		})
		t.Run("logout "+test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodDelete, strings.Replace(test.target, "/auth/mock", "/auth/session", 1), nil)
			request.Header.Set("Origin", request.URL.Scheme+"://"+request.Host)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
			}
			if got := recorder.Header().Get("Clear-Site-Data"); got != test.want {
				t.Fatalf("Clear-Site-Data = %q, want %q", got, test.want)
			}
		})
	}
}

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

const (
	accessKeysPath = "/api/v1/account/access-keys"
	deployToken    = "deployment-token-0123456789-abcdef"
)

type accessKeyBody struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
	RevokedAt  *string  `json:"revoked_at"`
	Active     bool     `json:"active"`
	Key        string   `json:"key"`
}

func decodeAccessKey(t *testing.T, response *httptest.ResponseRecorder) accessKeyBody {
	t.Helper()
	var body accessKeyBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("access key body %q: %v", response.Body.String(), err)
	}
	return body
}

func decodeAccessKeyList(t *testing.T, response *httptest.ResponseRecorder) []accessKeyBody {
	t.Helper()
	var body struct {
		Data []accessKeyBody `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("access key list %q: %v", response.Body.String(), err)
	}
	return body.Data
}

// accessKeyHarness is a signed-in browser of owner-a on the shipped Catalog.
func accessKeyHarness(t *testing.T) (http.Handler, *http.Cookie, *sqlite.Repository) {
	t.Helper()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	return handler, cookie, value.(*fixtureExecutionState).repository
}

func createAccessKey(t *testing.T, handler http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return grantTestRequest(t, handler, cookie, http.MethodPost, accessKeysPath, body, "")
}

func TestAccountAccessKeyIsIssuedOnceAndNeverReadBack(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := accessKeyHarness(t)
	response := createAccessKey(t, handler, cookie, `{"name":"  build bot ","scopes":["read","generate"],"ttl_days":30}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	created := decodeAccessKey(t, response)
	if !strings.HasPrefix(created.Key, "plk_") || len(created.Key) != 73 || created.Name != "build bot" ||
		created.Prefix != "plk_"+created.ID || !strings.HasPrefix(created.Key, created.Prefix+"_") ||
		strings.Join(created.Scopes, ",") != "read,generate" || !created.Active || created.LastUsedAt != nil || created.RevokedAt != nil {
		t.Fatalf("created = %+v", created)
	}
	expires, err := time.Parse(time.RFC3339Nano, *created.ExpiresAt)
	if err != nil || expires.Before(time.Now().Add(29*24*time.Hour)) || expires.After(time.Now().Add(31*24*time.Hour)) {
		t.Fatalf("expires_at = %v err=%v; want about 30 days from now", created.ExpiresAt, err)
	}
	if cache := response.Header().Get("Cache-Control"); !strings.Contains(cache, "no-store") || !strings.Contains(cache, "private") {
		t.Fatalf("Cache-Control = %q", cache)
	}

	// The list shows the key by its prefix, never its plaintext or digest.
	list := grantTestRequest(t, handler, cookie, http.MethodGet, accessKeysPath, "", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	secret := created.Key[len(created.Prefix)+1:]
	for _, forbidden := range []string{secret, created.Key, "secret_hash", "secret"} {
		if strings.Contains(list.Body.String(), forbidden) {
			t.Fatalf("the list contains %q: %s", forbidden, list.Body.String())
		}
	}
	keys := decodeAccessKeyList(t, list)
	if len(keys) != 1 || keys[0].ID != created.ID || keys[0].Prefix != created.Prefix || keys[0].Key != "" || !keys[0].Active {
		t.Fatalf("list = %+v", keys)
	}
	// What the server stored is the digest only.
	stored, found, err := repository.AccessKey(context.Background(), created.ID)
	if err != nil || !found || stored.OwnerID != "owner-a" || len(stored.SecretHash) != 64 || strings.Contains(stored.SecretHash, secret) {
		t.Fatalf("stored = %+v found=%v err=%v", stored, found, err)
	}
	if cache := list.Header().Get("Cache-Control"); !strings.Contains(cache, "private") {
		t.Fatalf("list Cache-Control = %q", cache)
	}
}

func TestAccountAccessKeyDefaultLifetimeAndScopeLabelsFollowTheSchema(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := accessKeyHarness(t)
	response := createAccessKey(t, handler, cookie, `{"name":"default","scopes":["read"]}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	created := decodeAccessKey(t, response)
	expires, err := time.Parse(time.RFC3339Nano, *created.ExpiresAt)
	if err != nil || expires.Before(time.Now().Add(89*24*time.Hour)) || expires.After(time.Now().Add(91*24*time.Hour)) {
		t.Fatalf("expires_at = %v err=%v; want the 90-day default", created.ExpiresAt, err)
	}
}

func TestAccountAccessKeyRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := accessKeyHarness(t)
	for name, body := range map[string]string{
		"no name":           `{"scopes":["read"]}`,
		"blank name":        `{"name":"   ","scopes":["read"]}`,
		"long name":         `{"name":"` + strings.Repeat("n", 81) + `","scopes":["read"]}`,
		"no scopes":         `{"name":"k"}`,
		"empty scopes":      `{"name":"k","scopes":[]}`,
		"unknown scope":     `{"name":"k","scopes":["read","admin"]}`,
		"zero lifetime":     `{"name":"k","scopes":["read"],"ttl_days":0}`,
		"negative lifetime": `{"name":"k","scopes":["read"],"ttl_days":-5}`,
		"over the maximum":  `{"name":"k","scopes":["read"],"ttl_days":366}`,
		"fractional days":   `{"name":"k","scopes":["read"],"ttl_days":1.5}`,
		"days as text":      `{"name":"k","scopes":["read"],"ttl_days":"30"}`,
		"unknown field":     `{"name":"k","scopes":["read"],"owner_id":"owner-b"}`,
		"admin flag":        `{"name":"k","scopes":["read"],"admin":true}`,
		"trailing json":     `{"name":"k","scopes":["read"]} {}`,
		"not json":          `not json`,
		"empty body":        ``,
	} {
		if response := createAccessKey(t, handler, cookie, body); response.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, response.Code, response.Body.String())
		}
	}
	if keys := decodeAccessKeyList(t, grantTestRequest(t, handler, cookie, http.MethodGet, accessKeysPath, "", "")); len(keys) != 0 {
		t.Fatalf("a refused request stored a key: %+v", keys)
	}
}

func TestAccountAccessKeyLimitRefusesTheNextKeyWithTheSchemaMessage(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := accessKeyHarness(t)
	var first accessKeyBody
	for index := range 10 {
		response := createAccessKey(t, handler, cookie, fmt.Sprintf(`{"name":"key %d","scopes":["read"]}`, index))
		if response.Code != http.StatusCreated {
			t.Fatalf("key %d = %d %s", index, response.Code, response.Body.String())
		}
		if index == 0 {
			first = decodeAccessKey(t, response)
		}
	}
	refused := createAccessKey(t, handler, cookie, `{"name":"one too many","scopes":["read"]}`)
	if refused.Code != http.StatusConflict {
		t.Fatalf("eleventh key = %d %s", refused.Code, refused.Body.String())
	}
	var problem struct {
		Code      string `json:"code"`
		Detail    string `json:"detail"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &problem); err != nil || problem.Code != "access_key_limit" ||
		!strings.Contains(problem.Detail, "10") || strings.Contains(problem.Detail, "{max}") || problem.RequestID == "" {
		t.Fatalf("problem = %+v err=%v", problem, err)
	}
	// The same refusal in English comes from the Schema's overlay.
	english := httptest.NewRequest(http.MethodPost, accessKeysPath, strings.NewReader(`{"name":"one too many","scopes":["read"]}`))
	english.Header.Set("Content-Type", "application/json")
	english.Header.Set("Origin", "http://example.com")
	english.Header.Set("Accept-Language", "en")
	english.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, english)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "at most 10 active keys") {
		t.Fatalf("english refusal = %d %s", recorder.Code, recorder.Body.String())
	}
	// Revoking a key gives its place back.
	if response := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/"+first.ID, "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", response.Code, response.Body.String())
	}
	if response := createAccessKey(t, handler, cookie, `{"name":"after revoke","scopes":["read"]}`); response.Code != http.StatusCreated {
		t.Fatalf("key after revoke = %d %s", response.Code, response.Body.String())
	}
}

func TestAccountAccessKeyRevokeEndsTheKeyAndIsIdempotent(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := accessKeyHarness(t)
	created := decodeAccessKey(t, createAccessKey(t, handler, cookie, `{"name":"k","scopes":["read"]}`))
	revoke := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/"+created.ID, "", "")
	if revoke.Code != http.StatusNoContent || revoke.Body.Len() != 0 {
		t.Fatalf("revoke = %d %s", revoke.Code, revoke.Body.String())
	}
	keys := decodeAccessKeyList(t, grantTestRequest(t, handler, cookie, http.MethodGet, accessKeysPath, "", ""))
	if len(keys) != 1 || keys[0].Active || keys[0].RevokedAt == nil {
		t.Fatalf("list after revoke = %+v", keys)
	}
	stored, _, _ := repository.AccessKey(context.Background(), created.ID)
	firstRevocation := stored.RevokedAt
	if firstRevocation.IsZero() {
		t.Fatal("the key is not revoked in the store")
	}
	// Revoking again succeeds and changes nothing, and writes no second audit entry.
	if again := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/"+created.ID, "", ""); again.Code != http.StatusNoContent {
		t.Fatalf("second revoke = %d %s", again.Code, again.Body.String())
	}
	stored, _, _ = repository.AccessKey(context.Background(), created.ID)
	if !stored.RevokedAt.Equal(firstRevocation) {
		t.Fatal("a repeated revoke moved the revocation time")
	}
	revocations := 0
	events, err := repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Action == "revoke_access_key" {
			revocations++
		}
	}
	if revocations != 1 {
		t.Fatalf("revoke audit entries = %d; a repeat is not a change", revocations)
	}
	if missing := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/no-such-key", "", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown key = %d %s", missing.Code, missing.Body.String())
	}
}

// Another person's keys are neither listed nor revocable, and nothing in the
// answer says whether such a key exists.
func TestAccountAccessKeyIsIsolatedPerPerson(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := accessKeyHarness(t)
	now := time.Now().UTC()
	if err := repository.CreateAccessKey(context.Background(), accesskeys.Record{
		ID: "foreignkey", OwnerID: "owner-b", Name: "theirs", Prefix: "plk_foreignkey", SecretHash: strings.Repeat("a", 64),
		Scopes: []string{"read"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, 10, now); err != nil {
		t.Fatal(err)
	}
	if keys := decodeAccessKeyList(t, grantTestRequest(t, handler, cookie, http.MethodGet, accessKeysPath, "", "")); len(keys) != 0 {
		t.Fatalf("another person's key was listed: %+v", keys)
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/foreignkey", "", "")
	unknown := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/neverexisted", "", "")
	if foreign.Code != http.StatusNotFound || foreign.Code != unknown.Code {
		t.Fatalf("foreign = %d, unknown = %d; both must read as not found", foreign.Code, unknown.Code)
	}
	stored, _, _ := repository.AccessKey(context.Background(), "foreignkey")
	if !stored.RevokedAt.IsZero() {
		t.Fatal("another person's key was revoked")
	}
	// A body cannot name the owner.
	if response := createAccessKey(t, handler, cookie, `{"name":"k","scopes":["read"],"owner_id":"owner-b"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("owner member = %d", response.Code)
	}
	created := decodeAccessKey(t, createAccessKey(t, handler, cookie, `{"name":"mine","scopes":["read"]}`))
	if stored, _, _ := repository.AccessKey(context.Background(), created.ID); stored.OwnerID != "owner-a" {
		t.Fatalf("owner = %q", stored.OwnerID)
	}
}

// Keys can never manage keys. Anything but a real browser session is refused,
// whatever it presents: a credential header, even beside a session cookie, and
// the development identity.
func TestAccountAccessKeyManagementNeedsTheBrowserSession(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := accessKeyHarness(t)
	created := decodeAccessKey(t, createAccessKey(t, handler, cookie, `{"name":"k","scopes":["read"]}`))
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, accessKeysPath, ""},
		{http.MethodPost, accessKeysPath, `{"name":"sneaky","scopes":["read","generate"]}`},
		{http.MethodDelete, accessKeysPath + "/" + created.ID, ""},
	} {
		for name, authorization := range map[string]string{
			"a key beside the session": "Bearer " + created.Key,
			"another bearer token":     "Bearer " + deployToken,
		} {
			request := httptest.NewRequest(call.method, call.path, strings.NewReader(call.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "http://example.com")
			request.Header.Set("Authorization", authorization)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Errorf("%s %s with %s = %d %s", call.method, call.path, name, response.Code, response.Body.String())
			}
		}
		// No cookie: the harness' development identity answers, which is not a person at a browser.
		if response := grantTestRequest(t, handler, nil, call.method, call.path, call.body, ""); response.Code != http.StatusForbidden {
			t.Errorf("%s %s without a session = %d %s", call.method, call.path, response.Code, response.Body.String())
		}
	}
	// Nothing was created or revoked by the refused calls.
	records, err := repository.ListAccessKeys(context.Background(), "owner-a")
	if err != nil || len(records) != 1 || !records[0].RevokedAt.IsZero() {
		t.Fatalf("keys after the refused calls = %+v err=%v", records, err)
	}
}

func TestAccountAccessKeyAuditNamesTheKeyButNeverItsSecret(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := accessKeyHarness(t)
	created := decodeAccessKey(t, createAccessKey(t, handler, cookie, `{"name":"ci runner","scopes":["read","generate"],"ttl_days":7}`))
	if response := grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/"+created.ID, "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", response.Code)
	}
	// Refused and invalid calls leave no entry.
	createAccessKey(t, handler, cookie, `{"name":"","scopes":["read"]}`)
	grantTestRequest(t, handler, cookie, http.MethodDelete, accessKeysPath+"/no-such-key", "", "")

	events, err := repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secret := created.Key[len(created.Prefix)+1:]
	seen := map[string]int{}
	for _, event := range events {
		if event.Action != "create_access_key" && event.Action != "revoke_access_key" {
			continue
		}
		seen[event.Action]++
		encoded, _ := json.Marshal(event)
		if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), created.Key) {
			t.Fatalf("the audit entry holds the key: %s", encoded)
		}
		if event.ActorID != "owner-a" || event.ResourceType != "access_key" || event.ResourceID != created.ID ||
			event.Detail["name"] != "ci runner" || event.Detail["prefix"] != created.Prefix {
			t.Fatalf("audit event = %+v", event)
		}
		if event.Action == "create_access_key" {
			scopes, _ := event.Detail["scopes"].([]any)
			if len(scopes) != 2 || event.Detail["expires_at"] == nil {
				t.Fatalf("create detail = %+v", event.Detail)
			}
		}
	}
	if seen["create_access_key"] != 1 || seen["revoke_access_key"] != 1 {
		t.Fatalf("audit entries = %v", seen)
	}
}

// A Catalog without the section registers nothing: older documents keep working.
func TestAccountAccessKeyRoutesAreAbsentWithoutTheSection(t *testing.T) {
	t.Parallel()
	_, handler, cookie, _ := grantTestBrowserWithApplication(t, func(application *catalog.ApplicationSchema) {
		application.AccessKeys = nil
		application.Runtime.HTTP.Routes.AccountAccessKeys = ""
		application.Runtime.HTTP.Routes.AccountAccessKey = ""
	}, []string{"user"})
	for _, call := range []struct{ method, path string }{
		{http.MethodGet, accessKeysPath}, {http.MethodPost, accessKeysPath}, {http.MethodDelete, accessKeysPath + "/abc"},
	} {
		response := grantTestRequest(t, handler, cookie, call.method, call.path, `{}`, "")
		if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s without the section = %d %s", call.method, call.path, response.Code, response.Body.String())
		}
	}
}

func TestAccountAccessKeyRoutesComeFromTheSchema(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	routes := product.Application().Runtime.HTTP.Routes
	if routes.AccountAccessKeys != accessKeysPath || routes.AccountAccessKey != accessKeysPath+"/{id}" {
		t.Fatalf("routes = %q %q", routes.AccountAccessKeys, routes.AccountAccessKey)
	}
}

// bearerKeyHarness is the server of a deployment whose only accepted
// credentials are the session cookie and one deployment Bearer token: no
// development identity answers for a request that has neither.
func bearerKeyHarness(t *testing.T) (handler http.Handler, cookie *http.Cookie, sessionID string, repository *sqlite.Repository) {
	t.Helper()
	record := authentication.UserRecord{
		ID: "owner-a", Username: "grant-user",
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "Grant User", Email: "grant@example.test", Active: true,
	}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{record.Username: record},
		byID:       map[string]authentication.UserRecord{record.ID: record},
	}
	module, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"user"}, nil,
		func(config *authentication.Config) {
			config.Directory = directory
			bearer, err := httpapi.NewBearerIdentity(map[string]access.Claims{deployToken: {ID: "deployment-owner"}}, config.Policy)
			if err != nil {
				t.Fatal(err)
			}
			config.Fallback = bearer
		}, nil, nil, nil)
	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"grant-user","password":"correct-horse"}`))
	request.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(login, request)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie = login.Result().Cookies()[0]
	session := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"keys"}`, "")
	var created struct {
		ID string `json:"id"`
	}
	if session.Code != http.StatusCreated || json.Unmarshal(session.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatalf("session = %d %s", session.Code, session.Body.String())
	}
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	return handler, cookie, created.ID, value.(*fixtureExecutionState).repository
}

// The main server does not accept access keys, in this change or any route of
// it. A valid key presented as a Bearer token is an unknown Bearer token like
// any other: every route answers 401 exactly as it does for a made-up token,
// the routes that read or change what a key must never change included.
func TestAccessKeyIsNotAcceptedByAnyRouteOfTheMainServer(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID, repository := bearerKeyHarness(t)
	created := decodeAccessKey(t, createAccessKey(t, handler, cookie, `{"name":"mcp client","scopes":["read","generate"]}`))
	if created.Key == "" {
		t.Fatal("no key was issued")
	}
	// Sanity: the deployment token this harness accepts does work, so a 401 for
	// the key is a refusal of the key and not a harness that rejects everything.
	token := httptest.NewRequest(http.MethodGet, "/api/v1/account/step-policy", nil)
	token.Header.Set("Authorization", "Bearer "+deployToken)
	if response := serve(handler, token); response.Code != http.StatusOK {
		t.Fatalf("deployment token on step-policy = %d %s", response.Code, response.Body.String())
	}

	calls := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/tasks", ""},
		{http.MethodPost, "/api/v1/tasks/prepare", `{"session_id":"` + sessionID + `","capability":"text-to-3d","input":{"prompt":"a chair"}}`},
		{http.MethodPost, "/api/v1/tasks/grants", `{"prepared_input_id":"x"}`},
		{http.MethodGet, "/api/v1/account/step-policy", ""},
		{http.MethodPut, "/api/v1/account/step-policy", `{"expected_version":0,"list":{"mode":"unlimited","cap":20},"mcp":{"mode":"unlimited","cap":20}}`},
		{http.MethodGet, "/api/v1/account/usage", ""},
		{http.MethodPut, "/api/v1/chat/sessions/" + sessionID + "/approval", `{"mode":"auto"}`},
		{http.MethodGet, "/api/v1/chat/sessions/" + sessionID + "/approval", ""},
		{http.MethodGet, "/api/v1/schema", ""},
		{http.MethodGet, accessKeysPath, ""},
		{http.MethodPost, accessKeysPath, `{"name":"another","scopes":["read"]}`},
		{http.MethodDelete, accessKeysPath + "/" + created.ID, ""},
	}
	for _, call := range calls {
		withKey := httptest.NewRequest(call.method, call.path, strings.NewReader(call.body))
		withKey.Header.Set("Content-Type", "application/json")
		withKey.Header.Set("Origin", "http://example.com")
		withKey.Header.Set("Authorization", "Bearer "+created.Key)
		keyResponse := serve(handler, withKey)

		withBogus := httptest.NewRequest(call.method, call.path, strings.NewReader(call.body))
		withBogus.Header.Set("Content-Type", "application/json")
		withBogus.Header.Set("Origin", "http://example.com")
		withBogus.Header.Set("Authorization", "Bearer plk_"+strings.Repeat("a", 16)+"_"+strings.Repeat("b", 52))
		bogusResponse := serve(handler, withBogus)
		if call.path == "/api/v1/schema" {
			// The public schema needs no credential at all: it answers the same for both.
			if keyResponse.Code != bogusResponse.Code {
				t.Errorf("%s %s: key %d, made-up key %d", call.method, call.path, keyResponse.Code, bogusResponse.Code)
			}
			continue
		}
		if keyResponse.Code != http.StatusUnauthorized || bogusResponse.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a key = %d, with a made-up key = %d; want 401 for both (%s)",
				call.method, call.path, keyResponse.Code, bogusResponse.Code, keyResponse.Body.String())
		}
	}
	// Nothing the key reached for changed.
	stored, found, err := repository.AccountSettings(context.Background(), "owner-a")
	if err != nil || found {
		t.Fatalf("a key wrote account settings: %+v found=%v err=%v", stored, found, err)
	}
	if records, err := repository.ListAccessKeys(context.Background(), "owner-a"); err != nil || len(records) != 1 || !records[0].RevokedAt.IsZero() {
		t.Fatalf("keys after the key's own attempts = %+v err=%v", records, err)
	}
	// The key was never recorded as used: no route of the main server looked at it.
	if record, _, _ := repository.AccessKey(context.Background(), created.ID); !record.LastUsedAt.IsZero() {
		t.Fatalf("the main server authenticated a key: last used %v", record.LastUsedAt)
	}
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A key beside a session cookie does not change who the request is: the cookie
// decides, as it does today, and a key never adds authority to it.
func TestAccessKeyBesideASessionChangesNothing(t *testing.T) {
	t.Parallel()
	handler, cookie, _, _ := bearerKeyHarness(t)
	created := decodeAccessKey(t, createAccessKey(t, handler, cookie, `{"name":"k","scopes":["read"]}`))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/step-policy", nil)
	request.Header.Set("Authorization", "Bearer "+created.Key)
	request.AddCookie(cookie)
	if response := serve(handler, request); response.Code != http.StatusOK {
		t.Fatalf("a session with an extra header = %d %s", response.Code, response.Body.String())
	}
	// Writes that need a person are refused as soon as a credential header is present.
	write := httptest.NewRequest(http.MethodPut, "/api/v1/account/step-policy",
		strings.NewReader(`{"expected_version":0,"list":{"mode":"unlimited","cap":20},"mcp":{"mode":"unlimited","cap":20}}`))
	write.Header.Set("Content-Type", "application/json")
	write.Header.Set("Origin", "http://example.com")
	write.Header.Set("Authorization", "Bearer "+created.Key)
	write.AddCookie(cookie)
	if response := serve(handler, write); response.Code != http.StatusForbidden {
		t.Fatalf("step policy write beside a key = %d %s", response.Code, response.Body.String())
	}
}

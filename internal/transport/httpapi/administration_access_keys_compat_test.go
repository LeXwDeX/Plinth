package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const administrationAccessKeysPath = "/api/admin/access-keys"

func administrationAccessKeyHarness(t *testing.T, roles []string) (http.Handler, *sqlite.Repository) {
	t.Helper()
	module, handler, _ := newHTTPHarnessWithRoles(t, roles)
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	return handler, value.(*fixtureExecutionState).repository
}

func administrationAccessKeyRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	return administrationRequest(handler, method, path, body, nil)
}

func seedAdministrationAccessKeys(t *testing.T, repository *sqlite.Repository) time.Time {
	t.Helper()
	// The server reads the wall clock for "active", so seed relative to it.
	now := time.Now().UTC().Truncate(time.Second)
	for _, key := range []struct {
		id, owner, name string
		created         time.Time
	}{
		{"key-old", "owner-a", "deploy bot", now},
		{"key-new", "owner-a", "laptop", now.Add(time.Minute)},
		{"key-b", "owner-b", "bob key", now.Add(2 * time.Minute)},
	} {
		record := accesskeys.Record{
			ID: key.id, OwnerID: key.owner, Name: key.name, Prefix: "plk_" + key.id,
			SecretHash: strings.Repeat("a", 64), Scopes: []string{"read"},
			CreatedAt: key.created, ExpiresAt: key.created.Add(24 * time.Hour),
		}
		if err := repository.CreateAccessKey(context.Background(), record, 10, key.created); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.RecordAccessKeyUse(context.Background(), "key-old", now.Add(5*time.Minute), 12); err != nil {
		t.Fatal(err)
	}
	return now
}

func administrationAccessKeyList(t *testing.T, handler http.Handler, query string) (int, map[string]any) {
	t.Helper()
	response := administrationAccessKeyRequest(handler, http.MethodGet, administrationAccessKeysPath+query, "")
	var body map[string]any
	if len(response.Body.Bytes()) > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %q: %v", response.Body.String(), err)
		}
	}
	return response.Code, body
}

func TestAdministrationAccessKeysRequireAnAdministrator(t *testing.T) {
	t.Parallel()
	handler, repository := administrationAccessKeyHarness(t, []string{"admin"})
	seedAdministrationAccessKeys(t, repository)
	if code, _ := administrationAccessKeyList(t, handler, ""); code != http.StatusOK {
		t.Fatalf("administrator list = %d", code)
	}
	_, unauthenticated, _ := newHTTPHarnessWithAuthentication(t, nil, nil, func(config *authentication.Config) {
		config.Fallback = deniedFallbackIdentity{}
	})
	if code, _ := administrationAccessKeyList(t, unauthenticated, ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d; want 401", code)
	}
	handler, _ = administrationAccessKeyHarness(t, []string{"user"})
	if code, _ := administrationAccessKeyList(t, handler, ""); code != http.StatusForbidden {
		t.Fatalf("non-administrator list = %d; want 403", code)
	}
	for _, call := range []struct{ method, path, body string }{
		{http.MethodDelete, administrationAccessKeysPath + "/key-old", ""},
		{http.MethodPut, administrationAccessKeysPath + "/switch", `{"enabled":false}`},
	} {
		response := administrationAccessKeyRequest(handler, call.method, call.path, call.body)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s as a non-administrator = %d; want 403", call.method, call.path, response.Code)
		}
	}
}

func TestAdministrationAccessKeysListPagesSearchesAndShowsUsage(t *testing.T) {
	t.Parallel()
	handler, repository := administrationAccessKeyHarness(t, []string{"admin"})
	now := seedAdministrationAccessKeys(t, repository)
	code, body := administrationAccessKeyList(t, handler, "?page=1&pageSize=2")
	if code != http.StatusOK || body["total"] != float64(3) || body["page"] != float64(1) ||
		body["pageSize"] != float64(2) || body["enabled"] != true {
		t.Fatalf("page 1 = %d %v", code, body)
	}
	firstPage := body["data"].([]any)
	if len(firstPage) != 2 {
		t.Fatalf("page 1 data = %v", firstPage)
	}
	newest := firstPage[0].(map[string]any)
	if newest["id"] != "key-b" || newest["active"] != true || newest["requestCount"] != float64(0) {
		t.Fatalf("newest = %v", newest)
	}
	code, body = administrationAccessKeyList(t, handler, "?page=2&pageSize=2")
	if code != http.StatusOK || len(body["data"].([]any)) != 1 {
		t.Fatalf("page 2 = %d %v", code, body)
	}
	oldest := body["data"].([]any)[0].(map[string]any)
	if oldest["id"] != "key-old" || oldest["requestCount"] != float64(12) || oldest["lastUsedAt"] == nil ||
		oldest["expiresAt"] == nil || oldest["revokedAt"] != nil || oldest["ownerId"] != "owner-a" ||
		oldest["prefix"] != "plk_key-old" {
		t.Fatalf("oldest = %v", oldest)
	}
	if used, err := time.Parse(time.RFC3339Nano, oldest["lastUsedAt"].(string)); err != nil || !used.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("lastUsedAt = %v err=%v", oldest["lastUsedAt"], err)
	}
	code, body = administrationAccessKeyList(t, handler, "?search=deploy")
	if code != http.StatusOK || body["total"] != float64(1) || body["data"].([]any)[0].(map[string]any)["id"] != "key-old" {
		t.Fatalf("search = %d %v", code, body)
	}
	code, body = administrationAccessKeyList(t, handler, "?ownerId=owner-b")
	if code != http.StatusOK || body["total"] != float64(1) || body["data"].([]any)[0].(map[string]any)["id"] != "key-b" {
		t.Fatalf("owner filter = %d %v", code, body)
	}
	if code, _ := administrationAccessKeyList(t, handler, "?search="+strings.Repeat("x", 200)); code != http.StatusBadRequest {
		t.Fatalf("overlong search = %d; want 400", code)
	}
}

func TestAdministrationAccessKeyRevokeAndSwitch(t *testing.T) {
	t.Parallel()
	handler, repository := administrationAccessKeyHarness(t, []string{"admin"})
	seedAdministrationAccessKeys(t, repository)
	response := administrationAccessKeyRequest(handler, http.MethodDelete, administrationAccessKeysPath+"/key-old", "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", response.Code, response.Body.String())
	}
	record, found, err := repository.AccessKey(context.Background(), "key-old")
	if err != nil || !found || record.RevokedAt.IsZero() {
		t.Fatalf("stored key = %+v found=%v err=%v", record, found, err)
	}
	for _, path := range []string{administrationAccessKeysPath + "/key-old", administrationAccessKeysPath + "/missing"} {
		want := http.StatusNoContent
		if strings.HasSuffix(path, "missing") {
			want = http.StatusNotFound
		}
		if response := administrationAccessKeyRequest(handler, http.MethodDelete, path, ""); response.Code != want {
			t.Errorf("delete %s = %d; want %d", path, response.Code, want)
		}
	}
	// The list reflects the revoke.
	_, body := administrationAccessKeyList(t, handler, "?search=deploy")
	revoked := body["data"].([]any)[0].(map[string]any)
	if revoked["active"] != false || revoked["revokedAt"] == nil {
		t.Fatalf("revoked listing = %v", revoked)
	}

	// The overall switch is writable, visible and refuses malformed bodies.
	response = administrationAccessKeyRequest(handler, http.MethodPut, administrationAccessKeysPath+"/switch", `{"enabled":false}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("switch off = %d %s", response.Code, response.Body.String())
	}
	if _, body := administrationAccessKeyList(t, handler, ""); body["enabled"] != false {
		t.Fatalf("list after switch off = %v", body["enabled"])
	}
	response = administrationAccessKeyRequest(handler, http.MethodPut, administrationAccessKeysPath+"/switch", `{"enabled":true}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":true`) {
		t.Fatalf("switch on = %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{}`, `{"enabled":"off"}`, `not json`} {
		if response := administrationAccessKeyRequest(handler, http.MethodPut, administrationAccessKeysPath+"/switch", body); response.Code != http.StatusBadRequest {
			t.Errorf("switch body %q = %d; want 400", body, response.Code)
		}
	}
}

func TestAdministrationAccessKeyRoutesComeFromTheSchema(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	routes := product.Application().Runtime.HTTP.LegacyRoutes
	if routes.AdminAccessKeys != "/api/admin/access-keys" ||
		routes.AdminAccessKey != "/api/admin/access-keys/{keyID}" ||
		routes.AdminAccessKeysSwitch != "/api/admin/access-keys/switch" {
		t.Fatalf("routes = %q %q %q", routes.AdminAccessKeys, routes.AdminAccessKey, routes.AdminAccessKeysSwitch)
	}
}

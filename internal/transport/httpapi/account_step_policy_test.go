package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const stepPolicyPath = "/api/v1/account/step-policy"

type stepPolicyBody struct {
	Version int64 `json:"version"`
	List    struct {
		Mode string `json:"mode"`
		Cap  int    `json:"cap"`
	} `json:"list"`
	MCP struct {
		Mode string `json:"mode"`
		Cap  int    `json:"cap"`
	} `json:"mcp"`
	Modes []struct {
		Value string `json:"value"`
		Label string `json:"label"`
	} `json:"modes"`
	CapRange struct {
		Minimum int `json:"minimum"`
		Maximum int `json:"maximum"`
	} `json:"cap_range"`
}

func decodeStepPolicy(t *testing.T, response *httptest.ResponseRecorder) stepPolicyBody {
	t.Helper()
	var body stepPolicyBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("step policy body %q: %v", response.Body.String(), err)
	}
	return body
}

func stepPolicyHarness(t *testing.T) (http.Handler, *http.Cookie, *sqlite.Repository) {
	t.Helper()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	return handler, cookie, value.(*fixtureExecutionState).repository
}

func putStepPolicy(t *testing.T, handler http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return grantTestRequest(t, handler, cookie, http.MethodPut, stepPolicyPath, body, "")
}

func TestAccountStepPolicyShowsTheSchemaDefaultsUntilSaved(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := stepPolicyHarness(t)
	response := grantTestRequest(t, handler, cookie, http.MethodGet, stepPolicyPath, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	body := decodeStepPolicy(t, response)
	if body.Version != 0 || body.List.Mode != "agent" || body.List.Cap != 20 ||
		body.MCP.Mode != "agent" || body.MCP.Cap != 20 ||
		body.CapRange.Minimum != 1 || body.CapRange.Maximum != 200 {
		t.Fatalf("defaults = %+v", body)
	}
	if len(body.Modes) != 3 || body.Modes[0].Value != "agent" || body.Modes[0].Label == "" ||
		body.Modes[1].Value != "unlimited" || body.Modes[2].Value != "cap" {
		t.Fatalf("modes = %+v", body.Modes)
	}
	if cache := response.Header().Get("Cache-Control"); !strings.Contains(cache, "private") {
		t.Fatalf("Cache-Control = %q; the response is private to the person", cache)
	}
}

func TestAccountStepPolicyLabelsFollowTheRequestLocale(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := stepPolicyHarness(t)
	request := httptest.NewRequest(http.MethodGet, stepPolicyPath, nil)
	request.Header.Set("Accept-Language", "en")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	labels := map[string]string{}
	for _, mode := range decodeStepPolicy(t, response).Modes {
		labels[mode.Value] = mode.Label
	}
	if labels["agent"] != "Managed by the Agent" || labels["cap"] != "Limit steps" {
		t.Fatalf("labels = %v", labels)
	}
}

func TestAccountStepPolicySavesBothSettingsAndReadsThemBack(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := stepPolicyHarness(t)
	put := putStepPolicy(t, handler, cookie,
		`{"expected_version":0,"list":{"mode":"cap","cap":30},"mcp":{"mode":"unlimited","cap":20}}`)
	if put.Code != http.StatusOK {
		t.Fatalf("put = %d %s", put.Code, put.Body.String())
	}
	saved := decodeStepPolicy(t, put)
	if saved.Version != 1 || saved.List.Mode != "cap" || saved.List.Cap != 30 || saved.MCP.Mode != "unlimited" {
		t.Fatalf("saved = %+v", saved)
	}
	read := decodeStepPolicy(t, grantTestRequest(t, handler, cookie, http.MethodGet, stepPolicyPath, "", ""))
	if read.Version != 1 || read.List.Mode != "cap" || read.List.Cap != 30 || read.MCP.Mode != "unlimited" {
		t.Fatalf("read = %+v", read)
	}
	// The cap is kept while the mode is not cap, and the next save needs version 1.
	again := putStepPolicy(t, handler, cookie,
		`{"expected_version":1,"list":{"mode":"agent","cap":30},"mcp":{"mode":"unlimited","cap":20}}`)
	if decoded := decodeStepPolicy(t, again); again.Code != http.StatusOK || decoded.Version != 2 || decoded.List.Mode != "agent" || decoded.List.Cap != 30 {
		t.Fatalf("second save = %d %s", again.Code, again.Body.String())
	}
}

func TestAccountStepPolicyRefusesAStaleVersion(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := stepPolicyHarness(t)
	if put := putStepPolicy(t, handler, cookie,
		`{"expected_version":0,"list":{"mode":"cap","cap":5},"mcp":{"mode":"agent","cap":20}}`); put.Code != http.StatusOK {
		t.Fatalf("first save = %d %s", put.Code, put.Body.String())
	}
	for _, version := range []string{"0", "7"} {
		stale := putStepPolicy(t, handler, cookie,
			`{"expected_version":`+version+`,"list":{"mode":"unlimited","cap":9},"mcp":{"mode":"agent","cap":20}}`)
		if stale.Code != http.StatusConflict {
			t.Fatalf("expected_version %s = %d %s", version, stale.Code, stale.Body.String())
		}
	}
	read := decodeStepPolicy(t, grantTestRequest(t, handler, cookie, http.MethodGet, stepPolicyPath, "", ""))
	if read.Version != 1 || read.List.Mode != "cap" || read.List.Cap != 5 {
		t.Fatalf("a refused save changed the settings: %+v", read)
	}
}

// Raising or removing the cap is a payment-level decision: it needs the real
// browser session, like a card decision. A request that carries a credential
// header, as an external MCP client does, can read but never write.
func TestAccountStepPolicyWriteNeedsTheBrowserSessionNotACredential(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := stepPolicyHarness(t)
	body := `{"expected_version":0,"list":{"mode":"unlimited","cap":20},"mcp":{"mode":"unlimited","cap":20}}`

	bearer := httptest.NewRequest(http.MethodPut, stepPolicyPath, strings.NewReader(body))
	bearer.Header.Set("Content-Type", "application/json")
	bearer.Header.Set("Origin", "http://example.com")
	bearer.Header.Set("Authorization", "Bearer an-external-mcp-token")
	bearer.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bearer)
	if response.Code != http.StatusForbidden {
		t.Fatalf("credential header write = %d %s", response.Code, response.Body.String())
	}

	// Without the session cookie there is no browser session at all.
	bare := putStepPolicy(t, handler, nil, body)
	if bare.Code == http.StatusOK || bare.Code < 400 {
		t.Fatalf("anonymous write = %d %s", bare.Code, bare.Body.String())
	}
	read := decodeStepPolicy(t, grantTestRequest(t, handler, cookie, http.MethodGet, stepPolicyPath, "", ""))
	if read.Version != 0 || read.List.Mode != "agent" {
		t.Fatalf("a refused write changed the settings: %+v", read)
	}
}

func TestAccountStepPolicyRejectsInvalidBodies(t *testing.T) {
	t.Parallel()
	handler, cookie, _ := stepPolicyHarness(t)
	for name, body := range map[string]string{
		"unknown mode":        `{"expected_version":0,"list":{"mode":"turbo","cap":5},"mcp":{"mode":"agent","cap":20}}`,
		"empty mode":          `{"expected_version":0,"list":{"mode":"","cap":5},"mcp":{"mode":"agent","cap":20}}`,
		"cap below the range": `{"expected_version":0,"list":{"mode":"cap","cap":0},"mcp":{"mode":"agent","cap":20}}`,
		"cap above the range": `{"expected_version":0,"list":{"mode":"cap","cap":201},"mcp":{"mode":"agent","cap":20}}`,
		"cap above for agent": `{"expected_version":0,"list":{"mode":"agent","cap":900},"mcp":{"mode":"agent","cap":20}}`,
		"negative version":    `{"expected_version":-1,"list":{"mode":"cap","cap":5},"mcp":{"mode":"agent","cap":20}}`,
		"missing version":     `{"list":{"mode":"cap","cap":5},"mcp":{"mode":"agent","cap":20}}`,
		"missing list":        `{"expected_version":0,"mcp":{"mode":"agent","cap":20}}`,
		"missing mcp":         `{"expected_version":0,"list":{"mode":"cap","cap":5}}`,
		"missing cap":         `{"expected_version":0,"list":{"mode":"cap"},"mcp":{"mode":"agent","cap":20}}`,
		"missing mode":        `{"expected_version":0,"list":{"cap":5},"mcp":{"mode":"agent","cap":20}}`,
		"cap not a number":    `{"expected_version":0,"list":{"mode":"cap","cap":"five"},"mcp":{"mode":"agent","cap":20}}`,
		"fractional cap":      `{"expected_version":0,"list":{"mode":"cap","cap":2.5},"mcp":{"mode":"agent","cap":20}}`,
		"unknown field":       `{"expected_version":0,"web":{"mode":"cap","cap":5},"list":{"mode":"cap","cap":5},"mcp":{"mode":"agent","cap":20}}`,
		"unknown member":      `{"expected_version":0,"list":{"mode":"cap","cap":5,"extra":1},"mcp":{"mode":"agent","cap":20}}`,
		"trailing json":       `{"expected_version":0,"list":{"mode":"cap","cap":5},"mcp":{"mode":"agent","cap":20}} {}`,
		"not json":            `not json`,
		"empty body":          ``,
	} {
		response := putStepPolicy(t, handler, cookie, body)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, response.Code, response.Body.String())
		}
	}
	read := decodeStepPolicy(t, grantTestRequest(t, handler, cookie, http.MethodGet, stepPolicyPath, "", ""))
	if read.Version != 0 {
		t.Fatalf("an invalid save changed the settings: %+v", read)
	}
}

func TestAccountStepPolicyChangeIsAuditedOnlyWhenSomethingChanged(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := stepPolicyHarness(t)
	save := func(version int, list string) *httptest.ResponseRecorder {
		return putStepPolicy(t, handler, cookie,
			`{"expected_version":`+strconv.Itoa(version)+`,"list":{"mode":"`+list+`","cap":30},"mcp":{"mode":"agent","cap":20}}`)
	}
	if response := save(0, "cap"); response.Code != http.StatusOK {
		t.Fatalf("first save = %d %s", response.Code, response.Body.String())
	}
	// The same values with the current version change nothing: no new version, no audit entry.
	same := save(1, "cap")
	if decoded := decodeStepPolicy(t, same); same.Code != http.StatusOK || decoded.Version != 1 {
		t.Fatalf("same values = %d %s", same.Code, same.Body.String())
	}
	if response := save(1, "unlimited"); response.Code != http.StatusOK {
		t.Fatalf("second save = %d %s", response.Code, response.Body.String())
	}
	// A refused save leaves no entry either.
	if response := save(1, "cap"); response.Code != http.StatusConflict {
		t.Fatalf("stale save = %d", response.Code)
	}

	events, err := repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listModes := map[string]bool{}
	entries := 0
	for _, event := range events {
		if event.Action != "set_step_policy" {
			continue
		}
		entries++
		if event.ActorID != "owner-a" || event.ResourceID != "owner-a" || event.ResourceType != "account_step_policy" {
			t.Fatalf("audit event = %+v", event)
		}
		from := event.Detail["from"].(map[string]any)["list"].(map[string]any)
		to := event.Detail["to"].(map[string]any)["list"].(map[string]any)
		if event.Detail["from"].(map[string]any)["mcp"].(map[string]any)["mode"] != "agent" || to["cap"] != float64(30) {
			t.Fatalf("audit detail = %+v", event.Detail)
		}
		listModes[from["mode"].(string)+">"+to["mode"].(string)] = true
	}
	// One entry per real change, each with the settings before and after.
	if entries != 2 || !listModes["agent>cap"] || !listModes["cap>unlimited"] {
		t.Fatalf("audit entries = %d %v, want agent>cap and cap>unlimited", entries, listModes)
	}
}

// Saving the values that are already in force is not a change, even for a
// person who never saved: the defaults are the settings in force.
func TestAccountStepPolicySavingTheDefaultsIsNoChange(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := stepPolicyHarness(t)
	response := putStepPolicy(t, handler, cookie,
		`{"expected_version":0,"list":{"mode":"agent","cap":20},"mcp":{"mode":"agent","cap":20}}`)
	if decoded := decodeStepPolicy(t, response); response.Code != http.StatusOK || decoded.Version != 0 {
		t.Fatalf("defaults save = %d %s", response.Code, response.Body.String())
	}
	events, err := repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Action == "set_step_policy" {
			t.Fatalf("a no-op save wrote an audit entry: %+v", event)
		}
	}
	if _, found, err := repository.AccountSettings(context.Background(), "owner-a"); err != nil || found {
		t.Fatalf("a no-op save wrote a row: found=%v err=%v", found, err)
	}
}

// Settings belong to the person: another person's settings are never read or
// changed through this person's session.
func TestAccountStepPolicyIsIsolatedPerPerson(t *testing.T) {
	t.Parallel()
	handler, cookie, repository := stepPolicyHarness(t)
	if _, err := repository.PutAccountSettings(context.Background(), "owner-b", 0, accountsettings.Settings{
		List: approvalpolicy.StepSetting{Mode: "cap", Cap: 3}, MCP: approvalpolicy.StepSetting{Mode: "unlimited", Cap: 4},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	read := decodeStepPolicy(t, grantTestRequest(t, handler, cookie, http.MethodGet, stepPolicyPath, "", ""))
	if read.Version != 0 || read.List.Mode != "agent" {
		t.Fatalf("another person's settings were read: %+v", read)
	}
	// This person's first save is not in conflict with a stranger's row.
	if response := putStepPolicy(t, handler, cookie,
		`{"expected_version":0,"list":{"mode":"cap","cap":9},"mcp":{"mode":"agent","cap":20}}`); response.Code != http.StatusOK {
		t.Fatalf("save = %d %s", response.Code, response.Body.String())
	}
	other, found, err := repository.AccountSettings(context.Background(), "owner-b")
	if err != nil || !found || other.Version != 1 || other.List.Cap != 3 || other.MCP.Mode != "unlimited" {
		t.Fatalf("another person's settings changed: %+v found=%v err=%v", other, found, err)
	}
	// The body cannot name an owner: an owner member is an unknown field.
	if response := putStepPolicy(t, handler, cookie,
		`{"expected_version":1,"owner_id":"owner-b","list":{"mode":"cap","cap":9},"mcp":{"mode":"agent","cap":20}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("owner member = %d", response.Code)
	}
}

// A Catalog without the section registers nothing: older documents keep working.
func TestAccountStepPolicyRouteIsAbsentWithoutTheSection(t *testing.T) {
	t.Parallel()
	_, handler, cookie, _ := grantTestBrowserWithApplication(t, func(application *catalog.ApplicationSchema) {
		application.StepPolicy = nil
		application.Runtime.HTTP.Routes.AccountStepPolicy = ""
		// Access keys and the external MCP service decide by the step mode.
		application.AccessKeys, application.MCPPublic = nil, nil
		application.Runtime.HTTP.Routes.AccountAccessKeys, application.Runtime.HTTP.Routes.AccountAccessKey = "", ""
	}, []string{"user"})
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		response := grantTestRequest(t, handler, cookie, method, stepPolicyPath, `{}`, "")
		if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s without the section = %d %s", method, response.Code, response.Body.String())
		}
	}
}

// Sanity: the harness wires the real Schema, so the route key is the declared one.
func TestAccountStepPolicyRouteComesFromTheSchema(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got := product.Application().Runtime.HTTP.Routes.AccountStepPolicy; got != stepPolicyPath {
		t.Fatalf("route = %q", got)
	}
}

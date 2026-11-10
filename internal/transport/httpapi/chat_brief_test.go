package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func briefPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/brief"
}

func explicitModel3DPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/model3d"
}

func decodeBrief(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var decoded struct {
		Brief map[string]json.RawMessage `json:"brief"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("brief body %q: %v", body, err)
	}
	return decoded.Brief
}

// #103: the brief is a whole replacement of the Schema-declared fields; the
// stored values are normalized and a null member clears it.
func TestChatSessionBriefCanBeReadAndReplacedByTheOwner(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	read := grantTestRequest(t, handler, cookie, http.MethodGet, briefPath(sessionID), "", "")
	if read.Code != http.StatusOK || len(decodeBrief(t, read.Body.String())) != 0 {
		t.Fatalf("a new session has no brief: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID),
		`{"brief":{"purpose":"game_character","style":"  写实  ","needs_rig":true,"triangle_budget":40000}}`, "")
	if put.Code != http.StatusOK {
		t.Fatalf("write: %d %s", put.Code, put.Body.String())
	}
	stored := decodeBrief(t, put.Body.String())
	if string(stored["purpose"]) != `"game_character"` || string(stored["style"]) != `"写实"` ||
		string(stored["needs_rig"]) != "true" || string(stored["triangle_budget"]) != "40000" {
		t.Fatalf("stored brief = %v", stored)
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, briefPath(sessionID), "", "")
	if string(decodeBrief(t, reread.Body.String())["style"]) != `"写实"` {
		t.Fatalf("brief not persisted: %s", reread.Body.String())
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID), `{"brief":null}`, "")
	if cleared.Code != http.StatusOK || len(decodeBrief(t, cleared.Body.String())) != 0 {
		t.Fatalf("clear: %d %s", cleared.Code, cleared.Body.String())
	}
}

// A rejected brief never replaces the stored one.
func TestChatSessionBriefRejectsUndeclaredFieldsAndOutOfBoundsValues(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	if seed := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID),
		`{"brief":{"purpose":"render","style":"keep"}}`, ""); seed.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", seed.Code, seed.Body.String())
	}
	for _, body := range []string{
		`{"brief":{"unknown":"x"}}`,
		`{"brief":{"purpose":"no-such-profile"}}`,
		`{"brief":{"style":"` + strings.Repeat("设", 201) + `"}}`,
		`{"brief":{"triangle_budget":99}}`,
		`{"brief":{"triangle_budget":2000001}}`,
		`{"brief":{"needs_rig":"yes"}}`,
		`{"brief":{"style":7}}`,
		`{"brief":7}`,
		`not json`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.60q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	read := grantTestRequest(t, handler, cookie, http.MethodGet, briefPath(sessionID), "", "")
	stored := decodeBrief(t, read.Body.String())
	if string(stored["purpose"]) != `"render"` || string(stored["style"]) != `"keep"` {
		t.Fatalf("a rejected edit changed the brief: %s", read.Body.String())
	}
}

func TestChatSessionBriefIsOwnerScoped(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-brief", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReplaceSessionBrief(context.Background(), "owner-b", "foreign-brief",
		chat.ProductionBrief{}, chat.ProductionBrief{"purpose": json.RawMessage(`"print"`)}); err != nil {
		t.Fatal(err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, briefPath("foreign-brief"), "", "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath("foreign-brief"), `{"brief":{"purpose":"render"}}`, "")
	missing := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath("no-such-session"), `{"brief":{"purpose":"render"}}`, "")
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d/%d missing=%d", get.Code, put.Code, missing.Code)
	}
	if strings.Contains(get.Body.String(), `"print"`) {
		t.Fatalf("a foreign brief leaked: %s", get.Body.String())
	}
	if brief, found, err := repository.SessionBrief(context.Background(), "owner-b", "foreign-brief"); err != nil || !found ||
		brief.Text("purpose") != "print" {
		t.Fatalf("a foreign write changed the brief: %v found=%v err=%v", brief, found, err)
	}
}

// A real change is audited by field name only; writing the same brief again
// is not a change and writes no second entry.
func TestChatSessionBriefChangeIsAuditedByFieldNames(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	for range 2 {
		put := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID),
			`{"brief":{"purpose":"game_character","style":"写实"}}`, "")
		if put.Code != http.StatusOK {
			t.Fatalf("write: %d %s", put.Code, put.Body.String())
		}
	}
	events, err := repository.ListAdministrationAuditEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	for _, event := range events {
		if event.Action != "set_production_brief" {
			continue
		}
		changes++
		fields, _ := event.Detail["fields"].([]any)
		if event.ResourceID != sessionID || event.ActorID != "owner-a" || len(fields) != 2 ||
			fields[0] != "purpose" || fields[1] != "style" {
			t.Fatalf("audit event = %+v", event)
		}
	}
	if changes != 1 {
		t.Fatalf("audit entries = %d, want 1", changes)
	}
}

// The explicit 3D model is a bounded lock: the owner stores and clears it, a
// foreign session is a 404, and free text is refused.
func TestChatSessionExplicitModel3DIsAValidatedPerSessionLock(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	path := explicitModel3DPath(sessionID)
	read := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"explicit_model_3d":""`) {
		t.Fatalf("a new session has no explicit model: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, path, `{"explicit_model_3d":"tripo/tripo-p2/text-to-3d"}`, "")
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"explicit_model_3d":"tripo/tripo-p2/text-to-3d"`) {
		t.Fatalf("lock: %d %s", put.Code, put.Body.String())
	}
	for _, body := range []string{`{"explicit_model_3d":"has space"}`, `{"explicit_model_3d":7}`, `not json`} {
		if response := grantTestRequest(t, handler, cookie, http.MethodPut, path, body, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodPut, path, `{"explicit_model_3d":""}`, "")
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"explicit_model_3d":""`) {
		t.Fatalf("clear: %d %s", cleared.Code, cleared.Body.String())
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodPut, explicitModel3DPath("no-such-session"),
		`{"explicit_model_3d":"tripo/tripo-p2/text-to-3d"}`, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d %s", foreign.Code, foreign.Body.String())
	}
}

func explicitModel2DPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/model2d"
}

// #239: the explicit 2D model is a bounded lock: the owner stores and clears
// it, only an enabled image Offering is accepted, and a foreign session is a
// 404.
func TestChatSessionExplicitModel2DIsAValidatedPerSessionLock(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	path := explicitModel2DPath(sessionID)
	read := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"explicit_model_2d":""`) {
		t.Fatalf("a new session has no explicit 2D model: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, path,
		`{"explicit_model_2d":"meshy/nano-banana-pro/text-to-image"}`, "")
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"explicit_model_2d":"meshy/nano-banana-pro/text-to-image"`) {
		t.Fatalf("lock: %d %s", put.Code, put.Body.String())
	}
	for _, body := range []string{
		`{"explicit_model_2d":"has space"}`,
		// A 3D Offering is not a 2D model.
		`{"explicit_model_2d":"tripo/tripo-p2/text-to-3d"}`,
		`{"explicit_model_2d":7}`,
		`not json`,
	} {
		if response := grantTestRequest(t, handler, cookie, http.MethodPut, path, body, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status=%d %s", body, response.Code, response.Body.String())
		}
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodPut, path, `{"explicit_model_2d":""}`, "")
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"explicit_model_2d":""`) {
		t.Fatalf("clear: %d %s", cleared.Code, cleared.Body.String())
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodPut, explicitModel2DPath("no-such-session"),
		`{"explicit_model_2d":"meshy/nano-banana-pro/text-to-image"}`, "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d %s", foreign.Code, foreign.Body.String())
	}
}

// #239: the brief accepts the 2D purpose and rejects one that is not declared.
func TestChatSessionBriefAcceptsTheImagePurpose(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	put := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID),
		`{"brief":{"image_use":"illustration","image_transparent":true,"image_for_3d":false}}`, "")
	if put.Code != http.StatusOK {
		t.Fatalf("2D brief write: %d %s", put.Code, put.Body.String())
	}
	stored := decodeBrief(t, put.Body.String())
	if string(stored["image_use"]) != `"illustration"` || string(stored["image_transparent"]) != "true" {
		t.Fatalf("stored 2D brief = %v", stored)
	}
	if rejected := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(sessionID),
		`{"brief":{"image_use":"render"}}`, ""); rejected.Code != http.StatusBadRequest {
		t.Fatalf("a 3D purpose was accepted as a 2D purpose: %d %s", rejected.Code, rejected.Body.String())
	}
}

// httpExplicitModelValid mirrors the application's explicit-model check for
// the test harness: a pick must name an enabled Offering of the domain.
func httpExplicitModelValid(offerings []generation.Offering, model string, domain generation.CapabilityDomain) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, offering := range offerings {
		if !offering.Enabled || offering.Domain != domain {
			continue
		}
		if model == offering.ModelID || model == string(offering.ID) ||
			(offering.ModelID != "" && offering.ProviderID != "" && model == offering.ProviderID+"/"+offering.ModelID) {
			return true
		}
	}
	return false
}

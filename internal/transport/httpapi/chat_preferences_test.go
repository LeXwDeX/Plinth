package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func preferencesPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/preferences"
}

func preferenceItemPath(sessionID, preferenceID string) string {
	return preferencesPath(sessionID) + "/" + preferenceID
}

func decodePreferences(t *testing.T, body string) []map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("preferences body %q: %v", body, err)
	}
	items, present := decoded["preferences"]
	if !present {
		t.Fatalf("preferences member missing: %s", body)
	}
	list, _ := items.([]any)
	result := make([]map[string]any, 0, len(list))
	for _, item := range list {
		object, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("preference item is not an object: %s", body)
		}
		result = append(result, object)
	}
	return result
}

func TestPreferencesAreRecordedListedEditedAndDeleted(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	empty := grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath(sessionID), "", "")
	if empty.Code != http.StatusOK || len(decodePreferences(t, empty.Body.String())) != 0 {
		t.Fatalf("a new session has no preferences: %d %s", empty.Code, empty.Body.String())
	}
	// An explicit rejection names the candidate claim plus the Schema reason.
	rejected := grantTestRequest(t, handler, cookie, http.MethodPost, preferencesPath(sessionID),
		`{"kind":"image","taskId":"own-task","assetId":"own-asset","reasonPreset":"style","reasonText":"too realistic"}`, "")
	if rejected.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", rejected.Code, rejected.Body.String())
	}
	items := decodePreferences(t, rejected.Body.String())
	if len(items) != 1 || items[0]["kind"] != "rejected" || items[0]["source"] != "reject" ||
		items[0]["reasonPreset"] != "style" || items[0]["reasonText"] != "too realistic" ||
		items[0]["explicit"] != true || items[0]["active"] != true || items[0]["id"] == "" {
		t.Fatalf("rejected preference = %#v", items)
	}
	preferenceID := items[0]["id"].(string)
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath(sessionID), "", "")
	if items := decodePreferences(t, reread.Body.String()); len(items) != 1 || items[0]["id"] != preferenceID {
		t.Fatalf("preference not persisted: %s", reread.Body.String())
	}
	// A weak signal is explicit=false and own source; the unverified handle is
	// skipped instead of trusted.
	signal := grantTestRequest(t, handler, cookie, http.MethodPost, preferencesPath(sessionID),
		`{"signal":"regenerate","handles":[{"kind":"image","taskId":"own-task","assetId":"own-asset-2"},`+
			`{"kind":"image","taskId":"someone-elses-task","assetId":"someone-elses-asset"}]}`, "")
	if signal.Code != http.StatusOK {
		t.Fatalf("signal: %d %s", signal.Code, signal.Body.String())
	}
	signals := decodePreferences(t, signal.Body.String())
	if len(signals) != 1 || signals[0]["kind"] != "signal" || signals[0]["source"] != "regenerate" ||
		signals[0]["explicit"] != false {
		t.Fatalf("signals = %#v", signals)
	}
	// Edit the rejected record's direction and reason.
	put := grantTestRequest(t, handler, cookie, http.MethodPut, preferenceItemPath(sessionID, preferenceID),
		`{"direction":"an ink-wash fox","reasonPreset":"other","reasonText":"washed out"}`, "")
	if put.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", put.Code, put.Body.String())
	}
	edited := decodePreferences(t, put.Body.String())
	if len(edited) != 1 || edited[0]["direction"] != "an ink-wash fox" || edited[0]["reasonPreset"] != "other" ||
		edited[0]["reasonText"] != "washed out" || edited[0]["userEdited"] != true {
		t.Fatalf("edited = %#v", edited)
	}
	// Delete hides the record; the next list no longer contains it.
	deleted := grantTestRequest(t, handler, cookie, http.MethodDelete, preferenceItemPath(sessionID, preferenceID), "", "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	remaining := decodePreferences(t, grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath(sessionID), "", "").Body.String())
	for _, item := range remaining {
		if item["id"] == preferenceID {
			t.Fatalf("deleted preference still listed: %#v", remaining)
		}
	}
}

func TestPreferenceWritesRejectMalformedBodiesAndUnknownReasons(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	for _, body := range []string{
		`not json`, `{}`, `[]`,
		`{"kind":"image","taskId":"own-task","assetId":"own-asset","reasonPreset":"undeclared"}`,
		`{"kind":"image","taskId":"own-task","assetId":"own-asset","reasonText":"` + strings.Repeat("设", 201) + `"}`,
		`{"kind":"image","taskId":"someone-elses-task","assetId":"someone-elses-asset"}`,
		`{"signal":"approve","handles":[{"kind":"image","taskId":"own-task","assetId":"own-asset"}]}`,
		`{"signal":"regenerate","handles":[]}`,
		// Both write shapes at once are refused, not silently resolved.
		`{"signal":"regenerate","handles":[{"kind":"image","taskId":"own-task","assetId":"own-asset"}],` +
			`"kind":"image","taskId":"own-task","assetId":"own-asset"}`,
		`{"signal":"regenerate","handles":[{"kind":"image","taskId":"own-task","assetId":"own-asset"}],` +
			`"reasonPreset":"style"}`,
		`{"signal":"regenerate","handles":[{"kind":"image","taskId":"own-task","assetId":"own-asset"}],` +
			`"reasonText":"too realistic"}`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPost, preferencesPath(sessionID), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %.60q status=%d %s", body, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "someone-elses") {
			t.Fatalf("a foreign ID was echoed: %s", response.Body.String())
		}
	}
	// A declared rejection body is accepted; then malformed edits and unknown
	// presets are refused.
	created := grantTestRequest(t, handler, cookie, http.MethodPost, preferencesPath(sessionID),
		`{"kind":"image","taskId":"own-task","assetId":"own-asset","reasonPreset":"style"}`, "")
	if created.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", created.Code, created.Body.String())
	}
	id := decodePreferences(t, created.Body.String())[0]["id"].(string)
	for _, body := range []string{
		`not json`, `{"direction":7}`, `{"active":"yes"}`, `{"reasonPreset":"undeclared"}`,
		`{"direction":"` + strings.Repeat("设", 401) + `"}`,
	} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, preferenceItemPath(sessionID, id), body, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("edit body %.50q status=%d %s", body, response.Code, response.Body.String())
		}
	}
}

func TestPreferencesAreOwnerAndSessionScoped(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-session", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PutPreference(context.Background(), "owner-b", "foreign-session", chat.Preference{
		ID: "foreign-preference", Kind: chat.PreferenceKindRejected, Source: chat.PreferenceSourceReject,
		Direction: "secret direction", ReasonText: "secret reason", TaskID: "own-task", AssetID: "own-asset",
		Explicit: true, Active: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	get := grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath("foreign-session"), "", "")
	post := grantTestRequest(t, handler, cookie, http.MethodPost, preferencesPath("foreign-session"),
		`{"kind":"image","taskId":"own-task","assetId":"own-asset","reasonPreset":"style"}`, "")
	put := grantTestRequest(t, handler, cookie, http.MethodPut, preferenceItemPath("foreign-session", "foreign-preference"),
		`{"direction":"taken"}`, "")
	del := grantTestRequest(t, handler, cookie, http.MethodDelete, preferenceItemPath("foreign-session", "foreign-preference"), "", "")
	missing := grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath("no-such-session"), "", "")
	if get.Code != http.StatusNotFound || post.Code != http.StatusNotFound || put.Code != http.StatusNotFound ||
		del.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign get=%d post=%d put=%d delete=%d missing=%d",
			get.Code, post.Code, put.Code, del.Code, missing.Code)
	}
	if strings.Contains(get.Body.String(), "secret") {
		t.Fatalf("a foreign preference leaked: %s", get.Body.String())
	}
	if preferences, _, _ := repository.ListPreferences(context.Background(), "owner-b", "foreign-session"); len(preferences) != 1 ||
		preferences[0].Direction != "secret direction" {
		t.Fatalf("a foreign write changed the preference: %+v", preferences)
	}
	// A preference ID that belongs to another session of the same owner is the
	// same 404: the session in the path must own the record.
	otherSession := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"other"}`, "")
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(otherSession.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("second session = %s (%v)", otherSession.Body.String(), err)
	}
	if _, _, err := repository.PutPreference(context.Background(), "owner-a", created.ID, chat.Preference{
		ID: "other-session-preference", Kind: chat.PreferenceKindSelected, Source: chat.PreferenceSourcePick,
		Direction: "elsewhere", TaskID: "own-task", AssetID: "own-asset",
		Explicit: true, Active: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	crossSession := grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath(created.ID), "", "")
	if crossSession.Code != http.StatusOK || len(decodePreferences(t, crossSession.Body.String())) != 1 {
		t.Fatalf("the owner's other session = %d %s", crossSession.Code, crossSession.Body.String())
	}
	wrongSession := grantTestRequest(t, handler, cookie, http.MethodPut,
		preferenceItemPath(sessionID, "other-session-preference"), `{"direction":"taken"}`, "")
	if wrongSession.Code != http.StatusNotFound {
		t.Fatalf("a preference id from another session = %d %s", wrongSession.Code, wrongSession.Body.String())
	}
	if strings.Contains(wrongSession.Body.String(), "elsewhere") {
		t.Fatalf("another session's preference leaked: %s", wrongSession.Body.String())
	}
}

func TestPreferenceRoutesAreNotRegisteredWithoutTheSchema(t *testing.T) {
	t.Parallel()
	_, handler, cookie, sessionID := grantTestBrowserWithCatalog(t, func(application *catalog.ApplicationSchema) {
		application.Runtime.HTTP.Routes.ChatSessionPreferences = ""
		application.Runtime.HTTP.Routes.ChatSessionPreferenceItem = ""
	}, []string{"user"}, nil)
	response := grantTestRequest(t, handler, cookie, http.MethodGet, preferencesPath(sessionID), "", "")
	if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("an undeclared preference route answered %d %s", response.Code, response.Body.String())
	}
}

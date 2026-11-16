package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #306: before a step is confirmed the 加工 panel shows the value a parameter
// that follows the session purpose (#292) will take: the export texture size of
// a Tripo processing step. The session route answers from the same resolver the
// plan creation fills the step with, so what the panel shows is what the plan
// freezes. A session whose purpose gives the parameter nothing answers with the
// Offering's default. The read is owner scoped like the session's brief.

const purposeParametersRigging = "tripo/processing/rigging-v3"

func purposeParametersPath(session, offering string) string {
	return "/api/v1/chat/sessions/" + session + "/purpose-parameters?offering_id=" + url.QueryEscape(offering)
}

type purposeParametersView struct {
	SessionID  string                     `json:"session_id"`
	OfferingID string                     `json:"offering_id"`
	Parameters map[string]json.RawMessage `json:"parameters"`
}

func readPurposeParameters(t *testing.T, handler http.Handler, cookie *http.Cookie, session, offering string) purposeParametersView {
	t.Helper()
	response := grantTestRequest(t, handler, cookie, http.MethodGet, purposeParametersPath(session, offering), "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read %s of %s: %d %s", offering, session, response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
	var view purposeParametersView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatalf("body %s: %v", response.Body.String(), err)
	}
	if view.SessionID != session || view.OfferingID != offering {
		t.Fatalf("body %s names another session or Offering", response.Body.String())
	}
	return view
}

func TestChatSessionPurposeParametersGiveTheValueTheSessionPurposeSets(t *testing.T) {
	t.Parallel()
	_, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	for name, test := range map[string]struct {
		brief string // "" leaves the session without a brief
		want  string
	}{
		"a game character on a phone is 2K":        {`{"purpose":"game_character","target_platform":"mobile"}`, `"2k"`},
		"a game character on a PC is 4K":           {`{"purpose":"game_character","target_platform":"pc_console"}`, `"4k"`},
		"a game prop is 2K":                        {`{"purpose":"game_prop"}`, `"2k"`},
		"print is 4K":                              {`{"purpose":"print"}`, `"4k"`},
		"the display purpose bounds nothing: 8K":   {`{"purpose":"render"}`, `"8k"`},
		"a brief without a purpose is the default": {`{"style":"写实"}`, `"8k"`},
		"a session without a brief is the default": {``, `"8k"`},
	} {
		t.Run(name, func(t *testing.T) {
			session := styleLock187Session(t, handler, cookie, "purpose parameters")
			if test.brief != "" {
				directPurposeSetBrief(t, handler, cookie, session, test.brief)
			}
			for _, offering := range []string{purposeParametersRigging, "tripo/geometry/decimate-triangle"} {
				view := readPurposeParameters(t, handler, cookie, session, offering)
				if len(view.Parameters) != 1 || string(view.Parameters["export_texture_size"]) != test.want {
					t.Fatalf("%s: parameters = %v, want only export_texture_size %s", offering, view.Parameters, test.want)
				}
			}
		})
	}
}

// The preview follows the brief: the same session reads another value once the
// person (or the Agent) changes the purpose or the platform.
func TestChatSessionPurposeParametersFollowTheBrief(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	sizeOf := func() string {
		return string(readPurposeParameters(t, handler, cookie, session, purposeParametersRigging).Parameters["export_texture_size"])
	}
	if got := sizeOf(); got != `"8k"` {
		t.Fatalf("without a brief = %s, want the default 8K", got)
	}
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"mobile"}`)
	if got := sizeOf(); got != `"2k"` {
		t.Fatalf("a game character on a phone = %s, want 2K", got)
	}
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"pc_console"}`)
	if got := sizeOf(); got != `"4k"` {
		t.Fatalf("after the platform became a PC = %s, want 4K", got)
	}
	cleared := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(session), `{"brief":null}`, "")
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear brief: %d %s", cleared.Code, cleared.Body.String())
	}
	if got := sizeOf(); got != `"8k"` {
		t.Fatalf("after the brief was cleared = %s, want the default 8K", got)
	}
}

// What the panel is shown is what the plan it then creates freezes: the same
// session, the request shape the panel posts (the parameter left out), and the
// size the plan holds for the step.
func TestChatSessionPurposeParametersAreWhatThePlanFreezes(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	const decimation = generation.OfferingID("tripo/geometry/decimate-triangle")
	for name, test := range map[string]struct {
		brief  string
		shown  string
		frozen any // nil: the plan froze none and the Offering's default (the shown 8K) applies
	}{
		"a game character on a phone": {`{"purpose":"game_character","target_platform":"mobile"}`, "2k", "2k"},
		"a game character on a PC":    {`{"purpose":"game_character","target_platform":"pc_console"}`, "4k", "4k"},
		"no purpose":                  {`{"style":"写实"}`, "8k", nil},
	} {
		t.Run(name, func(t *testing.T) {
			session := styleLock187Session(t, handler, cookie, "purpose parameters")
			directPurposeSetBrief(t, handler, cookie, session, test.brief)
			view := readPurposeParameters(t, handler, cookie, session, string(decimation))
			if string(view.Parameters["export_texture_size"]) != `"`+test.shown+`"` {
				t.Fatalf("shown = %v, want %s", view.Parameters, test.shown)
			}
			source, asset := processingPurposeSource(t, module, session, "tripo/tripo-v3.1-full/text-to-3d",
				`{"prompt":"a knight","authorize_extra_credits":true}`)
			frozen := processingPurposePlan(t, module, handler, cookie, session, source, asset, decimation,
				map[string]any{"authorize_extra_credits": true}, "purpose-parameters-plan-"+session)
			if frozen["export_texture_size"] != test.frozen {
				t.Fatalf("the plan froze %v, want %v after showing %s", frozen["export_texture_size"], test.frozen, test.shown)
			}
		})
	}
}

// An Offering with nothing that follows a purpose has nothing to show, whatever
// the session's purpose gives other Offerings.
func TestChatSessionPurposeParametersOfAnOfferingWithNothingToFollowAreEmpty(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"mobile"}`)
	view := readPurposeParameters(t, handler, cookie, session, "meshy/processing/remesh")
	if len(view.Parameters) != 0 {
		t.Fatalf("parameters = %v, want none", view.Parameters)
	}
	// The member is present and empty, not omitted: a client reads it as "nothing to show".
	response := grantTestRequest(t, handler, cookie, http.MethodGet, purposeParametersPath(session, "meshy/processing/remesh"), "", "")
	if !strings.Contains(response.Body.String(), `"parameters":{}`) {
		t.Fatalf("body = %s, want an empty parameters object", response.Body.String())
	}
}

// The read is owner scoped like the session's brief: someone else's session and
// a missing one are the same 404, and nothing of the other brief leaks. Without
// the owner's login not even the owner's own session answers.
func TestChatSessionPurposeParametersAreOwnerScoped(t *testing.T) {
	t.Parallel()
	module, handler, cookie, own := grantTestBrowserWithApplication(t, nil, []string{"user"})
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-purpose", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ReplaceSessionBrief(context.Background(), "owner-b", "foreign-purpose",
		chat.ProductionBrief{}, chat.ProductionBrief{"purpose": json.RawMessage(`"print"`)}); err != nil {
		t.Fatal(err)
	}
	foreign := grantTestRequest(t, handler, cookie, http.MethodGet, purposeParametersPath("foreign-purpose", purposeParametersRigging), "", "")
	missing := grantTestRequest(t, handler, cookie, http.MethodGet, purposeParametersPath("no-such-session", purposeParametersRigging), "", "")
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d missing=%d, want 404 for both", foreign.Code, missing.Code)
	}
	if strings.Contains(foreign.Body.String(), "4k") || strings.Contains(foreign.Body.String(), "export_texture_size") {
		t.Fatalf("a foreign session's purpose leaked: %s", foreign.Body.String())
	}
	if purposeParametersRefusal(t, foreign.Body.String()) != purposeParametersRefusal(t, missing.Body.String()) {
		t.Fatalf("a foreign session is refused differently from a missing one: %s vs %s", foreign.Body.String(), missing.Body.String())
	}
	// An Offering with nothing to follow does not skip the session check.
	foreignPlain := grantTestRequest(t, handler, cookie, http.MethodGet, purposeParametersPath("foreign-purpose", "meshy/processing/remesh"), "", "")
	if foreignPlain.Code != http.StatusNotFound {
		t.Fatalf("a foreign session read for an Offering with nothing to follow = %d, want 404", foreignPlain.Code)
	}
	directPurposeSetBrief(t, handler, cookie, own, `{"purpose":"game_character","target_platform":"mobile"}`)
	anonymous := grantTestRequest(t, handler, nil, http.MethodGet, purposeParametersPath(own, purposeParametersRigging), "", "")
	// Without the owner's login the caller is someone else (the route answers
	// 401 or the same 404 a foreign session gets): never the purpose's value.
	if anonymous.Code != http.StatusUnauthorized && anonymous.Code != http.StatusNotFound || strings.Contains(anonymous.Body.String(), "export_texture_size") {
		t.Fatalf("without a login = %d %s", anonymous.Code, anonymous.Body.String())
	}
}

// purposeParametersRefusal is a refusal body without the request id, which differs
// from one request to the next.
func purposeParametersRefusal(t *testing.T, body string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("refusal %q: %v", body, err)
	}
	delete(decoded, "request_id")
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// The route only reads: the brief is changed through its own route.
func TestChatSessionPurposeParametersAreReadOnly(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		response := grantTestRequest(t, handler, cookie, method, purposeParametersPath(session, purposeParametersRigging), "{}", "")
		if response.Code != http.StatusMethodNotAllowed && response.Code != http.StatusNotFound {
			t.Fatalf("%s = %d %s, want the route to refuse it", method, response.Code, response.Body.String())
		}
	}
}

// The query names one Offering the Schema declares; anything else is refused
// before the session's brief is read.
func TestChatSessionPurposeParametersRefuseAnUnknownOffering(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	for name, path := range map[string]string{
		"no offering":       "/api/v1/chat/sessions/" + session + "/purpose-parameters",
		"an empty offering": "/api/v1/chat/sessions/" + session + "/purpose-parameters?offering_id=",
		"an unknown one":    purposeParametersPath(session, "tripo/processing/does-not-exist"),
	} {
		t.Run(name, func(t *testing.T) {
			response := grantTestRequest(t, handler, cookie, http.MethodGet, path, "", "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d %s, want 400", path, response.Code, response.Body.String())
			}
		})
	}
}

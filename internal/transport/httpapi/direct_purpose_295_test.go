package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #295: a direct task request follows the session's production brief the way
// the Agent does. The purpose's, platform's and engine's values fill the
// parameters the request left unset and freeze with the purpose as their source
// and its reason; a session without a purpose is left exactly as it was; and a
// caller can explain an advised change of its own with parameter_reasons.

const directPurposeMeshy = "meshy/meshy-7.1/text-to-3d"

// directPurposeApplication gives the game character purpose values of its own on
// one Offering, and a variant for one platform, so the assertions do not depend
// on the numbers the shipped Catalog declares today.
func directPurposeApplication(application *catalog.ApplicationSchema) {
	usage := application.Chat.Model3DUsage
	profiles := append([]catalog.ChatModel3DUsageProfile(nil), usage.Profiles...)
	maximum := 100000
	for index := range profiles {
		if profiles[index].ID != "game_character" {
			continue
		}
		overrides := make(map[string]map[string]json.RawMessage, len(profiles[index].ParameterOverrides)+1)
		for offering, values := range profiles[index].ParameterOverrides {
			overrides[offering] = values
		}
		overrides[directPurposeMeshy] = map[string]json.RawMessage{
			"should_remesh": json.RawMessage(`true`), "topology": json.RawMessage(`"quad"`),
			"target_polycount": json.RawMessage(`12000`), "texture_resolution": json.RawMessage(`"2k"`),
			"pose_mode": json.RawMessage(`"a-pose"`),
		}
		profiles[index].ParameterOverrides = overrides
		profiles[index].Platforms = []catalog.ChatModel3DUsagePlatform{{
			IDs:    []string{"pc_console"},
			Budget: catalog.ChatModel3DUsagePlatformBudget{MaxTriangles: &maximum},
			ParameterOverrides: map[string]map[string]json.RawMessage{
				directPurposeMeshy: {"target_polycount": json.RawMessage(`33000`)},
			},
		}}
	}
	usage.Profiles = profiles
	application.Chat.Model3DUsage = usage
}

func directPurposeReason(t *testing.T, purpose string) string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range product.Application().Chat.Model3DUsage.Profiles {
		if profile.ID == purpose {
			if profile.Reason == "" {
				t.Fatalf("%s declares no reason", purpose)
			}
			return profile.Reason
		}
	}
	t.Fatalf("purpose %s is not declared", purpose)
	return ""
}

func directPurposeSetBrief(t *testing.T, handler http.Handler, cookie *http.Cookie, session, brief string) {
	t.Helper()
	response := grantTestRequest(t, handler, cookie, http.MethodPut, briefPath(session), `{"brief":`+brief+`}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("set brief %s: %d %s", brief, response.Code, response.Body.String())
	}
}

type directPurposeAdjustment struct {
	Name    string          `json:"name"`
	Value   json.RawMessage `json:"value"`
	Reason  string          `json:"reason"`
	Source  string          `json:"source"`
	Profile string          `json:"profile"`
}

type directPurposeView struct {
	ID          string                    `json:"prepared_input_id"`
	Adjustments []directPurposeAdjustment `json:"parameter_adjustments"`
}

// directPurposePrepare posts a direct prepare for the Offering with the input
// and any further body members (parameter_reasons).
func directPurposePrepare(t *testing.T, handler http.Handler, cookie *http.Cookie, session, capability, offering string,
	input map[string]any, members map[string]any) (*directPurposeView, int, string) {
	t.Helper()
	body := map[string]any{"session_id": session, "capability": capability, "offering_id": offering, "input": input}
	for name, value := range members {
		body[name] = value
	}
	response := styleLock187Prepare(t, handler, cookie, body)
	var view directPurposeView
	if response.Code == http.StatusCreated {
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || view.ID == "" {
			t.Fatalf("prepare body %s: %v", response.Body.String(), err)
		}
	}
	return &view, response.Code, response.Body.String()
}

func directPurposeFrozen(t *testing.T, module *generation.Module, id string) (map[string]any, []generation.ParameterAdjustment) {
	t.Helper()
	prepared := styleLock187Prepared(t, module, id)
	var input map[string]any
	if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
		t.Fatalf("normalized input %s: %v", prepared.NormalizedInput, err)
	}
	return input, prepared.ParameterAdjustments
}

func directPurposeNames(adjustments []directPurposeAdjustment) []string {
	names := make([]string, 0, len(adjustments))
	for _, adjustment := range adjustments {
		names = append(names, adjustment.Name)
	}
	sort.Strings(names)
	return names
}

func directPurposeAdjustmentOf(adjustments []directPurposeAdjustment, name string) (directPurposeAdjustment, bool) {
	for _, adjustment := range adjustments {
		if adjustment.Name == name {
			return adjustment, true
		}
	}
	return directPurposeAdjustment{}, false
}

// The same game character session freezes the same values whether the Agent or
// a direct request starts the step: the purpose's overrides fill what the
// request left unset, the advised ones as adjustments of source profile:<id>
// with the purpose's reason, the others as plain values.
func TestDirectPrepareFreezesTheSessionPurposeLikeTheAgent(t *testing.T) {
	t.Parallel()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, directPurposeApplication, []string{"user"})
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character"}`)
	reason := directPurposeReason(t, "game_character")

	view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy,
		map[string]any{"prompt": "a knight"}, nil)
	if status != http.StatusCreated {
		t.Fatalf("prepare: %d %s", status, body)
	}
	// pose_mode is not advised, and should_remesh is forced true by the Offering
	// once the face count is not its default, so its recommended value is true.
	wantNames := []string{"target_polycount", "texture_resolution", "topology"}
	if got := directPurposeNames(view.Adjustments); strings.Join(got, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("adjustments = %v, want %v: %s", got, wantNames, body)
	}
	for _, adjustment := range view.Adjustments {
		if adjustment.Source != "profile:game_character" || adjustment.Profile != "game_character" || adjustment.Reason != reason {
			t.Fatalf("adjustment %s = %+v, want the purpose as source with its reason", adjustment.Name, adjustment)
		}
	}
	frozen, adjustments := directPurposeFrozen(t, module, view.ID)
	for name, want := range map[string]any{
		"should_remesh": true, "topology": "quad", "target_polycount": float64(12000),
		"texture_resolution": "2k", "pose_mode": "a-pose", "prompt": "a knight",
	} {
		if frozen[name] != want {
			t.Fatalf("frozen %s = %v, want %v (input %v)", name, frozen[name], want, frozen)
		}
	}
	if len(adjustments) != len(wantNames) {
		t.Fatalf("frozen adjustments = %+v", adjustments)
	}
	for _, adjustment := range adjustments {
		if adjustment.Source != "profile:game_character" || adjustment.Reason != reason {
			t.Fatalf("frozen adjustment %s = %+v", adjustment.Name, adjustment)
		}
	}
}

// The brief's target platform picks the purpose's variant, as for the Agent.
func TestDirectPrepareFollowsTheBriefPlatform(t *testing.T) {
	t.Parallel()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, directPurposeApplication, []string{"user"})
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"pc_console"}`)
	view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy,
		map[string]any{"prompt": "a knight"}, nil)
	if status != http.StatusCreated {
		t.Fatalf("prepare: %d %s", status, body)
	}
	frozen, _ := directPurposeFrozen(t, module, view.ID)
	if frozen["target_polycount"] != float64(33000) {
		t.Fatalf("target_polycount = %v, want the platform variant 33000", frozen["target_polycount"])
	}
	if adjustment, found := directPurposeAdjustmentOf(view.Adjustments, "target_polycount"); !found ||
		adjustment.Source != "profile:game_character" {
		t.Fatalf("target_polycount adjustment = %+v %v", adjustment, found)
	}
}

// A session with no brief, or a brief that names no purpose, is prepared exactly
// as before: the frozen input equals what the kernel freezes for the request.
func TestDirectPrepareWithoutAPurposeIsUnchanged(t *testing.T) {
	t.Parallel()
	module, handler, cookie, withoutBrief := grantTestBrowserWithApplication(t, directPurposeApplication, []string{"user"})
	withStyle := styleLock187Session(t, handler, cookie, "style only")
	directPurposeSetBrief(t, handler, cookie, withStyle, `{"style":"写实"}`)
	baseline, err := module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: withoutBrief, Capability: generation.TextTo3D,
		OfferingID: directPurposeMeshy, Input: json.RawMessage(`{"prompt":"a knight"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, session := range map[string]string{"no brief": withoutBrief, "a brief without a purpose": withStyle} {
		t.Run(name, func(t *testing.T) {
			view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy,
				map[string]any{"prompt": "a knight"}, nil)
			if status != http.StatusCreated {
				t.Fatalf("prepare: %d %s", status, body)
			}
			if len(view.Adjustments) != 0 || strings.Contains(body, "parameter_adjustments") {
				t.Fatalf("a session without a purpose froze adjustments: %s", body)
			}
			prepared := styleLock187Prepared(t, module, view.ID)
			if !bytes.Equal(prepared.NormalizedInput, baseline.NormalizedInput) || len(prepared.ParameterAdjustments) != 0 {
				t.Fatalf("frozen input = %s, want the kernel's %s", prepared.NormalizedInput, baseline.NormalizedInput)
			}
		})
	}
}

// A caller explains an advised change of its own with parameter_reasons: refused
// without one, accepted with one (source agent), and a reason for a parameter
// that is not advised or does not exist is refused.
func TestDirectPrepareTakesReasonsForAdvisedChanges(t *testing.T) {
	t.Parallel()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, directPurposeApplication, []string{"user"})
	input := map[string]any{"prompt": "a knight", "enable_pbr": false}

	if _, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy, input, nil); status != http.StatusBadRequest ||
		!strings.Contains(body, `"code":"invalid_input"`) {
		t.Fatalf("an advised change without a reason: %d %s", status, body)
	}
	view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy, input,
		map[string]any{"parameter_reasons": map[string]string{"enable_pbr": "this model is flat shaded"}})
	if status != http.StatusCreated {
		t.Fatalf("an advised change with a reason: %d %s", status, body)
	}
	adjustment, found := directPurposeAdjustmentOf(view.Adjustments, "enable_pbr")
	if !found || adjustment.Source != "agent" || adjustment.Profile != "" || adjustment.Reason != "this model is flat shaded" ||
		string(adjustment.Value) != "false" {
		t.Fatalf("enable_pbr adjustment = %+v %v", adjustment, found)
	}
	frozen, adjustments := directPurposeFrozen(t, module, view.ID)
	if frozen["enable_pbr"] != false || len(adjustments) != 1 || adjustments[0].Reason != "this model is flat shaded" {
		t.Fatalf("frozen = %v %+v", frozen, adjustments)
	}
	for name, reasons := range map[string]map[string]string{
		"a parameter that is not advised": {"pose_mode": "because"},
		"a parameter that does not exist": {"no_such_parameter": "because"},
	} {
		t.Run(name, func(t *testing.T) {
			_, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy,
				map[string]any{"prompt": "a knight"}, map[string]any{"parameter_reasons": reasons})
			if status != http.StatusBadRequest {
				t.Fatalf("a reason for %s: %d %s", name, status, body)
			}
		})
	}
}

// What the caller sets stays the caller's, even when the purpose would give the
// same value: an advised change then needs the caller's own reason, while the
// values the purpose still supplies keep its source.
func TestDirectPrepareKeepsACallersAdvisedValueTheCallers(t *testing.T) {
	t.Parallel()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, directPurposeApplication, []string{"user"})
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character"}`)
	// 12000 is the very value the purpose gives this Offering.
	input := map[string]any{"prompt": "a knight", "target_polycount": 12000}

	if _, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy, input, nil); status != http.StatusBadRequest ||
		!strings.Contains(body, `"code":"invalid_input"`) {
		t.Fatalf("a caller's advised value without a reason: %d %s", status, body)
	}
	view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy, input,
		map[string]any{"parameter_reasons": map[string]string{"target_polycount": "match the rig budget"}})
	if status != http.StatusCreated {
		t.Fatalf("a caller's advised value with a reason: %d %s", status, body)
	}
	polycount, found := directPurposeAdjustmentOf(view.Adjustments, "target_polycount")
	if !found || polycount.Source != "agent" || polycount.Reason != "match the rig budget" {
		t.Fatalf("target_polycount adjustment = %+v %v", polycount, found)
	}
	for _, name := range []string{"topology", "texture_resolution"} {
		adjustment, found := directPurposeAdjustmentOf(view.Adjustments, name)
		if !found || adjustment.Source != "profile:game_character" {
			t.Fatalf("%s adjustment = %+v %v", name, adjustment, found)
		}
	}
	if frozen, _ := directPurposeFrozen(t, module, view.ID); frozen["target_polycount"] != float64(12000) {
		t.Fatalf("frozen = %v", frozen)
	}
}

// The shipped Catalog: for every override the game character purpose declares on
// an Offering, a direct request freezes it, and each advised one that differs
// from the Offering's default carries the purpose as its source. The numbers are
// read from the Catalog, so a retuned purpose keeps this test true.
func TestDirectPrepareFreezesTheShippedGamePurpose(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, found := product.Get(directPurposeMeshy)
	if !found {
		t.Fatalf("%s missing", directPurposeMeshy)
	}
	var overrides map[string]json.RawMessage
	for _, profile := range product.Application().Chat.Model3DUsage.Profiles {
		if profile.ID == "game_character" {
			overrides = profile.ParameterOverrides[directPurposeMeshy]
		}
	}
	if len(overrides) == 0 {
		t.Fatalf("game_character no longer overrides %s: pick another Offering", directPurposeMeshy)
	}
	module, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character"}`)
	reason := directPurposeReason(t, "game_character")
	view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-3d", directPurposeMeshy,
		map[string]any{"prompt": "a knight"}, nil)
	if status != http.StatusCreated {
		t.Fatalf("prepare: %d %s", status, body)
	}
	frozen, _ := directPurposeFrozen(t, module, view.ID)
	for name, raw := range overrides {
		var want any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if got := frozen[name]; !sameDirectPurposeValue(got, want) {
			t.Fatalf("frozen %s = %v, want the purpose's %v", name, got, want)
		}
	}
	// The request set nothing, so every adjustment is the purpose's: an advised
	// override of the Offering, with the purpose's reason.
	if len(view.Adjustments) == 0 {
		t.Fatalf("the purpose froze no adjustment: %s", body)
	}
	for _, adjustment := range view.Adjustments {
		if _, declared := overrides[adjustment.Name]; !declared || !offering.AdvisedParameter(adjustment.Name) ||
			adjustment.Source != "profile:game_character" || adjustment.Profile != "game_character" || adjustment.Reason != reason {
			t.Fatalf("adjustment %+v is not an advised purpose override", adjustment)
		}
	}
}

func sameDirectPurposeValue(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

// A 2D call follows the brief's 2D purpose when it names one and nothing
// otherwise: the Agent's fallback to the Schema's default purpose does not reach
// a direct request, whose session never chose one.
func TestDirectPrepareFollowsOnlyAnExplicit2DPurpose(t *testing.T) {
	t.Parallel()
	const gemini = "gemini/gemini-3-pro-image/text-to-image"
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	wanted := ""
	for _, profile := range product.Application().Chat.Image2DUsage.Profiles {
		if profile.ID == "illustration" {
			var ratio string
			if raw, ok := profile.ParameterOverrides[gemini]["aspect_ratio"]; ok && json.Unmarshal(raw, &ratio) == nil {
				wanted = ratio
			}
		}
	}
	if wanted == "" {
		t.Fatalf("illustration no longer overrides the aspect ratio of %s: pick another Offering", gemini)
	}
	module, handler, cookie, plain := grantTestBrowserWithApplication(t, nil, []string{"user"})
	illustrated := styleLock187Session(t, handler, cookie, "illustration")
	directPurposeSetBrief(t, handler, cookie, illustrated, `{"image_use":"illustration"}`)
	threeD := styleLock187Session(t, handler, cookie, "3d only")
	directPurposeSetBrief(t, handler, cookie, threeD, `{"purpose":"game_character"}`)

	frozenRatio := func(session string) any {
		view, status, body := directPurposePrepare(t, handler, cookie, session, "text-to-image", gemini,
			map[string]any{"prompt": "a knight"}, nil)
		if status != http.StatusCreated {
			t.Fatalf("prepare: %d %s", status, body)
		}
		frozen, _ := directPurposeFrozen(t, module, view.ID)
		return frozen["aspect_ratio"]
	}
	if got := frozenRatio(illustrated); got != wanted {
		t.Fatalf("aspect_ratio with an explicit 2D purpose = %v, want %s", got, wanted)
	}
	// The Offering's own default: neither a session without a brief nor one with a
	// 3D purpose only picks up the default 2D purpose's frame.
	if got := frozenRatio(plain); got != "1:1" {
		t.Fatalf("aspect_ratio without a brief = %v, want the Offering default 1:1", got)
	}
	if got := frozenRatio(threeD); got != "1:1" {
		t.Fatalf("aspect_ratio with a 3D purpose only = %v, want the Offering default 1:1", got)
	}
}

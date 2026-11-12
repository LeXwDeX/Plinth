package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #292: the 加工 panel leaves the export texture size out of a step until the
// person picks one, and the plan follows the session purpose for it: the session
// brief fills the parameter the request omitted, the way the Agent and the direct
// task API resolve it. A size the person picked is theirs, even when it is the
// default, and a session without a purpose, an Offering with nothing that follows
// a purpose and every other parameter of the step are exactly what they were.

// processingPurposeSource runs a text-to-3D generation of the Offering to
// success in the session through the fixture providers, and returns the task and
// the saved model file a plan step starts from.
func processingPurposeSource(t *testing.T, module *generation.Module, session string, offering generation.OfferingID,
	input string) (generation.Task, generation.AssetID) {
	t.Helper()
	ctx := context.Background()
	accepted, err := submitHarnessTask(t, module, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", SessionID: session, Capability: generation.TextTo3D, OfferingID: offering,
		Input: []byte(input), IdempotencyKey: "processing-purpose-source-" + session,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{
			WorkerID: "processing-purpose", LeaseFor: time.Minute, BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(ctx, "owner-a", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded {
			for _, asset := range task.Assets {
				if asset.Role == "model" && asset.StorageKey != "" {
					return task, asset.ID
				}
			}
			t.Fatalf("the source has no saved model: %+v", task.Assets)
		}
		if task.State.Terminal() {
			t.Fatalf("source task = %#v", task)
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("the source task did not succeed in time")
	return generation.Task{}, ""
}

// processingPurposeBody is the request body the panel posts for a one-step plan.
func processingPurposeBody(t *testing.T, session string, source generation.Task, asset generation.AssetID,
	offering generation.OfferingID, input map[string]any) string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared, ok := product.Get(offering)
	if !ok {
		t.Fatalf("missing Offering %s", offering)
	}
	body, err := json.Marshal(map[string]any{
		"session_id": session, "source_task_id": source.ID, "source_asset_id": asset,
		"steps": []any{map[string]any{"offering_id": offering, "expected_offering_revision": declared.Revision, "input": input}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// processingPurposePlan posts a one-step plan from the panel's request shape and
// returns the input the plan froze for the step.
func processingPurposePlan(t *testing.T, module *generation.Module, handler http.Handler, cookie *http.Cookie, session string,
	source generation.Task, asset generation.AssetID, offering generation.OfferingID, input map[string]any, key string) map[string]any {
	t.Helper()
	body := processingPurposeBody(t, session, source, asset, offering, input)
	response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/processing-plans", body, key)
	if response.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("plan = %s: %v", response.Body.String(), err)
	}
	value, found := fixtureExecutions.Load(module)
	if !found {
		t.Fatal("missing fixture repository")
	}
	stored, err := value.(*fixtureExecutionState).repository.GetProcessingPlan(context.Background(), "owner-a", created.ID)
	if err != nil || len(stored.Steps) != 1 {
		t.Fatalf("stored plan = %+v: %v", stored, err)
	}
	var frozen map[string]any
	if err := json.Unmarshal(stored.Steps[0].Input, &frozen); err != nil {
		t.Fatalf("frozen input %s: %v", stored.Steps[0].Input, err)
	}
	return frozen
}

func TestProcessingPlanFillsTheExportSizeTheSessionPurposeGives(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	const decimation = generation.OfferingID("tripo/geometry/decimate-triangle")
	for name, test := range map[string]struct {
		brief string
		input map[string]any
		// want is the size the plan froze; nil means the plan froze none and left the Offering's default to apply.
		want any
	}{
		"a game character on a phone gets 2K": {`{"purpose":"game_character","target_platform":"mobile"}`, map[string]any{"authorize_extra_credits": true}, "2k"},
		"a game character on a PC gets 4K":    {`{"purpose":"game_character","target_platform":"pc_console"}`, map[string]any{"authorize_extra_credits": true}, "4k"},
		"a game prop gets 2K":                 {`{"purpose":"game_prop"}`, map[string]any{"authorize_extra_credits": true}, "2k"},
		"a picked size equal to the default":  {`{"purpose":"game_character","target_platform":"mobile"}`, map[string]any{"authorize_extra_credits": true, "export_texture_size": "8k"}, "8k"},
		"a picked size above the purpose's":   {`{"purpose":"game_character","target_platform":"mobile"}`, map[string]any{"authorize_extra_credits": true, "export_texture_size": "4k"}, "4k"},
		"the display purpose bounds nothing":  {`{"purpose":"render"}`, map[string]any{"authorize_extra_credits": true}, nil},
		"a brief without a purpose":           {`{"style":"写实"}`, map[string]any{"authorize_extra_credits": true}, nil},
		"a session without a brief":           {``, map[string]any{"authorize_extra_credits": true}, nil},
		"a picked size without a brief":       {``, map[string]any{"authorize_extra_credits": true, "export_texture_size": "2k"}, "2k"},
	} {
		t.Run(name, func(t *testing.T) {
			session := styleLock187Session(t, handler, cookie, "processing purpose")
			if test.brief != "" {
				directPurposeSetBrief(t, handler, cookie, session, test.brief)
			}
			source, asset := processingPurposeSource(t, module, session, "tripo/tripo-v3.1-full/text-to-3d",
				`{"prompt":"a knight","authorize_extra_credits":true}`)
			frozen := processingPurposePlan(t, module, handler, cookie, session, source, asset, decimation, test.input,
				"processing-purpose-plan-"+session)
			want := map[string]any{}
			for field, value := range test.input {
				want[field] = value
			}
			if test.want != nil {
				want["export_texture_size"] = test.want
			}
			// Only the size follows the purpose: nothing else of the step changed.
			if !reflect.DeepEqual(frozen, want) {
				t.Fatalf("the plan froze %v, want %v", frozen, want)
			}
		})
	}
}

// An Offering with no parameter that follows a purpose is not touched, whatever
// the session's purpose gives other Offerings.
func TestProcessingPlanOfAnOfferingWithNothingToFollowIsUntouched(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	session := styleLock187Session(t, handler, cookie, "processing purpose")
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"mobile"}`)
	source, asset := processingPurposeSource(t, module, session, "meshy/meshy-7.1/text-to-3d", `{"prompt":"a knight"}`)
	frozen := processingPurposePlan(t, module, handler, cookie, session, source, asset, "meshy/processing/remesh",
		map[string]any{"topology": "quad", "target_polycount": 20000}, "processing-purpose-plan-"+session)
	if !reflect.DeepEqual(frozen, map[string]any{"topology": "quad", "target_polycount": float64(20000)}) {
		t.Fatalf("the plan froze %v, want exactly the request", frozen)
	}
}

// The purpose is the session's own: a plan for a session the owner does not hold
// is refused as before, ahead of any purpose.
func TestProcessingPlanRefusesASessionTheOwnerDoesNotHold(t *testing.T) {
	t.Parallel()
	module, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"mobile"}`)
	source, asset := processingPurposeSource(t, module, session, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a knight","authorize_extra_credits":true}`)
	body, err := json.Marshal(map[string]any{
		"session_id": "somebody-elses-session", "source_task_id": source.ID, "source_asset_id": asset,
		"steps": []any{map[string]any{"offering_id": "tripo/geometry/decimate-triangle", "expected_offering_revision": "x",
			"input": map[string]any{"authorize_extra_credits": true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/processing-plans", string(body), "processing-purpose-foreign-key")
	if response.Code != http.StatusNotFound {
		t.Fatalf("a foreign session = %d %s, want 404", response.Code, response.Body.String())
	}
}

// The plan key is the client's retry handle, and the size the server filled is
// part of what the key names: the same body under the same brief returns the same
// plan, and a replay after the brief moved to another purpose is a conflict
// instead of a quietly different size.
func TestProcessingPlanReplayNamesTheSizeTheServerFilled(t *testing.T) {
	t.Parallel()
	module, handler, cookie, _ := grantTestBrowserWithApplication(t, nil, []string{"user"})
	session := styleLock187Session(t, handler, cookie, "processing purpose")
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"mobile"}`)
	source, asset := processingPurposeSource(t, module, session, "tripo/tripo-v3.1-full/text-to-3d",
		`{"prompt":"a knight","authorize_extra_credits":true}`)
	body := processingPurposeBody(t, session, source, asset, "tripo/geometry/decimate-triangle",
		map[string]any{"authorize_extra_credits": true})
	key := "processing-purpose-replay-" + session
	post := func() *httptest.ResponseRecorder {
		return grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/processing-plans", body, key)
	}
	planID := func(response *httptest.ResponseRecorder) string {
		t.Helper()
		var plan struct {
			ID string `json:"id"`
		}
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &plan) != nil || plan.ID == "" {
			t.Fatalf("plan: %d %s", response.Code, response.Body.String())
		}
		return plan.ID
	}
	first := planID(post())
	if replay := planID(post()); replay != first {
		t.Fatalf("a replay under the same brief made plan %s, want %s", replay, first)
	}
	directPurposeSetBrief(t, handler, cookie, session, `{"purpose":"game_character","target_platform":"pc_console"}`)
	if moved := post(); moved.Code != http.StatusConflict {
		t.Fatalf("a replay after the purpose moved = %d %s, want 409", moved.Code, moved.Body.String())
	}
}

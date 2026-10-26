package tripo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Official v3 endpoint contracts, checked 2026-10-17:
// https://developers.tripo3d.ai/en/docs/mesh-segment
// https://developers.tripo3d.ai/en/docs/mesh-complete
// https://developers.tripo3d.ai/en/docs/mesh-decimate
// https://developers.tripo3d.ai/en/docs/models-texture
// https://developers.tripo3d.ai/en/docs/animations-rig
// https://developers.tripo3d.ai/en/docs/animations-retarget
// https://developers.tripo3d.ai/en/docs/animations-rig-check
// Shapes/enums come from those pages. All task IDs, prompts, URLs and responses
// below are synthetic examples, not recordings or proof of account eligibility.
func simpleNativeRequest(kind string) generation.StartRequest {
	switch kind {
	case "texture":
		return nativeProcessingFixture(generation.Retexture)
	case "rig":
		return nativeProcessingFixture(generation.Rigging)
	case "retarget":
		return nativeProcessingFixture(generation.Animation)
	case "rig-check":
		return rigCheckFixture()
	default:
		return geometryFixture(kind)
	}
}

var simpleNativeKinds = []string{"segment", "complete", "quick-cap", "decimate-triangle", "decimate-quad", "texture", "rig", "retarget", "rig-check"}

func TestNativeSimpleContractAcceptsLegalContentVariants(t *testing.T) {
	for _, kind := range simpleNativeKinds {
		for i, prompt := range []string{"blue ceramic", "red wooden toy", "石质雕像"} {
			t.Run(fmt.Sprintf("%s/%d", kind, i), func(t *testing.T) {
				req := simpleNativeRequest(kind)
				source := fmt.Sprintf("task_source_%d", i)
				remote := fmt.Sprintf("task_result_%d", i)
				locator := fmt.Sprintf("https://assets.example.test/models/%d/result.glb?signature=example%d", i, i)
				dep := req.TaskDependencies["source_task"]
				dep.UpstreamJob.RemoteID = source
				if dep.ProviderBinding != nil {
					dep.ProviderBinding = nativeRigBindingFixture(dep)
				}
				req.TaskDependencies["source_task"] = dep
				var input map[string]any
				if err := json.Unmarshal(req.Input, &input); err != nil {
					t.Fatal(err)
				}
				if kind == "texture" {
					input["prompt"] = prompt
				}
				if kind == "segment" {
					input["segmentation_granularity"] = []string{"simple", "balanced", "detailed"}[i]
				}
				if strings.HasPrefix(kind, "decimate") {
					input["face_limit"] = []int{500, 2400, 10000}[i]
				}
				req.Input, _ = json.Marshal(input)
				// Independent official routes: never derive the oracle from the Offering.
				endpoint := map[string]string{
					"segment": "/v3/mesh/segment", "complete": "/v3/mesh/complete",
					"quick-cap": "/v3/mesh/complete", "decimate-triangle": "/v3/mesh/decimate",
					"decimate-quad": "/v3/mesh/decimate", "texture": "/v3/models/texture",
					"rig": "/v3/animations/rig", "retarget": "/v3/animations/retarget",
					"rig-check": "/v3/animations/rig-check",
				}[kind]
				if endpoint == "" {
					t.Fatalf("missing official route for %q", kind)
				}
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					switch r.Method + " " + r.URL.Path {
					case "POST " + endpoint:
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							http.Error(w, "invalid JSON", 400)
							return
						}
						if body["input"] != source {
							t.Errorf("required input string lost: %#v", body)
						}
						if _, exists := body["source_task"]; exists {
							t.Error("platform task field leaked")
						}
						switch kind {
						case "segment":
							if body["model"] != "v2.0-20260430" || body["segmentation_granularity"] != input["segmentation_granularity"] || body["split_by_connectivity"] != true {
								t.Errorf("segment shape: %#v", body)
							}
						case "complete", "quick-cap":
							mode := "ai_completion"
							if kind == "quick-cap" {
								mode = "quick_cap"
							}
							if body["model"] != "v1.0-20250506" || body["completion_mode"] != mode {
								t.Errorf("complete shape: %#v", body)
							}
						case "decimate-triangle", "decimate-quad":
							count, ok := body["face_limit"].(float64)
							if !ok || count != float64(input["face_limit"].(int)) || body["model"] != "v3.0" || body["quad"] != (kind == "decimate-quad") || body["bake"] != true || body["smart_uv"] != true {
								t.Errorf("decimate shape: %#v", body)
							}
						case "texture":
							nested, ok := body["texture_prompt"].(map[string]any)
							if !ok || nested["text"] != prompt || body["model"] != "v3.5-20260815" || body["texture_quality"] != "extreme" || body["pbr"] != true || body["bake"] != true || body["delight"] != true {
								t.Errorf("texture shape: %#v", body)
							}
						case "rig":
							if body["model"] != "v2.5-20260210" || body["rig_type"] != "biped" || body["spec"] != "tripo" || body["out_format"] != "glb" {
								t.Errorf("rig shape: %#v", body)
							}
						case "retarget":
							// bake_animation is documented as deprecated and ignored; it is not sent.
							_, bake := body["bake_animation"]
							_, model := body["model"]
							if bake || model || body["animation"] != "preset:walk" || body["out_format"] != "glb" || body["export_with_geometry"] != true || body["animate_in_place"] != false {
								t.Errorf("retarget shape: %#v", body)
							}
						case "rig-check":
							if len(body) != 1 {
								t.Errorf("rig-check shape: %#v", body)
							}
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"task_id": remote}})
					case "GET /v3/tasks/" + remote:
						output := map[string]any{"model_url": locator}
						if kind == "rig-check" {
							output = map[string]any{"riggable": i != 0, "rig_type": []string{"quadruped", "biped", "avian"}[i]}
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"task_id": remote, "status": "success", "progress": 100, "output": output}})
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				port := rigAdapter(t, server)
				accepted, err := port.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), req)
				if err != nil || accepted.Job == nil || accepted.Job.RemoteID != remote {
					t.Fatalf("Start: %+v %v", accepted, err)
				}
				done, err := port.Observe(context.Background(), rigObserve(req, accepted))
				if err != nil || done.Phase != generation.UpstreamCompleted || len(done.Artifacts) != 1 || calls != 2 {
					t.Fatalf("Observe: %+v %v calls=%d", done, err, calls)
				}
				if kind != "rig-check" && done.Artifacts[0].Locator != locator {
					t.Fatalf("URL changed: %+v", done.Artifacts)
				}
			})
		}
	}
}

func TestNativeSimpleContractRejectsMalformedCreateResponse(t *testing.T) {
	for _, kind := range simpleNativeKinds {
		for _, body := range []string{`{"code":0,"data":{}}`, `{"code":0,"data":{"task_id":42}}`, `{"code":0,"data":{"task_id":[]}}`} {
			t.Run(kind+"/"+body, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = io.WriteString(w, body) }))
				defer server.Close()
				got, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), simpleNativeRequest(kind))
				if err == nil || got.Job != nil || calls != 1 {
					t.Fatalf("malformed response accepted: %+v %v", got, err)
				}
			})
		}
	}
}

func TestNativeSimpleContractRejectsMissingOrWrongTypeInput(t *testing.T) {
	for _, kind := range simpleNativeKinds {
		for _, value := range []any{nil, 123, []string{"source"}} {
			t.Run(fmt.Sprintf("%s/%T", kind, value), func(t *testing.T) {
				req := simpleNativeRequest(kind)
				var input map[string]any
				if err := json.Unmarshal(req.Input, &input); err != nil {
					t.Fatal(err)
				}
				if value == nil {
					delete(input, "source_task")
				} else {
					input["source_task"] = value
				}
				req.Input, _ = json.Marshal(input)
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "must not dispatch", 500) }))
				defer server.Close()
				_, err := rigAdapter(t, server).Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), req)
				if err == nil || calls != 0 {
					t.Fatalf("invalid input dispatched: %v calls=%d", err, calls)
				}
			})
		}
	}
}

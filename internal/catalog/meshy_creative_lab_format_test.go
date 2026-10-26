package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Keychain and Fridge Magnet builds return exactly one model_urls key that
// matches output.format (https://docs.meshy.ai/en/api/creative-lab-keychain,
// https://docs.meshy.ai/en/api/creative-lab-fridge-magnet, checked
// 2026-10-21): glb -> model_urls.glb, zip -> model_urls.bundle_zip. The
// 2026-10-17 Fridge zip receipt held model.obj/model.mtl/texture.png/
// metrics.json and no GLB, so the PC 3D stage could not open it. GLB is the
// default; the complete ZIP stays selectable and each choice freezes exactly
// one required slot.
var reliefBuildFormats = map[string]struct {
	role, variant, path, media string
}{
	"glb": {"model", "glb", "model_urls.glb", "model/gltf-binary"},
	"zip": {"bundle", "zip", "model_urls.bundle_zip", "application/zip"},
}

func requiredReliefSlots(t *testing.T, o schema.Offering, frozen json.RawMessage) []schema.AssetRequirement {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal(frozen, &values); err != nil {
		t.Fatal(err)
	}
	var required []schema.AssetRequirement
	for _, output := range o.Outputs {
		if output.Delivery != "required" {
			continue
		}
		if output.RequiredWhen != nil && !schema.ConditionMatches(*output.RequiredWhen, values) {
			continue
		}
		required = append(required, output)
	}
	return required
}

func TestCreativeLabReliefBuildsDefaultToPreviewableGLBAndKeepSelectableZIP(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet"} {
		t.Run(product, func(t *testing.T) {
			o, ok := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
			if !ok || o.Enabled != creativeLabEnabledProducts[product] {
				t.Fatal("unexpected candidate enablement")
			}
			format := specialParameter(t, o, "output_format")
			values := make([]string, 0, len(format.Options))
			for _, option := range format.Options {
				values = append(values, option.Value)
			}
			if format.Type != "select" || format.Fixed || string(format.Default) != `"glb"` || !slices.Equal(values, []string{"glb", "zip"}) {
				t.Fatalf("format contract = type %s fixed %v default %s options %v", format.Type, format.Fixed, format.Default, values)
			}
			if format.Presentation == nil || format.Presentation.Surface != "provider" {
				t.Fatal("format must be a provider choice, not a readonly fixed value")
			}
			for _, input := range []string{`{"source_task":"task","output_format":"obj"}`, `{"source_task":"task","output_format":"stl"}`} {
				if _, err := normalizeInput(o, json.RawMessage(input)); err == nil {
					t.Fatalf("accepted undeclared format %s", input)
				}
			}
			var wire struct {
				ResultAssets []struct {
					Path, Role, Mode, Variant string
					MediaType                 string `json:"media_type"`
				} `json:"result_assets"`
				InputPaths map[string][]string `json:"input_paths"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &wire); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(wire.InputPaths["output_format"], []string{"output", "format"}) {
				t.Fatal("format must reach output.format")
			}
			for name, want := range reliefBuildFormats {
				input := `{"source_task":"task","output_format":"` + name + `"}`
				if name == "glb" {
					input = `{"source_task":"task"}`
				}
				frozen, err := normalizeInput(o, json.RawMessage(input))
				if err != nil {
					t.Fatal(err)
				}
				required := requiredReliefSlots(t, o, frozen)
				if len(required) != 1 || required[0].Role != want.role || required[0].Variant != want.variant || !slices.Equal(required[0].MediaTypes, []string{want.media}) || required[0].Minimum == nil || *required[0].Minimum != 1 {
					t.Fatalf("%s froze required slots %+v", name, required)
				}
				projected := false
				for _, projection := range wire.ResultAssets {
					if projection.Path == want.path && projection.Role == want.role && projection.Mode == "scalar" && projection.Variant == want.variant && projection.MediaType == want.media {
						projected = true
					}
				}
				if !projected {
					t.Fatalf("%s has no exact %s projection", name, want.path)
				}
			}
		})
	}
}

func TestCreativeLabReliefBuildObservesOnlyTheRequestedFormat(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet"} {
		o, _ := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
		for name, want := range reliefBuildFormats {
			t.Run(product+"/"+name, func(t *testing.T) {
				key := strings.TrimPrefix(want.path, "model_urls.")
				payload := map[string]any{"id": "opaque", "type": "creative-lab-" + product + "-build", "status": "SUCCEEDED", "progress": 100, "model_urls": map[string]string{key: "https://assets.meshy.ai/fixture/" + key}}
				raw, _ := json.Marshal(payload)
				port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet || r.URL.Path != "/openapi/creative-lab/"+product+"/v1/build/opaque" {
						t.Errorf("route = %s %s", r.Method, r.URL.Path)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				observed, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: "task", AttemptID: "attempt", Offering: o, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "opaque"}})
				if err != nil || observed.Phase != generation.UpstreamCompleted {
					t.Fatalf("observe=%+v err=%v", observed, err)
				}
				if len(observed.Artifacts) != 1 || observed.Artifacts[0].Role != want.role || observed.Artifacts[0].Variant != want.variant || observed.Artifacts[0].MediaType != want.media {
					t.Fatalf("claims = %+v", observed.Artifacts)
				}
			})
		}
	}
}

func TestCreativeLabRequiredWhenSelectDeclarationsFailClosed(t *testing.T) {
	cases := map[string]func(output map[string]any, parameters []any){
		"value-outside-options": func(output map[string]any, _ []any) {
			output["required_when"] = map[string]any{"parameter": "output_format", "operator": "equals", "value": "obj"}
		},
		"non-string-value": func(output map[string]any, _ []any) {
			output["required_when"] = map[string]any{"parameter": "output_format", "operator": "equals", "value": 1}
		},
		"unsupported-operator": func(output map[string]any, _ []any) {
			output["required_when"] = map[string]any{"parameter": "output_format", "operator": "truthy"}
		},
		"number-parameter": func(output map[string]any, _ []any) {
			output["required_when"] = map[string]any{"parameter": "size_mm", "operator": "equals", "value": 40}
		},
		"unknown-parameter": func(output map[string]any, _ []any) {
			output["required_when"] = map[string]any{"parameter": "format", "operator": "equals", "value": "glb"}
		},
		"background-slot": func(output map[string]any, _ []any) {
			output["delivery"] = "background"
			output["minimum"] = 0
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var d map[string]any
			if err := json.Unmarshal(defaultCatalog, &d); err != nil {
				t.Fatal(err)
			}
			var build map[string]any
			for _, raw := range d["offerings"].([]any) {
				if o := raw.(map[string]any); o["id"] == "meshy/creative-lab/fridge-magnet/build" {
					build = o
				}
			}
			var glb map[string]any
			for _, raw := range build["outputs"].([]any) {
				if output := raw.(map[string]any); output["variant"] == "glb" {
					glb = output
				}
			}
			if glb == nil {
				t.Fatal("GLB slot missing")
			}
			mutate(glb, build["parameters"].([]any))
			raw, _ := json.Marshal(d)
			if _, err := Load(bytes.NewReader(raw)); err == nil {
				t.Fatal("invalid select required_when accepted")
			}
		})
	}
}

// Pin the declared candidate defaults. A reconstructed request is not proof
// of what a past paid qualification actually sent.
func TestCreativeLabFridgeMagnetDefaultBuildBodyIsPinned(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := c.Get("meshy/creative-lab/fridge-magnet/build")
	frozen, err := normalizeInput(o, json.RawMessage(`{"source_task":"public-prototype"}`))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(frozen, &values); err != nil {
		t.Fatal(err)
	}
	if values["output_format"] != "glb" || values["export_resolution"] != float64(512) {
		t.Fatalf("frozen defaults = %v", values)
	}
	if !reflect.DeepEqual(requiredReliefSlots(t, o, frozen)[0].MediaTypes, []string{"model/gltf-binary"}) {
		t.Fatal("default does not require the GLB")
	}
}

// The current official Keychain and Fridge Magnet build contracts
// (https://docs.meshy.ai/api/creative-lab-keychain.md,
// https://docs.meshy.ai/api/creative-lab-fridge-magnet.md, checked
// 2026-10-21) declare options.export_resolution as an integer in [64, 512],
// default 512, because the geometry comes from a 512px depth map. The highest
// quality tier is therefore the default. Values above 512 fail before any
// upstream request; the exact frozen value reaches options.export_resolution.
func TestCreativeLabReliefBuildResolutionUsesCurrentOfficialRange(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet"} {
		t.Run(product, func(t *testing.T) {
			o, _ := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
			if o.Enabled != creativeLabEnabledProducts[product] {
				t.Fatal("unexpected candidate enablement")
			}
			quality := specialParameter(t, o, "export_resolution")
			if quality.Minimum == nil || *quality.Minimum != 64 || quality.Maximum == nil || *quality.Maximum != 512 || string(quality.Default) != "512" || !quality.Integer {
				t.Fatalf("resolution contract = min %v max %v default %s integer %v", quality.Minimum, quality.Maximum, quality.Default, quality.Integer)
			}
			for _, value := range []int{63, 513, 1024, 2048} {
				input := json.RawMessage(fmt.Sprintf(`{"source_task":"public-prototype","export_resolution":%d}`, value))
				if _, err := normalizeInput(o, input); err == nil {
					t.Errorf("out-of-range resolution %d accepted", value)
				}
			}
			for _, input := range []string{`{"source_task":"public-prototype"}`, `{"source_task":"public-prototype","export_resolution":512}`, `{"source_task":"public-prototype","export_resolution":64}`} {
				frozen, err := normalizeInput(o, json.RawMessage(input))
				if err != nil {
					t.Fatalf("documented input %s rejected: %v", input, err)
				}
				var values map[string]any
				if err := json.Unmarshal(frozen, &values); err != nil {
					t.Fatal(err)
				}
				want := values["export_resolution"]
				posts := 0
				port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"prototype-remote","type":"creative-lab-` + product + `-prototype","status":"SUCCEEDED","image_urls":["https://assets.meshy.ai/preview.png"]}`))}, nil
					}
					posts++
					var body struct {
						Options map[string]any `json:"options"`
					}
					if r.URL.Path != "/openapi/creative-lab/"+product+"/v1/build" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Options["export_resolution"] != want {
						t.Errorf("wire %s options=%v want export_resolution=%v", r.URL.Path, body.Options, want)
					}
					return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"result":"created-remote"}`))}, nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				request := generation.StartRequest{SessionID: "session", OwnerID: "owner", Offering: o, SubmissionKey: "one-create", Input: frozen, TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "public-prototype", SessionID: "session", ProviderID: "meshy", OfferingID: schema.OfferingID("meshy/creative-lab/" + product + "/prototype"), UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}}}}
				if _, err := port.Start(context.Background(), request); err != nil || posts != 1 {
					t.Fatalf("start err=%v posts=%d", err, posts)
				}
			}
		})
	}
}

// #363: the current official Keychain and Fridge Magnet build pages define
// size_mm, curve_param and relief_scale as "0 uses the default". A build must
// accept 0 and send it unchanged, not reject it or replace it with the local
// default. The mock records the actual POST body; no upstream is called.
func TestCreativeLabReliefBuildsSendZeroDefaults(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet"} {
		t.Run(product, func(t *testing.T) {
			o, _ := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
			frozen, err := normalizeInput(o, json.RawMessage(`{"source_task":"public-prototype","size_mm":0,"curve_param":0,"relief_scale":0}`))
			if err != nil {
				t.Fatalf("documented zero defaults rejected: %v", err)
			}
			var values map[string]any
			if err := json.Unmarshal(frozen, &values); err != nil {
				t.Fatal(err)
			}
			zeroParameters := []string{"size_mm", "curve_param", "relief_scale"}
			for _, name := range zeroParameters {
				if values[name] != float64(0) {
					t.Fatalf("frozen %s=%v, want 0", name, values[name])
				}
			}
			posts := 0
			port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"prototype-remote","type":"creative-lab-` + product + `-prototype","status":"SUCCEEDED","image_urls":["https://assets.meshy.ai/preview.png"]}`))}, nil
				}
				posts++
				var body struct {
					Options map[string]any `json:"options"`
				}
				if r.Method != http.MethodPost || r.URL.Path != "/openapi/creative-lab/"+product+"/v1/build" {
					t.Errorf("wire = %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode body: %v", err)
				}
				for _, name := range zeroParameters {
					if body.Options[name] != float64(0) {
						t.Errorf("%s reached the wire as %v, want 0", name, body.Options[name])
					}
				}
				return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"result":"created-remote"}`))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			request := generation.StartRequest{SessionID: "session", OwnerID: "owner", Offering: o, SubmissionKey: "zero-default", Input: frozen, TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "public-prototype", SessionID: "session", ProviderID: "meshy", OfferingID: schema.OfferingID("meshy/creative-lab/" + product + "/prototype"), UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}}}}
			if _, err := port.Start(context.Background(), request); err != nil || posts != 1 {
				t.Fatalf("start err=%v posts=%d", err, posts)
			}
		})
	}
}

// #189: the lampshade build follows the current official default output
// (https://docs.meshy.ai/api/creative-lab-lamp.md, 2026-10-22): output.format
// stl returns model_urls.lamp_stl, plus model_urls.base_stl unless the fixture
// preset is none. The ZIP bundle (and the result.json it alone can carry) is
// not registered, so every delivered part is a separately verified STL.
func TestCreativeLabLampBuildDeliversTheOfficialSTLParts(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	lamp, _ := c.Get("meshy/creative-lab/lamp/build")
	for _, parameter := range lamp.Parameters {
		if parameter.Name == "include_result_json" {
			t.Fatal("result.json only exists inside the unregistered ZIP bundle")
		}
		if parameter.Name == "output_format" && (!parameter.Fixed || len(parameter.Options) != 1 || parameter.Options[0].Value != "stl") {
			t.Fatalf("output_format = %+v", parameter)
		}
	}
	required := func(input string) []string {
		normalized, err := normalizeInput(lamp, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := json.Unmarshal(normalized, &values); err != nil {
			t.Fatal(err)
		}
		var slots []string
		for _, output := range lamp.Outputs {
			if output.MediaTypes[0] != "model/stl" || *output.Minimum != 1 {
				t.Fatalf("output %+v", output)
			}
			if output.RequiredWhen == nil || schema.ConditionMatches(*output.RequiredWhen, values) {
				slots = append(slots, output.Role+"/"+output.Variant)
			}
		}
		return slots
	}
	if got := required(`{"source_task":"task"}`); !reflect.DeepEqual(got, []string{"model/lamp_stl", "model/base_stl"}) {
		t.Fatalf("default fixture preset requires %v", got)
	}
	if got := required(`{"source_task":"task","light_source_preset":"none"}`); !reflect.DeepEqual(got, []string{"model/lamp_stl"}) {
		t.Fatalf("preset none requires %v", got)
	}
	// A caller cannot ask for the bundle: the fixed format is rejected or pinned to stl.
	if normalized, err := normalizeInput(lamp, json.RawMessage(`{"source_task":"task","output_format":"zip"}`)); err == nil &&
		!strings.Contains(string(normalized), `"output_format":"stl"`) {
		t.Fatalf("zip output accepted: %s", normalized)
	}
}

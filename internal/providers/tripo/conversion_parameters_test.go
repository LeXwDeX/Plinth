package tripo_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #278: the models/convert requests of a generation task carry the export
// options its frozen Offering and input declare, only for the formats the
// purpose selected, and the generation request itself carries none of them. An
// option on its upstream default is left out, so an export that asks for nothing
// special is the plain {input, format} request the adapter always sent.

const exportOfferingID = "tripo/tripo-v3.1-multiview/image-to-3d"

// gamePurposeExport is what the game purposes declare: the advanced export.
var gamePurposeExport = map[string]any{
	"output_formats": []string{"GLTF", "FBX"}, "export_texture_format": "PNG", "export_texture_size": "2k", "export_pivot_bottom_center": true,
}

// exportRecorder forwards to the Tripo v3 fake and keeps what the adapter sent
// to POST /models/convert and to the generation endpoint, so the requests are
// checked against the fake's reading of the official convert page.
type exportRecorder struct {
	mu          sync.Mutex
	converts    []map[string]any
	generations []map[string]any
	statuses    []int
}

func newExportUpstream(t *testing.T) (*httptest.Server, *exportRecorder) {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	recorder := &exportRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
			_ = json.Unmarshal(raw, &body)
		}
		recording := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		fake.ServeHTTP(recording, r)
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/models/convert"):
			recorder.converts = append(recorder.converts, body)
			recorder.statuses = append(recorder.statuses, recording.status)
		case strings.HasSuffix(r.URL.Path, "/generation/text-to-model"), strings.HasSuffix(r.URL.Path, "/generation/image-to-model"):
			recorder.generations = append(recorder.generations, body)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorder
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func exportAdapter(t *testing.T, server *httptest.Server) *tripo.Adapter {
	t.Helper()
	errorMapping, statusMapping := declaredTripoMappings(t)
	adapter, err := tripo.New(tripo.Config{
		BaseURL: server.URL + tripov3.Prefix, APIKey: "test-key", HTTPClient: server.Client(),
		AllowHTTPAssets: true, ErrorMapping: errorMapping, StatusMapping: statusMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

// exportInput freezes a valid input of one exporting Offering with the changes
// applied, through the real Catalog normalization.
func exportInput(t *testing.T, c *catalog.Catalog, id string, changes map[string]any) json.RawMessage {
	t.Helper()
	input := map[string]any{"authorize_extra_credits": true}
	if id == exportOfferingID {
		input["reference_assets"] = []string{"asset-1", "asset-2"}
		input["reference_views"] = []string{"front", "left"}
	} else {
		input["prompt"] = "a crate"
	}
	for name, value := range changes {
		input[name] = value
	}
	offering, ok := c.Get(schema.OfferingID(id))
	if !ok {
		t.Fatalf("missing %s", id)
	}
	encoded, _ := json.Marshal(input)
	_, frozen, err := c.Normalize(offering.Capability, schema.OfferingID(id), encoded)
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

// observeToCompletion drives the pre-completion observation against a finished
// base task until every selected conversion is claimed.
func observeToCompletion(t *testing.T, adapter *tripo.Adapter, offering generation.Offering, input json.RawMessage, baseTask string) generation.Observation {
	t.Helper()
	request := generation.ObserveRequest{
		AttemptID: "attempt", Offering: offering, Input: input,
		Job: generation.UpstreamJob{ProviderID: "tripo", RemoteID: baseTask},
	}
	for range 60 {
		observation, err := adapter.Observe(context.Background(), request)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Phase == generation.UpstreamCompleted {
			return observation
		}
		if observation.Phase == generation.UpstreamFailed {
			t.Fatalf("task failed: %+v", observation.Failure)
		}
		request.Opaque = observation.Opaque
	}
	t.Fatal("conversions did not complete")
	return generation.Observation{}
}

func createBaseTask(t *testing.T, server *httptest.Server) string {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, server.URL+tripov3.Prefix+"/generation/text-to-model",
		strings.NewReader(`{"model":"v3.1-20260211","prompt":"a crate"}`))
	request.Header.Set("Authorization", "Bearer test-key")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var created struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil || created.Data.TaskID == "" {
		t.Fatalf("base task not created: %v", err)
	}
	return created.Data.TaskID
}

func convertFormats(recorder *exportRecorder) []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	formats := make([]string, 0, len(recorder.converts))
	for _, body := range recorder.converts {
		format, _ := body["format"].(string)
		formats = append(formats, format)
	}
	return formats
}

// convertBodies are the recorded export requests by format, without the source.
func convertBodies(t *testing.T, recorder *exportRecorder) map[string]map[string]any {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	bodies := map[string]map[string]any{}
	for _, body := range recorder.converts {
		format, _ := body["format"].(string)
		if body["input"] == nil {
			t.Errorf("%s export has no source: %v", format, body)
		}
		copied := map[string]any{}
		for name, value := range body {
			if name != "input" {
				copied[name] = value
			}
		}
		bodies[format] = copied
	}
	for _, status := range recorder.statuses {
		if status != http.StatusOK {
			t.Errorf("the fake refused an export request: %d", status)
		}
	}
	return bodies
}

func runExports(t *testing.T, changes map[string]any) (map[string]map[string]any, generation.Observation, *httptest.Server) {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server, recorder := newExportUpstream(t)
	adapter := exportAdapter(t, server)
	offering, _ := c.Get(exportOfferingID)
	input := exportInput(t, c, exportOfferingID, changes)
	observation := observeToCompletion(t, adapter, offering, input, createBaseTask(t, server))
	return convertBodies(t, recorder), observation, server
}

func TestGamePurposeExportsOnlyItsFormatsWithTheAdvancedOptions(t *testing.T) {
	bodies, observation, _ := runExports(t, gamePurposeExport)
	formats := make([]string, 0, len(bodies))
	for format := range bodies {
		formats = append(formats, format)
	}
	slices.Sort(formats)
	if !slices.Equal(formats, []string{"FBX", "GLTF"}) {
		t.Fatalf("converted formats = %v, want only GLTF and FBX", formats)
	}
	// The two exports are the only conversion claims; the GLB of the model role is
	// the GLTF export.
	variants := map[string]int{}
	var primary generation.ArtifactClaim
	for _, claim := range observation.Artifacts {
		if claim.Role != "model" && claim.Role != "converted_model" {
			continue // the thumbnails the base task returns
		}
		variants[claim.Role+"/"+claim.Variant]++
		if claim.Role == "model" {
			primary = claim
		}
	}
	if variants["converted_model/converted.gltf"] != 1 || variants["converted_model/converted.fbx"] != 1 || variants["model/"] != 1 || len(variants) != 3 {
		t.Fatalf("claims = %v, want the model, converted.gltf and converted.fbx", variants)
	}
	if !strings.HasSuffix(strings.Split(primary.Locator, "?")[0], ".gltf") {
		t.Fatalf("the model claim must be the GLTF conversion, got %s", primary.Locator)
	}
	// The FBX preset stays on its upstream default, so it is not sent.
	want := map[string]any{"texture_format": "PNG", "texture_size": float64(2048), "pivot_to_center_bottom": true}
	for format, body := range bodies {
		if len(body) != len(want)+1 {
			t.Errorf("%s request = %v, want format plus exactly %v", format, body, want)
		}
		for name, value := range want {
			if body[name] != value {
				t.Errorf("%s request %s = %v, want %v", format, name, body[name], value)
			}
		}
	}
}

// The display and web/AR purposes (and every task that asks for nothing special)
// export with no option at all, which is the request and the basic price this
// Offering had before the options existed.
func TestUpstreamDefaultsExportAsPlainConversions(t *testing.T) {
	bodies, observation, _ := runExports(t, nil)
	if len(bodies) != 6 {
		t.Fatalf("converted formats = %v, want all six", bodies)
	}
	for format, body := range bodies {
		if len(body) != 1 || body["format"] != format {
			t.Errorf("%s request = %v, want only input and format", format, body)
		}
	}
	models := 0
	for _, claim := range observation.Artifacts {
		if claim.Role == "model" || claim.Role == "converted_model" {
			models++
		}
	}
	if models != 7 {
		t.Fatalf("model claims = %d, want the model and six conversions", models)
	}
}

// Explicit upstream defaults are the same request as no option.
func TestExplicitUpstreamDefaultsAreLeftOut(t *testing.T) {
	bodies, _, _ := runExports(t, map[string]any{
		"output_formats": []string{"GLTF", "FBX"}, "export_texture_format": "JPEG", "export_texture_size": "4k",
		"export_pivot_bottom_center": false, "export_fbx_preset": "blender",
	})
	for format, body := range bodies {
		if len(body) != 1 {
			t.Errorf("%s request = %v, want only input and format", format, body)
		}
	}
}

// Each advanced option is sent alone when it alone leaves its default.
func TestEachAdvancedOptionIsSentOnlyWhenItLeavesItsDefault(t *testing.T) {
	for name, test := range map[string]struct {
		changes map[string]any
		want    map[string]any
	}{
		"PNG":        {map[string]any{"export_texture_format": "PNG"}, map[string]any{"texture_format": "PNG"}},
		"a small":    {map[string]any{"export_texture_size": "2k"}, map[string]any{"texture_size": float64(2048)}},
		"a large":    {map[string]any{"export_texture_size": "8k"}, map[string]any{"texture_size": float64(8192)}},
		"the pivot":  {map[string]any{"export_pivot_bottom_center": true}, map[string]any{"pivot_to_center_bottom": true}},
		"the preset": {map[string]any{"export_fbx_preset": "mixamo"}, map[string]any{"fbx_preset": "mixamo"}},
	} {
		t.Run(name, func(t *testing.T) {
			changes := map[string]any{"output_formats": []string{"GLTF", "FBX"}}
			for option, value := range test.changes {
				changes[option] = value
			}
			bodies, _, _ := runExports(t, changes)
			for format, body := range bodies {
				want := map[string]any{"format": format}
				for field, value := range test.want {
					// The FBX preset belongs to the FBX export only.
					if field == "fbx_preset" && format != "FBX" {
						continue
					}
					want[field] = value
				}
				if len(body) != len(want) {
					t.Errorf("%s request = %v, want %v", format, body, want)
				}
				for field, value := range want {
					if body[field] != value {
						t.Errorf("%s request %s = %v, want %v", format, field, body[field], value)
					}
				}
			}
		})
	}
}

// The delivered FBX of a PNG export embeds a PNG texture (read back from the
// bytes); without the option the official default, a JPEG texture, applies.
func TestExportedFBXCarriesTheRequestedTextureFormat(t *testing.T) {
	for name, test := range map[string]struct {
		changes map[string]any
		want    string
		other   string
	}{
		"PNG":     {map[string]any{"export_texture_format": "PNG"}, "\x89PNG\r\n\x1a\n", "\xff\xd8\xff"},
		"default": {nil, "\xff\xd8\xff", "\x89PNG\r\n\x1a\n"},
	} {
		t.Run(name, func(t *testing.T) {
			changes := map[string]any{"output_formats": []string{"GLTF", "FBX"}}
			for option, value := range test.changes {
				changes[option] = value
			}
			_, observation, _ := runExports(t, changes)
			for _, claim := range observation.Artifacts {
				if claim.Variant != "converted.fbx" {
					continue
				}
				response, err := http.Get(claim.Locator)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if !strings.Contains(string(data), test.want) || strings.Contains(string(data), test.other) {
					t.Fatal("the exported FBX does not carry the requested texture format")
				}
				return
			}
			t.Fatal("no FBX claim")
		})
	}
}

// The export options never reach the generation request, and a selection
// without the GLTF conversion is refused before it.
func TestExportOptionsStayOutOfTheGenerationRequest(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server, recorder := newExportUpstream(t)
	adapter := exportAdapter(t, server)
	offering, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	input := exportInput(t, c, "tripo/tripo-v3.1-full/text-to-3d", map[string]any{
		"prompt": "a crate", "output_formats": []string{"GLTF", "FBX"}, "export_pivot_bottom_center": true,
		"export_texture_format": "PNG", "export_texture_size": "8k", "export_fbx_preset": "mixamo",
	})
	if _, err := adapter.Start(context.Background(), generation.StartRequest{Offering: offering, Input: input, SubmissionKey: "key-1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.generations) != 1 {
		t.Fatalf("generation requests = %d", len(recorder.generations))
	}
	for _, name := range []string{"output_formats", "authorize_extra_credits", "export_pivot_bottom_center", "export_texture_size", "export_texture_format",
		"export_fbx_preset", "texture_format", "texture_size", "pivot_to_center_bottom", "fbx_preset"} {
		if _, sent := recorder.generations[0][name]; sent {
			t.Errorf("the generation request carries %s", name)
		}
	}
	if recorder.generations[0]["prompt"] != "a crate" || recorder.generations[0]["face_limit"] == nil {
		t.Errorf("the generation request lost its own parameters: %v", recorder.generations[0])
	}
}

func TestSelectionWithoutTheGLBConversionIsRefusedBeforeAnySpend(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server, recorder := newExportUpstream(t)
	adapter := exportAdapter(t, server)
	offering, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	// Normalization refuses it; the adapter refuses it too when a frozen input
	// reaches it another way.
	input := exportInput(t, c, "tripo/tripo-v3.1-full/text-to-3d", map[string]any{"prompt": "a crate", "output_formats": []string{"GLTF", "FBX"}})
	var values map[string]any
	_ = json.Unmarshal(input, &values)
	values["output_formats"] = []string{"FBX", "STL"}
	forged, _ := json.Marshal(values)
	if _, err := adapter.Start(context.Background(), generation.StartRequest{Offering: offering, Input: forged, SubmissionKey: "key-2"}); err == nil {
		t.Fatal("a selection without GLTF reached the paid generation")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.generations) != 0 || len(recorder.converts) != 0 {
		t.Fatalf("upstream was called: %d generations, %d converts", len(recorder.generations), len(recorder.converts))
	}
}

// A snapshot frozen before the export options existed declares none, so its
// exports keep the historical {input, format} request whatever the input holds.
func TestOfferingWithoutAnExportDeclarationKeepsTheHistoricalRequest(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server, recorder := newExportUpstream(t)
	adapter := exportAdapter(t, server)
	offering, _ := c.Get(exportOfferingID)
	var options map[string]json.RawMessage
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	delete(options, "native_conversion")
	offering.ProviderOptions, _ = json.Marshal(options)
	input := exportInput(t, c, exportOfferingID, gamePurposeExport)
	observeToCompletion(t, adapter, offering, input, createBaseTask(t, server))
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.converts) != 2 {
		t.Fatalf("exports = %d", len(recorder.converts))
	}
	for _, body := range recorder.converts {
		if len(body) != 2 || body["input"] == nil || body["format"] == nil {
			t.Errorf("historical request = %v, want only input and format", body)
		}
	}
}

// A binding that names no upstream default sends its field whenever the input has
// a value, defaults included: the omission is the declaration's, not a rule.
func TestBindingWithoutAnUpstreamDefaultSendsItsFieldAlways(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server, recorder := newExportUpstream(t)
	adapter := exportAdapter(t, server)
	offering, _ := c.Get(exportOfferingID)
	var options map[string]any
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	for _, raw := range options["native_conversion"].(map[string]any)["bindings"].([]any) {
		delete(raw.(map[string]any), "upstream_default")
	}
	offering.ProviderOptions, _ = json.Marshal(options)
	input := exportInput(t, c, exportOfferingID, map[string]any{"output_formats": []string{"GLTF"}})
	observeToCompletion(t, adapter, offering, input, createBaseTask(t, server))
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.converts) != 1 {
		t.Fatalf("exports = %d", len(recorder.converts))
	}
	body := recorder.converts[0]
	if body["texture_format"] != "JPEG" || body["texture_size"] != float64(4096) || body["pivot_to_center_bottom"] != false {
		t.Errorf("request = %v, want the defaults sent explicitly", body)
	}
	if _, preset := body["fbx_preset"]; preset {
		t.Errorf("the FBX preset belongs to the FBX export only: %v", body)
	}
}

// A declaration that cannot be written is refused before the paid generation
// and before any export.
func TestMalformedExportDeclarationsAreRefusedBeforeAnySpend(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := c.Get("tripo/tripo-v3.1-full/text-to-3d")
	input := exportInput(t, c, "tripo/tripo-v3.1-full/text-to-3d", map[string]any{"prompt": "a crate", "output_formats": []string{"GLTF", "FBX"}})
	for name, nativeConversion := range map[string]string{
		"reserved constant field":     `{"parameters":{"format":"STL"}}`,
		"non scalar constant":         `{"parameters":{"texture_format":["PNG"]}}`,
		"processing shaped":           `{"source_alias":"model","parameters":{"texture_format":"PNG"}}`,
		"empty":                       `{}`,
		"unknown bound parameter":     `{"bindings":[{"parameter":"nope","wire":"texture_size"}]}`,
		"bound selection":             `{"bindings":[{"parameter":"output_formats","wire":"x"}]}`,
		"bound consent":               `{"bindings":[{"parameter":"authorize_extra_credits","wire":"x"}]}`,
		"reserved wire":               `{"bindings":[{"parameter":"export_texture_size","wire":"input"}]}`,
		"duplicate wire":              `{"parameters":{"texture_size":1},"bindings":[{"parameter":"export_texture_size","wire":"texture_size"}]}`,
		"format outside the options":  `{"bindings":[{"parameter":"export_fbx_preset","wire":"fbx_preset","formats":["GLB"]}]}`,
		"parameter bound twice":       `{"bindings":[{"parameter":"export_fbx_preset","wire":"a"},{"parameter":"export_fbx_preset","wire":"b"}]}`,
		"non scalar upstream default": `{"bindings":[{"parameter":"export_texture_size","wire":"texture_size","upstream_default":[4096]}]}`,
		"object upstream default":     `{"bindings":[{"parameter":"export_texture_size","wire":"texture_size","upstream_default":{"a":1}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server, recorder := newExportUpstream(t)
			adapter := exportAdapter(t, server)
			offering := base
			var options map[string]json.RawMessage
			if err := json.Unmarshal(base.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}
			options["native_conversion"] = json.RawMessage(nativeConversion)
			offering.ProviderOptions, _ = json.Marshal(options)
			if _, err := adapter.Start(context.Background(), generation.StartRequest{Offering: offering, Input: input, SubmissionKey: "key-3"}); err == nil {
				t.Fatal("a malformed export declaration reached the paid generation")
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if len(recorder.generations) != 0 || len(recorder.converts) != 0 {
				t.Fatalf("upstream was called: %d generations, %d converts", len(recorder.generations), len(recorder.converts))
			}
		})
	}
}

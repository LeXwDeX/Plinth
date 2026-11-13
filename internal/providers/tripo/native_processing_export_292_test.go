package tripo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #292: the private exports of a Tripo processing step write the texture size
// from the frozen input, so the size a session purpose bounds (a game: 2K) is
// the size of every exported file, while the default stays the request the
// exports have always been (8192). The tests use the shipped Product Schema and
// the real adapter, and the upstream is the Tripo v3 fake: it checks every
// request against the parameters of the official convert page (a refusal fails
// the step), serves the model the exports are made from, and shows each export
// back on its task object, which driveExports holds against what the adapter
// sent.

// exportSizes are the sizes a processing Offering declares, with the integer
// the upstream receives for each.
var exportSizes = map[string]float64{"2k": 2048, "4k": 4096, "8k": 8192}

// exportOfferings are the shipped Offerings whose exports carry the bound size.
var exportOfferings = []string{
	"tripo/processing/texture", "tripo/processing/rigging", "tripo/processing/rigging-v3",
	"tripo/geometry/decimate-triangle", "tripo/geometry/decimate-quad", "tripo/geometry/segment",
	"tripo/geometry/complete", "tripo/geometry/quick-cap",
	"tripo/geometry/smartsegment-image", "tripo/geometry/smartsegment-model",
}

// shippedRigging is a rigging step of the shipped Schema frozen the way the
// kernel freezes it: through the Catalog's normalization, so every default (the
// export size included) is in the input.
func shippedRigging(t *testing.T, id string, changes map[string]any) generation.StartRequest {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"source_task": "source", "authorize_extra_credits": true}
	for name, value := range changes {
		input[name] = value
	}
	encoded, _ := json.Marshal(input)
	offering, normalized, err := product.Normalize(generation.Rigging, generation.OfferingID(id), encoded)
	if err != nil {
		t.Fatalf("%s %v: %v", id, changes, err)
	}
	r := nativeProcessingFixture(generation.Rigging)
	r.Offering, r.Input = offering, normalized
	return r
}

// exportAssetHost is the host the fake writes into the artifact URLs of these
// runs. Tripo's native sources are read only over HTTPS from the Offering's
// asset hosts, so the fake is told to name one and the adapter's source client
// reaches it through the fixture (testkit.Fixture.RouteHost): the model the
// exports are made from is the fake's own file, read under the adapter's own
// host, scheme and GLB checks.
const exportAssetHost = "assets.example"

// exportStack is a Tripo v3 fake with the adapters that run against it. Every
// adapter sends its requests through recorder and reads the artifact URLs the
// fake writes (under exportAssetHost) through the fixture.
type exportStack struct {
	fixture  *testkit.Fixture
	recorder *rigRecorder
	// adapter builds a new adapter, as a restart does.
	adapter func() *Adapter
}

// newExportStack starts the fake. inputs resolves the owned source file of an
// Offering that uploads one (Smart Segmentation) and is nil for the others.
func newExportStack(t *testing.T, inputs generation.InputAssetResolver) exportStack {
	t.Helper()
	fixture := testkit.New(t, fakeupstream.Config{ArtifactOrigin: "https://" + exportAssetHost}, tripov3.Version())
	errorMapping, statusMapping := realSampleMappings(t)
	recorder := &rigRecorder{}
	artifacts := fixture.RouteHost(exportAssetHost)
	return exportStack{fixture: fixture, recorder: recorder, adapter: func() *Adapter {
		adapter, err := New(Config{
			BaseURL: fixture.Server.URL + tripov3.Prefix, APIKey: "test-key", HTTPClient: &http.Client{Transport: recorder},
			InputAssets: inputs, ErrorMapping: errorMapping, StatusMapping: statusMapping,
			NativeSourceTransport: func(http.RoundTripper) http.RoundTripper { return artifacts },
		})
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	}}
}

// exportRun is one step with private exports that the real adapter ran to
// completion against the Tripo v3 fake.
type exportRun struct {
	fake *testkit.Fixture
	// source is the fake task a processing step worked on, baseID the fake task
	// the step's own request created, and model the artifact URL of that task's
	// result: the source of every export.
	source, baseID, model string
	// base are the bodies the step's own endpoint received, as the adapter sent
	// them.
	base []string
	// order are the formats in the order their exports were requested, sent the
	// export requests by format as the adapter sent them (raw), and converts the
	// same decoded.
	order    []string
	sent     map[string]string
	converts map[string]map[string]any
	// echoes are the parameters the fake's task object shows for each export.
	echoes   map[string]map[string]any
	observed generation.Observation
}

// collect reads what the finished run sent and what the fake ran. The task
// object of every export must show exactly the parameters that were sent (the
// fake keeps the documented ones and nothing else, the source named
// original_model_id), so what the tests read from the sent requests is what the
// fake took. conversions is the private export record of the final checkpoint.
func (run *exportRun) collect(t *testing.T, stack exportStack, endpoint string, conversions map[string]conversionItem) {
	t.Helper()
	run.base = stack.recorder.posts(endpoint)
	run.sent, run.converts, run.echoes = map[string]string{}, map[string]map[string]any{}, map[string]map[string]any{}
	for _, raw := range stack.recorder.posts("models/convert") {
		var body map[string]any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("export request %s: %v", raw, err)
		}
		format, _ := body["format"].(string)
		if _, again := run.converts[format]; again {
			t.Errorf("the %s export was requested twice", format)
		}
		run.order = append(run.order, format)
		run.sent[format], run.converts[format] = raw, body
		// Every export is made from the model the step produced.
		if source, _ := body["input"].(string); run.model == "" {
			run.model = source
		} else if source != run.model {
			t.Errorf("the %s export is made from %s, the others from %s", format, source, run.model)
		}
	}
	if prefix := "https://" + exportAssetHost + tripov3.Prefix + "/artifacts/" + run.baseID + "/"; !strings.HasPrefix(run.model, prefix) {
		t.Errorf("the exports are made from %q, not from a result of the step's own task (%s)", run.model, prefix)
	}
	for format, sent := range run.converts {
		_, _, polled := stack.fixture.Do(http.MethodGet, tripov3.Prefix+"/tasks/"+conversions[format].RemoteID, "test-key", nil)
		input, _ := testkit.Field(t, polled, "data", "input").(map[string]any)
		echoed, want := map[string]any{}, map[string]any{}
		for name, value := range input {
			if name != "original_model_id" {
				echoed[name] = value
			}
		}
		for name, value := range sent {
			if name != "input" {
				want[name] = value
			}
		}
		if !reflect.DeepEqual(echoed, want) {
			t.Errorf("the fake ran the %s export under %v, the adapter asked for %v", format, echoed, want)
		}
		run.echoes[format] = echoed
	}
}

// fakeTask creates a task of the Tripo v3 fake the way a client does and returns
// its id.
func fakeTask(t *testing.T, fixture *testkit.Fixture, path string, body map[string]any) string {
	t.Helper()
	status, _, created := fixture.Do(http.MethodPost, tripov3.Prefix+"/"+path, "test-key", body)
	if status != http.StatusOK {
		t.Fatalf("the fake refused POST %s: %d %v", path, status, created)
	}
	id, _ := testkit.Field(t, created, "data", "task_id").(string)
	return id
}

// driveExports runs a started native processing request to completion against
// the Tripo v3 fake. Each observation uses a new adapter, as a restart does.
// Besides the requests the adapter sent it holds what the fake ran (see collect).
func driveExports(t *testing.T, r generation.StartRequest, endpoint string) exportRun {
	t.Helper()
	r.Offering.AssetHosts = []string{exportAssetHost}
	stack := newExportStack(t, nil)

	// The model the step works on is a task of the fake, as it is on the real API.
	run := exportRun{fake: stack.fixture}
	run.source = fakeTask(t, stack.fixture, "generation/text-to-model", map[string]any{"model": "v3.1-20260211", "prompt": "a robot"})
	dependency := r.TaskDependencies["source_task"]
	if dependency.Capability == generation.Capability("mesh-segmentation") {
		run.source = fakeTask(t, stack.fixture, "mesh/segment", map[string]any{"input": run.source, "model": "v2.0-20260430"})
	}
	dependency.UpstreamJob.RemoteID = run.source
	r.TaskDependencies = map[string]generation.TaskDependency{"source_task": dependency}

	ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})
	accepted, err := stack.adapter().Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	run.baseID = accepted.Job.RemoteID
	q := rigObserve(r, accepted)
	for i := 0; i < 60 && run.observed.Phase != generation.UpstreamCompleted; i++ {
		if run.observed, err = stack.adapter().Observe(ctx, q); err != nil {
			t.Fatal(err)
		}
		if run.observed.Phase == generation.UpstreamFailed {
			t.Fatalf("the step failed: %+v", run.observed.Failure)
		}
		q.Opaque = run.observed.Opaque
	}
	if run.observed.Phase != generation.UpstreamCompleted {
		t.Fatalf("the exports did not complete: %+v", run.observed)
	}
	var state nativeProcessingState
	if err := json.Unmarshal(run.observed.Opaque, &state); err != nil {
		t.Fatal(err)
	}
	run.collect(t, stack, endpoint, state.Conversions)
	return run
}

// The default frozen input is the request every export of a processing step has
// always been; a size the purpose bounds changes that field and nothing else.
func TestRiggingExportsCarryThePurposeTextureSize(t *testing.T) {
	for name, wire := range map[string]float64{"": 8192, "8k": 8192, "4k": 4096, "2k": 2048} {
		t.Run("size="+name, func(t *testing.T) {
			changes := map[string]any{"rig_type": "biped", "spec": "unreal"}
			if name != "" {
				changes["export_texture_size"] = name
			}
			r := shippedRigging(t, "tripo/processing/rigging-v3", changes)
			run := driveExports(t, r, "animations/rig")
			// The processing request carries the Rig's own fields only.
			want := fmt.Sprintf(`{"input":%q,"model":"v3.0-20260909","out_format":"glb","rig_type":"biped","spec":"unreal"}`, run.source)
			if len(run.base) != 1 || run.base[0] != want {
				t.Fatalf("rig request = %v, want exactly %s", run.base, want)
			}
			fbx, ok := run.converts["FBX"]
			if !ok || len(run.converts) != 1 {
				t.Fatalf("exports = %v, want the FBX alone", run.converts)
			}
			expected := map[string]any{
				"input": run.model, "format": "FBX", "texture_size": wire,
				"texture_format": "PNG", "with_animation": true, "bake": true, "fbx_preset": "blender",
			}
			if !reflect.DeepEqual(fbx, expected) {
				t.Fatalf("FBX export = %v, want %v", fbx, expected)
			}
		})
	}
}

// Every shipped Offering that exports files writes the size it was frozen with,
// for each of its formats, and keeps the rest of its fixed export.
func TestEveryProcessingExportWritesTheFrozenTextureSize(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range exportOfferings {
		offering, ok := product.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if err := ValidateOffering(offering); err != nil {
			t.Fatalf("%s: the shipped declaration is refused: %v", id, err)
		}
		var options providerOptions
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil || options.NativeConversion == nil {
			t.Fatalf("%s: %v", id, err)
		}
		c := options.NativeConversion
		for size, wire := range exportSizes {
			input, _ := json.Marshal(map[string]any{"export_texture_size": size})
			for _, format := range c.Formats {
				body, err := nativeConversionBody(offering, c, input, "https://assets.example/processed.glb", format.Format)
				if err != nil {
					t.Fatalf("%s %s %s: %v", id, size, format.Format, err)
				}
				// The fixed export is lossless PNG with the declared options.
				if body["texture_size"] != wire || body["texture_format"] != "PNG" || body["bake"] != true || body["fbx_preset"] != "blender" ||
					body["format"] != format.Format || body["input"] != "https://assets.example/processed.glb" {
					t.Fatalf("%s %s %s: export = %v", id, size, format.Format, body)
				}
				if len(body) != 7 {
					t.Fatalf("%s %s %s: export carries more than the declared fields: %v", id, size, format.Format, body)
				}
			}
		}
	}
}

// A Task frozen before the size was a parameter keeps the Offering it was
// accepted with: the export size is a constant of that snapshot, its input has
// no export_texture_size, and the declaration, the requests and the checkpoint
// are the ones it always had. The snapshot is the shipped rigging taken back to
// the shape of revision 2026-11-12.277.1.
func TestTaskFrozenBeforeTheSizeWasAParameterKeepsItsConstantExport(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"})
	legacy := r.Offering
	legacy.Revision = "2026-11-12.277.1"
	legacy.Parameters = nil
	for _, parameter := range r.Offering.Parameters {
		if parameter.Name != "export_texture_size" {
			legacy.Parameters = append(legacy.Parameters, parameter)
		}
	}
	var options map[string]any
	if err := json.Unmarshal(r.Offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	native := options["native_conversion"].(map[string]any)
	delete(native, "bindings")
	native["parameters"].(map[string]any)["texture_size"] = 8192
	legacy.ProviderOptions, _ = json.Marshal(options)
	var values map[string]json.RawMessage
	if err := json.Unmarshal(r.Input, &values); err != nil {
		t.Fatal(err)
	}
	delete(values, "export_texture_size")
	r.Offering = legacy
	r.Input, _ = json.Marshal(values)
	if err := ValidateOffering(legacy); err != nil {
		t.Fatalf("the snapshot's declaration is refused: %v", err)
	}
	run := driveExports(t, r, "animations/rig")
	want := fmt.Sprintf(`{"bake":true,"fbx_preset":"blender","format":"FBX","input":%q,"texture_format":"PNG","texture_size":8192,"with_animation":true}`, run.model)
	if run.sent["FBX"] != want {
		t.Fatalf("the snapshot's export = %s, want %s", run.sent["FBX"], want)
	}
}

// The default size is the request the exports had before the size became a
// public parameter: 8192, PNG, bake, the Blender preset and the declared
// animation flag, byte for byte, whatever the format.
func TestDefaultExportRequestIsUnchanged(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", nil)
	run := driveExports(t, r, "animations/rig")
	want := fmt.Sprintf(`{"bake":true,"fbx_preset":"blender","format":"FBX","input":%q,"texture_format":"PNG","texture_size":8192,"with_animation":true}`, run.model)
	if run.sent["FBX"] != want {
		t.Fatalf("default export = %s, want %s", run.sent["FBX"], want)
	}
}

// The geometry Offerings take the same parameter; a purpose size reaches each of
// their five exports, and the processing request never carries it.
func TestGeometryExportsCarryThePurposeTextureSize(t *testing.T) {
	for _, kind := range []string{"decimate-triangle", "segment", "complete", "quick-cap"} {
		t.Run(kind, func(t *testing.T) {
			r := geometryCatalogFixture(t, kind)
			var values map[string]json.RawMessage
			_ = json.Unmarshal(r.Input, &values)
			values["export_texture_size"] = json.RawMessage(`"2k"`)
			r.Input, _ = json.Marshal(values)
			endpoint := map[string]string{"decimate-triangle": "mesh/decimate", "segment": "mesh/segment", "complete": "mesh/complete", "quick-cap": "mesh/complete"}[kind]
			run := driveExports(t, r, endpoint)
			if len(run.base) != 1 || strings.Contains(run.base[0], "texture") {
				t.Fatalf("the processing request carries an export option: %v", run.base)
			}
			if len(run.converts) != 5 {
				t.Fatalf("exports = %d, want 5", len(run.converts))
			}
			for format, body := range run.converts {
				if body["texture_size"] != float64(2048) || body["texture_format"] != "PNG" || body["with_animation"] != false {
					t.Fatalf("%s export = %v", format, body)
				}
			}
		})
	}
}

// The requests the adapter sends under a game purpose are accepted by the Tripo
// v3 fake, which reads them against the official convert page (a refusal would
// have failed the step), and the fake shows them back on the task object the way
// the real API does: the rigging export asks for PNG textures of 2048 pixels,
// with the animation kept.
func TestGamePurposeRiggingExportIsAcceptedByTheTripoFake(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"rig_type": "biped", "spec": "unreal", "export_texture_size": "2k"})
	run := driveExports(t, r, "animations/rig")
	body := run.converts["FBX"]
	if body["texture_size"] != float64(2048) || body["texture_format"] != "PNG" || body["with_animation"] != true {
		t.Fatalf("game purpose export request = %v", body)
	}
	echoed := run.echoes["FBX"]
	if echoed["texture_size"] != float64(2048) || echoed["texture_format"] != "PNG" ||
		echoed["with_animation"] != true || echoed["format"] != "FBX" {
		t.Fatalf("the fake did not record the export options: %v", echoed)
	}

	// A size the adapter could never send is refused by the fake as the official
	// page says (texture_size is an integer), so the check above is not vacuous.
	refused := map[string]any{}
	for name, value := range body {
		refused[name] = value
	}
	refused["texture_size"] = "2k"
	if status, _, answer := run.fake.Do(http.MethodPost, tripov3.Prefix+"/models/convert", "test-key", refused); status != http.StatusBadRequest || answer["code"] != float64(1004) {
		t.Fatalf("the fake accepted a texture size that is not an integer: %d %v", status, answer)
	}
}

// A binding that cannot be written is refused with the declaration, before a
// paid job exists.
func TestProcessingExportBindingsAreValidatedWithTheDeclaration(t *testing.T) {
	base := shippedOffering(t, "tripo/processing/rigging-v3")
	mutations := map[string]func(native map[string]any){
		"a field the constants already send": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "export_texture_size", "wire": "texture_format"}}
		},
		"a reserved field": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "export_texture_size", "wire": "format"}}
		},
		"no field": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "export_texture_size"}}
		},
		"a parameter the Offering lacks": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "export_pivot", "wire": "pivot_to_center_bottom"}}
		},
		"the format selection": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "output_formats", "wire": "texture_size"}}
		},
		"the consent": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "authorize_extra_credits", "wire": "texture_size"}}
		},
		"the source": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "source_task", "wire": "texture_size"}}
		},
		"a format the exports do not produce": func(native map[string]any) {
			native["bindings"] = []any{map[string]any{"parameter": "export_texture_size", "wire": "texture_size", "formats": []any{"GLB"}}}
		},
		"one parameter twice": func(native map[string]any) {
			native["bindings"] = []any{
				map[string]any{"parameter": "export_texture_size", "wire": "texture_size"},
				map[string]any{"parameter": "export_texture_size", "wire": "texture_height"},
			}
		},
		"a wire declared by two bindings": func(native map[string]any) {
			native["bindings"] = []any{
				map[string]any{"parameter": "export_texture_size", "wire": "texture_size"},
				map[string]any{"parameter": "rig_type", "wire": "texture_size"},
			}
		},
	}
	if err := ValidateOffering(base); err != nil {
		t.Fatalf("the shipped declaration is refused: %v", err)
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var options map[string]any
			if err := json.Unmarshal(base.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}
			mutate(options["native_conversion"].(map[string]any))
			broken := base
			broken.ProviderOptions, _ = json.Marshal(options)
			if err := ValidateOffering(broken); err == nil {
				t.Fatal("the declaration was accepted")
			}
		})
	}
	// An option without an upstream value cannot be written either.
	broken := base
	broken.Parameters = append([]generation.Parameter(nil), base.Parameters...)
	for index := range broken.Parameters {
		if broken.Parameters[index].Name == "export_texture_size" {
			values := map[string]json.RawMessage{}
			for option, mapped := range broken.Parameters[index].WireValues {
				if option != "4k" {
					values[option] = mapped
				}
			}
			broken.Parameters[index].WireValues = values
		}
	}
	if err := ValidateOffering(broken); err == nil {
		t.Fatal("an option without an upstream value was accepted")
	}
}

// A value outside the declared sizes never reaches the upstream: the step is
// refused before the paid job is dispatched.
func TestProcessingRefusesAnUndeclaredExportSizeBeforeDispatch(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", nil)
	for _, size := range []string{`"16k"`, `2048`, `null`, `""`} {
		var values map[string]json.RawMessage
		_ = json.Unmarshal(r.Input, &values)
		values["export_texture_size"] = json.RawMessage(size)
		bad := r
		bad.Input, _ = json.Marshal(values)
		permit := &nativeDeliveryPermit{}
		if _, err := (&Adapter{}).Start(generation.WithDispatchCheckpointer(context.Background(), permit), bad); err == nil || permit.calls != 0 {
			t.Fatalf("size %s: err=%v permits=%d", size, err, permit.calls)
		}
	}
}

// A recorded export request that is not the one the frozen Offering and input
// write fails the checkpoint, so a ledger can neither carry another size nor an
// extra field into a paid export.
func TestExportCheckpointMustMatchTheFrozenTextureSize(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"export_texture_size": "2k"})
	var options providerOptions
	if err := json.Unmarshal(r.Offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	locator := "https://assets.example/original.glb"
	record := func(size string) nativeConversionLedger {
		input, _ := json.Marshal(map[string]any{"export_texture_size": size})
		body, err := nativeConversionBody(r.Offering, options.NativeConversion, input, locator, "FBX")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(body)
		return nativeConversionLedger{
			ConversionSource:   &nativeConversionSource{Alias: "model", SHA256: strings.Repeat("a", 64), Size: 16},
			Conversions:        map[string]conversionItem{"FBX": {Status: conversionItemSubmitted, RemoteID: "child_fbx"}},
			ConversionRequests: map[string]json.RawMessage{"FBX": raw},
		}
	}
	if err := nativeConversionCheckpoint(record("2k"), r.Offering, r.Input, options); err != nil {
		t.Fatalf("the request the frozen input writes was refused: %v", err)
	}
	for _, other := range []string{"4k", "8k"} {
		if err := nativeConversionCheckpoint(record(other), r.Offering, r.Input, options); err == nil {
			t.Fatalf("a recorded %s export was accepted for a 2k input", other)
		}
	}
}

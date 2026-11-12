package tripo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// #292: the private exports of a Tripo processing step write the texture size
// from the frozen input, so the size a session purpose bounds (a game: 2K) is
// the size of every exported file, while the default stays the request the
// exports have always been (8192). The tests use the shipped Product Schema and
// the real adapter; the scripted upstream plays the part of Tripo, and the
// requests it saw are then replayed to the Tripo v3 fake, which checks them
// against the parameters of the official convert page.

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

type scriptedExports struct {
	base     []string
	converts map[string]map[string]any
	observed generation.Observation
}

// driveExports runs a started native processing request to completion against a
// scripted upstream. Each observation uses a new adapter, as a restart does.
func driveExports(t *testing.T, r generation.StartRequest, endpoint string) scriptedExports {
	t.Helper()
	r.Offering.AssetHosts = []string{"assets.example"}
	run := scriptedExports{converts: map[string]map[string]any{}}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if q.Method == http.MethodPost {
			raw, _ := io.ReadAll(q.Body)
			if q.URL.Path == "/v3/models/convert" {
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				format, _ := body["format"].(string)
				run.converts[format] = body
				fmt.Fprintf(w, `{"code":0,"data":{"task_id":"child_%s"}}`, format)
				return
			}
			if q.URL.Path != "/v3/"+endpoint {
				t.Errorf("unexpected POST %s", q.URL.Path)
			}
			run.base = append(run.base, string(raw))
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			return
		}
		id := strings.TrimPrefix(q.URL.Path, "/v3/tasks/")
		locator := "https://assets.example/processed.glb"
		if id != "task_processed" {
			locator = "https://assets.example/" + id + ".bin"
		}
		fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q,"status":"success","credits_consumed":10,"output":{"model_url":%q}}}`, id, locator)
	}))
	defer server.Close()
	downloads := 0
	factory := func() *Adapter {
		a := rigAdapter(t, server)
		a.nativeSourceClient = nativeDeliveryTestSourceClient(nativeTestGLB, &downloads)
		return a
	}
	ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})
	accepted, err := factory().Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	for i := 0; i < 20 && run.observed.Phase != generation.UpstreamCompleted; i++ {
		if run.observed, err = factory().Observe(ctx, q); err != nil {
			t.Fatal(err)
		}
		q.Opaque = run.observed.Opaque
	}
	if run.observed.Phase != generation.UpstreamCompleted {
		t.Fatalf("the exports did not complete: %+v", run.observed)
	}
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
			want := `{"input":"task_native","model":"v3.0-20260909","out_format":"glb","rig_type":"biped","spec":"unreal"}`
			if len(run.base) != 1 || run.base[0] != want {
				t.Fatalf("rig request = %v, want exactly %s", run.base, want)
			}
			fbx, ok := run.converts["FBX"]
			if !ok || len(run.converts) != 1 {
				t.Fatalf("exports = %v, want the FBX alone", run.converts)
			}
			expected := map[string]any{
				"input": "https://assets.example/processed.glb", "format": "FBX", "texture_size": wire,
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

// The default size is the request the exports had before the size became a
// public parameter: 8192, PNG, bake, the Blender preset and the declared
// animation flag, byte for byte, whatever the format.
func TestDefaultExportRequestIsUnchanged(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", nil)
	run := driveExports(t, r, "animations/rig")
	raw, _ := json.Marshal(run.converts["FBX"])
	want := `{"bake":true,"fbx_preset":"blender","format":"FBX","input":"https://assets.example/processed.glb","texture_format":"PNG","texture_size":8192,"with_animation":true}`
	if string(raw) != want {
		t.Fatalf("default export = %s, want %s", raw, want)
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
// v3 fake, which reads them against the official convert page, and the fake
// shows them back on the task object the way the real API does: the rigging
// export asks for PNG textures of 2048 pixels, with the animation kept.
func TestGamePurposeRiggingExportIsAcceptedByTheTripoFake(t *testing.T) {
	r := shippedRigging(t, "tripo/processing/rigging-v3", map[string]any{"rig_type": "biped", "spec": "unreal", "export_texture_size": "2k"})
	run := driveExports(t, r, "animations/rig")
	body := run.converts["FBX"]
	if body["texture_size"] != float64(2048) || body["texture_format"] != "PNG" || body["with_animation"] != true {
		t.Fatalf("game purpose export request = %v", body)
	}

	fake, err := fakeupstream.New(fakeupstream.Config{}, tripov3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	call := func(method, path string, payload any) map[string]any {
		t.Helper()
		var reader io.Reader
		if payload != nil {
			encoded, _ := json.Marshal(payload)
			reader = strings.NewReader(string(encoded))
		}
		request, err := http.NewRequest(method, server.URL+tripov3.Prefix+"/"+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer test-key")
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var decoded map[string]any
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	created := call(http.MethodPost, "models/convert", body)
	if created["code"] != float64(0) {
		t.Fatalf("the fake refused the rigging export: %v", created)
	}
	taskID, _ := created["data"].(map[string]any)["task_id"].(string)
	var polled map[string]any
	for range 8 {
		polled, _ = call(http.MethodGet, "tasks/"+taskID, nil)["data"].(map[string]any)
		if polled["status"] == "success" {
			break
		}
	}
	echoed, _ := polled["input"].(map[string]any)
	if polled["status"] != "success" || echoed["texture_size"] != float64(2048) || echoed["texture_format"] != "PNG" ||
		echoed["with_animation"] != true || echoed["format"] != "FBX" {
		t.Fatalf("the fake did not record the export options: %v", polled)
	}

	// A size the adapter could never send is refused by the fake as the official
	// page says (texture_size is an integer), so the check above is not vacuous.
	body["texture_size"] = "2k"
	if refused := call(http.MethodPost, "models/convert", body); refused["code"] == float64(0) {
		t.Fatalf("the fake accepted a texture size that is not an integer: %v", refused)
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

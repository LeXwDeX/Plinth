package tripo

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func nativeDeliveryFixture(cap generation.Capability) generation.StartRequest {
	r := nativeProcessingFixture(cap)
	r.Offering.AssetHosts = []string{"assets.example"}
	r.Offering.Outputs[0].Delivery = schema.AssetDeliveryRequired
	formats := []string{"FBX"}
	if cap == generation.Retexture {
		formats = []string{"FBX", "OBJ", "STL", "USDZ", "3MF"}
	}
	raw, _ := json.Marshal(formats)
	opts := []generation.ParameterOption{}
	outputs := []map[string]string{}
	one := 1
	for _, f := range formats {
		opts = append(opts, generation.ParameterOption{Value: f})
		role, variant, media := "converted_model", "converted."+strings.ToLower(f), "application/octet-stream"
		if cap == generation.Rigging {
			role, variant, media = "model", "fbx", "model/fbx"
		}
		r.Offering.Outputs = append(r.Offering.Outputs, generation.AssetRequirement{Role: role, Variant: variant, Minimum: &one, Delivery: schema.AssetDeliveryRequired, MediaTypes: []string{media}, MaxBytes: 1 << 30})
		outputs = append(outputs, map[string]string{"format": f, "role": role, "variant": variant, "media_type": media})
	}
	r.Offering.Parameters = append(r.Offering.Parameters, generation.Parameter{Name: "output_formats", Type: "string-array", Required: true, Fixed: true, Default: raw, Options: opts}, generation.Parameter{Name: "authorize_extra_credits", Type: "boolean"})
	r.Offering.Cost = &schema.CostDeclaration{ExtraCredits: 10, SelectionParameter: "output_formats", ConsentParameter: "authorize_extra_credits"}
	r.Offering.CostPolicy = &schema.CostPolicy{MaxAuthorizedExtraCredits: 10 * len(formats)}
	var values map[string]json.RawMessage
	json.Unmarshal(r.Input, &values)
	values["output_formats"] = raw
	values["authorize_extra_credits"] = json.RawMessage(`true`)
	r.Input, _ = json.Marshal(values)
	var options map[string]any
	json.Unmarshal(r.Offering.ProviderOptions, &options)
	options["convert_endpoint"] = "models/convert"
	options["native_conversion"] = map[string]any{"source_alias": "model_url", "max_source_bytes": 150000000, "parameters": map[string]any{"texture_size": 8192, "texture_format": "PNG", "with_animation": cap == generation.Rigging, "bake": true, "fbx_preset": "blender"}, "formats": outputs}
	r.Offering.ProviderOptions, _ = json.Marshal(options)
	return r
}

type nativeRoundTrip func(*http.Request) (*http.Response, error)

func (f nativeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func nativeTestGLB() []byte {
	b := make([]byte, 16)
	copy(b, "glTF")
	binary.LittleEndian.PutUint32(b[4:], 2)
	binary.LittleEndian.PutUint32(b[8:], uint32(len(b)))
	return b
}

func TestNativeDeliveryDeclarationAndConsent(t *testing.T) {
	for _, cap := range []generation.Capability{generation.Rigging, generation.Retexture} {
		r := nativeDeliveryFixture(cap)
		if err := ValidateOffering(r.Offering); err != nil {
			t.Fatal(err)
		}
		if _, _, body, _, err := nativeProcessingInput(r.Offering, r.Input); err != nil {
			t.Fatal(err)
		} else if body["output_formats"] != nil || body["authorize_extra_credits"] != nil {
			t.Fatal("private control leaked")
		}
		var values map[string]json.RawMessage
		json.Unmarshal(r.Input, &values)
		values["authorize_extra_credits"] = json.RawMessage(`false`)
		r.Input, _ = json.Marshal(values)
		p := &rigCheckPermit{}
		if _, err := (&Adapter{}).Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil || p.calls != 0 {
			t.Fatalf("missing cost consent dispatched %v", err)
		}
	}
}

func TestNativeDeliveryRigExactWireRestoreAndRetainedBase(t *testing.T) {
	r := nativeDeliveryFixture(generation.Rigging)
	posts, downloads := 0, 0
	var latest json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "POST" {
			posts++
			raw, _ := io.ReadAll(q.Body)
			if strings.HasSuffix(q.URL.Path, "models/convert") {
				want := `{"bake":true,"fbx_preset":"blender","format":"FBX","input":"https://assets.example/processed.glb","texture_format":"PNG","texture_size":8192,"with_animation":true}`
				if string(raw) != want {
					t.Errorf("conversion wire %s", raw)
				}
				if !strings.Contains(string(latest), `"status":"dispatching"`) || !strings.Contains(string(latest), `"sha256":`) {
					t.Error("conversion not durably byte-bound")
				}
				io.WriteString(w, `{"code":0,"data":{"task_id":"child_fbx"}}`)
			} else {
				if strings.Contains(string(raw), "output_formats") || strings.Contains(string(raw), "authorize_extra") {
					t.Error("control leaked")
				}
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			}
			return
		}
		if strings.HasSuffix(q.URL.Path, "child_fbx") {
			io.WriteString(w, `{"code":0,"data":{"task_id":"child_fbx","status":"success","credits_consumed":10.25,"output":{"model_url":"https://assets.example/result.fbx"}}}`)
		} else {
			io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed","status":"success","credits_consumed":25.75,"output":{"model_url":"https://assets.example/processed.glb"}}}`)
		}
	}))
	defer server.Close()
	newAdapter := func() *Adapter {
		a := rigAdapter(t, server)
		a.nativeSourceClient = &http.Client{Transport: nativeRoundTrip(func(q *http.Request) (*http.Response, error) {
			downloads++
			if q.Header.Get("Authorization") != "" || q.URL.Host != "assets.example" {
				t.Fatal("not anonymous CDN")
			}
			b := nativeTestGLB()
			return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: int64(len(b)), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
		})}
		return a
	}
	p := &nativeDeliveryPermit{capture: func(b json.RawMessage) { latest = append(json.RawMessage(nil), b...) }}
	ctx := generation.WithDispatchCheckpointer(context.Background(), p)
	accepted, err := newAdapter().Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	var obs generation.Observation
	for i := 0; i < 5; i++ {
		obs, err = newAdapter().Observe(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if obs.Sequence != 0 || !obs.ObservedAt.IsZero() {
			t.Fatal("fabricated source ordering")
		}
		q.Opaque = obs.Opaque
		if obs.Phase == generation.UpstreamCompleted {
			break
		}
	}
	if obs.Phase != generation.UpstreamCompleted || len(obs.Artifacts) != 2 || obs.Artifacts[0].Locator != "https://assets.example/processed.glb" || obs.Artifacts[1].Variant != "fbx" {
		t.Fatalf("incomplete claims %+v", obs)
	}
	if posts != 2 || downloads < 1 || !strings.Contains(string(obs.Opaque), `"consumed_credit":25.75`) || !strings.Contains(string(obs.Opaque), `"credits_consumed":10.25`) {
		t.Fatalf("lost evidence posts%d downloads%d opaque%s", posts, downloads, obs.Opaque)
	}
}

type nativeDeliveryPermit struct {
	capture func(json.RawMessage)
	calls   int
}

func (p *nativeDeliveryPermit) Checkpoint(_ context.Context, i generation.DispatchIntent) (generation.DispatchAck, error) {
	p.calls++
	if p.capture != nil {
		p.capture(i.Bytes)
	}
	return generation.DispatchAck{Sequence: int64(p.calls), AckToken: fmt.Sprint(p.calls), Opaque: i.Bytes}, nil
}

func nativeDeliveryTestSourceClient(body func() []byte, calls *int) *http.Client {
	return &http.Client{Transport: nativeRoundTrip(func(*http.Request) (*http.Response, error) {
		if calls != nil {
			(*calls)++
		}
		b := body()
		return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: int64(len(b)), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
}

func TestNativeDeliveryTextureFiveFormatsSurviveRestarts(t *testing.T) {
	nativeDeliveryFiveFormatsSurviveRestarts(t, nativeDeliveryFixture(generation.Retexture))
}

// Exercise the shared private export protocol with any frozen native operation.
func nativeDeliveryFiveFormatsSurviveRestarts(t *testing.T, r generation.StartRequest) {
	t.Helper()
	posts, downloads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "POST" {
			posts++
			if strings.HasSuffix(q.URL.Path, "models/convert") {
				var body map[string]any
				json.NewDecoder(q.Body).Decode(&body)
				if body["with_animation"] != false || body["texture_size"] != float64(8192) || body["input"] != "https://assets.example/original.glb" {
					t.Error("wrong frozen export parameters")
				}
				fmt.Fprintf(w, `{"code":0,"data":{"task_id":"child_%s"}}`, body["format"])
			} else {
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			}
			return
		}
		id := strings.TrimPrefix(q.URL.Path, "/v3/tasks/")
		locator := "https://assets.example/original.glb"
		if id != "task_processed" {
			locator = "https://assets.example/" + id + ".bin"
		}
		fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q,"status":"success","credits_consumed":3.75,"output":{"model_url":%q}}}`, id, locator)
	}))
	defer server.Close()
	factory := func() *Adapter {
		a := rigAdapter(t, server)
		a.nativeSourceClient = nativeDeliveryTestSourceClient(nativeTestGLB, &downloads)
		return a
	}
	p := &nativeDeliveryPermit{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), p)
	accepted, err := factory().Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	last := 0
	var observation generation.Observation
	for i := 0; i < 16; i++ {
		observation, err = factory().Observe(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if observation.Progress <= last {
			t.Fatalf("child stage not strictly increasing %d→%d", last, observation.Progress)
		}
		last = observation.Progress
		q.Opaque = observation.Opaque
		if observation.Phase == generation.UpstreamCompleted {
			break
		}
		if len(observation.Artifacts) != 0 || observation.Job.RemoteID != "task_processed" {
			t.Fatal("private export became public task/artifact")
		}
	}
	if observation.Phase != generation.UpstreamCompleted || len(observation.Artifacts) != 6 || posts != 6 || downloads != 6 {
		t.Fatalf("incomplete formats posts%d downloads%d claims%+v", posts, downloads, observation.Artifacts)
	}
	if observation.Artifacts[0].Locator != "https://assets.example/original.glb" {
		t.Fatal("source replaced")
	}
	var state nativeProcessingState
	json.Unmarshal(observation.Opaque, &state)
	if len(state.Conversions) != 5 || len(state.ConversionRequests) != 5 {
		t.Fatal("lost child evidence")
	}
	for _, item := range state.Conversions {
		if string(item.CreditsConsumed) != "3.75" || item.Status != conversionItemDone {
			t.Fatal("lost child credits")
		}
	}
}

func TestNativeDeliveryDeclarationFailsClosed(t *testing.T) {
	mutations := []func(*generation.StartRequest, map[string]any){
		func(_ *generation.StartRequest, c map[string]any) { c["source_alias"] = "unknown" },
		func(_ *generation.StartRequest, c map[string]any) { c["max_source_bytes"] = 150000001 },
		func(_ *generation.StartRequest, c map[string]any) {
			c["parameters"].(map[string]any)["input"] = "override"
		},
		func(_ *generation.StartRequest, c map[string]any) {
			c["parameters"].(map[string]any)["texture_size"] = []any{8192}
		},
		func(_ *generation.StartRequest, c map[string]any) { c["formats"] = []any{} },
		func(_ *generation.StartRequest, c map[string]any) {
			c["formats"].([]any)[0].(map[string]any)["media_type"] = "application/octet-stream"
		},
		func(r *generation.StartRequest, _ map[string]any) { r.Offering.Cost = nil },
		func(r *generation.StartRequest, _ map[string]any) {
			r.Offering.Parameters[len(r.Offering.Parameters)-2].Fixed = false
		},
		func(r *generation.StartRequest, _ map[string]any) {
			r.Offering.Parameters[len(r.Offering.Parameters)-1].Fixed = true
		},
	}
	for i, mutate := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := nativeDeliveryFixture(generation.Rigging)
			var opts map[string]any
			json.Unmarshal(r.Offering.ProviderOptions, &opts)
			mutate(&r, opts["native_conversion"].(map[string]any))
			r.Offering.ProviderOptions, _ = json.Marshal(opts)
			p := &rigCheckPermit{}
			if err := ValidateOffering(r.Offering); err == nil {
				t.Fatal("bad declaration accepted")
			}
			if _, err := (&Adapter{}).Start(generation.WithDispatchCheckpointer(context.Background(), p), r); err == nil || p.calls != 0 {
				t.Fatal("bad declaration got permit")
			}
		})
	}
}

func TestNativeDeliveryMaximumFormatStagesStrictlyIncrease(t *testing.T) {
	r := generation.ObserveRequest{}
	for count := 1; count <= maximumNativeConversionFormats; count++ {
		state := nativeProcessingState{nativeConversionLedger: nativeConversionLedger{Conversions: map[string]conversionItem{}}}
		run := &nativeConversionRun{ledger: &state.nativeConversionLedger, encode: func() json.RawMessage { return state.encode() }}
		last := nativeConversionRunning(r, run, generation.Observation{}, count).Progress
		for i := 0; i < count; i++ {
			key := fmt.Sprint(i)
			for _, status := range []string{conversionItemSubmitted, conversionItemDone} {
				state.Conversions[key] = conversionItem{Status: status, RemoteID: "child"}
				obs := nativeConversionRunning(r, run, generation.Observation{}, count)
				if obs.Progress <= last || obs.Progress > 98 || obs.Sequence != 0 || !obs.ObservedAt.IsZero() {
					t.Fatalf("invalid stages count%d %d→%d", count, last, obs.Progress)
				}
				last = obs.Progress
			}
		}
	}
	rStart := nativeDeliveryFixture(generation.Rigging)
	var options map[string]any
	json.Unmarshal(rStart.Offering.ProviderOptions, &options)
	c := options["native_conversion"].(map[string]any)
	formats := c["formats"].([]any)
	for len(formats) <= maximumNativeConversionFormats {
		formats = append(formats, formats[0])
	}
	c["formats"] = formats
	rStart.Offering.ProviderOptions, _ = json.Marshal(options)
	if ValidateOffering(rStart.Offering) == nil {
		t.Fatal("unbounded conversion set accepted")
	}
}

func TestNativeDeliveryUnknownResponseAndPermitNeverReplay(t *testing.T) {
	for _, mode := range []string{"missing-permit", "pending-permit", "malformed", "null-code", "duplicate-id", "missing-id", "business-rejected"} {
		t.Run(mode, func(t *testing.T) {
			posts, gets, downloads := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method == "POST" {
					posts++
					if strings.HasSuffix(q.URL.Path, "models/convert") {
						responses := map[string]string{"malformed": "not-json", "null-code": `{"code":null,"data":{"task_id":"child"}}`, "duplicate-id": `{"code":0,"data":{"task_id":"child","task_id":"other"}}`, "missing-id": `{"code":0,"data":{}}`, "business-rejected": `{"code":1004,"message":"private URL removed"}`}
						io.WriteString(w, responses[mode])
					} else {
						io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
					}
					return
				}
				gets++
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed","status":"success","output":{"model_url":"https://assets.example/source.glb"}}}`)
			}))
			defer server.Close()
			r := nativeDeliveryFixture(generation.Rigging)
			a := rigAdapter(t, server)
			a.nativeSourceClient = nativeDeliveryTestSourceClient(nativeTestGLB, &downloads)
			accepted, err := a.Start(generation.WithDispatchCheckpointer(context.Background(), &rigCheckPermit{}), r)
			if err != nil {
				t.Fatal(err)
			}
			q := rigObserve(r, accepted)
			ctx := context.Background()
			p := &rigCheckPermit{}
			if mode != "missing-permit" {
				if mode == "pending-permit" {
					p.pending = true
				}
				ctx = generation.WithDispatchCheckpointer(ctx, p)
			}
			obs, err := a.Observe(ctx, q)
			if mode == "business-rejected" {
				if err != nil || obs.Phase != generation.UpstreamFailed || obs.Dispatch == nil {
					t.Fatalf("business rejection became ambiguous %v %+v", err, obs)
				}
				return
			}
			if err == nil {
				t.Fatal("missing permit/bad response accepted")
			}
			if mode == "missing-permit" || mode == "pending-permit" {
				if posts != 1 || mode == "missing-permit" && downloads != 0 {
					t.Fatal("unpermitted paid dispatch")
				}
				return
			}
			var provider *generation.ProviderError
			if !errors.As(err, &provider) || !provider.Failure.Ambiguous {
				t.Fatalf("malformed response not unknown %v", err)
			}
			q.Opaque = p.intent
			beforeGets := gets
			if _, err := a.Observe(ctx, q); err == nil || posts != 2 || gets != beforeGets {
				t.Fatalf("uncertain replay posts%d gets%d err%v", posts, gets, err)
			}
		})
	}
}

func TestNativeDeliveryChangedSourceBytesStopFurtherConversion(t *testing.T) {
	r := nativeDeliveryFixture(generation.Retexture)
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "POST" {
			posts++
			if strings.HasSuffix(q.URL.Path, "models/convert") {
				io.WriteString(w, `{"code":0,"data":{"task_id":"child_FBX"}}`)
			} else {
				io.WriteString(w, `{"code":0,"data":{"task_id":"task_processed"}}`)
			}
			return
		}
		id := strings.TrimPrefix(q.URL.Path, "/v3/tasks/")
		fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q,"status":"success","output":{"model_url":"https://assets.example/model.glb"}}}`, id)
	}))
	defer server.Close()
	a := rigAdapter(t, server)
	body := nativeTestGLB()
	a.nativeSourceClient = nativeDeliveryTestSourceClient(func() []byte { return body }, nil)
	ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})
	accepted, err := a.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	for range 2 {
		obs, err := a.Observe(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		q.Opaque = obs.Opaque
	}
	body[len(body)-1] = 1
	if _, err := a.Observe(ctx, q); err == nil || posts != 2 {
		t.Fatalf("changed bytes dispatched another conversion posts%d err%v", posts, err)
	}
}

func TestNativeDeliveryCatalogDeclarationAndFrozenQuote(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id           generation.OfferingID
		total, count int
	}{{"tripo/processing/rigging", 35, 1}, {"tripo/processing/rigging-v3", 35, 1}, {"tripo/processing/texture", 80, 5}} {
		t.Run(string(tc.id), func(t *testing.T) {
			o, ok := c.Get(tc.id)
			if !ok {
				t.Fatal("missing catalog offering")
			}
			if err := ValidateOffering(o); err != nil {
				t.Fatalf("actual catalog contract rejected %v", err)
			}
			values := map[string]json.RawMessage{}
			for _, p := range o.Parameters {
				if len(p.Default) > 0 {
					values[p.Name] = p.Default
				}
				if p.Semantic == schema.SemanticSourceTask {
					values[p.Name] = json.RawMessage(`"source"`)
				}
				if p.Semantic == schema.SemanticPrompt {
					values[p.Name] = json.RawMessage(`"blue gold uniform"`)
				}
			}
			values[o.Cost.ConsentParameter] = json.RawMessage(`true`)
			raw, _ := json.Marshal(values)
			_, _, body, options, err := nativeProcessingInput(o, raw)
			if err != nil {
				t.Fatal(err)
			}
			if o.EstimateTotalCredits(raw) != tc.total || len(options.NativeConversion.Formats) != tc.count || body[o.Cost.SelectionParameter] != nil || body[o.Cost.ConsentParameter] != nil {
				t.Fatalf("quote/wire mismatch %d %+v", o.EstimateTotalCredits(raw), body)
			}
		})
	}
}

package tripo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// The eight Offerings of the seven Tripo v3 endpoints that #89 first sampled
// from the official API (image-to-model, image-to-splat, image-to-multiview,
// edit-multiview, models/texture, mesh/decimate, mesh/smartsegment in both
// seg_type modes). Those samples overturned several assumptions that were made
// from the documentation (the decimate and smartsegment task types, output
// fields, progress). The fake was corrected then; this test holds the product
// side to the same samples: the Catalog declaration the Offering ships and the
// real adapter code must accept every captured poll, map every captured status,
// claim every output the sample returned and cover every required slot.
//
// The samples live next to the fake that was derived from them. They are
// sanitized (placeholder ids, example hosts) but structurally the official
// responses; a re-captured sample changes the expectation here too.
const realSampleDirectory = "../../testutil/fakeupstream/tripo/v3/testdata"

type realSampleCase struct {
	stem, offering, endpoint, taskType string
	// claims maps "role:variant" to the dotted output path the sample returns it at.
	claims map[string]string
	// protocol selects the claim extraction: "" is the alias mapping every
	// non-multiview protocol shares, "multiview" is the nested view mapping.
	protocol string
	// intentionallyCheaper names fixed Catalog values the sampling run replaced
	// with the cheapest option to save credits.
	intentionallyCheaper []string
}

var realSampleCases = []realSampleCase{
	{stem: "image-to-model", offering: "tripo/tripo-v3.1-full/image-to-3d", endpoint: "generation/image-to-model", taskType: "image_to_model",
		claims: map[string]string{"model:": "model_url", "thumbnail:rendered_image": "rendered_image_url"}},
	{stem: "image-to-splat", offering: "tripo/image-to-splat", endpoint: "generation/image-to-splat", taskType: splatTaskType,
		claims: map[string]string{"gaussian-splat:splat": "model_url"}},
	{stem: "image-to-multiview", offering: "tripo/image-to-multiview", endpoint: "generation/image-to-multiview", taskType: "image_to_multiview", protocol: "multiview",
		claims: map[string]string{
			"image:front": "generate_multiview_image.front_view_url", "image:left": "generate_multiview_image.left_view_url",
			"image:back": "generate_multiview_image.back_view_url", "image:right": "generate_multiview_image.right_view_url"}},
	{stem: "edit-multiview", offering: "tripo/edit-multiview", endpoint: "generation/edit-multiview", taskType: "edit_multiview", protocol: "multiview",
		claims: map[string]string{"image:front": "generate_multiview_image.front_view_url"}},
	{stem: "texture", offering: "tripo/processing/texture", endpoint: "models/texture", taskType: "texture_model",
		intentionallyCheaper: []string{"pbr", "texture_quality"},
		claims:               map[string]string{"model:glb": "model_url", "thumbnail:rendered_image": "rendered_image_url"}},
	{stem: "decimate", offering: "tripo/geometry/decimate-triangle", endpoint: "mesh/decimate", taskType: "highpoly_to_lowpoly",
		claims: map[string]string{"model:glb": "model_url", "thumbnail:rendered_image": "rendered_image_url"}},
	{stem: "smartsegment-image", offering: "tripo/geometry/smartsegment-image", endpoint: "mesh/smartsegment", taskType: "smartsegment_image",
		claims: map[string]string{"model:glb": "seg_model_url", "mask:png": "mask_url"}},
	{stem: "smartsegment-model", offering: "tripo/geometry/smartsegment-model", endpoint: "mesh/smartsegment", taskType: "smartsegment_model",
		claims: map[string]string{"model:glb": "seg_model_url", "mask:png": "mask_url"}},
}

func loadRealSample(t *testing.T, stem string) []wirefixture.Exchange {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(os.DirFS(realSampleDirectory), "tripo-v3-"+stem+".candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	return samples.Exchanges
}

func realSampleEnvelopeData(t *testing.T, exchange wirefixture.Exchange) map[string]any {
	t.Helper()
	var envelope struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(exchange.Response.Body.JSON, &envelope); err != nil || envelope.Code != 0 {
		t.Fatalf("sample #%d is not a v3 success envelope: %v", exchange.Sequence, err)
	}
	return envelope.Data
}

func outputAt(output map[string]any, path string) string {
	var current any = output
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[part]
	}
	value, _ := current.(string)
	return value
}

func realSampleMappings(t *testing.T) (*generation.ErrorMapping, *generation.StatusMapping) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.AdapterKey == "tripo.v3" {
			return connection.ErrorMapping, connection.StatusMapping
		}
	}
	t.Fatal("the default Catalog has no tripo.v3 connection")
	return nil, nil
}

// realSamplePollServer answers GET /v3/tasks/id-2 with the sample poll the
// returned index selects, byte for byte, so the adapter parses what the official
// API sent.
func realSamplePollServer(t *testing.T, polls []wirefixture.Exchange) (*httptest.Server, *int) {
	t.Helper()
	index := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v3/tasks/id-2" || *index >= len(polls) {
			t.Errorf("unexpected wire %s %s (poll %d of %d)", r.Method, r.URL.Path, *index, len(polls))
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(polls[*index].Response.Body.JSON)
	}))
	t.Cleanup(server.Close)
	return server, index
}

func TestOfficialSamplesMatchTheCatalogDeclarationsAndAdapterMappings(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	errorMapping, statusMapping := realSampleMappings(t)
	phaseOf := map[string]generation.UpstreamPhase{
		"queued": generation.UpstreamAccepted, "running": generation.UpstreamRunning,
		"success": generation.UpstreamCompleted, "failed": generation.UpstreamFailed,
	}
	for _, tc := range realSampleCases {
		t.Run(tc.stem, func(t *testing.T) {
			offering, ok := product.Get(generation.OfferingID(tc.offering))
			if !ok || !offering.Enabled {
				t.Fatalf("Offering %s is not enabled in the default Catalog", tc.offering)
			}
			exchanges := loadRealSample(t, tc.stem)
			create, polls := exchanges[0], exchanges[1:]
			var options providerOptions
			if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
				t.Fatal(err)
			}

			// Request: route, model and body keys.
			if create.Request.Method != http.MethodPost || create.Request.Path != "/v3/"+tc.endpoint || options.Endpoint != tc.endpoint {
				t.Fatalf("route: sample %s %s, Catalog endpoint %q, want %q", create.Request.Method, create.Request.Path, options.Endpoint, tc.endpoint)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(create.Request.Body.JSON, &body); err != nil {
				t.Fatal(err)
			}
			allowed := map[string]bool{"input": true, "prompts": true, "transform": true}
			if options.Model != "" {
				allowed["model"] = true
			}
			fixed := map[string]string{}
			for _, p := range offering.Parameters {
				key := p.WireName
				if key == "" {
					key = p.Name
				}
				allowed[strings.SplitN(key, ".", 2)[0]] = true
				if p.Fixed && p.Type == "boolean" {
					fixed[key] = string(p.Default)
				}
			}
			for key, value := range body {
				if !allowed[key] {
					t.Errorf("the official request carries %q, which the Offering declares no parameter or option for", key)
				}
				if declared, isFixed := fixed[key]; isFixed && string(value) != declared && !slices.Contains(tc.intentionallyCheaper, key) {
					t.Errorf("fixed %s: Catalog %s, official request %s", key, declared, value)
				}
			}
			if options.Model != "" {
				var model string
				if json.Unmarshal(body["model"], &model) != nil || model != options.Model {
					t.Errorf("model: Catalog %q, official request %s", options.Model, body["model"])
				}
			} else if _, sent := body["model"]; sent {
				t.Errorf("the official request carries a model but the Catalog declares none")
			}

			// Create response: the task type the adapter's checks rely on.
			created := realSampleEnvelopeData(t, create)
			if created["type"] != tc.taskType || created["status"] != "queued" {
				t.Fatalf("create response type/status = %v/%v, want %s/queued", created["type"], created["status"], tc.taskType)
			}
			var view multiviewOptions
			if tc.protocol == "multiview" {
				if _, view, err = multiviewDeclaration(offering); err != nil {
					t.Fatal(err)
				}
				if view.ResponseType != tc.taskType {
					t.Errorf("response_type = %q, sample task type %q", view.ResponseType, tc.taskType)
				}
			}
			if strings.HasPrefix(tc.stem, "smartsegment") {
				if got := smartSegmentTaskType(offering); got != tc.taskType {
					t.Errorf("smartSegmentTaskType = %q, sample %q", got, tc.taskType)
				}
			}

			// Polls: the strict parsers accept every captured body, every status
			// word is declared, and the final success claims what the sample returned.
			server, index := realSamplePollServer(t, polls)
			adapter, err := New(Config{BaseURL: server.URL + "/v3", APIKey: "replay", HTTPClient: server.Client(), ErrorMapping: errorMapping, StatusMapping: statusMapping})
			if err != nil {
				t.Fatal(err)
			}
			request := generation.ObserveRequest{TaskID: "task", AttemptID: "attempt", Offering: offering,
				Job: generation.UpstreamJob{ProviderID: offering.ProviderID, RemoteID: "id-2"}}
			var final generation.Observation
			for i, poll := range polls {
				data := realSampleEnvelopeData(t, poll)
				status, _ := data["status"].(string)
				if data["type"] != tc.taskType {
					t.Fatalf("poll %d reports type %v, want %s", i+1, data["type"], tc.taskType)
				}
				*index = i
				observed, err := adapter.observeTask(context.Background(), options, "id-2", "replay")
				if err != nil {
					t.Fatalf("poll %d (%s): observeTask: %v", i+1, status, err)
				}
				// observeNativeTask is the strict parser texture and decimate use; it
				// must accept the same official body.
				native, err := adapter.observeNativeTask(context.Background(), options, "id-2", "replay")
				if err != nil {
					t.Fatalf("poll %d (%s): observeNativeTask: %v", i+1, status, err)
				}
				// The native parser deliberately does not read the task type (texture and
				// decimate never depended on it); the generic parser does.
				if native.Status != observed.Status || native.Progress != observed.Progress || observed.Type != tc.taskType {
					t.Fatalf("poll %d: parsers disagree: %+v vs %+v", i+1, native, observed)
				}
				observation, err := adapter.observation(request, observed)
				if err != nil {
					t.Fatalf("poll %d (%s): observation: %v", i+1, status, err)
				}
				if want, known := phaseOf[status]; !known || observation.Phase != want {
					t.Fatalf("poll %d: status %q mapped to phase %q, want %q", i+1, status, observation.Phase, want)
				}
				final = observation
			}
			if final.Phase != generation.UpstreamCompleted {
				t.Fatalf("the sample ends in %q", final.Phase)
			}

			// Claims.
			output, _ := realSampleEnvelopeData(t, polls[len(polls)-1])["output"].(map[string]any)
			claimed := map[string]string{}
			artifacts := final.Artifacts
			if tc.protocol == "multiview" {
				wanted := view.ViewOrder
				if tc.stem == "edit-multiview" {
					wanted = []string{"front"}
				}
				artifacts, err = multiviewClaims(output, "attempt", wanted)
				if err != nil {
					t.Fatalf("multiviewClaims: %v", err)
				}
			}
			for _, claim := range artifacts {
				claimed[claim.Role+":"+claim.Variant] = claim.Locator
			}
			for key, path := range tc.claims {
				if want := outputAt(output, path); want == "" || claimed[key] != want {
					t.Errorf("claim %s: got %q, the sample returns %q at %s", key, claimed[key], want, path)
				}
			}
			for key := range claimed {
				if _, wanted := tc.claims[key]; !wanted {
					t.Errorf("unexpected claim %s", key)
				}
			}

			// Slots: every required non-converted slot the Offering declares is
			// claimed by the sample's output (conditional slots by the sample's prompts).
			var prompted map[string]bool
			if prompts, ok := realSampleRequestPrompts(body); ok {
				prompted = prompts
			}
			for _, slot := range offering.Outputs {
				if slot.Role == "converted_model" || slot.Delivery != "required" || slot.Minimum != nil && *slot.Minimum == 0 {
					continue
				}
				if slot.RequiredWhen != nil && !prompted[strings.TrimSuffix(slot.RequiredWhen.Parameter, "_prompt")] {
					continue
				}
				if _, ok := claimed[slot.Role+":"+slot.Variant]; !ok {
					t.Errorf("required slot %s:%s is not claimed from the official output", slot.Role, slot.Variant)
				}
			}
		})
	}
}

func realSampleRequestPrompts(body map[string]json.RawMessage) (map[string]bool, bool) {
	var prompts []struct {
		View string `json:"view"`
	}
	if json.Unmarshal(body["prompts"], &prompts) != nil || len(prompts) == 0 {
		return nil, false
	}
	views := map[string]bool{}
	for _, p := range prompts {
		views[p.View] = true
	}
	return views, true
}

// The one failed official task: the shaded-sphere Smart Segmentation of #89 ended
// after minutes of polling with status failed, error_code 1001, no output and no
// progress. It must end the attempt as a rejection without claiming anything.
func TestOfficialFailedSmartSegmentationEndsTheAttemptWithoutArtifacts(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := product.Get("tripo/geometry/smartsegment-image")
	exchanges := loadRealSample(t, "smartsegment-image-failed")
	polls := exchanges[1:]
	errorMapping, statusMapping := realSampleMappings(t)
	server, index := realSamplePollServer(t, polls)
	adapter, err := New(Config{BaseURL: server.URL + "/v3", APIKey: "replay", HTTPClient: server.Client(), ErrorMapping: errorMapping, StatusMapping: statusMapping})
	if err != nil {
		t.Fatal(err)
	}
	var options providerOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	request := generation.ObserveRequest{TaskID: "task", AttemptID: "attempt", Offering: offering,
		Job: generation.UpstreamJob{ProviderID: offering.ProviderID, RemoteID: "id-2"}}
	var last generation.Observation
	for i := range polls {
		*index = i
		value, err := adapter.observeTask(context.Background(), options, "id-2", "replay")
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		if value.Type != smartSegmentTaskType(offering) {
			t.Fatalf("poll %d reports %q", i+1, value.Type)
		}
		if last, err = adapter.observation(request, value); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	if last.Phase != generation.UpstreamFailed || last.Failure == nil || last.Failure.Class != generation.FailureRejected || len(last.Artifacts) != 0 {
		t.Fatalf("final observation = %+v", last)
	}
}

package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Synthetic minimal success examples follow these v1 endpoint references
// (checked 2026-10-17):
// https://docs.meshy.ai/en/api/creative-lab-keychain
// https://docs.meshy.ai/en/api/creative-lab-fridge-magnet
// https://docs.meshy.ai/en/api/creative-lab-figure
// https://docs.meshy.ai/en/api/creative-lab-vinyl-figure
// https://docs.meshy.ai/en/api/creative-lab-brick-figure
// https://docs.meshy.ai/en/api/creative-lab-lamp
// https://docs.meshy.ai/en/api/creative-lab-keycap
// https://docs.meshy.ai/en/api/creative-lab-fidget-collapsible
// https://docs.meshy.ai/en/api/creative-lab-fidget-pixel
// The independent
// 2026-10-17 redacted live samples cover only Collapsible, Fridge, Figure,
// Vinyl, and Brick; they are observations, not universal response guarantees.
// Existing exact-wire tests pin product defaults; this checks that real Adapter
// projection accepts different opaque IDs/asset URLs with the documented shape.
func TestSimpleCreativeLabSeventeenStageResponseShape(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	products := []string{"keychain", "fridge-magnet", "figure", "vinyl-figure", "brick-figure", "lamp", "keycap", "fidget-pixel", "fidget-collapsible"}
	count := 0
	for _, product := range products {
		stages := []string{"prototype", "build"}
		if product == "fidget-collapsible" {
			stages = []string{"generate"}
		}
		for _, stage := range stages {
			count++
			for variant := 1; variant <= 2; variant++ {
				t.Run(fmt.Sprintf("%s/%s/value-%d", product, stage, variant), func(t *testing.T) {
					offering, ok := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/" + stage))
					if !ok {
						t.Fatal("missing Offering")
					}
					input := fmt.Sprintf(`{"source_image":"owned-image-%d"}`, variant)
					if stage == "build" {
						input = fmt.Sprintf(`{"source_task":"owned-prototype-%d"}`, variant)
					}
					if _, err := normalizeInput(offering, json.RawMessage(input)); err != nil {
						t.Fatalf("different valid business value rejected: %v", err)
					}
					taskID := fmt.Sprintf("opaque-remote-%d", variant)
					taskType := "creative-lab-" + product + "-" + stage
					path := "/openapi/creative-lab/" + product + "/v1/" + stage + "/" + taskID
					if stage == "generate" {
						taskType = "creative-lab-" + product
						path = "/openapi/creative-lab/" + product + "/v1/" + taskID
					}
					url := func(name string) string { return fmt.Sprintf("https://assets.meshy.ai/simple/%d/%s", variant, name) }
					payload := map[string]any{"id": taskID, "type": taskType, "status": "SUCCEEDED", "progress": 100}
					if stage == "prototype" {
						payload["image_urls"] = []string{url("preview.png")}
						if product == "keycap" {
							payload["candidate_ids"] = []string{fmt.Sprintf("candidate-%d", variant)}
						}
						if product == "lamp" {
							payload["model_urls"] = map[string]string{"glb": url("prototype.glb")}
							payload["thumbnail_url"] = url("thumbnail.png")
						}
					} else {
						switch product {
						case "keychain", "fridge-magnet":
							payload["model_urls"] = map[string]string{"glb": url("model.glb")}
						case "lamp":
							payload["model_urls"] = map[string]string{"lamp_stl": url("lamp.stl"), "base_stl": url("base.stl")}
						case "figure", "vinyl-figure", "brick-figure":
							payload["model_urls"] = map[string]string{"glb": url("model.glb"), "obj": url("model.obj"), "mtl": url("model.mtl")}
							payload["texture_urls"] = []map[string]string{{"base_color": url("base.png")}}
						case "keycap":
							payload["model_urls"] = map[string]string{"glb": url("model.glb"), "obj_zip": url("model-obj.zip")}
						case "fidget-pixel":
							payload["model_urls"] = map[string]string{"3mf": url("model.3mf")}
						case "fidget-collapsible":
							payload["model_urls"] = map[string]string{"stl": url("model.stl")}
						}
					}
					calls := 0
					port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.Method != http.MethodGet || r.URL.Path != path {
							t.Errorf("route = %s %s, want GET %s", r.Method, r.URL.Path, path)
						}
						raw, _ := json.Marshal(payload)
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
					})}})
					if err != nil {
						t.Fatal(err)
					}
					observed, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: "public-task", AttemptID: "attempt", Offering: offering, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: taskID}})
					if err != nil || observed.Phase != generation.UpstreamCompleted || calls != 1 {
						t.Fatalf("observe=%+v err=%v calls=%d", observed, err, calls)
					}
					defaults := creativeLabDefaultValues(t, offering, stage)
					for _, required := range offering.Outputs {
						if required.RequiredWhen != nil && !schema.ConditionMatches(*required.RequiredWhen, defaults) {
							continue
						}
						matched := 0
						for _, claim := range observed.Artifacts {
							if generation.AssetMatchesOutput([]generation.AssetRequirement{required}, claim.Role, claim.Variant, claim.MediaType) {
								matched++
							}
							if claim.Locator == "" {
								t.Fatal("empty output URL")
							}
						}
						if required.Minimum != nil && matched < *required.Minimum {
							t.Fatalf("missing required output %+v: %d claims", required, len(observed.Artifacts))
						}
					}
				})
			}
		}
	}
	if count != 17 {
		t.Fatalf("covered %d stages, want 17", count)
	}
}

func TestSimpleCreativeLabRequiredInputAndResponseTypeNegatives(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []schema.OfferingID{"meshy/creative-lab/figure/prototype", "meshy/creative-lab/keycap/build", "meshy/creative-lab/fidget-collapsible/generate"} {
		offering, _ := c.Get(id)
		if _, err := normalizeInput(offering, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s accepted missing required source", id)
		}
		bad := `{"source_image":42}`
		if id == "meshy/creative-lab/keycap/build" {
			bad = `{"source_task":42}`
		}
		if _, err := normalizeInput(offering, json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted wrong source type", id)
		}
	}
	// Synthetic negative: the real Adapter must reject a malformed image_urls type.
	offering, _ := c.Get("meshy/creative-lab/figure/prototype")
	port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(`{"id":"opaque","type":"creative-lab-figure-prototype","status":"SUCCEEDED","image_urls":"not-an-array"}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: "task", AttemptID: "attempt", Offering: offering, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "opaque"}}); err == nil {
		t.Fatal("wrong image_urls type accepted")
	}
}

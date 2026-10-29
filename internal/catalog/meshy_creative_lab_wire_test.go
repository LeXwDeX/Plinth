package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type creativeLabTransport func(*http.Request) (*http.Response, error)

func (f creativeLabTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type creativeLabInputs struct{ bytes []byte }

// Meshy Creative Lab v1 Build Task Objects, checked on 2026-10-17. These
// templates do not expose an ai_model selector; the product/stage endpoint and
// exact task type identify the response contract. The documented Figure,
// Vinyl Figure, and Brick Figure examples contain one base_color texture, not
// a guaranteed normal map.
var documentedFigureBuildSources = map[string]struct {
	documentURL string
	getPath     string
	taskType    string
}{
	"figure":       {"https://docs.meshy.ai/en/api/creative-lab-figure", "/openapi/creative-lab/figure/v1/build/actual-remote", "creative-lab-figure-build"},
	"vinyl-figure": {"https://docs.meshy.ai/en/api/creative-lab-vinyl-figure", "/openapi/creative-lab/vinyl-figure/v1/build/actual-remote", "creative-lab-vinyl-figure-build"},
	"brick-figure": {"https://docs.meshy.ai/en/api/creative-lab-brick-figure", "/openapi/creative-lab/brick-figure/v1/build/actual-remote", "creative-lab-brick-figure-build"},
}

var documentedFigureBuildArtifacts = map[string]string{
	"model/glb":            "model/gltf-binary",
	"model/obj":            "model/obj",
	"material/mtl":         "text/plain",
	"texture/1.base_color": "image/png",
	"thumbnail/thumbnail":  "image/png",
}

func (s creativeLabInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	panic("already resolved")
}

func TestCreativeLabDocumentedCandidateResultsKeepEveryDeclaredArtifact(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet", "figure", "vinyl-figure", "brick-figure", "lamp", "keycap", "fidget-pixel", "fidget-collapsible"} {
		stages := []string{"prototype", "build"}
		if product == "fidget-collapsible" {
			stages = []string{"generate"}
		}
		for _, stage := range stages {
			t.Run(product+"/"+stage, func(t *testing.T) {
				o, ok := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/" + stage))
				if !ok || o.Enabled != creativeLabEnabled {
					t.Fatal("unexpected candidate enablement")
				}
				endpoint := product + "/v1/" + stage
				taskType := "creative-lab-" + product + "-" + stage
				if stage == "generate" {
					endpoint = product + "/v1"
					taskType = "creative-lab-" + product
				}
				payload := map[string]any{"id": "actual-remote", "type": taskType, "status": "SUCCEEDED", "progress": 100}
				expected := map[string]string{}
				if stage == "prototype" {
					imageURL, imageType := "https://example.invalid/design.png", "image/png"
					if product == "fidget-pixel" { // The official pixel-art example is a JPEG.
						imageURL, imageType = "https://example.invalid/pixel-art.jpg", "image/jpeg"
					}
					payload["image_urls"] = []string{imageURL}
					if product == "keycap" {
						payload["candidate_ids"] = []string{"candidate"}
					}
					expected["image/1"] = imageType
					if product == "lamp" {
						payload["model_urls"] = map[string]string{"glb": "https://example.invalid/lamp.glb"}
						payload["thumbnail_url"] = "https://example.invalid/thumbnail.png"
						expected["model/glb"] = "model/gltf-binary"
						expected["thumbnail/thumbnail"] = "image/png"
					}
				} else {
					switch product {
					case "keychain", "fridge-magnet":
						// Documented default output.format=glb returns only model_urls.glb.
						payload["model_urls"] = map[string]string{"glb": "https://example.invalid/model.glb"}
						expected["model/glb"] = "model/gltf-binary"
					case "lamp":
						// Documented default output.format=stl with the default fixture preset:
						// the lampshade with its base plate and the separate fixture mount.
						payload["model_urls"] = map[string]string{"lamp_stl": "https://example.invalid/lamp.stl", "base_stl": "https://example.invalid/base.stl"}
						expected = map[string]string{"model/lamp_stl": "model/stl", "model/base_stl": "model/stl"}
					case "figure", "vinyl-figure", "brick-figure":
						payload["model_urls"] = map[string]string{"glb": "https://example.invalid/model.glb", "obj": "https://example.invalid/model.obj", "mtl": "https://example.invalid/model.mtl"}
						payload["texture_urls"] = []map[string]string{{"base_color": "https://example.invalid/base.png"}}
						payload["thumbnail_url"] = "https://example.invalid/thumbnail.png"
						expected = documentedFigureBuildArtifacts
						source := documentedFigureBuildSources[product]
						if source.documentURL == "" || source.getPath != "/openapi/creative-lab/"+endpoint+"/actual-remote" || source.taskType != taskType {
							t.Fatalf("undocumented Figure Build fixture: %+v", source)
						}
					case "keycap":
						payload["model_urls"] = map[string]string{"glb": "https://example.invalid/model.glb", "obj_zip": "https://example.invalid/model.zip"}
						payload["process_image_urls"] = map[string]string{"head_design": "https://example.invalid/head-design.png"}
						expected = map[string]string{"model/glb": "model/gltf-binary", "bundle/obj_zip": "application/zip"}
					case "fidget-pixel":
						payload["model_urls"] = map[string]string{"3mf": "https://example.invalid/result.3mf"}
						expected["print-archive/3mf"] = "model/3mf"
					case "fidget-collapsible":
						payload["model_urls"] = map[string]string{"stl": "https://example.invalid/result.stl", "glb": "https://example.invalid/result.glb"}
						expected = map[string]string{"model/stl": "model/stl", "model/glb": "model/gltf-binary"}
					}
				}
				calls := 0
				port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != "GET" || r.URL.Path != "/openapi/creative-lab/"+endpoint+"/actual-remote" {
						t.Fatal("observation crossed endpoint")
					}
					raw, _ := json.Marshal(payload)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				got, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: "task", Offering: o, AttemptID: "attempt", Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "actual-remote"}})
				if err != nil || calls != 1 || got.Phase != generation.UpstreamCompleted || len(got.Artifacts) != len(expected) {
					t.Fatalf("got=%+v err=%v calls=%d", got, err, calls)
				}
				for _, claim := range got.Artifacts {
					if expected[claim.Role+"/"+claim.Variant] != claim.MediaType || !generation.AssetMatchesOutput(o.Outputs, claim.Role, claim.Variant, claim.MediaType) {
						t.Fatalf("lost/misidentified artifact: %+v", claim)
					}
				}
				defaults := creativeLabDefaultValues(t, o, stage)
				for _, output := range o.Outputs {
					if output.RequiredWhen != nil && !schema.ConditionMatches(*output.RequiredWhen, defaults) {
						continue
					}
					count := 0
					for _, claim := range got.Artifacts {
						if generation.AssetMatchesOutput([]generation.AssetRequirement{output}, claim.Role, claim.Variant, claim.MediaType) {
							count++
						}
					}
					if output.Minimum != nil && count < *output.Minimum {
						t.Fatalf("required slot not represented: %+v", output)
					}
				}
			})
		}
	}
}

// creativeLabDefaultValues freezes the minimal valid input so conditional
// slots resolve exactly as a default public Task would.
func creativeLabDefaultValues(t *testing.T, o schema.Offering, stage string) map[string]any {
	t.Helper()
	input := `{"source_image":"owned-image"}`
	if stage == "build" {
		input = `{"source_task":"public-prototype"}`
	}
	frozen, err := normalizeInput(o, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(frozen, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

// A normal map is a valid extra texture slot, but it is not in the current
// Figure Build Task Object examples above. This fixture is deliberately
// synthetic and must not become an upstream required-output assertion.
func TestCreativeLabSyntheticFigureBuildNormalTextureIsPreserved(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for product, source := range documentedFigureBuildSources {
		t.Run(product, func(t *testing.T) {
			o, ok := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
			if !ok || o.Enabled != creativeLabEnabled {
				t.Fatal("unexpected candidate enablement")
			}
			payload := map[string]any{
				"id": "actual-remote", "type": source.taskType, "status": "SUCCEEDED", "progress": 100,
				"model_urls":    map[string]string{"glb": "https://example.invalid/model.glb", "obj": "https://example.invalid/model.obj", "mtl": "https://example.invalid/model.mtl"},
				"texture_urls":  []map[string]string{{"base_color": "https://example.invalid/base.png", "normal": "https://example.invalid/normal.png"}},
				"thumbnail_url": "https://example.invalid/thumbnail.png",
			}
			port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != source.getPath {
					t.Fatalf("observation crossed endpoint: %s %s", r.Method, r.URL.Path)
				}
				raw, _ := json.Marshal(payload)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: "task", Offering: o, AttemptID: "attempt", Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "actual-remote"}})
			if err != nil || got.Phase != generation.UpstreamCompleted || len(got.Artifacts) != len(documentedFigureBuildArtifacts)+1 {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			seen := map[string]string{}
			for _, claim := range got.Artifacts {
				key := claim.Role + "/" + claim.Variant
				if seen[key] != "" || !generation.AssetMatchesOutput(o.Outputs, claim.Role, claim.Variant, claim.MediaType) {
					t.Fatalf("duplicate or unsupported synthetic artifact: %+v", claim)
				}
				seen[key] = claim.MediaType
			}
			if seen["texture/1.normal"] != "image/png" {
				t.Fatalf("synthetic normal texture lost: %+v", seen)
			}
			for key, mediaType := range documentedFigureBuildArtifacts {
				if seen[key] != mediaType {
					t.Fatalf("baseline artifact %s lost: %+v", key, seen)
				}
			}
		})
	}
}

func (s creativeLabInputs) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.bytes)), nil
}

// Exercise actual disabled declarations against an in-memory wire. This does
// not qualify remote jobs, artifacts, archive contents or enable a candidate.
func TestCreativeLabActualCandidatesSendExactDefaultsAndBoundSource(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	content := pngBytes.Bytes()
	sum := sha256.Sum256(content)
	imageURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(content)
	buildBodies := map[string]string{
		"keychain":      `{"input_task_id":"prototype-remote","options":{"badge_shape":"circle","size_mm":40,"relief_height_mm":2.2,"relief_offset_mm":0,"base_thickness_mm":0.1,"has_closed_back":true,"relief_curve":"linear","curve_param":1,"invert_depth":false,"smoothing":0.24,"relief_scale":1,"depth_threshold":0.1,"remove_background":true,"export_resolution":512},"output":{"format":"glb"}}`,
		"fridge-magnet": `{"input_task_id":"prototype-remote","options":{"badge_shape":"rounded-rect","size_mm":60,"relief_height_mm":3.3,"relief_offset_mm":0,"base_thickness_mm":2,"has_closed_back":true,"relief_curve":"linear","curve_param":1,"invert_depth":false,"smoothing":0.24,"relief_scale":1,"depth_threshold":0.1,"remove_background":true,"export_resolution":512},"output":{"format":"glb"}}`,
		"figure":        `{"input_task_id":"prototype-remote"}`,
		"vinyl-figure":  `{"input_task_id":"prototype-remote"}`,
		"brick-figure":  `{"input_task_id":"prototype-remote"}`,
		"lamp":          `{"input_task_id":"prototype-remote","options":{"diameter_mm":150,"thickness_mm":1,"cut_amount_percent":1,"light_source_preset":"bambu_mh001_60mm","fixture_offset_x_mm":0,"fixture_offset_z_mm":0,"rotate_x_deg":0,"rotate_y_deg":0,"rotate_z_deg":0},"output":{"format":"stl"}}`,
		"keycap":        `{"input_task_id":"prototype-remote","candidate_id":"opaque:candidate/1","options":{"base_model":"cherry-mx-1x1-r1","head_size_mm":23,"vertical_offset_mm":0}}`,
		"fidget-pixel":  `{"input_task_id":"prototype-remote","options":{"shape":"square","grid_size":32,"piece_size_mm":8,"color_count":8,"piece_height_mm":15},"output":{"format":"3mf"}}`,
	}
	for _, product := range []string{"keychain", "fridge-magnet", "figure", "vinyl-figure", "brick-figure", "lamp", "keycap", "fidget-pixel", "fidget-collapsible"} {
		stages := []string{"prototype", "build"}
		if product == "fidget-collapsible" {
			stages = []string{"generate"}
		}
		for _, stage := range stages {
			t.Run(product+"/"+stage, func(t *testing.T) {
				o, ok := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/" + stage))
				if !ok || o.Enabled != creativeLabEnabled {
					t.Fatal("unexpected candidate enablement")
				}
				path := "/openapi/creative-lab/" + product + "/v1/" + stage
				if stage == "generate" {
					path = "/openapi/creative-lab/" + product + "/v1"
				}
				var expected map[string]any
				request := generation.StartRequest{SessionID: "session", OwnerID: "owner", Offering: o, SubmissionKey: "one-create", Input: json.RawMessage(`{"source_image":"owned-image"}`)}
				if stage == "build" {
					if err := json.Unmarshal([]byte(buildBodies[product]), &expected); err != nil {
						t.Fatal(err)
					}
					request.Input = json.RawMessage(`{"source_task":"public-prototype"}`)
					request.TaskDependencies = map[string]generation.TaskDependency{"source_task": {TaskID: "public-prototype", SessionID: "session", ProviderID: "meshy", OfferingID: schema.OfferingID("meshy/creative-lab/" + product + "/prototype"), UpstreamJob: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}}}
				} else {
					expected = map[string]any{"image_url": imageURI}
					switch product {
					case "fidget-pixel":
						expected["type"] = "other"
					case "fidget-collapsible":
					default:
						expected["remove_background"] = false
					}
					if product == "lamp" {
						expected["image_subject"] = "character"
					}
					request.InputAssets = map[string][]generation.InputAsset{"source_image": {{ID: "owned-image", OwnerID: "owner", StorageKey: "fixture", MediaType: "image/png", Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}}}
				}
				request.Input, err = normalizeInput(o, request.Input)
				if err != nil {
					t.Fatal(err)
				}
				gets, posts := 0, 0
				port, err := meshy.New(meshy.Config{BaseURL: "https://meshy.invalid", APIKey: "fixture", AuthHeader: "Authorization", AuthPrefix: "Bearer ", InputAssets: creativeLabInputs{content}, StatusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}, HTTPClient: &http.Client{Transport: creativeLabTransport(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "Bearer fixture" {
						t.Fatal("same-key authentication missing")
					}
					response := `{"result":"created-remote"}`
					status := http.StatusAccepted
					if r.Method == "GET" {
						gets++
						if stage != "build" || posts != 0 || r.URL.Path != "/openapi/creative-lab/"+product+"/v1/prototype/prototype-remote" {
							t.Fatal("source was not verified before dispatch")
						}
						status = http.StatusOK
						response = `{"id":"prototype-remote","type":"creative-lab-` + product + `-prototype","status":"SUCCEEDED","image_urls":["https://example.invalid/preview.png"],"candidate_ids":["opaque:candidate/1"]}`
						if product == "lamp" {
							// Official Lamp Prototype GET also carries the required GLB.
							response = `{"id":"prototype-remote","type":"creative-lab-lamp-prototype","status":"SUCCEEDED","image_urls":["https://example.invalid/preview.png"],"model_urls":{"glb":"https://example.invalid/lamp.glb"},"thumbnail_url":"https://example.invalid/lamp-thumbnail.png"}`
						}
					} else {
						posts++
						if r.Method != "POST" || r.URL.Path != path || stage == "build" && gets != 1 {
							t.Fatal("wrong dispatch route/order")
						}
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, expected) {
							t.Fatalf("wire body=%#v want=%#v err=%v", body, expected, err)
						}
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				if product == "keycap" && stage == "build" {
					prototype, _ := c.Get("meshy/creative-lab/keycap/prototype")
					obs, err := port.Observe(context.Background(), generation.ObserveRequest{TaskID: "public-prototype", AttemptID: "prototype-attempt", Offering: prototype, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}})
					if err != nil {
						t.Fatal(err)
					}
					asset := generation.Asset{ID: "preview", TaskID: "public-prototype", AttemptID: "prototype-attempt", Role: "image", Variant: "1", SHA256: strings.Repeat("a", 64), Size: 100}
					obs.Artifacts[0].StoredAsset = &asset
					binding, err := port.ResolveDependencyBinding(generation.DependencyBindingRequest{TaskID: "public-prototype", AttemptID: "prototype-attempt", Offering: prototype, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-remote"}, Claims: obs.Artifacts, Assets: []generation.Asset{asset}})
					if err != nil {
						t.Fatal(err)
					}
					dep := request.TaskDependencies["source_task"]
					dep.ProviderBinding = binding
					dep.Assets = []generation.Asset{asset}
					request.TaskDependencies["source_task"] = dep
				}
				got, err := port.Start(context.Background(), request)
				if err != nil || posts != 1 || got.Job == nil || got.Job.RemoteID != "created-remote" {
					t.Fatalf("got=%+v err=%v posts=%d", got, err, posts)
				}
			})
		}
	}
}

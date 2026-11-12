package tripo_test

// #275 turned Tripo P2's documented quad output on for the game character
// purpose (https://developers.tripo3d.ai/en/docs/generation-image-to-model/p:
// with quad=true, face_limit is 48-25000); #294 took it back, because the first
// real P2 task with quad=true (2026-11-12) delivered an FBX and no GLB. This
// contract test takes the overrides the shipped purpose resolves for each target
// platform, freezes them the way a submission does and sends them through the
// shared Tripo v3 adapter, so the request body that reaches the upstream asks for
// triangle output (no quad=true) and the platform's face budget on all three P2
// endpoints. It pins only the request; what a quad request delivers is the fake's
// and the delivery test's (p2_quad_fbx_294_test.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestTripoP2ProfileOverridesReachTheV3WireAsTriangles(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	var character catalog.ChatModel3DUsageProfile
	for _, profile := range usage.Profiles {
		if profile.ID == "game_character" {
			character = profile
		}
	}
	if character.ID == "" {
		t.Fatal("game_character purpose missing")
	}
	oneImage := map[string][]generation.InputAsset{
		"reference_assets": {{ID: "asset-one", MediaType: "image/png"}},
	}
	twoImages := map[string][]generation.InputAsset{
		"reference_assets": {{ID: "asset-one", MediaType: "image/png"}, {ID: "asset-two", MediaType: "image/png"}},
	}
	endpoints := []struct {
		id       string
		endpoint string
		uploads  int
		input    map[string]any
		assets   map[string][]generation.InputAsset
	}{
		{"tripo/tripo-p2/image-to-3d", "/v3/generation/image-to-model", 1,
			map[string]any{"reference_assets": []any{"asset-one"}}, oneImage},
		{"tripo/tripo-p2/text-to-3d", "/v3/generation/text-to-model", 0,
			map[string]any{"prompt": "a game-ready knight"}, nil},
		{"tripo/tripo-p2-multiview/image-to-3d", "/v3/generation/multiview-to-model", 2,
			map[string]any{"reference_assets": []any{"asset-one", "asset-two"}, "reference_views": []any{"front", "left"}}, twoImages},
	}
	// The face budget each platform resolves to, in triangles: the single-budget
	// default and the mobile class ask for 20000 (the mobile ceiling), PC and
	// console for the 50000 P2 accepts without quad output (half the 100000 a PC
	// character may have).
	platforms := []struct {
		platform string
		faces    float64
	}{{"", 20000}, {"mobile", 20000}, {"web", 20000}, {"xr", 20000}, {"pc_console", 50000}}

	for _, endpoint := range endpoints {
		offering, found := product.Get(schema.OfferingID(endpoint.id))
		if !found {
			t.Fatalf("%s missing", endpoint.id)
		}
		for _, platform := range platforms {
			t.Run(endpoint.id+" on "+platform.platform, func(t *testing.T) {
				resolved, _ := usage.ForPlatform(character, platform.platform)
				input := map[string]any{}
				for name, value := range endpoint.input {
					input[name] = value
				}
				for name, raw := range resolved.ParameterOverrides[endpoint.id] {
					var value any
					if err := json.Unmarshal(raw, &value); err != nil {
						t.Fatal(err)
					}
					input[name] = value
				}

				var posted map[string]any
				calls := []string{}
				uploaded := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, r.URL.Path)
					if r.URL.Path == "/v3/files" {
						uploaded++
						token := "file-token-one"
						if uploaded == 2 {
							token = "file-token-two"
						}
						writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"file_token": token}})
						return
					}
					if r.URL.Path != endpoint.endpoint {
						t.Errorf("unexpected route %s, want %s", r.URL.Path, endpoint.endpoint)
					}
					if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
						t.Error(err)
					}
					writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"task_id": "task-p2-profile"}})
				}))
				defer server.Close()

				errorMapping, statusMapping := declaredTripoMappings(t)
				config := tripo.Config{
					BaseURL: server.URL + "/v3", APIKey: "tripo-key", HTTPClient: server.Client(),
					AllowHTTPAssets: true, OfferingRoutes: []generation.Offering{offering},
					ErrorMapping: errorMapping, StatusMapping: statusMapping,
				}
				if len(endpoint.assets) > 0 {
					config.InputAssets = memoryInputAssets{content: []byte("fixture-image-bytes")}
				}
				adapter, err := tripo.New(config)
				if err != nil {
					t.Fatal(err)
				}
				result, err := adapter.Start(context.Background(), generation.StartRequest{
					AttemptID: "attempt-p2-profile", SubmissionKey: "submit-p2-profile", Offering: offering,
					Input: p2FrozenInput(t, offering, input), InputAssets: endpoint.assets,
				})
				if err != nil {
					t.Fatal(err)
				}
				if result.Job == nil || result.Job.RemoteID != "task-p2-profile" {
					t.Fatalf("task response = %+v", result.Job)
				}
				if len(calls) != endpoint.uploads+1 {
					t.Fatalf("calls = %v, want %d uploads then one generation", calls, endpoint.uploads)
				}
				for key, want := range map[string]any{
					"model": "P2-20260801", "face_limit": platform.faces, "texture_quality": "standard",
				} {
					if !jsonEqual(posted[key], want) {
						t.Errorf("body.%s = %v, want %v (body %v)", key, posted[key], want, posted)
					}
				}
				// Triangle output: the purpose no longer asks for quads, so the body
				// carries no quad=true (an omitted or false field).
				if quad, asked := posted["quad"]; asked && quad != false {
					t.Errorf("body.quad = %v, want triangle output (body %v)", quad, posted)
				}
				for _, key := range []string{"reference_assets", "reference_views", "authorize_extra_credits"} {
					if value, leaked := posted[key]; leaked {
						t.Errorf("non-wire field %s leaked: %v", key, value)
					}
				}
				if strings.HasSuffix(endpoint.endpoint, "multiview-to-model") && posted["inputs"] == nil {
					t.Errorf("multiview body lost its view-keyed inputs: %v", posted)
				}
			})
		}
	}
}

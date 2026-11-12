package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #276: from a high-detail Tripo model to a game low-poly model that keeps its
// textures. The whole stack runs against the fake upstream (the real Catalog, the
// kernel resolving the source Task and pinning the required slots, the Tripo
// adapter with its private exports, the asset store with its delivery checks):
// a game character brief (the purpose and the platform of the production brief)
// generates a high-detail Tripo v3.1 model, then the baked decimation reduces it
// to the budget of that purpose on that platform. The fake builds the reduced
// model at the requested face_limit (it cannot reduce the fixed source), so the
// face count is what the request asked for, not a measurement of a reduction; the
// texture and UVs of the delivered GLB are the fixture's.

// pathLog keeps the bodies the fake upstream received, by the end of the path.
type pathLog struct {
	mu    sync.Mutex
	posts map[string][]map[string]any
}

func (l *pathLog) bodies(suffix string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any(nil), l.posts[suffix]...)
}

func newPathRecordingFake(t *testing.T) (*httptest.Server, *pathLog) {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost})
	if err != nil {
		t.Fatal(err)
	}
	log := &pathLog{posts: map[string][]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			log.mu.Lock()
			for _, suffix := range []string{"/mesh/decimate", "/models/convert", "/generation/text-to-model"} {
				if strings.HasSuffix(r.URL.Path, suffix) {
					log.posts[suffix] = append(log.posts[suffix], body)
				}
			}
			log.mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, log
}

// submitStep prepares, approves and submits one step the way the Agent's path
// does (session approval by a human), with the reasons the kernel asks for when
// a value leaves an advised default.
func submitStep(t *testing.T, h *acmeHarness, capability generation.Capability, offering generation.OfferingID,
	input string, key string, reasons map[string]string) generation.Accepted {
	t.Helper()
	ctx := context.Background()
	sources := map[string]string{}
	for name := range reasons {
		sources[name] = "agent"
	}
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-acme", SessionID: "acme-session", Capability: capability,
		OfferingID: offering, Input: json.RawMessage(input),
		ParameterReasons: reasons, ParameterSources: sources,
	})
	if err != nil {
		t.Fatalf("%s: %v", offering, err)
	}
	operationID := "acme:" + string(prepared.ID)
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: "owner-acme", ActorID: "acme-test-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: "acme-session", OfferingID: string(prepared.OfferingID),
		OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
		PreparedInputHash: prepared.InputHash, IdempotencyKey: key, OperationID: operationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: "owner-acme", SessionID: "acme-session", PreparedInputID: prepared.ID,
		IdempotencyKey: key, OperationID: operationID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func lowPolyHarness(t *testing.T) (*acmeHarness, *catalog.Catalog, *pathLog) {
	t.Helper()
	server, log := newPathRecordingFake(t)
	fakeURL, _ := url.Parse(server.URL)
	settings := map[string]string{
		"tripo_base_url": server.URL + "/tripo/v3",
		"tripo_api_key":  fakeUpstreamKeyPrefix + "test-key",
	}
	taskCatalog, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	return newAcmeHarnessWithTransport(t, taskCatalog, settings, fakeShapeHTTPTransport(fakeURL)), taskCatalog, log
}

// gamePurposeBudget is the budget a game purpose resolves to on a platform.
func gamePurposeBudget(t *testing.T, product *catalog.Catalog, purpose, platform string) catalog.ChatModel3DUsageBudget {
	t.Helper()
	usage := product.Application().Chat.Model3DUsage
	for _, profile := range usage.Profiles {
		if profile.ID == purpose {
			resolved, _ := usage.ForPlatform(profile, platform)
			return resolved.Budget
		}
	}
	t.Fatalf("purpose %q is not declared", purpose)
	return catalog.ChatModel3DUsageBudget{}
}

func TestGameCharacterBriefDecimatesAHighDetailModelIntoTheBudget(t *testing.T) {
	for _, tc := range []struct {
		name, purpose, platform string
		// faceLimit is the target the Agent gives the decimation: the purpose's upper
		// triangle bound, at most the 20000 Tripo accepts for a triangle mesh.
		faceLimit int
	}{
		{"character on a phone", "game_character", "mobile", 20000},
		{"character on a PC", "game_character", "pc_console", 20000},
		{"prop on a phone", "game_prop", "mobile", 8000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, product, log := lowPolyHarness(t)
			budget := gamePurposeBudget(t, product, tc.purpose, tc.platform)
			if tc.faceLimit > budget.MaxTriangles {
				t.Fatalf("the test asks for %d faces under a %d-triangle budget", tc.faceLimit, budget.MaxTriangles)
			}

			// The high-detail model: Tripo v3.1 with a face count far above the
			// purpose's budget (the intermediate model of the route, so the budget
			// is expected to be exceeded at this step) and the GLB as its only format.
			source := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
				`{"prompt":"a game character in an A pose","authorize_extra_credits":true,"face_limit":400000,"output_formats":["GLTF"]}`,
				"lowpoly-source-"+tc.name, map[string]string{"face_limit": "the source of a baked decimation keeps its detail", "output_formats": "only the GLB feeds the decimation"})
			sourceTask := harness.runUntilTerminal(t, "owner-acme", source.TaskID)
			if sourceTask.State != generation.TaskSucceeded {
				t.Fatalf("source state = %s, failure = %#v", sourceTask.State, sourceTask.Failure)
			}
			if generations := log.bodies("/generation/text-to-model"); len(generations) != 1 || generations[0]["face_limit"] != float64(400000) {
				t.Fatalf("source requests = %v, want one with face_limit 400000", generations)
			}

			// The baked decimation takes that Task as its source and the purpose's
			// face budget as its target.
			sourceExports := len(log.bodies("/models/convert"))
			decimation := submitStep(t, harness, generation.Remesh, "tripo/geometry/decimate-triangle",
				`{"source_task":"`+string(sourceTask.ID)+`","face_limit":`+itoaInt(tc.faceLimit)+`,"authorize_extra_credits":true}`,
				"lowpoly-decimate-"+tc.name, nil)
			task := harness.runUntilTerminal(t, "owner-acme", decimation.TaskID)
			if task.State != generation.TaskSucceeded {
				t.Fatalf("decimation state = %s, failure = %#v", task.State, task.Failure)
			}

			// What Tripo was asked: the decimation of the source's upstream task, baked
			// and unwrapped, as a triangle mesh at the target.
			requests := log.bodies("/mesh/decimate")
			if len(requests) != 1 {
				t.Fatalf("decimate requests = %d, want 1", len(requests))
			}
			want := map[string]any{"model": "v3.0", "bake": true, "smart_uv": true, "quad": false, "face_limit": float64(tc.faceLimit)}
			for name, value := range want {
				if requests[0][name] != value {
					t.Errorf("decimate request %s = %v, want %v", name, requests[0][name], value)
				}
			}
			if input, _ := requests[0]["input"].(string); input == "" || input == string(sourceTask.ID) {
				t.Errorf("decimate input = %q, want the source's upstream task id", input)
			}
			// The five exports run on the decimated result, as PNG at 8192 with the
			// bake (the source's own GLTF export came before and is not counted).
			converts := log.bodies("/models/convert")[sourceExports:]
			sentFormats := []string{}
			for _, body := range converts {
				format, _ := body["format"].(string)
				sentFormats = append(sentFormats, format)
				if body["texture_format"] != "PNG" || body["texture_size"] != float64(8192) || body["bake"] != true {
					t.Errorf("%s export = %v", format, body)
				}
			}
			slices.Sort(sentFormats)
			if !slices.Equal(sentFormats, []string{"3MF", "FBX", "OBJ", "STL", "USDZ"}) {
				t.Errorf("exports = %v", sentFormats)
			}

			// The delivered GLB: textured, unwrapped, inside the budget.
			var glb generation.Asset
			for _, asset := range task.Assets {
				if asset.Role == "model" {
					glb = asset
				}
			}
			if glb.ID == "" {
				t.Fatalf("the decimation delivered no model: %+v", task.Assets)
			}
			content, err := harness.module.OpenAsset(context.Background(), "owner-acme", task.ID, glb.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(content.Body)
			_ = content.Body.Close()
			model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
			if err != nil {
				t.Fatalf("the delivered GLB cannot be read: %v", err)
			}
			report := modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
			if report.TextureCount < 1 || !report.UV.AllHaveUV {
				t.Errorf("the baked, unwrapped result lost its texture or UVs: textures %d, all UV %v", report.TextureCount, report.UV.AllHaveUV)
			}
			if budget.MinTriangles != nil && report.Triangles < int64(*budget.MinTriangles) || report.Triangles > int64(budget.MaxTriangles) {
				t.Errorf("%d triangles are outside the %v..%d budget of %s on %s", report.Triangles, budget.MinTriangles, budget.MaxTriangles, tc.purpose, tc.platform)
			}
			if report.Triangles > int64(tc.faceLimit) {
				t.Errorf("%d triangles are above the requested %d", report.Triangles, tc.faceLimit)
			}
			if want := int64(fakeupstream.LowPolyTriangles(tc.faceLimit)); report.Triangles != want {
				t.Errorf("triangles = %d, the fake's model for %d is %d", report.Triangles, tc.faceLimit, want)
			}
			longest := 0
			for _, material := range report.Materials {
				for _, slot := range material.Slots {
					if slot.Present {
						longest = max(longest, slot.Width, slot.Height)
					}
				}
			}
			if budget.MaxTextureSize == nil || longest == 0 || longest > *budget.MaxTextureSize {
				t.Errorf("texture %d px against the budget %v", longest, budget.MaxTextureSize)
			}
		})
	}
}

func itoaInt(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// A source the baked decimation does not take is refused in Prepare, before a
// grant, a Task or any upstream request exists, and the refusal says what to do
// instead: here a Tripo P2 result (the official page names no P-series task as a
// source), in both languages.
func TestDecimationRefusesATripoP2SourceBeforePaymentAndNamesTheAlternative(t *testing.T) {
	harness, product, log := lowPolyHarness(t)
	source := submitStep(t, harness, generation.TextTo3D, "tripo/tripo-p2/text-to-3d",
		`{"prompt":"a game character in an A pose","authorize_extra_credits":true}`, "lowpoly-p2-source", nil)
	sourceTask := harness.runUntilTerminal(t, "owner-acme", source.TaskID)
	if sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("P2 source state = %s, failure = %#v", sourceTask.State, sourceTask.Failure)
	}

	_, err := harness.module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-acme", SessionID: "acme-session", Capability: generation.Remesh,
		OfferingID: "tripo/geometry/decimate-triangle",
		Input:      json.RawMessage(`{"source_task":"` + string(sourceTask.ID) + `","authorize_extra_credits":true}`),
	})
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.Code != generation.ErrInvalidInput {
		t.Fatalf("a P2 source was not refused as invalid input: %v", err)
	}
	for _, want := range []string{"这一步没有提交，也没有扣积分", "Tripo v3.1", "带烘焙的减面", "Meshy 重拓扑", "换贴图"} {
		if !strings.Contains(refusal.Detail, want) {
			t.Errorf("the refusal %q does not say %q", refusal.Detail, want)
		}
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	for _, want := range []string{"no credits were spent", "Tripo v3.1", "Meshy remesh", "retexturing"} {
		if !strings.Contains(english, want) {
			t.Errorf("the English refusal %q does not say %q", english, want)
		}
	}
	if strings.ContainsAny(english, "{}") || strings.IndexFunc(english, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		t.Errorf("the English refusal is not English text: %q", english)
	}
	// Nothing was paid or sent: no decimation request reached the upstream.
	if requests := log.bodies("/mesh/decimate"); len(requests) != 0 {
		t.Fatalf("a refused source reached the upstream: %v", requests)
	}
}

package tripov3

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The generation endpoints (text, image and multiview to model) are shared by the
// H series (v3.1, v3.0, v2.5) and the P series (P2, P1); the model names the family.
// generationBody is a valid minimal request of each endpoint for a model; a case
// adds or overrides fields on it. Every case below is checked against the official
// pages read on 2026-11-24 (generation.go).
func generationBody(path, model string, extra map[string]any) map[string]any {
	body := map[string]any{"model": model}
	switch path {
	case "/generation/text-to-model":
		body["prompt"] = "a game-ready knight"
	case "/generation/image-to-model":
		body["input"] = "file_image"
	case "/generation/multiview-to-model":
		body["inputs"] = []any{map[string]any{"front": "file_front"}, map[string]any{"left": "file_left"}}
	}
	for name, value := range extra {
		if value == nil {
			delete(body, name)
			continue
		}
		body[name] = value
	}
	return body
}

var generationPaths = []string{"/generation/text-to-model", "/generation/image-to-model", "/generation/multiview-to-model"}

const (
	modelH31 = "v3.1-20260211"
	modelH30 = "v3.0-20250812"
	modelH25 = "v2.5-20250123"
	modelP1  = "P1-20260311"
	modelP2  = "P2-20260801"
)

type generationCase struct {
	name  string
	model string
	extra map[string]any
	// code is 0 for an accepted request, else the business code of the refusal.
	code int
}

// codeOf reads the business code of an error envelope or a created task.
func codeOf(t *testing.T, body map[string]any) int {
	t.Helper()
	number, _ := field(t, body, "code").(float64)
	return int(number)
}

func TestGenerationRequestsAreCheckedAgainstTheOfficialContract(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	longText := func(n int) string { return strings.Repeat("a", n) }
	cases := []generationCase{
		// model.
		{"unknown model", "v9.9-20990101", nil, 1004},
		{"a name from no family", "default", nil, 1004},
		// A retired name is an unknown name (v2.0 captured 2026-11-25, #577), not 2015.
		{"retired v2.0 is 1004", "v2.0-20240919", nil, 1004},
		{"retired v1.4 is 1004", "v1.4-20240625", nil, 1004},
		{"retired Turbo is 1004", "Turbo-v1.0-20250506", nil, 1004},
		{"H3.1 plain", modelH31, nil, 0},
		{"H3.0 plain", modelH30, nil, 0},
		{"H2.5 plain", modelH25, nil, 0},
		{"P1 plain", modelP1, nil, 0},
		{"P2 plain", modelP2, nil, 0},
		// Enumerations.
		{"texture_quality outside the enum", modelH31, map[string]any{"texture_quality": "ultra"}, 1004},
		{"texture_quality fast", modelH31, map[string]any{"texture_quality": "fast"}, 0},
		{"texture_quality extreme", modelH31, map[string]any{"texture_quality": "extreme"}, 0},
		{"texture_version outside the enum", modelH31, map[string]any{"texture_version": "v4.0-20270101"}, 1004},
		{"texture_version v3.5", modelH31, map[string]any{"texture_version": "v3.5-20260815"}, 0},
		{"geometry_quality extreme was never an option", modelH31, map[string]any{"geometry_quality": "extreme"}, 1004},
		{"geometry_quality detailed", modelH31, map[string]any{"geometry_quality": "detailed"}, 0},
		{"export_orientation outside the enum", modelH31, map[string]any{"export_orientation": "up"}, 1004},
		{"export_orientation -y", modelH31, map[string]any{"export_orientation": "-y"}, 0},
		{"compress outside the enum", modelH31, map[string]any{"compress": "texture"}, 1004},
		{"compress geometry", modelH31, map[string]any{"compress": "geometry"}, 0},
		{"texture_alignment outside the enum", modelH31, map[string]any{"texture_alignment": "front"}, 1004},
		{"orientation outside the enum", modelH31, map[string]any{"orientation": "up"}, 1004},
		{"orientation align_image", modelH31, map[string]any{"orientation": "align_image"}, 0},
		// Types.
		{"texture must be boolean", modelH31, map[string]any{"texture": "yes"}, 1004},
		{"quad must be boolean", modelH31, map[string]any{"quad": 1}, 1004},
		{"model_seed must be an integer", modelH31, map[string]any{"model_seed": 1.5}, 1004},
		{"face_limit must be an integer", modelH31, map[string]any{"face_limit": 1000.5}, 1004},
		{"face_limit must be a number", modelH31, map[string]any{"face_limit": "1000"}, 1004},
		// Parameters that need model 3.0 or later.
		{"geometry_quality on H2.5", modelH25, map[string]any{"geometry_quality": "standard"}, 1004},
		{"quad on H2.5", modelH25, map[string]any{"quad": true}, 1004},
		{"smart_low_poly on H2.5", modelH25, map[string]any{"smart_low_poly": true}, 1004},
		{"auto_size on H2.5", modelH25, map[string]any{"auto_size": true}, 1004},
		{"compress on H2.5", modelH25, map[string]any{"compress": "geometry"}, 1004},
		{"generate_parts on H2.5", modelH25, map[string]any{"generate_parts": true, "texture": false, "pbr": false}, 1004},
		{"geometry_quality on H3.0", modelH30, map[string]any{"geometry_quality": "detailed"}, 0},
		{"quad on H3.0", modelH30, map[string]any{"quad": true}, 0},
		// face_limit upper bounds.
		{"H3.1 standard 1,500,000", modelH31, map[string]any{"geometry_quality": "standard", "face_limit": 1500000}, 0},
		{"H3.1 standard 1,500,001", modelH31, map[string]any{"geometry_quality": "standard", "face_limit": 1500001}, 1004},
		{"H3.1 detailed 2,000,000", modelH31, map[string]any{"geometry_quality": "detailed", "face_limit": 2000000}, 0},
		{"H3.1 detailed 2,000,001", modelH31, map[string]any{"geometry_quality": "detailed", "face_limit": 2000001}, 1004},
		{"H3.1 default quality 1,500,001", modelH31, map[string]any{"face_limit": 1500001}, 1004},
		{"H3.0 standard 1,000,000", modelH30, map[string]any{"face_limit": 1000000}, 0},
		{"H3.0 standard 1,000,001", modelH30, map[string]any{"face_limit": 1000001}, 1004},
		{"H3.0 detailed 2,000,000", modelH30, map[string]any{"geometry_quality": "detailed", "face_limit": 2000000}, 0},
		{"H2.5 500,000", modelH25, map[string]any{"face_limit": 500000}, 0},
		{"H2.5 500,001", modelH25, map[string]any{"face_limit": 500001}, 1004},
		{"H quad 150,000", modelH31, map[string]any{"quad": true, "face_limit": 150000}, 0},
		{"H quad 150,001", modelH31, map[string]any{"quad": true, "face_limit": 150001, "geometry_quality": "detailed"}, 1004},
		{"smart low poly 20,000", modelH31, map[string]any{"smart_low_poly": true, "face_limit": 20000}, 0},
		{"smart low poly 20,001", modelH31, map[string]any{"smart_low_poly": true, "face_limit": 20001}, 1004},
		{"smart low poly 499", modelH31, map[string]any{"smart_low_poly": true, "face_limit": 499}, 1004},
		{"smart low poly quad 10,000", modelH31, map[string]any{"smart_low_poly": true, "quad": true, "face_limit": 10000}, 0},
		{"smart low poly quad 10,001", modelH31, map[string]any{"smart_low_poly": true, "quad": true, "face_limit": 10001}, 1004},
		{"P1 50", modelP1, map[string]any{"face_limit": 50}, 0},
		{"P1 49", modelP1, map[string]any{"face_limit": 49}, 1004},
		{"P1 20,000", modelP1, map[string]any{"face_limit": 20000}, 0},
		{"P1 20,001", modelP1, map[string]any{"face_limit": 20001}, 1004},
		{"P2 48", modelP2, map[string]any{"face_limit": 48}, 0},
		{"P2 47", modelP2, map[string]any{"face_limit": 47}, 1004},
		{"P2 50,000", modelP2, map[string]any{"face_limit": 50000}, 0},
		{"P2 50,001", modelP2, map[string]any{"face_limit": 50001}, 1004},
		{"P2 quad 25,000", modelP2, map[string]any{"quad": true, "face_limit": 25000}, 0},
		{"P2 quad 25,001", modelP2, map[string]any{"quad": true, "face_limit": 25001}, 1004},
		// quad is P2 only among the P series.
		{"P1 quad", modelP1, map[string]any{"quad": true}, 1004},
		{"P1 quad false is not a quad request", modelP1, map[string]any{"quad": false}, 0},
		{"P2 quad", modelP2, map[string]any{"quad": true}, 0},
		// The H-only parameters are refused by the P series.
		{"P1 geometry_quality", modelP1, map[string]any{"geometry_quality": "standard"}, 1004},
		{"P1 smart_low_poly", modelP1, map[string]any{"smart_low_poly": true}, 1004},
		{"P1 generate_parts", modelP1, map[string]any{"generate_parts": true, "texture": false, "pbr": false}, 1004},
		{"P2 geometry_quality", modelP2, map[string]any{"geometry_quality": "standard"}, 1004},
		{"P2 smart_low_poly", modelP2, map[string]any{"smart_low_poly": true}, 1004},
		{"P2 generate_parts", modelP2, map[string]any{"generate_parts": true, "texture": false, "pbr": false}, 1004},
		{"P2 compress", modelP2, map[string]any{"compress": "geometry"}, 1004},
		{"P2 delight and auto_size", modelP2, map[string]any{"delight": true, "auto_size": true, "export_uv": true}, 0},
		// generate_parts.
		{"parts with texture omitted", modelH31, map[string]any{"generate_parts": true}, 1004},
		{"parts with texture true", modelH31, map[string]any{"generate_parts": true, "texture": true, "pbr": false}, 1004},
		{"parts with pbr true", modelH31, map[string]any{"generate_parts": true, "texture": false, "pbr": true}, 1004},
		{"parts with texture and pbr off", modelH31, map[string]any{"generate_parts": true, "texture": false, "pbr": false}, 0},
		{"parts false needs nothing", modelH31, map[string]any{"generate_parts": false}, 0},
		// texture_quality fast.
		{"fast on H2.5 geometry without a texture version", modelH25, map[string]any{"texture_quality": "fast"}, 1004},
		{"fast with a v2.5 texture version", modelH31, map[string]any{"texture_quality": "fast", "texture_version": "v2.5-20250123"}, 1004},
		{"fast with a v2.5 texture version on P2", modelP2, map[string]any{"texture_quality": "fast", "texture_version": "v2.5-20250123"}, 1004},
		{"fast with the v3.5 texture version", modelH31, map[string]any{"texture_quality": "fast", "texture_version": "v3.5-20260815"}, 0},
		{"fast on H2.5 with a v3.5 texture version", modelH25, map[string]any{"texture_quality": "fast", "texture_version": "v3.5-20260815"}, 0},
		{"fast on P2 without a texture version", modelP2, map[string]any{"texture_quality": "fast"}, 0},
		{"standard on H2.5 with a v2.5 texture version", modelH25, map[string]any{"texture_quality": "standard", "texture_version": "v2.5-20250123"}, 0},
		// Text lengths (characters, not bytes).
		{"prompt of 1024 characters", modelH31, map[string]any{"prompt": longText(1024), "input": nil}, 0},
		{"prompt of 1025 characters", modelH31, map[string]any{"prompt": longText(1025), "input": nil}, 1004},
		{"prompt of 1024 multibyte characters", modelH31, map[string]any{"prompt": strings.Repeat("龙", 1024)}, 0},
		{"negative_prompt of 255 characters", modelH31, map[string]any{"negative_prompt": longText(255)}, 0},
		{"negative_prompt of 256 characters", modelH31, map[string]any{"negative_prompt": longText(256)}, 1004},
		{"negative_prompt must be text", modelH31, map[string]any{"negative_prompt": 7}, 1004},
	}
	for _, path := range generationPaths {
		for _, tc := range cases {
			// prompt is the text endpoint's own parameter; negative_prompt rides on
			// every endpoint here, which the fake only length-checks.
			if _, usesPrompt := tc.extra["prompt"]; usesPrompt && path != "/generation/text-to-model" {
				continue
			}
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				body := generationBody(path, tc.model, tc.extra)
				status, _, response := f.do(http.MethodPost, path, key, body)
				if tc.code == 0 {
					if status != http.StatusOK || codeOf(t, response) != 0 {
						t.Fatalf("a valid request was refused: %d %v", status, response)
					}
					return
				}
				if status != http.StatusBadRequest || codeOf(t, response) != tc.code {
					t.Fatalf("status %d code %d (%v), want 400 and %d", status, codeOf(t, response), response, tc.code)
				}
				if message, _ := response["message"].(string); strings.TrimSpace(message) == "" {
					t.Fatalf("the refusal names no reason: %v", response)
				}
			})
		}
	}
}

// A refused request creates nothing: it never takes a place in the concurrency
// pool, so with a pool of one the next valid request is still accepted.
func TestRefusedGenerationNeverTakesAPoolSlot(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: 1})
	path := "/generation/text-to-model"
	if status, _, _ := f.do(http.MethodPost, path, key, generationBody(path, modelP1, map[string]any{"quad": true})); status != http.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	if status, _, response := f.do(http.MethodPost, path, key, generationBody(path, modelP1, nil)); status != http.StatusOK {
		t.Fatalf("the valid request after a refusal got %d %v", status, response)
	}
	if status, _, _ := f.do(http.MethodPost, path, key, generationBody(path, modelP1, nil)); status != http.StatusTooManyRequests {
		t.Fatalf("a second valid request in a pool of one got %d, want 429", status)
	}
}

// The bodies Plinth's Offerings send are accepted: the H3.1 defaults of the
// production Offering (extreme textures, detailed geometry, the whole face budget)
// and the P2 defaults.
func TestGenerationAcceptsTheProductionDefaults(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	h31 := map[string]any{
		"texture": true, "pbr": true, "auto_size": false, "texture_quality": "extreme", "texture_version": "v3.5-20260815",
		"geometry_quality": "detailed", "face_limit": 2000000, "quad": false, "smart_low_poly": false, "export_uv": true,
		"generate_parts": false,
	}
	p2 := map[string]any{
		"texture": true, "pbr": true, "auto_size": false, "texture_quality": "standard", "texture_version": "v3.5-20260815",
		"delight": true, "quad": false, "export_uv": true, "face_limit": 20000,
	}
	for _, path := range generationPaths {
		for name, tc := range map[string]struct {
			model string
			extra map[string]any
		}{"H3.1 production": {modelH31, h31}, "P2 production": {modelP2, p2}} {
			status, _, response := f.do(http.MethodPost, path, key, generationBody(path, tc.model, tc.extra))
			if status != http.StatusOK {
				t.Errorf("%s %s: %d %v", path, name, status, response)
			}
		}
	}
}

// A generation request without a model gets the real answer of the 2026-10-29
// account sample (1004 "model is required, allowed values: ..."), and a name no
// family has the real "invalid model" answer of the 2026-11-25 probes (#577), on
// every generation endpoint: the same five names in byte order.
func TestModelRefusalsCarryTheRealText(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, "testdata/tripo-v3-account.candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	var missing string
	for _, exchange := range samples.Find("POST /v3/generation/text-to-model", "") {
		if exchange.Response.Status == http.StatusBadRequest {
			missing, _ = mustBody(t, exchange)["message"].(string)
		}
	}
	if missing == "" {
		t.Fatal("the account sample has no refused generation request")
	}
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	for _, path := range generationPaths {
		for _, tc := range []struct{ model, want string }{
			{"", missing},
			{"v2.0-20240919", "invalid model 'v2.0-20240919', allowed values: " + probeModels},
		} {
			body := generationBody(path, tc.model, nil)
			if tc.model == "" {
				delete(body, "model")
			}
			status, _, response := f.do(http.MethodPost, path, key, body)
			if status != http.StatusBadRequest || codeOf(t, response) != 1004 || response["message"] != tc.want {
				t.Errorf("%s model %q: %d %v, want 400/1004 %q", path, tc.model, status, response, tc.want)
			}
		}
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	goimage "image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

type geminiStubInputs struct{ content []byte }

func (g geminiStubInputs) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(g.content)), nil
}
func (g geminiStubInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func TestGeminiCurrentProtocolThroughApplication(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// A real opaque PNG: the Gemini reference parameter declares alpha_flatten (#271), which decodes
	// its source and passes an opaque one through byte for byte.
	image := opaqueReferencePNG(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer gemini-key" || r.Header.Get("x-goog-api-key") != "" {
			t.Errorf("incorrect chat image wire: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if body["model"] != "gemini-3-pro-image" || !reflect.DeepEqual(body["modalities"], []any{"text", "image"}) {
			t.Errorf("model/modalities: %#v", body)
		}
		format, _ := body["eca_image_config"].(map[string]any)
		if len(format) != 2 || format["aspect_ratio"] != "16:9" || format["image_size"] != "2K" {
			t.Errorf("format: %#v", format)
		}
		if body["stream"] != false {
			t.Error("non-streaming contract lost")
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 1 {
			t.Error("missing chat message")
			http.Error(w, "missing message", 400)
			return
		}
		input, _ := messages[0].(map[string]any)["content"].([]any)
		if len(input) != calls {
			t.Errorf("input: %#v", input)
		}
		if calls == 2 {
			item := input[1].(map[string]any)
			if item["type"] != "image_url" || item["image_url"].(map[string]any)["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image) {
				t.Errorf("asset: %#v", item)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)}}}}}}})
	}))
	defer server.Close()
	registry := providers.NewRegistry()
	if err := registerProviders(Config{ProviderMode: "live", RuntimeSettings: map[string]string{"gemini_base_url": server.URL + "/v1", "gemini_api_key": "gemini-key"}}, c, registry, geminiStubInputs{image}, nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
		o, _ := c.Get(generation.OfferingID("gemini/gemini-3-pro-image/" + string(capability)))
		adapter, ok := registry.Provider(o)
		if !ok {
			t.Fatal("missing adapter")
		}
		raw := `{"prompt":"fox","aspect_ratio":"16:9","image_size":"2K"}`
		assets := map[string][]generation.InputAsset{}
		if capability == generation.ImageToImage {
			raw = `{"prompt":"fox","aspect_ratio":"16:9","image_size":"2K","reference_assets":["ref"]}`
			assets["reference_assets"] = []generation.InputAsset{{ID: "ref", MediaType: "image/png"}}
		}
		_, normalized, err := c.Normalize(capability, o.ID, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		obs, err := adapter.Start(context.Background(), generation.StartRequest{Offering: o, Input: normalized, InputAssets: assets, AttemptID: "attempt"})
		if err != nil {
			t.Fatal(err)
		}
		if obs.Phase != generation.UpstreamCompleted || len(obs.Artifacts) != 1 {
			t.Fatalf("observation: %#v", obs)
		}
	}
	if calls != 2 {
		t.Fatalf("expected one request per capability, got %d", calls)
	}
}

// #252: GEMINI_BASE_URL has no default and never borrows the Agent's
// LLM_BASE_URL. A Gemini key without an address leaves the connection
// unregistered (its offerings show as unavailable) and logs which variable is
// missing, instead of sending the key to a guessed address.
func TestGeminiKeyWithoutBaseURLIsNotRegistered(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	registry := providers.NewRegistry()
	settings := map[string]string{"gemini_api_key": "gemini-secret-key", "openai_api_key": "openai-key"}
	if err := registerProviders(Config{ProviderMode: "live", RuntimeSettings: settings}, c, registry, geminiStubInputs{}, nil, slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(registry.Keys(), "gemini-images.v1") || !slices.Contains(registry.Keys(), "openai-images.v1") {
		t.Fatalf("registered = %v; Gemini without GEMINI_BASE_URL must stay unregistered", registry.Keys())
	}
	if !strings.Contains(logs.String(), "connection=gemini-images") ||
		!strings.Contains(logs.String(), "missing_settings=GEMINI_BASE_URL") ||
		strings.Contains(logs.String(), "gemini-secret-key") {
		t.Fatalf("startup log = %s", logs.String())
	}

	registry = providers.NewRegistry()
	settings["gemini_base_url"] = "https://gateway.example/v1"
	if err := registerProviders(Config{ProviderMode: "live", RuntimeSettings: settings}, c, registry, geminiStubInputs{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(registry.Keys(), "gemini-images.v1") {
		t.Fatalf("registered = %v; an explicit GEMINI_BASE_URL enables Gemini", registry.Keys())
	}
}

func opaqueReferencePNG(t *testing.T) []byte {
	t.Helper()
	picture := goimage.NewNRGBA(goimage.Rect(0, 0, 2, 2))
	for index := 3; index < len(picture.Pix); index += 4 {
		picture.Pix[index] = 0xff
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

func TestOpenAICurrentProtocolThroughApplication(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// A real opaque PNG: GPT Image's reference parameter declares alpha_flatten (#271), which decodes
	// its source and passes an opaque one through byte for byte.
	image := opaqueReferencePNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			if body["model"] != "gpt-image-2.5-sunburst" || body["prompt"] != "fox" {
				t.Errorf("generation body: %#v", body)
			}
		case "/v1/images/edits":
			if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
				t.Errorf("edit content type: %q", contentType)
			}
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("multipart parse: %v", err)
				http.Error(w, "bad form", 400)
				return
			}
			if r.FormValue("model") != "gpt-image-2.5-sunburst" || r.FormValue("prompt") != "fox" {
				t.Errorf("edit fields: model=%q prompt=%q", r.FormValue("model"), r.FormValue("prompt"))
			}
			if file, _, err := r.FormFile("image[]"); err != nil {
				t.Errorf("image[] part: %v", err)
			} else {
				data, _ := io.ReadAll(file)
				if !bytes.Equal(data, image) {
					t.Errorf("image part bytes = %d", len(data))
				}
			}
			if file, _, err := r.FormFile("mask"); err != nil {
				t.Errorf("mask part: %v", err)
			} else {
				data, _ := io.ReadAll(file)
				if !bytes.Equal(data, image) {
					t.Errorf("mask part bytes = %d", len(data))
				}
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(image)}}})
	}))
	defer server.Close()
	registry := providers.NewRegistry()
	if err := registerProviders(Config{ProviderMode: "live", RuntimeSettings: map[string]string{"openai_base_url": server.URL + "/v1", "openai_api_key": "openai-key"}}, c, registry, geminiStubInputs{image}, nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		capability generation.Capability
		input      string
	}{
		{generation.TextToImage, `{"prompt":"fox","quality":"high"}`},
		{generation.ImageToImage, `{"prompt":"fox","reference_assets":["ref"],"mask":"mask","quality":"xhigh"}`},
	} {
		o, _ := c.Get(generation.OfferingID("openai/gpt-image-2/" + string(testCase.capability)))
		adapter, ok := registry.Provider(o)
		if !ok {
			t.Fatal("missing adapter")
		}
		assets := map[string][]generation.InputAsset{}
		if testCase.capability == generation.ImageToImage {
			assets["reference_assets"] = []generation.InputAsset{{ID: "ref", MediaType: "image/png"}}
			assets["mask"] = []generation.InputAsset{{ID: "mask", MediaType: "image/png"}}
		}
		obs, err := adapter.Start(context.Background(), generation.StartRequest{Offering: o, Input: []byte(testCase.input), InputAssets: assets, AttemptID: "attempt"})
		if err != nil {
			t.Fatal(err)
		}
		if obs.Phase != generation.UpstreamCompleted || len(obs.Artifacts) != 1 {
			t.Fatalf("observation: %#v", obs)
		}
	}
}

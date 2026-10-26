package schemaimages_test

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
)

type assets struct{ data []byte }

func (a assets) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}
func (a assets) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(a.data)), nil
}

func TestConfiguredParametersAndAssetsReachCurrentJSONAPI(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	png := opaqueFixturePNG(1)
	for _, tc := range []struct {
		id      string
		input   string
		path    string
		inspect func(*testing.T, map[string]any)
	}{
		{"openai/gpt-image-2/text-to-image", `{"prompt":"fox","size":"2048x1152","n":2,"output_format":"webp","background":"transparent"}`, "/images/generations", func(t *testing.T, b map[string]any) {
			if b["n"] != float64(2) || b["size"] != "2048x1152" || b["output_format"] != "png" || b["quality"] != "max" || b["output_compression"] != nil {
				t.Errorf("body=%#v", b)
			}
		}},
		{"jimeng/seedream-4-5/image-to-image", `{"prompt":"edit","reference_assets":["a"],"size":"5504x3040","sequential_image_generation":"auto","max_images":3}`, "/api/v3/images/generations", func(t *testing.T, b map[string]any) {
			if b["sequential_image_generation_options"].(map[string]any)["max_images"] != float64(3) || len(b["image"].([]any)) != 1 {
				t.Errorf("body=%#v", b)
			}
		}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			o, _ := c.Get(generation.OfferingID(tc.id))
			_, input, err := c.Normalize(o.Capability, o.ID, []byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			// A new parameter is configured only in the Offering, without adapter edits.
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "future_parameter", Type: "number", WireName: "future.settings.value"})
			var values map[string]any
			_ = json.Unmarshal(input, &values)
			values["future_parameter"] = float64(7)
			input, _ = json.Marshal(values)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path=%s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["future"].(map[string]any)["settings"].(map[string]any)["value"] != float64(7) {
					t.Error("schema-added parameter dropped")
				}
				tc.inspect(t, body)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(png)}, map[string]any{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
			}))
			defer upstream.Close()
			adapter, err := schemaimages.New(schemaimages.Config{BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client(), InputAssets: assets{png}})
			if err != nil {
				t.Fatal(err)
			}
			obs, err := adapter.Start(context.Background(), generation.StartRequest{Offering: o, Input: input, AttemptID: "a", InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "a", MediaType: "image/png"}}, "mask": {{ID: "m", MediaType: "image/png"}}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Artifacts) != 2 || obs.Artifacts[0].ID == obs.Artifacts[1].ID {
				t.Fatalf("claims=%#v", obs.Artifacts)
			}
		})
	}
}

func TestOpenAIImageToImagePostsDeclaredMultipartEdit(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, input, quality string }{
		{"highest-quality-default", `{"prompt":"edit","reference_assets":["a"],"mask":"m","size":"1536x1024"}`, "max"},
		{"legal-agent-adjustment", `{"prompt":"edit","reference_assets":["a"],"mask":"m","size":"1536x1024","quality":"xhigh","output_format":"webp"}`, "xhigh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			png := opaqueFixturePNG(1)
			var model, prompt, size, quality, outputFormat string
			var imageBody, maskBody []byte
			var imageType, imageName, maskType string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/images/edits" {
					t.Errorf("path=%s", r.URL.Path)
				}
				if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
					t.Errorf("content-type=%q", contentType)
				}
				if err := r.ParseMultipartForm(4 << 20); err != nil {
					t.Errorf("multipart parse: %v", err)
					http.Error(w, "not multipart", http.StatusBadRequest)
					return
				}
				model = r.FormValue("model")
				prompt = r.FormValue("prompt")
				size = r.FormValue("size")
				quality = r.FormValue("quality")
				outputFormat = r.FormValue("output_format")
				if r.FormValue("future_param") != "7" {
					t.Errorf("schema-added multipart field dropped: %q", r.FormValue("future_param"))
				}
				if file, header, err := r.FormFile("image[]"); err != nil {
					t.Errorf("image[] part: %v", err)
				} else {
					imageBody, _ = io.ReadAll(file)
					imageType = header.Header.Get("Content-Type")
					imageName = header.Filename
				}
				if file, header, err := r.FormFile("mask"); err != nil {
					t.Errorf("mask part: %v", err)
				} else {
					maskBody, _ = io.ReadAll(file)
					maskType = header.Header.Get("Content-Type")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
			}))
			defer upstream.Close()
			adapter, err := schemaimages.New(schemaimages.Config{BaseURL: upstream.URL, APIKey: "test", HTTPClient: upstream.Client(), InputAssets: assets{png}})
			if err != nil {
				t.Fatal(err)
			}
			o, _ := c.Get("openai/gpt-image-2/image-to-image")
			_, input, err := c.Normalize(o.Capability, o.ID, []byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			// A new parameter is configured only in the Offering, without adapter edits.
			o.Parameters = append(o.Parameters, generation.Parameter{Name: "future_parameter", Type: "number", WireName: "future_param"})
			var values map[string]any
			_ = json.Unmarshal(input, &values)
			values["future_parameter"] = float64(7)
			input, _ = json.Marshal(values)
			obs, err := adapter.Start(context.Background(), generation.StartRequest{
				Offering:  o,
				Input:     input,
				AttemptID: "multipart",
				InputAssets: map[string][]generation.InputAsset{
					"reference_assets": {{ID: "a", MediaType: "image/png"}},
					"mask":             {{ID: "m", MediaType: "image/png"}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Artifacts) != 1 {
				t.Fatalf("claims=%#v", obs.Artifacts)
			}
			if model != "gpt-image-2.5-sunburst" || prompt != "edit" || size != "1536x1024" || quality != tc.quality || outputFormat != "png" {
				t.Errorf("form fields: model=%q prompt=%q size=%q quality=%q format=%q", model, prompt, size, quality, outputFormat)
			}
			if !bytes.Equal(imageBody, png) || imageType != "image/png" || imageName == "" {
				t.Errorf("image part: type=%q name=%q bytes=%d", imageType, imageName, len(imageBody))
			}
			if !bytes.Equal(maskBody, png) || maskType != "image/png" {
				t.Errorf("mask part: type=%q bytes=%d", maskType, len(maskBody))
			}
		})
	}
}

func TestOpenAIImageToImageAdvisedQualityStillRejectsInvalidInputs(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := c.Get("openai/gpt-image-2/image-to-image")
	for _, raw := range []string{
		`{"prompt":"edit","reference_assets":["a"],"quality":"unsupported"}`,
		`{"prompt":"edit","reference_assets":["a"],"size":"1x1"}`,
		`{"prompt":"edit","reference_assets":["a"],"n":11}`,
		`{"prompt":"edit","reference_assets":["a"],"unexpected_field":true}`,
	} {
		if _, _, err := c.Normalize(o.Capability, o.ID, []byte(raw)); err == nil {
			t.Errorf("accepted invalid input %s", raw)
		}
	}
}

func TestRejectedConfiguredProtocolNeverFallsBack(t *testing.T) {
	c, _ := catalog.LoadDefault()
	o, _ := c.Get("gemini/gemini-3-pro-image/text-to-image")
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected fallback %s", r.URL.Path)
		}
		http.Error(w, `{"error":"unsupported"}`, 404)
	}))
	defer upstream.Close()
	adapter, _ := schemaimages.New(schemaimages.Config{BaseURL: upstream.URL, APIKey: "test"})
	_, err := adapter.Start(context.Background(), generation.StartRequest{Offering: o, Input: []byte(`{"prompt":"fox"}`)})
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

// The gateway returns Gemini images as data URIs inside chat content, mixed with text.
func TestGeminiChatImagesRoundTrip(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	png := opaqueFixturePNG(1)
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	for _, count := range []int{0, 1, 3, 14} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			capability := "text-to-image"
			values := map[string]any{"prompt": "change the teapot to red"}
			resolved := map[string][]generation.InputAsset{}
			if count > 0 {
				capability = "image-to-image"
				ids := make([]string, count)
				for i := range ids {
					ids[i] = fmt.Sprint(i)
					resolved["reference_assets"] = append(resolved["reference_assets"], generation.InputAsset{ID: generation.AssetID(ids[i]), MediaType: "image/png"})
				}
				values["reference_assets"] = ids
			}
			o, _ := c.Get(generation.OfferingID("gemini/gemini-3-pro-image/" + capability))
			raw, _ := json.Marshal(values)
			_, input, err := c.Normalize(o.Capability, o.ID, raw)
			if err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("x-goog-api-key") != "" {
					t.Errorf("unexpected request path/auth: %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != "gemini-3-pro-image" || body["stream"] != false {
					t.Errorf("request=%#v", body)
				}
				messages, ok := body["messages"].([]any)
				if !ok || len(messages) != 1 {
					t.Errorf("missing chat message")
					http.Error(w, "missing chat message", 400)
					return
				}
				message := messages[0].(map[string]any)
				content := message["content"].([]any)
				if message["role"] != "user" || len(content) != count+1 || content[0].(map[string]any)["text"] != values["prompt"] {
					t.Errorf("incorrect content: %#v", content)
				}
				for _, v := range content[1:] {
					part := v.(map[string]any)
					if part["type"] != "image_url" || part["image_url"].(map[string]any)["url"] != dataURI {
						t.Errorf("reference image not preserved")
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "done"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURI}}}}}}})
			}))
			defer upstream.Close()
			adapter, err := schemaimages.New(schemaimages.Config{BaseURL: upstream.URL + "/v1", APIKey: "test", HTTPClient: upstream.Client(), InputAssets: assets{png}})
			if err != nil {
				t.Fatal(err)
			}
			obs, err := adapter.Start(context.Background(), generation.StartRequest{Offering: o, Input: input, AttemptID: "chat", InputAssets: resolved})
			if err != nil {
				t.Fatal(err)
			}
			if obs.Phase != generation.UpstreamCompleted || len(obs.Artifacts) != 1 || !bytes.Equal(obs.Artifacts[0].Inline, png) || obs.Artifacts[0].MediaType != "image/png" || obs.Artifacts[0].Locator != "" {
				t.Fatalf("invalid image observation: %#v", obs)
			}
		})
	}
}

func TestGeminiChatTextOnlyIsNotImageSuccess(t *testing.T) {
	c, _ := catalog.LoadDefault()
	o, _ := c.Get("gemini/gemini-3-pro-image/text-to-image")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Here is your image"}}]}`)
	}))
	defer upstream.Close()
	adapter, _ := schemaimages.New(schemaimages.Config{BaseURL: upstream.URL, APIKey: "test"})
	_, err := adapter.Start(context.Background(), generation.StartRequest{Offering: o, Input: []byte(`{"prompt":"teapot"}`)})
	var failure *generation.ProviderError
	if !errors.As(err, &failure) || failure.Failure.Class != generation.FailureProtocol {
		t.Fatalf("text-only response accepted: %v", err)
	}
}

package tripo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

const tripoMultiviewOffering = "tripo/tripo-v3.1-multiview/image-to-3d"

type multiviewHarness struct {
	adapter  *tripo.Adapter
	offering generation.Offering
	calls    atomic.Int32
	uploads  atomic.Int32
	bodies   chan map[string]any
}

func newMultiviewHarness(t *testing.T) *multiviewHarness {
	t.Helper()
	h := &multiviewHarness{bodies: make(chan map[string]any, 4)}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		switch r.URL.Path {
		case "/files":
			n := h.uploads.Add(1)
			if _, err := nextMultipartFile(r); err != nil {
				t.Error(err)
			}
			writeEnvelope(w, map[string]string{"file_token": fmt.Sprintf("tok-%d", n)})
		case "/generation/multiview-to-model":
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			h.bodies <- body
			writeEnvelope(w, map[string]string{"task_id": "tripo-mv-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get(tripoMultiviewOffering)
	if !ok {
		t.Fatal("multiview offering missing")
	}
	h.offering = offering
	h.adapter, err = tripo.New(tripo.Config{
		BaseURL:     upstream.URL,
		APIKey:      "k",
		HTTPClient:  upstream.Client(),
		InputAssets: memoryInputAssets{content: []byte("\x89PNG\r\n\x1a\nmv")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// input builds a Schema-normalized input for n references, then applies raw
// overrides so the adapter's own checks see inputs the Catalog would refuse.
func (h *multiviewHarness) request(t *testing.T, n int, views []string, override map[string]any) generation.StartRequest {
	t.Helper()
	c, _ := catalog.LoadDefault()
	ids := make([]string, 0, n)
	assets := make([]generation.InputAsset, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("asset-%d", i)
		ids = append(ids, id)
		assets = append(assets, generation.InputAsset{ID: generation.AssetID(id), MediaType: "image/png"})
	}
	seed := map[string]any{"reference_assets": ids[:min(n, 4)], "authorize_extra_credits": true}
	if len(views) > 0 && len(override) == 0 {
		seed["reference_views"] = views
	}
	if n < 2 || n > 4 {
		seed["reference_assets"] = []string{"a", "b"}
	}
	raw, _ := json.Marshal(seed)
	_, normalized, err := c.Normalize(generation.ImageTo3D, tripoMultiviewOffering, raw)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	_ = json.Unmarshal(normalized, &input)
	input["reference_assets"] = ids
	if len(views) > 0 {
		input["reference_views"] = views
	}
	for k, v := range override {
		input[k] = v
	}
	encoded, _ := json.Marshal(input)
	return generation.StartRequest{
		AttemptID: "a", SubmissionKey: "s", Offering: h.offering, Input: encoded,
		InputAssets: map[string][]generation.InputAsset{"reference_assets": assets},
	}
}

func TestMultiviewInputsWireShape(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		views []string
		want  []map[string]any
	}{
		{"two default", 2, nil, []map[string]any{{"front": "tok-1"}, {"left": "tok-2"}}},
		{"three default", 3, nil, []map[string]any{{"front": "tok-1"}, {"left": "tok-2"}, {"back": "tok-3"}}},
		{"four default", 4, nil, []map[string]any{{"front": "tok-1"}, {"left": "tok-2"}, {"back": "tok-3"}, {"right": "tok-4"}}},
		{"front and back", 2, []string{"front", "back"}, []map[string]any{{"front": "tok-1"}, {"back": "tok-2"}}},
		{"order free", 3, []string{"right", "front", "left"}, []map[string]any{{"right": "tok-1"}, {"front": "tok-2"}, {"left": "tok-3"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMultiviewHarness(t)
			started, err := h.adapter.Start(context.Background(), h.request(t, tc.n, tc.views, nil))
			if err != nil {
				t.Fatal(err)
			}
			if started.Job == nil || started.Job.RemoteID != "tripo-mv-1" {
				t.Fatalf("started = %#v", started)
			}
			body := <-h.bodies
			inputs, _ := json.Marshal(body["inputs"])
			want, _ := json.Marshal(tc.want)
			if string(inputs) != string(want) {
				t.Fatalf("inputs = %s, want %s", inputs, want)
			}
			for _, banned := range []string{"input", "reference_views", "reference_assets", "enable_image_autofix"} {
				if _, present := body[banned]; present {
					t.Errorf("body leaked %q: %#v", banned, body)
				}
			}
			if body["model"] != "v3.1-20260211" {
				t.Errorf("model = %v", body["model"])
			}
			if got := int(h.uploads.Load()); got != tc.n {
				t.Errorf("uploads = %d, want %d", got, tc.n)
			}
		})
	}
}

func TestMultiviewRefusesBadViewsBeforeAnyUpstreamCall(t *testing.T) {
	cases := []struct {
		name     string
		n        int
		override map[string]any
	}{
		{"missing front", 2, map[string]any{"reference_views": []string{"left", "back"}}},
		{"duplicate view", 2, map[string]any{"reference_views": []string{"front", "front"}}},
		{"count mismatch fewer views", 3, map[string]any{"reference_views": []string{"front", "left"}}},
		{"count mismatch more views", 2, map[string]any{"reference_views": []string{"front", "left", "back"}}},
		{"unknown view", 2, map[string]any{"reference_views": []string{"front", "top"}}},
		{"too many assets", 5, nil},
		{"single asset", 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMultiviewHarness(t)
			req := h.request(t, tc.n, nil, tc.override)
			_, err := h.adapter.Start(context.Background(), req)
			var provider *generation.ProviderError
			if err == nil || !errors.As(err, &provider) || provider.Failure.Class != generation.FailureInvalidInput {
				t.Fatalf("Start err = %v", err)
			}
			if got := h.calls.Load(); got != 0 {
				t.Fatalf("upstream calls = %d", got)
			}
			if err := h.adapter.ValidateInput(context.Background(), generation.InputValidationRequest{
				Offering: req.Offering, Input: req.Input, InputAssets: req.InputAssets,
			}); err == nil {
				t.Fatal("ValidateInput accepted invalid views")
			}
			if got := h.calls.Load(); got != 0 {
				t.Fatalf("upstream calls after ValidateInput = %d", got)
			}
		})
	}
}

func TestMultiviewValidateInputAcceptsValid(t *testing.T) {
	h := newMultiviewHarness(t)
	req := h.request(t, 3, []string{"front", "right", "back"}, nil)
	if err := h.adapter.ValidateInput(context.Background(), generation.InputValidationRequest{
		Offering: req.Offering, Input: req.Input, InputAssets: req.InputAssets,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMultiviewOfferingDeclaration(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get(tripoMultiviewOffering)
	if !ok {
		t.Fatal("missing")
	}
	if o.ReferenceSelectionPolicy != "preserve" || o.Default || o.Recommended {
		t.Fatalf("policy=%q default=%v recommended=%v", o.ReferenceSelectionPolicy, o.Default, o.Recommended)
	}
	names := []string{}
	for _, p := range o.Parameters {
		names = append(names, p.Name)
		if p.Name == "enable_image_autofix" {
			t.Fatal("enable_image_autofix must not exist")
		}
	}
	if !strings.Contains(strings.Join(names, ","), "reference_views") {
		t.Fatalf("parameters = %v", names)
	}
	if !reflect.DeepEqual(strings.Contains(string(o.ProviderOptions), "multiview-to-model"), true) {
		t.Fatalf("options = %s", o.ProviderOptions)
	}
}

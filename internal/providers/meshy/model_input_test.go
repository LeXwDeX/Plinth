package meshy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type profiledInputs struct {
	bodies [][]byte
	opens  int
}

func (s *profiledInputs) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	panic("already resolved")
}
func (s *profiledInputs) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	i := s.opens
	s.opens++
	if i >= len(s.bodies) {
		i = len(s.bodies) - 1
	}
	return io.NopCloser(bytes.NewReader(s.bodies[i])), nil
}

type profileTransport func(*http.Request) (*http.Response, error)

func (f profileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func modelProfileRequest(raw []byte, mime string) generation.InputValidationRequest {
	l := schema.ModelInputLimits{MaxBytes: 10 << 20, MaxJSONBytes: 2 << 20, MaxDepth: 32, MaxElements: 100000, MaxVertices: 1000000, MaxTriangles: 1000000, MaxNodes: 10000, MaxWork: 64 << 20, MaxImagePixels: 1000000, MaxTotalImagePixels: 2000000}
	o := generation.Offering{ProviderID: "meshy", Parameters: []schema.Parameter{{Name: "source_asset", Type: "asset", Required: true, Semantic: schema.SemanticSourceAsset, WireName: "model_url", MaxBytes: l.MaxBytes, MediaTypes: []string{"model/gltf-binary"}, UploadedModel: &schema.UploadedModel{ProfileID: "static-glb-v1", ProfileRevision: "1", ProfileLimits: &l}}}}
	opts := wireOptions{Endpoint: "uv-unwrap", APIVersion: "v1", InlineModelInput: &inlineModelInput{MaxBytes: l.MaxBytes, MediaType: mime}, ModelInputProfile: &schema.ModelInputProfileOptions{Parameter: "source_asset", ProfileID: "static-glb-v1", ProfileRevision: "1", ProfileLimits: &l, MaxInstancedTriangles: 40000}, ResultAssets: []resultAssetProjection{{Path: "model_urls", Role: "model", Mode: "map"}}}
	o.ProviderOptions, _ = json.Marshal(opts)
	sum := sha256.Sum256(raw)
	asset := generation.InputAsset{ID: "owned-model", OwnerID: "owner", StorageKey: "owned-source", MediaType: "model/gltf-binary", Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}
	return generation.InputValidationRequest{OwnerID: "owner", Offering: o, Input: json.RawMessage(`{"source_asset":"owned-model"}`), InputAssets: map[string][]generation.InputAsset{"source_asset": {asset}}}
}
func mutateProfileOptions(r *generation.InputValidationRequest, f func(*wireOptions)) {
	var o wireOptions
	_ = json.Unmarshal(r.Offering.ProviderOptions, &o)
	f(&o)
	r.Offering.ProviderOptions, _ = json.Marshal(o)
}
func TestProfiledModelInputRejectsBeforeNetwork(t *testing.T) {
	for _, name := range []string{"valid", "foreign owner", "different id", "hash changed", "size changed", "wrong mime", "missing owner", "invalid GLB", "animation", "instance limit", "half snapshot", "mismatched limits", "extra assets", "missing color atlas"} {
		t.Run(name, func(t *testing.T) {
			doc, bin := modelinputtest.Document(modelinputtest.Options{})
			if name == "animation" {
				doc["animations"] = []any{}
			}
			if name == "instance limit" {
				doc["nodes"] = []any{map[string]any{"mesh": 0}, map[string]any{"mesh": 0}}
				doc["scenes"] = []any{map[string]any{"nodes": []any{0, 1}}}
			}
			raw := modelinputtest.GLB(doc, bin)
			if name == "invalid GLB" {
				raw = []byte("not a model")
			}
			r := modelProfileRequest(raw, "application/octet-stream")
			a := r.InputAssets["source_asset"][0]
			switch name {
			case "foreign owner":
				a.OwnerID = "other"
			case "different id":
				a.ID = "other"
			case "hash changed":
				a.SHA256 = hex.EncodeToString(make([]byte, 32))
			case "size changed":
				a.Size++
			case "wrong mime":
				a.MediaType = "model/fbx"
			case "missing owner":
				r.OwnerID = ""
			case "instance limit":
				mutateProfileOptions(&r, func(o *wireOptions) { o.ModelInputProfile.MaxInstancedTriangles = 1 })
			case "half snapshot":
				r.Offering.Parameters[0].UploadedModel.ProfileRevision = ""
			case "mismatched limits":
				copy := *r.Offering.Parameters[0].UploadedModel.ProfileLimits
				copy.MaxVertices--
				r.Offering.Parameters[0].UploadedModel.ProfileLimits = &copy
			case "extra assets":
				r.InputAssets["hidden"] = []generation.InputAsset{a}
			case "missing color atlas":
				r.Offering.Parameters[0].UploadedModel.RequireSingleBaseColorTexture = true
			}
			r.InputAssets["source_asset"][0] = a
			store := &profiledInputs{bodies: [][]byte{raw}}
			adapter := &Adapter{inputAssets: store}
			err := adapter.ValidateInput(context.Background(), r)
			if name == "valid" {
				if err != nil || store.opens != 1 {
					t.Fatalf("valid: opens=%d err=%v", store.opens, err)
				}
			} else if err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}
func TestProfiledStartUsesOneValidatedBufferAndDeclaredMIME(t *testing.T) {
	for _, mime := range []string{"application/octet-stream", "model/gltf-binary"} {
		t.Run(mime, func(t *testing.T) {
			raw := modelinputtest.Triangle(modelinputtest.Options{})
			r := modelProfileRequest(raw, mime)
			store := &profiledInputs{bodies: [][]byte{raw, []byte("changed after first read")}}
			calls := 0
			adapter, err := New(Config{BaseURL: "https://meshy.invalid", APIKey: "fixture-only", InputAssets: store, HTTPClient: &http.Client{Transport: profileTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				want := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
				if body["model_url"] != want {
					t.Errorf("wire bytes or MIME differ from validated buffer")
				}
				if _, ok := body["source_asset"]; ok {
					t.Error("private asset ID leaked")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(`{"result":"accepted"}`))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			started, err := adapter.Start(context.Background(), generation.StartRequest{OwnerID: r.OwnerID, TaskID: "task", AttemptID: "attempt", Offering: r.Offering, Input: r.Input, InputAssets: r.InputAssets})
			if err != nil || started.Job == nil || store.opens != 1 || calls != 1 {
				t.Fatalf("start=%+v err=%v opens=%d calls=%d", started, err, store.opens, calls)
			}
		})
	}
}
func TestProfiledStartRechecksAfterPreparation(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	r := modelProfileRequest(raw, "application/octet-stream")
	store := &profiledInputs{bodies: [][]byte{raw, []byte("changed after prepare")}}
	calls := 0
	a, err := New(Config{BaseURL: "https://meshy.invalid", APIKey: "fixture-only", InputAssets: store, HTTPClient: &http.Client{Transport: profileTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(`{"result":"accepted"}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ValidateInput(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	_, err = a.Start(context.Background(), generation.StartRequest{OwnerID: r.OwnerID, TaskID: "task", AttemptID: "attempt", Offering: r.Offering, Input: r.Input, InputAssets: r.InputAssets})
	if err == nil || calls != 0 || store.opens != 2 {
		t.Fatalf("changed source submitted: err=%v calls=%d opens=%d", err, calls, store.opens)
	}
}
func TestProfiledOfferingRejectsMismatchedContract(t *testing.T) {
	for _, name := range []string{"valid", "missing media", "wrong media", "missing limits", "wrong revision", "missing parameter", "wrong inline bound", "ambiguous source"} {
		t.Run(name, func(t *testing.T) {
			r := modelProfileRequest(modelinputtest.Triangle(modelinputtest.Options{}), "application/octet-stream")
			mutateProfileOptions(&r, func(o *wireOptions) {
				switch name {
				case "missing media":
					o.InlineModelInput.MediaType = ""
				case "wrong media":
					o.InlineModelInput.MediaType = "text/html"
				case "missing limits":
					o.ModelInputProfile.ProfileLimits = nil
				case "wrong revision":
					o.ModelInputProfile.ProfileRevision = "other"
				case "missing parameter":
					o.ModelInputProfile.Parameter = "other"
				case "wrong inline bound":
					o.InlineModelInput.MaxBytes++
				}
			})
			if name == "ambiguous source" {
				r.Offering.Parameters = append(r.Offering.Parameters, schema.Parameter{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask})
			}
			err := ValidateOffering(r.Offering)
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid frozen contract accepted")
			}
		})
	}
}

// #358: a broken Offering declaration is an internal server condition, not a
// user source refusal.
func TestProfiledModelContractFailureStaysInternal(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	r := modelProfileRequest(raw, "application/octet-stream")
	limits := *r.Offering.Parameters[0].UploadedModel.ProfileLimits
	limits.MaxVertices--
	r.Offering.Parameters[0].UploadedModel.ProfileLimits = &limits
	err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), r)
	if !generation.IsCode(err, generation.ErrUnavailable) || generation.UserFacingError(err) != nil {
		t.Fatalf("error = %v", err)
	}
}

// #358: a model-source mismatch is a Schema source refusal with the original
// internal cause behind it.
func TestProfiledModelSourceMismatchUsesSchema(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	request := modelProfileRequest(raw, "application/octet-stream")
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	request.Offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	request.InputAssets["source_asset"][0].ID = "other"
	err = (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), request)
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.DetailTemplate != request.Offering.ExecutionMessages.InputSourceMismatch {
		t.Fatalf("error = %v", err)
	}
	if refusal.Cause == nil {
		t.Fatal("internal cause lost")
	}
}

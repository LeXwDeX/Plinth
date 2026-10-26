package localmodel_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func options(direction string) schema.ModelConversionOptions {
	return schema.ModelConversionOptions{Direction: direction, ProfileID: modelinput.ProfileID, ProfileRevision: modelinput.ProfileRevision, MaxWork: 64 << 20, ProfileLimits: &schema.ModelInputLimits{MaxBytes: 2 << 20, MaxJSONBytes: 1 << 20, MaxDepth: 64, MaxElements: 100000, MaxVertices: 1000000, MaxTriangles: 1000000, MaxNodes: 10000, MaxWork: 64 << 20, MaxImagePixels: 1000000, MaxTotalImagePixels: 2000000}}
}
func offering(direction string) generation.Offering {
	opts := options(direction)
	raw, _ := json.Marshal(opts)
	minimum := 1
	parameter := generation.Parameter{Name: "source_asset", Type: "asset", Required: true, Semantic: generation.SemanticSourceAsset, UploadedModel: &schema.UploadedModel{ProfileID: modelinput.ProfileID}, MediaTypes: []string{"model/gltf-binary"}, Extensions: []string{"glb"}, MaxBytes: 2 << 20}
	variant, mime, id := "gltf", "model/gltf+json", "local/test-forward"
	if direction == schema.ModelConversionGLTFToGLB {
		id, variant, mime = "local/test-reverse", "glb", "model/gltf-binary"
		parameter = generation.Parameter{Name: "source_task", Type: "task", Required: true, Semantic: generation.SemanticSourceTask, TaskSourceKind: schema.TaskSourceAssets, TaskOfferings: []generation.OfferingID{"local/test-forward"}, TaskCapabilities: []generation.Capability{generation.FormatConversion}, MediaTypes: []string{"model/gltf+json"}, Extensions: []string{"gltf"}, MaxBytes: 3 << 20}
	}
	return generation.Offering{ID: generation.OfferingID(id), ProviderID: "local-model", AdapterKey: "local-model", Capability: generation.FormatConversion, Domain: generation.DomainProcessing, Revision: "fixture-v1", ProviderOptions: raw, Parameters: []generation.Parameter{parameter, {Name: "format", Type: "select", Required: true, Fixed: true, Default: json.RawMessage(`"` + variant + `"`), Options: []generation.ParameterOption{{Value: variant}}}}, Outputs: []generation.AssetRequirement{{Role: "model", Variant: variant, Minimum: &minimum, MediaTypes: []string{mime}, MaxBytes: 3 << 20}}}
}

type inputMemory struct {
	body  []byte
	opens int
}

func (*inputMemory) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}
func (m *inputMemory) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	m.opens++
	return io.NopCloser(bytes.NewReader(m.body)), nil
}

type assetMemory struct {
	body                 []byte
	opens, puts, deletes int
}

func (m *assetMemory) Put(context.Context, generation.AssetWrite) (generation.Asset, error) {
	m.puts++
	return generation.Asset{}, errors.New("local provider must not store artifacts")
}
func (m *assetMemory) Open(context.Context, generation.Asset) (io.ReadCloser, error) {
	m.opens++
	return io.NopCloser(bytes.NewReader(m.body)), nil
}
func (m *assetMemory) Delete(context.Context, generation.Asset) error {
	m.deletes++
	return errors.New("local provider must not delete")
}
func forwardRequest(raw []byte) generation.StartRequest {
	return generation.StartRequest{TaskID: "forward-task", OwnerID: "owner", AttemptID: "forward-attempt", SubmissionKey: "forward-submission", Offering: offering(schema.ModelConversionGLBToGLTF), Input: json.RawMessage(`{"source_asset":"upload","format":"gltf"}`), InputAssets: map[string][]generation.InputAsset{"source_asset": {{ID: "upload", OwnerID: "owner", StorageKey: "original-upload", MediaType: "model/gltf-binary", AssetType: "model", Size: int64(len(raw)), SHA256: digest(raw)}}}}
}
func reverseRequest(raw []byte) generation.StartRequest {
	asset := generation.Asset{ID: "forward-asset", TaskID: "forward-task", StorageKey: "saved-forward-output", Role: "model", Variant: "gltf", MediaType: "model/gltf+json", Size: int64(len(raw)), SHA256: digest(raw), Required: true}
	return generation.StartRequest{TaskID: "reverse-task", OwnerID: "owner", AttemptID: "reverse-attempt", SubmissionKey: "reverse-submission", Offering: offering(schema.ModelConversionGLTFToGLB), Input: json.RawMessage(`{"source_task":"forward-task","format":"glb"}`), TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "forward-task", Capability: generation.FormatConversion, OfferingID: "local/test-forward", ProviderID: "local-model", Assets: []generation.Asset{asset}}}}
}
func adapter(t *testing.T, input *inputMemory, assets *assetMemory) *localmodel.Adapter {
	t.Helper()
	port, err := localmodel.New(localmodel.Config{Inputs: input, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}
	return port
}
func TestLocalModelConvertsActualBytesBothDirectionsWithoutDispatchOrStorage(t *testing.T) {
	for _, shape := range []modelinputtest.Options{{}, {Indexed: true}, {Interleaved: true, Indexed: true}, {Interleaved: true, Indexed: true, Textured: true, MultiplePrimitives: true}} {
		original := modelinputtest.Triangle(shape)
		input := &inputMemory{body: bytes.Clone(original)}
		assets := &assetMemory{}
		port := adapter(t, input, assets)
		request := forwardRequest(original)
		first, err := port.Start(generation.WithDeniedDispatchCheckpointer(context.Background()), request)
		if err != nil {
			t.Fatal(err)
		}
		if first.Phase != generation.UpstreamCompleted || first.Job != nil || first.Dispatch != nil || len(first.Artifacts) != 1 || len(first.Artifacts[0].Inline) == 0 {
			t.Fatalf("not a synchronous local output: %#v", first)
		}
		claim := first.Artifacts[0]
		if claim.Role != "model" || claim.Variant != "gltf" || claim.MediaType != "model/gltf+json" || claim.Locator != "" {
			t.Fatal("claim identity differs from frozen output")
		}
		if !bytes.Equal(input.body, original) || assets.puts != 0 || assets.deletes != 0 || assets.opens != 0 {
			t.Fatal("forward mutated or stored a source")
		}
		stream, meta, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: request.Offering, Claim: claim})
		if err != nil {
			t.Fatal(err)
		}
		gltf, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil || !bytes.Equal(gltf, claim.Inline) || meta.Size != int64(len(gltf)) {
			t.Fatal("OpenAsset changed inline bytes or identity")
		}
		assets.body = bytes.Clone(gltf)
		reversed, err := port.Start(context.Background(), reverseRequest(gltf))
		if err != nil {
			t.Fatal(err)
		}
		if reversed.Phase != generation.UpstreamCompleted || reversed.Job != nil || reversed.Dispatch != nil || len(reversed.Artifacts) != 1 || reversed.Artifacts[0].Variant != "glb" || reversed.Artifacts[0].MediaType != "model/gltf-binary" {
			t.Fatal("reverse did not produce its one frozen output")
		}
		if !bytes.Equal(original, reversed.Artifacts[0].Inline) {
			t.Fatal("container round trip changed valid fixture bytes")
		}
		if input.opens != 1 || assets.opens != 1 || assets.puts != 0 || assets.deletes != 0 {
			t.Fatal("local conversion used extra data ports")
		}
	}
}
func TestLocalModelOfferingValidationIsClosedAndFree(t *testing.T) {
	for _, name := range []string{"unknown", "duplicate", "trailing", "missing_profile", "bad_profile", "zero_input", "zero_output", "wrong_direction_mime", "paid", "no_origin", "upstream_source"} {
		t.Run(name, func(t *testing.T) {
			got := offering(schema.ModelConversionGLBToGLTF)
			switch name {
			case "unknown":
				got.ProviderOptions = bytes.Replace(got.ProviderOptions, []byte(`"direction"`), []byte(`"url":"https://invalid.example/","direction"`), 1)
			case "duplicate":
				got.ProviderOptions = bytes.Replace(got.ProviderOptions, []byte(`"direction"`), []byte(`"direction":"gltf-to-glb","direction"`), 1)
			case "trailing":
				got.ProviderOptions = append(got.ProviderOptions, []byte(` {}`)...)
			case "missing_profile":
				o := options(schema.ModelConversionGLBToGLTF)
				o.ProfileLimits = nil
				got.ProviderOptions, _ = json.Marshal(o)
			case "bad_profile":
				o := options(schema.ModelConversionGLBToGLTF)
				o.ProfileRevision = "unknown"
				got.ProviderOptions, _ = json.Marshal(o)
			case "zero_input":
				got.Parameters[0].MaxBytes = 0
			case "zero_output":
				got.Outputs[0].MaxBytes = 0
			case "wrong_direction_mime":
				got.Outputs[0].MediaTypes = []string{"model/gltf-binary"}
			case "paid":
				got.EstimatedCredits = 5
			case "no_origin":
				got = offering(schema.ModelConversionGLTFToGLB)
				got.Parameters[0].TaskOfferings = nil
			case "upstream_source":
				got = offering(schema.ModelConversionGLTFToGLB)
				got.Parameters[0].TaskSourceKind = schema.TaskSourceUpstreamJob
			}
			if localmodel.ValidateOffering(got) == nil {
				t.Fatal("invalid local protocol accepted")
			}
		})
	}
	if _, err := localmodel.New(localmodel.Config{}); err == nil {
		t.Fatal("missing storage ports accepted")
	}
}
func TestLocalModelSourceIdentityAndActualBytesFailClosed(t *testing.T) {
	for _, name := range []string{"owner", "id", "size", "hash", "actual_bytes", "extra", "url", "limit"} {
		t.Run(name, func(t *testing.T) {
			raw := modelinputtest.Triangle(modelinputtest.Options{})
			input := &inputMemory{body: bytes.Clone(raw)}
			assets := &assetMemory{}
			port := adapter(t, input, assets)
			request := forwardRequest(raw)
			switch name {
			case "owner":
				request.InputAssets["source_asset"][0].OwnerID = "foreign"
			case "id":
				request.InputAssets["source_asset"][0].ID = "another-upload"
			case "size":
				request.InputAssets["source_asset"][0].Size++
			case "hash":
				request.InputAssets["source_asset"][0].SHA256 = "wrong"
			case "actual_bytes":
				input.body[0] ^= 1
			case "extra":
				request.Input = json.RawMessage(`{"source_asset":"upload","format":"gltf","source_url":"https://invalid.example/model.glb"}`)
			case "url":
				input.body = []byte(`{"buffers":[{"uri":"https://invalid.example/model.bin"}]}`)
			case "limit":
				request.InputAssets["source_asset"][0].Size = request.Offering.Parameters[0].MaxBytes + 1
			}
			observation, err := port.Start(context.Background(), request)
			if err == nil || len(observation.Artifacts) != 0 || assets.puts != 0 || assets.deletes != 0 {
				t.Fatal("unsafe source emitted partial or persisted output")
			}
		})
	}
}
func TestLocalModelReverseRequiresDeclaredTaskOriginAndUniqueStoredAsset(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	input := &inputMemory{body: raw}
	assets := &assetMemory{}
	port := adapter(t, input, assets)
	first, err := port.Start(context.Background(), forwardRequest(raw))
	if err != nil {
		t.Fatal(err)
	}
	assets.body = first.Artifacts[0].Inline
	for _, name := range []string{"foreign_offering", "wrong_task", "ambiguous", "wrong_variant", "stale_bytes", "no_dependency"} {
		t.Run(name, func(t *testing.T) {
			request := reverseRequest(first.Artifacts[0].Inline)
			dep := request.TaskDependencies["source_task"]
			dep.Assets = append([]generation.Asset(nil), dep.Assets...)
			switch name {
			case "foreign_offering":
				dep.OfferingID = "other/converter"
			case "wrong_task":
				dep.Assets[0].TaskID = "different-task"
			case "ambiguous":
				other := dep.Assets[0]
				other.ID = "second-output"
				dep.Assets = append(dep.Assets, other)
			case "wrong_variant":
				dep.Assets[0].Variant = "unrelated"
			case "stale_bytes":
				dep.Assets[0].SHA256 = hex.EncodeToString(make([]byte, 32))
			case "no_dependency":
				dep = generation.TaskDependency{}
			}
			request.TaskDependencies["source_task"] = dep
			observation, err := port.Start(context.Background(), request)
			if err == nil || len(observation.Artifacts) != 0 {
				t.Fatal("unverified reverse provenance produced output")
			}
		})
	}
}
func TestLocalModelUntrustedClaimCannotOpenUncheckedBytes(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	port := adapter(t, &inputMemory{body: raw}, &assetMemory{})
	forward := offering(schema.ModelConversionGLBToGLTF)
	output, err := port.Start(context.Background(), forwardRequest(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mime", "variant", "locator", "hash", "bytes", "oversize"} {
		t.Run(name, func(t *testing.T) {
			claim := output.Artifacts[0]
			claim.Inline = bytes.Clone(claim.Inline)
			claim.Metadata = bytes.Clone(claim.Metadata)
			switch name {
			case "mime":
				claim.MediaType = "application/octet-stream"
			case "variant":
				claim.Variant = "other"
			case "locator":
				claim.Locator = "https://invalid.example/model.gltf"
			case "hash":
				claim.Metadata = json.RawMessage(`{"sha256":"wrong","size":1}`)
			case "bytes":
				claim.Inline[0] ^= 1
			case "oversize":
				claim.Inline = bytes.Repeat([]byte("x"), int(forward.Outputs[0].MaxBytes)+1)
			}
			reader, _, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: forward, Claim: claim})
			if err == nil || reader != nil {
				t.Fatal("untrusted inline claim bypassed validation")
			}
		})
	}
	if _, err := port.Observe(context.Background(), generation.ObserveRequest{Offering: forward}); err == nil {
		t.Fatal("synchronous local conversion fabricated an observable job")
	}
}

func TestLocalModelReadOnlyValidatorRejectsUnsupportedAndLimitedInputs(t *testing.T) {
	for _, name := range []string{"valid_octet", "animation", "work", "output", "cancel"} {
		t.Run(name, func(t *testing.T) {
			document, binary := modelinputtest.Document(modelinputtest.Options{})
			if name == "animation" {
				document["animations"] = []any{map[string]any{}}
			}
			raw := modelinputtest.GLB(document, binary)
			input := &inputMemory{body: raw}
			assets := &assetMemory{}
			port := adapter(t, input, assets)
			request := forwardRequest(raw)
			ctx := context.Background()
			switch name {
			case "valid_octet":
				request.Offering.Parameters[0].MediaTypes = append(request.Offering.Parameters[0].MediaTypes, "application/octet-stream")
				request.InputAssets["source_asset"][0].MediaType = "application/octet-stream"
			case "work":
				o := options(schema.ModelConversionGLBToGLTF)
				o.MaxWork = 1
				request.Offering.ProviderOptions, _ = json.Marshal(o)
			case "output":
				request.Offering.Outputs[0].MaxBytes = 1
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			err := port.ValidateInput(ctx, generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets})
			if name == "valid_octet" {
				if err != nil {
					t.Fatal(err)
				}
			} else if name == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			} else {
				code := generation.ErrModelInputLimit
				if name == "animation" {
					code = generation.ErrUnsupportedModelInput
				}
				if !generation.IsCode(err, code) {
					t.Fatalf("wrong typed validator error: %v", err)
				}
			}
			if assets.puts != 0 || assets.deletes != 0 || assets.opens != 0 {
				t.Fatal("read-only validation materialized or deleted assets")
			}
		})
	}
}

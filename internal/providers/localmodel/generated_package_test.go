package localmodel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/modelconvert/modelconverttest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const generatedOfferingID generation.OfferingID = "local/generated-glb/to-gltf"

func generatedOffering(t testing.TB) generation.Offering {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get(generatedOfferingID)
	if !ok || !offering.Enabled {
		t.Fatal("default Schema must enable the Task-output glTF package route")
	}
	return offering
}

// capturingStore stands in for the CAS: it keeps the streamed package.
type capturingStore struct {
	mu     sync.Mutex
	writes []generation.AssetWrite
	bodies [][]byte
	opens  int
}

func (s *capturingStore) Put(_ context.Context, write generation.AssetWrite) (generation.Asset, error) {
	body, err := io.ReadAll(io.LimitReader(write.Body, write.MaxBytes+1))
	if err != nil {
		return generation.Asset{}, err
	}
	if int64(len(body)) > write.MaxBytes {
		return generation.Asset{}, errors.New("asset exceeds its declared maximum")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, write)
	s.bodies = append(s.bodies, body)
	return generation.Asset{ID: write.AssetID, TaskID: write.TaskID, AttemptID: write.AttemptID, Role: write.Role, Variant: write.Variant, Ordinal: write.Ordinal, StorageKey: "cas-" + digest(body), MediaType: write.MediaType, Size: int64(len(body)), SHA256: digest(body)}, nil
}
func (s *capturingStore) Open(context.Context, generation.Asset) (io.ReadCloser, error) {
	s.opens++
	return nil, errors.New("package route must not read stored assets")
}
func (s *capturingStore) Delete(context.Context, generation.Asset) error { return nil }

func packageRequest(offering generation.Offering, raw []byte, role string) generation.StartRequest {
	return generation.StartRequest{TaskID: "package-task", OwnerID: "owner", AttemptID: "package-attempt", SubmissionKey: "package-submission", Offering: offering, Input: json.RawMessage(`{"source_asset":"source-model","format":"gltf"}`), InputAssets: map[string][]generation.InputAsset{"source_asset": {{ID: "source-model", OwnerID: "owner", StorageKey: "task-output", MediaType: "model/gltf-binary", Size: int64(len(raw)), SHA256: digest(raw), Role: role, Variant: "glb"}}}}
}

func validation(request generation.StartRequest) generation.InputValidationRequest {
	return generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets}
}

func TestGeneratedPackageStreamsAValidGLTFZipIntoTheStoreFromATaskModel(t *testing.T) {
	offering := generatedOffering(t)
	raw := modelinputtest.Large(modelinputtest.LargeOptions{Grid: 722, Images: []modelinputtest.LargeImage{{Width: 8192, Height: 8192, RGBA: true}, {Width: 512, Height: 512, Noise: true}}})
	if len(raw) <= 10<<20 {
		t.Fatal("fixture must exceed the static upload limit")
	}
	input := &inputMemory{body: bytes.Clone(raw)}
	store := &capturingStore{}
	port, err := localmodel.New(localmodel.Config{Inputs: input, Assets: store})
	if err != nil {
		t.Fatal(err)
	}
	request := packageRequest(offering, raw, "model")
	observation, err := port.Start(generation.WithDeniedDispatchCheckpointer(context.Background()), request)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamCompleted || observation.Job != nil || observation.Dispatch != nil || len(observation.Artifacts) != 1 {
		t.Fatalf("not a synchronous local output: %#v", observation)
	}
	claim := observation.Artifacts[0]
	if claim.StoredAsset == nil || len(claim.Inline) != 0 || claim.Locator != "" || claim.Role != "model" || claim.Variant != "gltf" || claim.MediaType != "application/zip" {
		t.Fatalf("claim = %#v", claim)
	}
	stored := *claim.StoredAsset
	if len(store.writes) != 1 || stored.TaskID != request.TaskID || stored.AttemptID != request.AttemptID || stored.MediaType != "application/zip" || stored.Size != int64(len(store.bodies[0])) || observation.DedupeKey != stored.SHA256 {
		t.Fatalf("stored = %#v writes=%d", stored, len(store.writes))
	}
	write := store.writes[0]
	if write.OwnerID != "owner" || write.MaxBytes != offering.Outputs[0].MaxBytes || write.Role != "model" || write.Variant != "gltf" {
		t.Fatalf("write identity = %#v", write)
	}
	if err := modelconverttest.VerifyPackage(store.bodies[0], raw); err != nil {
		t.Fatal(err)
	}
	if input.opens != 1 || store.opens != 0 || !bytes.Equal(input.body, raw) {
		t.Fatal("conversion used extra reads or changed its source")
	}
	if _, _, err := port.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: offering, Claim: claim}); err == nil {
		t.Fatal("a stored package must not be reopened through the Provider")
	}
}

// The trust signal is InputAsset.Role, projected by the server from the stored
// row of a successful owned Task. A direct upload has no role; other Task
// outputs (textures, converted files) are not the Task's model.
func TestGeneratedPackageRefusesEverySourceThatIsNotATaskModel(t *testing.T) {
	offering := generatedOffering(t)
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	for _, role := range []string{"", "texture", "converted_model", "thumbnail", "Model"} {
		input := &inputMemory{body: bytes.Clone(raw)}
		store := &capturingStore{}
		port, err := localmodel.New(localmodel.Config{Inputs: input, Assets: store})
		if err != nil {
			t.Fatal(err)
		}
		request := packageRequest(offering, raw, role)
		if err := port.ValidateInput(context.Background(), validation(request)); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
			t.Fatalf("role %q passed validation: %v", role, err)
		}
		_, err = port.Start(context.Background(), request)
		var provider *generation.ProviderError
		if !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.Retryable || !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
			t.Fatalf("role %q started: %v", role, err)
		}
		if input.opens != 0 || len(store.writes) != 0 {
			t.Fatalf("role %q read or stored bytes", role)
		}
	}
	// A foreign owner's asset is refused the same way, before any read.
	request := packageRequest(offering, raw, "model")
	request.InputAssets["source_asset"][0].OwnerID = "foreign"
	input := &inputMemory{body: raw}
	port, _ := localmodel.New(localmodel.Config{Inputs: input, Assets: &capturingStore{}})
	if err := port.ValidateInput(context.Background(), validation(request)); err == nil || input.opens != 0 {
		t.Fatalf("foreign source accepted: %v", err)
	}
}

func TestGeneratedPackageVerdictIsCachedPerSourceDigestAndNeverForCancellation(t *testing.T) {
	offering := generatedOffering(t)
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	input := &inputMemory{body: bytes.Clone(raw)}
	port, err := localmodel.New(localmodel.Config{Inputs: input, Assets: &capturingStore{}})
	if err != nil {
		t.Fatal(err)
	}
	request := validation(packageRequest(offering, raw, "model"))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := port.ValidateInput(canceled, request); err == nil {
		t.Fatal("canceled validation succeeded")
	}
	for repeat := 0; repeat < 3; repeat++ {
		if err := port.ValidateInput(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if input.opens != 1 {
		t.Fatalf("polled validation read the source %d times", input.opens)
	}
	document, binary := modelinputtest.Document(modelinputtest.Options{Indexed: true})
	document["animations"] = []any{map[string]any{}}
	animated := modelinputtest.GLB(document, binary)
	input.body, input.opens = animated, 0
	animatedRequest := validation(packageRequest(offering, animated, "model"))
	for repeat := 0; repeat < 2; repeat++ {
		if err := port.ValidateInput(context.Background(), animatedRequest); !schema.IsCode(err, generation.ErrUnsupportedModelInput) {
			t.Fatalf("animated source: %v", err)
		}
	}
	if input.opens != 1 {
		t.Fatalf("a deterministic rejection was recomputed %d times", input.opens)
	}
}

func TestGeneratedPackageContractIsClosed(t *testing.T) {
	base := generatedOffering(t)
	if err := localmodel.ValidateOffering(base); err != nil {
		t.Fatal(err)
	}
	options := func(offering generation.Offering) schema.ModelConversionOptions {
		var value schema.ModelConversionOptions
		if err := json.Unmarshal(offering.ProviderOptions, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	encode := func(value schema.ModelConversionOptions) json.RawMessage {
		raw, _ := json.Marshal(value)
		return raw
	}
	clone := func() generation.Offering {
		raw, _ := json.Marshal(base)
		var copy generation.Offering
		if err := json.Unmarshal(raw, &copy); err != nil {
			t.Fatal(err)
		}
		return copy
	}
	for name, mutate := range map[string]func(*generation.Offering){
		"upload_route": func(o *generation.Offering) {
			o.Parameters[0].UploadedModel = &schema.UploadedModel{ProfileID: modelinput.GeneratedProfileID}
		},
		"static_profile": func(o *generation.Offering) {
			value := options(*o)
			value.ProfileID, value.ProfileRevision = modelinput.ProfileID, modelinput.ProfileRevision
			value.ProfileLimits.MaxImagePixels, value.ProfileLimits.MaxTotalImagePixels = 1<<24, 1<<25
			o.ProviderOptions = encode(value)
		},
		"no_concurrency": func(o *generation.Offering) {
			value := options(*o)
			value.MaxConcurrent = 0
			o.ProviderOptions = encode(value)
		},
		"source_above_profile": func(o *generation.Offering) { o.Parameters[0].MaxBytes++ },
		"json_output": func(o *generation.Offering) {
			o.Outputs[0].MediaTypes = []string{"model/gltf+json"}
		},
		"upload_direction_on_generated_profile": func(o *generation.Offering) {
			value := options(*o)
			value.Direction, value.MaxConcurrent = schema.ModelConversionGLBToGLTF, 0
			o.ProviderOptions = encode(value)
			o.Outputs[0].MediaTypes = []string{"model/gltf+json"}
			o.Parameters[0].UploadedModel = &schema.UploadedModel{ProfileID: modelinput.GeneratedProfileID}
		},
	} {
		offering := clone()
		mutate(&offering)
		if err := localmodel.ValidateOffering(offering); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

type blockingInput struct {
	inputMemory
	entered chan struct{}
	release chan struct{}
}

func (b *blockingInput) Open(ctx context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	b.entered <- struct{}{}
	<-b.release
	return b.inputMemory.Open(ctx, asset)
}

// max_concurrent bounds how many large sources the adapter holds at once: a
// second conversion waits for the slot (and gives up with its context).
func TestGeneratedPackageConcurrencyIsBoundedBySchema(t *testing.T) {
	offering := generatedOffering(t)
	first := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
	second := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
	input := &blockingInput{inputMemory: inputMemory{body: first}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	port, err := localmodel.New(localmodel.Config{Inputs: input, Assets: &capturingStore{}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- port.ValidateInput(context.Background(), validation(packageRequest(offering, first, "model")))
	}()
	<-input.entered
	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := port.ValidateInput(waiting, validation(packageRequest(offering, second, "model"))); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second conversion did not wait for the slot: %v", err)
	}
	if len(input.entered) != 0 {
		t.Fatal("second conversion read its source while the slot was held")
	}
	close(input.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

package localmodel_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
)

// texturedGLBWithTrailer is the #330 keychain shape in miniature: the embedded
// PNG's bufferView counts bytes after the IEND chunk (the real file has one
// space of alignment padding).
func texturedGLBWithTrailer(t *testing.T, trailer []byte) []byte {
	t.Helper()
	document, bin := modelinputtest.Document(modelinputtest.Options{Indexed: true, Textured: true})
	views := document["bufferViews"].([]any)
	image := views[len(views)-1].(map[string]any)
	if image["byteOffset"].(int)+image["byteLength"].(int) != len(bin) {
		t.Fatal("fixture image must be the last bytes of the buffer")
	}
	bin = append(bin, trailer...)
	image["byteLength"] = image["byteLength"].(int) + len(trailer)
	document["buffers"] = []any{map[string]any{"byteLength": len(bin)}}
	return modelinputtest.GLB(document, bin)
}

func realStore(t *testing.T) *local.Store {
	t.Helper()
	store, err := local.New(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// dev-test task_8fadb9f3 (note 99526): the free glTF package of the keychain
// GLB failed at once because the package's texture file kept the padding and
// the store refused it as png_invalid_structure.
func TestGeneratedPackageWithPaddedTextureIsStored330(t *testing.T) {
	offering := generatedOffering(t)
	raw := texturedGLBWithTrailer(t, []byte{' '})
	port, err := localmodel.New(localmodel.Config{Inputs: &inputMemory{body: raw}, Assets: realStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := port.Start(context.Background(), packageRequest(offering, raw, "model"))
	if err != nil {
		t.Fatalf("padded texture package refused: %v", err)
	}
	if len(observation.Artifacts) != 1 || observation.Artifacts[0].StoredAsset == nil {
		t.Fatalf("observation = %#v", observation)
	}
}

// A package the store still refuses is a failed delivery of the conversion,
// not a network failure: the Task fails as integrity_failure (its public text
// names the integrity check, not "cannot reach the provider"), and the
// internal forensics name the stage and reason for the attempt.failed log.
func TestGeneratedPackageStoreRejectionIsADiagnosedIntegrityFailure330(t *testing.T) {
	offering := generatedOffering(t)
	raw := texturedGLBWithTrailer(t, []byte("benign trailer.."))
	port, err := localmodel.New(localmodel.Config{Inputs: &inputMemory{body: raw}, Assets: realStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = port.Start(context.Background(), packageRequest(offering, raw, "model"))
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("expected a provider failure, got %v", err)
	}
	failure := provider.Failure
	if failure.Class != generation.FailureIntegrity || failure.Retryable || failure.UpstreamCode != "asset_store_rejected" {
		t.Fatalf("failure = %#v", failure)
	}
	for _, want := range []string{"role=model", "format=gltf", "stage=inspect", "reason=raster_trailing_data"} {
		if !strings.Contains(failure.UpstreamExcerpt, want) {
			t.Fatalf("forensics %q lacks %q", failure.UpstreamExcerpt, want)
		}
	}
	if bytes.Contains([]byte(failure.UpstreamExcerpt), []byte("trailer")) {
		t.Fatal("forensics must not echo content bytes")
	}
}

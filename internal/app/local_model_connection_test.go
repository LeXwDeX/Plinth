package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/localmodel"
)

func TestLocalModelRegistrationUsesRealConversionInLiveAndMock(t *testing.T) {
	for _, mode := range []string{"live", "mock"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			repo, err := sqlite.Open(filepath.Join(root, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			blobs, err := local.New(filepath.Join(root, "cas"))
			if err != nil {
				t.Fatal(err)
			}
			defer blobs.Close()
			inputs, err := inputassets.New(repo, blobs)
			if err != nil {
				t.Fatal(err)
			}
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			registry := providers.NewRegistry()
			if err := registerProviders(Config{ProviderMode: mode}, product, registry, inputs, blobs, discardLogger()); err != nil {
				t.Fatal(err)
			}
			forward, ok := product.Get("local/static-glb/to-gltf")
			if !ok {
				t.Fatal("missing route")
			}
			port, ok := registry.Provider(forward)
			if !ok {
				t.Fatal("local conversion unavailable without credentials")
			}
			if _, real := port.(*localmodel.Adapter); !real {
				t.Fatalf("%s substituted a mock for local conversion", mode)
			}
			raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true})
			upload, err := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(raw), OriginalFilename: "triangle.glb", AssetType: "model", MediaType: "model/gltf-binary", MaxBytes: 10 << 20})
			if err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(map[string]string{"source_asset": string(upload.ID), "format": "gltf"})
			result, err := port.Start(ctx, generation.StartRequest{OwnerID: "owner", TaskID: "actual-task", AttemptID: "actual-attempt", Offering: forward, Input: input, InputAssets: map[string][]generation.InputAsset{"source_asset": {upload}}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Phase != generation.UpstreamCompleted || result.Job != nil || result.Dispatch != nil || len(result.Artifacts) != 1 {
				t.Fatal("local conversion did not synchronously produce model")
			}
			opened, _, err := port.OpenAsset(ctx, generation.OpenAssetRequest{Offering: forward, Claim: result.Artifacts[0]})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			actual, err := io.ReadAll(opened)
			if err != nil || !bytes.Contains(actual, []byte("data:application/octet-stream;base64,")) {
				t.Fatal("registered local route did not produce self-contained glTF")
			}
		})
	}
}

func TestLocalModelRegistrationWithoutStorageDoesNotAdvertiseRoute(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	forward, _ := product.Get("local/static-glb/to-gltf")
	for _, mode := range []string{"live", "mock"} {
		registry := providers.NewRegistry()
		_ = registerProviders(Config{ProviderMode: mode}, product, registry, nil, nil, discardLogger())
		if _, available := registry.Provider(forward); available {
			t.Fatalf("%s advertised local conversion without storage", mode)
		}
	}
}

func TestLocalModelFactoryRejectsUnknownProtocolAndMissingStorage(t *testing.T) {
	for _, protocol := range []string{"local-model", "unknown-local-protocol"} {
		port, err := buildProviderAdapter(nil, catalog.ProviderConnectionSchema{ID: "local", Protocol: protocol, AdapterKey: "local.model"}, "", "", 0, "", nil, nil)
		if err == nil || port != nil {
			t.Fatalf("invalid factory dependency/protocol accepted: %s", protocol)
		}
	}
}

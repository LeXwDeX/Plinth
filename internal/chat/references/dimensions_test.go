package references_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type pictureInputs struct {
	fakeInputs
	bodies map[generation.AssetID][]byte
}

func (inputs *pictureInputs) OpenOwned(_ context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	asset, found := inputs.owned[id]
	if !found || asset.OwnerID != owner {
		return generation.InputAsset{}, nil, errors.New("not owned")
	}
	return asset, io.NopCloser(bytes.NewReader(inputs.bodies[id])), nil
}

// #272: the artboard snapshot's size is read from the owner's own picture, so
// the canvas layout the Agent is told about cannot be claimed by the client.
func TestImageDimensionsReadTheOwnersPictureOnly(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	inputs := &pictureInputs{
		fakeInputs: fakeInputs{owned: map[generation.AssetID]generation.InputAsset{
			"snapshot": {ID: "snapshot", OwnerID: "owner-a"},
			"text":     {ID: "text", OwnerID: "owner-a"},
		}},
		bodies: map[generation.AssetID][]byte{"snapshot": encoded.Bytes(), "text": []byte("not a picture")},
	}
	resolver, err := references.New(references.Config{
		Inputs: inputs, Tasks: &fakeTasks{}, Legacy: fakeLegacy{},
		InputAssetRoutes:    []string{"/configured/assets/{assetID}"},
		TaskAssetRoute:      "/configured/tasks/{taskID}/assets/{assetID}",
		GeneratedImageRoute: "/configured/generated/{filename}",
		UploadMaxBytes:      1024, DefaultMediaType: "image/png", DefaultAssetType: "image",
		GeneratedReferenceMaxBytes: 4096, GeneratedReferenceTooLargeTemplate: "too large {size} {limit}",
		ReferenceFilename: "reference",
		LegacyGeneratedImage: catalog.LegacyGeneratedImageRuntimeSchema{
			FilenameSeparator: "_", AssetRoles: []string{"image"},
			ExtensionMediaTypes: map[string]string{"png": "image/png"}, DefaultMediaType: "image/png",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if width, height, err := resolver.ImageDimensions(ctx, "owner-a", "snapshot"); err != nil || width != 64 || height != 48 {
		t.Fatalf("size = %dx%d, %v", width, height, err)
	}
	for name, call := range map[string][2]string{
		"another owner": {"owner-b", "snapshot"},
		"not a picture": {"owner-a", "text"},
		"unknown asset": {"owner-a", "missing"},
		"no owner":      {"", "snapshot"},
	} {
		if _, _, err := resolver.ImageDimensions(ctx, call[0], call[1]); err == nil {
			t.Fatalf("%s: measured", name)
		}
	}
}

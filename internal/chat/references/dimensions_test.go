package references_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type pictureInputs struct {
	fakeInputs
	bodies map[generation.AssetID][]byte
	opens  int
}

func (inputs *pictureInputs) OpenOwned(_ context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	asset, found := inputs.owned[id]
	if !found || asset.OwnerID != owner {
		return generation.InputAsset{}, nil, errors.New("not owned")
	}
	inputs.opens++
	return asset, io.NopCloser(bytes.NewReader(inputs.bodies[id])), nil
}

func (inputs *pictureInputs) OwnedInputAsset(_ context.Context, owner generation.OwnerID, id generation.AssetID) (generation.InputAsset, error) {
	asset, found := inputs.owned[id]
	if !found || asset.OwnerID != owner {
		return generation.InputAsset{}, errors.New("not owned")
	}
	return asset, nil
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
			"snapshot": {ID: "snapshot", OwnerID: "owner-a", SHA256: "digest-of-snapshot"},
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

	// #49: the snapshot's digest is the one recorded for the owner's asset,
	// read from its record alone: comparing a long history must not open
	// (and so re-hash) every earlier picture.
	opens := inputs.opens
	if digest, err := resolver.InputAssetDigest(ctx, "owner-a", "snapshot"); err != nil || digest != "digest-of-snapshot" {
		t.Fatalf("digest = %q, %v", digest, err)
	}
	for name, call := range map[string][2]string{
		"another owner": {"owner-b", "snapshot"},
		"unknown asset": {"owner-a", "missing"},
		"no owner":      {"", "snapshot"},
	} {
		if _, err := resolver.InputAssetDigest(ctx, call[0], call[1]); err == nil {
			t.Fatalf("%s: digest read", name)
		}
	}
	if inputs.opens != opens {
		t.Fatalf("digest comparison opened %d pictures", inputs.opens-opens)
	}
}

// pictureFormatWebP is a VP8L WebP header of a 2x3 picture: enough for the
// registered decoder to name its format and size.
func pictureFormatWebP() []byte {
	header := uint32(1) | uint32(2)<<14
	payload := []byte{0x2f, byte(header), byte(header >> 8), byte(header >> 16), byte(header >> 24)}
	var result bytes.Buffer
	result.WriteString("RIFF")
	_ = binary.Write(&result, binary.LittleEndian, uint32(4+8+len(payload)+1))
	result.WriteString("WEBP")
	result.WriteString("VP8L")
	_ = binary.Write(&result, binary.LittleEndian, uint32(len(payload)))
	result.Write(payload)
	result.WriteByte(0)
	return result.Bytes()
}

// #583: a snapshot's format is read from the owner's bytes; the stored media
// type and filename are not consulted, so a JPEG named and typed as a PNG is
// still a JPEG.
func TestPictureFormatIsSniffedFromTheOwnersBytes(t *testing.T) {
	var pngBytes, jpegBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	disguised := func(id generation.AssetID) generation.InputAsset {
		return generation.InputAsset{ID: id, OwnerID: "owner-a", MediaType: "image/png", OriginalFilename: "board.png"}
	}
	inputs := &pictureInputs{
		fakeInputs: fakeInputs{owned: map[generation.AssetID]generation.InputAsset{
			"png": disguised("png"), "jpeg": disguised("jpeg"), "webp": disguised("webp"), "text": disguised("text"),
		}},
		bodies: map[generation.AssetID][]byte{"png": pngBytes.Bytes(), "jpeg": jpegBytes.Bytes(),
			"webp": pictureFormatWebP(), "text": []byte("not a picture")},
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
	// The app installs this resolver as the chat attachment verifier, so the
	// service must find the format reader on it (a missing one refuses every
	// selection mask).
	var verifier chat.AttachmentVerifier = resolver
	if _, ok := verifier.(chat.PictureFormatReader); !ok {
		t.Fatal("the reference resolver does not read picture formats")
	}
	ctx := context.Background()
	for id, want := range map[generation.AssetID]string{"png": "png", "jpeg": "jpeg", "webp": "webp"} {
		if format, err := resolver.PictureFormat(ctx, "owner-a", string(id)); err != nil || format != want {
			t.Fatalf("%s: format = %q, %v", id, format, err)
		}
	}
	for name, call := range map[string][2]string{
		"another owner": {"owner-b", "png"},
		"not a picture": {"owner-a", "text"},
		"unknown asset": {"owner-a", "missing"},
		"no owner":      {"", "png"},
	} {
		if _, err := resolver.PictureFormat(ctx, call[0], call[1]); err == nil {
			t.Fatalf("%s: format read", name)
		}
	}
}

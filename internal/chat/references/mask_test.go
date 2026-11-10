package references_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/imaging"
)

func maskPNG(t *testing.T, width, height int, transparent image.Rectangle) []byte {
	t.Helper()
	mask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if transparent.Empty() || !image.Pt(x, y).In(transparent) {
				mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, mask); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func maskResolver(t *testing.T, pictures map[generation.AssetID][]byte) *references.Resolver {
	t.Helper()
	owned := map[generation.AssetID]generation.InputAsset{}
	for id, body := range pictures {
		owned[id] = generation.InputAsset{ID: id, OwnerID: "owner-a", Size: int64(len(body))}
	}
	inputs := &pictureInputs{fakeInputs: fakeInputs{owned: owned}, bodies: pictures}
	resolver, err := references.New(references.Config{
		Inputs: inputs, Tasks: &fakeTasks{}, Legacy: fakeLegacy{},
		InputAssetRoutes:    []string{"/configured/assets/{assetID}"},
		TaskAssetRoute:      "/configured/tasks/{taskID}/assets/{assetID}",
		GeneratedImageRoute: "/configured/generated/{filename}",
		UploadMaxBytes:      1024, DefaultMediaType: "image/png", DefaultAssetType: "image",
		GeneratedReferenceMaxBytes: 4096, GeneratedReferenceTooLargeTemplate: "too large {size} {limit}",
		ReferenceFilename:   "reference",
		SelectionMaskLimits: imageinspect.Limits{MaxBytes: 1 << 20, MaxPixels: 1_000_000},
		SelectionPreview:    imaging.SelectionPreviewStyle{MaxPixels: 1 << 20, DimOpacity: 0.5, TintColor: "#ff8a00", TintOpacity: 0.5},
		LegacyGeneratedImage: catalog.LegacyGeneratedImageRuntimeSchema{
			FilenameSeparator: "_", AssetRoles: []string{"image"},
			ExtensionMediaTypes: map[string]string{"png": "image/png"}, DefaultMediaType: "image/png",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

// #244: the mask's size, editable box and coverage come from its bytes, never
// from a client claim.
func TestMeasureMaskReadsTheOwnedMaskOnly(t *testing.T) {
	mask := maskPNG(t, 64, 48, image.Rect(8, 4, 24, 20))
	resolver := maskResolver(t, map[generation.AssetID][]byte{"mask": mask})
	measured, err := resolver.MeasureMask(context.Background(), "owner-a", "mask")
	if err != nil {
		t.Fatal(err)
	}
	if measured.Width != 64 || measured.Height != 48 || measured.BBox != [4]int{8, 4, 23, 19} {
		t.Fatalf("measured = %+v", measured)
	}
	want := 16.0 * 16.0 / (64.0 * 48.0)
	if diff := measured.Coverage - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("coverage = %v, want %v", measured.Coverage, want)
	}
	if !measured.Editable() {
		t.Fatal("mask with a transparent box is not editable")
	}
	opaque := maskPNG(t, 16, 16, image.Rectangle{})
	resolver = maskResolver(t, map[generation.AssetID][]byte{"opaque": opaque})
	if measured, err := resolver.MeasureMask(context.Background(), "owner-a", "opaque"); err != nil || measured.Editable() {
		t.Fatalf("opaque mask = %+v, %v", measured, err)
	}
	for name, call := range map[string][2]string{
		"another owner": {"owner-b", "mask"},
		"unknown asset": {"owner-a", "missing"},
		"no owner":      {"", "mask"},
	} {
		if _, err := resolver.MeasureMask(context.Background(), call[0], call[1]); err == nil {
			t.Fatalf("%s: measured", name)
		}
	}
	resolver = maskResolver(t, map[generation.AssetID][]byte{
		"text": []byte("not a picture"),
		"jpeg": jpegBytes(t),
	})
	if _, err := resolver.MeasureMask(context.Background(), "owner-a", "text"); err == nil {
		t.Fatal("text measured as a mask")
	}
	if _, err := resolver.MeasureMask(context.Background(), "owner-a", "jpeg"); err == nil {
		t.Fatal("JPEG measured as a mask")
	}
}

// #244: the preview is composed from the owned snapshot and mask.
func TestOpenSelectionPreviewComposesOwnedPictures(t *testing.T) {
	picture := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	resolver := maskResolver(t, map[generation.AssetID][]byte{
		"snapshot": encoded.Bytes(),
		"mask":     maskPNG(t, 32, 32, image.Rect(4, 4, 20, 20)),
	})
	preview, ok := resolver.OpenSelectionPreview(context.Background(), "owner-a",
		"/configured/assets/snapshot", "mask")
	if !ok || !strings.HasPrefix(preview, "data:image/png;base64,") {
		t.Fatalf("preview = %q, ok=%v", preview, ok)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(preview, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := image.Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("preview does not decode: %v", err)
	}
	for name, call := range map[string][3]string{
		"another owner":   {"owner-b", "/configured/assets/snapshot", "mask"},
		"unowned picture": {"owner-a", "https://attacker.example/p.png", "mask"},
		"missing mask":    {"owner-a", "/configured/assets/snapshot", "missing"},
	} {
		if _, ok := resolver.OpenSelectionPreview(context.Background(), call[0], call[1], call[2]); ok {
			t.Fatalf("%s: preview composed", name)
		}
	}
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, picture, nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

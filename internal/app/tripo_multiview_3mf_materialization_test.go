package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #386: a Tripo multiview task at the Offering's defaults exports every format,
// and the real 3MF it got on 2026-11-19 was refused by the archive isolation (the
// model root declares Tripo's vendor namespace), which failed the task with
// integrity_failure because converted.3mf is a required slot. The whole stack runs
// against the fake upstream, whose 3MF export now has the real file's structure
// (internal/testutil/fakeupstream/tripo3mf.go): the real Catalog, the kernel
// pinning the required slots at submission, the Tripo adapter with its three
// uploads, the multiview request and the conversions, and the asset store with its
// delivery checks. The task has to succeed and keep a converted.3mf that is that
// package, byte for byte.

// multiviewPicture is a valid image-to-3D input (a plain studio background with
// one centred subject) whose subject shade tells the three views apart.
func multiviewPicture(t *testing.T, shade uint8) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			canvas.Set(x, y, color.RGBA{R: 250, G: 250, B: 250, A: 255})
		}
	}
	for x := 20; x < 44; x++ {
		for y := 16; y < 48; y++ {
			canvas.Set(x, y, color.RGBA{R: shade, G: uint8(x * 6), B: uint8(y * 6), A: 255})
		}
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, canvas); err != nil {
		t.Fatal(err)
	}
	return picture.Bytes()
}

func TestTripoMultiviewTaskMaterializesTheRealShapedConverted3MF(t *testing.T) {
	harness, _, log, inputs := fakeStackHarnessWithInputs(t, func(origin string) map[string]string {
		return map[string]string{"tripo_base_url": origin + "/tripo/v3", "tripo_api_key": fakeUpstreamKeyPrefix + "test-key"}
	}, "/models/convert", "/generation/multiview-to-model")
	ctx := context.Background()

	// Three views, as the production default of the Offering takes them: front,
	// left and back, in the order given.
	ids := make([]string, 0, 3)
	for _, shade := range []uint8{40, 120, 200} {
		asset, err := inputs.Upload(ctx, "owner-acme", bytes.NewReader(multiviewPicture(t, shade)), "image/png", 1<<26)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, string(asset.ID))
	}
	input, err := json.Marshal(map[string]any{"reference_assets": ids, "authorize_extra_credits": true})
	if err != nil {
		t.Fatal(err)
	}
	accepted := submitStep(t, harness, generation.ImageTo3D, multiviewOfferingID, string(input), "multiview-3mf-386", nil)
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
	}

	// The upstream saw the multiview request and a conversion to each of the six
	// formats; the 3MF one is the plain {input, format} of the real run.
	if generations := log.bodies("/generation/multiview-to-model"); len(generations) != 1 || len(generations[0]["inputs"].([]any)) != 3 {
		t.Fatalf("multiview requests = %v", generations)
	}
	var threeMF []map[string]any
	for _, body := range log.bodies("/models/convert") {
		if body["format"] == "3MF" {
			threeMF = append(threeMF, body)
		}
	}
	if len(threeMF) != 1 || len(threeMF[0]) != 2 {
		t.Fatalf("3MF export requests = %v, want one {input, format}", threeMF)
	}

	// Every converted slot of the Offering's defaults is delivered, the 3MF among them.
	var delivered []string
	var converted generation.Asset
	for _, asset := range task.Assets {
		if asset.Role != "converted_model" {
			continue
		}
		delivered = append(delivered, asset.Variant)
		if asset.Variant == "converted.3mf" {
			converted = asset
		}
	}
	slices.Sort(delivered)
	if want := []string{"converted.3mf", "converted.fbx", "converted.gltf", "converted.obj", "converted.stl", "converted.usdz"}; !slices.Equal(delivered, want) {
		t.Fatalf("delivered converted assets = %v, want %v", delivered, want)
	}

	// The render the task delivers is a WebP, as the real task's was, stored as one.
	thumbnails := 0
	for _, asset := range task.Assets {
		if asset.Role == "thumbnail" && asset.Variant == "rendered_image" {
			thumbnails++
			if asset.MediaType != "image/webp" {
				t.Errorf("the render is stored as %s, want image/webp", asset.MediaType)
			}
		}
	}
	if thumbnails != 1 {
		t.Errorf("rendered_image thumbnails = %d, want 1", thumbnails)
	}

	// The stored file is the Tripo-shaped package: the namespace the isolation once
	// refused is in its model root.
	content, err := harness.module.OpenAsset(ctx, "owner-acme", task.ID, converted.ID)
	if err != nil {
		t.Fatalf("the delivered 3MF cannot be opened: %v", err)
	}
	stored, err := io.ReadAll(content.Body)
	_ = content.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	want, err := fakeupstream.ModelFile("3mf:tripo")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, want.Data) {
		t.Fatalf("the stored 3MF is %d bytes, want the fake's Tripo package of %d", len(stored), len(want.Data))
	}
	archive, err := zip.NewReader(bytes.NewReader(stored), int64(len(stored)))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range archive.File {
		if member.Name != "3D/3dmodel.model" {
			continue
		}
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		model, _ := io.ReadAll(reader)
		_ = reader.Close()
		if !bytes.Contains(model, []byte(`xmlns:customXMLNS0="https://www.tripo3d.ai/ns/aigc/1.0"`)) {
			t.Error("the stored 3MF does not declare Tripo's vendor namespace")
		}
	}
}

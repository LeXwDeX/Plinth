package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func encodedImage(t *testing.T, mediaType string, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{R: byte(x), G: byte(y), B: 90, A: 255})
		}
	}
	var buffer bytes.Buffer
	var err error
	if mediaType == "image/jpeg" {
		err = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&buffer, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// #276: a Task's derived input images are listed with their source, method,
// sizes and dimensions, only within the Task owner's assets.
func TestTaskInputDerivationsAreOwnerScoped(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	upload := func(owner generation.OwnerID, mediaType string) generation.InputAsset {
		body := encodedImage(t, mediaType, 64, 48)
		asset, err := h.inputs.Upload(ctx, owner, bytes.NewReader(body), mediaType, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return asset
	}
	derive := func(id generation.AssetID, owner generation.OwnerID, source generation.InputAsset) generation.InputAsset {
		encoded := upload(owner, "image/jpeg")
		derived := encoded
		derived.ID, derived.OriginalFilename = id, "reference.jpg"
		if err := h.repository.CreateDerivedInputAsset(ctx, derived, generation.InputAssetDerivation{
			SourceAssetID: source.ID, Method: "jpeg-q95", SourceSize: source.Size,
		}); err != nil {
			t.Fatal(err)
		}
		return derived
	}
	source := upload("owner-a", "image/png")
	derived := derive("asset_derived_own_source", "owner-a", source)
	// A derivation whose recorded source is not the owner's asset never names it.
	foreign := upload("owner-b", "image/png")
	orphan := derive("asset_derived_foreign_source", "owner-a", foreign)
	plain := upload("owner-a", "image/png")

	catalogue, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := catalogue.Get("openai/gpt-image-2/image-to-image")
	if !ok {
		t.Fatal("offering missing")
	}
	input, _ := json.Marshal(map[string]any{"prompt": "door",
		"reference_assets": []generation.AssetID{derived.ID, plain.ID, orphan.ID}})
	task := generation.Task{ID: "task-derivations", OwnerID: "owner-a", Offering: offering, Input: input}

	views, err := h.module.TaskInputDerivations(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("views = %+v, want the two derived copies", views)
	}
	first := views[0]
	if first.Parameter != "reference_assets" || first.AssetID != derived.ID || first.MediaType != "image/jpeg" ||
		first.Size != derived.Size || first.Method != "jpeg-q95" || first.SourceAssetID != source.ID ||
		first.SourceMediaType != "image/png" || first.SourceSize != source.Size || first.Width != 64 || first.Height != 48 {
		t.Fatalf("view = %+v", first)
	}
	second := views[1]
	if first.Index != 0 || views[1].Index != 2 {
		t.Fatalf("indexes = %d, %d, want 0, 2", first.Index, views[1].Index)
	}
	if second.AssetID != orphan.ID || second.SourceAssetID != "" || second.SourceMediaType != "" || second.SourceSize != foreign.Size {
		t.Fatalf("foreign source leaked: %+v", second)
	}
	encoded, _ := json.Marshal(views)
	if bytes.Contains(encoded, []byte(foreign.ID)) || bytes.Contains(encoded, []byte("storage")) {
		t.Fatalf("projection = %s", encoded)
	}

	// The same input read as another owner's Task names nothing.
	task.OwnerID = "owner-b"
	if views, err := h.module.TaskInputDerivations(ctx, task); err != nil || len(views) != 0 {
		t.Fatalf("other owner views = %+v %v", views, err)
	}
}

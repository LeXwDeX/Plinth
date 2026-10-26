package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"testing"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type derivationWire struct {
	Parameter       string `json:"parameter"`
	Index           int    `json:"index"`
	AssetID         string `json:"asset_id"`
	MediaType       string `json:"media_type"`
	Size            int64  `json:"size"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	Method          string `json:"method"`
	SourceAssetID   string `json:"source_asset_id"`
	SourceMediaType string `json:"source_media_type"`
	SourceSize      int64  `json:"source_size"`
}

// submitDerivedReferenceTask stores a JPEG copy recorded as derived from the
// owner's PNG (as input fitting does, #276) and runs an image-to-image task
// with it as the reference.
func submitDerivedReferenceTask(
	t *testing.T,
	module *generation.Module,
	inputs *inputassets.Service,
) (generation.TaskID, generation.InputAsset, generation.InputAsset) {
	t.Helper()
	ctx := context.Background()
	upload := func(mediaType string) generation.InputAsset {
		img := image.NewNRGBA(image.Rect(0, 0, 80, 60))
		for y := range 60 {
			for x := range 80 {
				img.SetNRGBA(x, y, color.NRGBA{R: byte(x * 3), G: byte(y * 4), B: 50, A: 255})
			}
		}
		var buffer bytes.Buffer
		var err error
		if mediaType == "image/jpeg" {
			err = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 95})
		} else {
			err = png.Encode(&buffer, img)
		}
		if err != nil {
			t.Fatal(err)
		}
		asset, err := inputs.UploadAsset(ctx, "owner-a", generation.InputAssetUpload{
			Body: bytes.NewReader(buffer.Bytes()), OriginalFilename: "door.png", AssetType: "image",
			MediaType: mediaType, MaxBytes: 1 << 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		return asset
	}
	source := upload("image/png")
	derived := upload("image/jpeg")
	derived.ID, derived.OriginalFilename = "asset_derived_http_reference", "door.jpg"
	value, _ := fixtureExecutions.Load(module)
	repository := value.(*fixtureExecutionState).repository
	if err := repository.CreateDerivedInputAsset(ctx, derived, generation.InputAssetDerivation{
		SourceAssetID: source.ID, Method: "jpeg-q95", SourceSize: source.Size,
	}); err != nil {
		t.Fatal(err)
	}
	accepted, err := submitHarnessTask(t, module, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.ImageToImage,
		OfferingID: "openai/gpt-image-2/image-to-image",
		Input:      json.RawMessage(`{"prompt":"fix the door","reference_assets":["` + string(derived.ID) + `"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileFixtureTask(t, module, "owner-a", accepted.TaskID)
	return accepted.TaskID, source, derived
}

func checkDerivation(t *testing.T, got []derivationWire, source, derived generation.InputAsset) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("derivations = %+v", got)
	}
	view := got[0]
	if view.Parameter != "reference_assets" || view.Index != 0 || view.AssetID != string(derived.ID) ||
		view.MediaType != "image/jpeg" || view.Size != derived.Size || view.Method != "jpeg-q95" ||
		view.SourceAssetID != string(source.ID) || view.SourceMediaType != "image/png" ||
		view.SourceSize != source.Size || view.Width != 80 || view.Height != 60 {
		t.Fatalf("derivation = %+v", view)
	}
}

// #276: the owner's task detail says which reference image was re-encoded,
// from which of their assets, how and at what size; nobody else reads it.
func TestTaskDetailShowsInputDerivationToOwnerOnly(t *testing.T) {
	t.Parallel()
	module, handler, inputs := newHTTPHarnessWithRoles(t, []string{"user"})
	taskID, source, derived := submitDerivedReferenceTask(t, module, inputs)

	own := serveScopedRequest(handler, "/api/v1/tasks/"+string(taskID), "owner-a")
	if own.Code != http.StatusOK {
		t.Fatalf("owner status = %d body = %s", own.Code, own.Body.String())
	}
	if body := own.Body.String(); strings.Contains(body, "storage_key") || strings.Contains(body, derived.StorageKey) {
		t.Fatalf("storage leaked: %s", body)
	}
	var detail struct {
		InputDerivations []derivationWire `json:"input_derivations"`
	}
	if err := json.Unmarshal(own.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	checkDerivation(t, detail.InputDerivations, source, derived)

	if other := serveScopedRequest(handler, "/api/v1/tasks/"+string(taskID), "owner-b"); other.Code != http.StatusNotFound ||
		strings.Contains(other.Body.String(), string(source.ID)) {
		t.Fatalf("other owner status = %d body = %s", other.Code, other.Body.String())
	}
}

// The administrator's generation detail explains the same derivation next to
// the raw params, which already name the derived asset.
func TestAdministrationGenerationDetailShowsInputDerivation(t *testing.T) {
	t.Parallel()
	module, handler, inputs := newHTTPHarnessWithRoles(t, []string{"admin"})
	taskID, source, derived := submitDerivedReferenceTask(t, module, inputs)

	response := serveScopedRequest(handler, "/api/admin/generations/"+string(taskID), "admin-user")
	if response.Code != http.StatusOK {
		t.Fatalf("admin status = %d body = %s", response.Code, response.Body.String())
	}
	var detail struct {
		InputDerivations []derivationWire `json:"inputDerivations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	checkDerivation(t, detail.InputDerivations, source, derived)
	if strings.Contains(response.Body.String(), derived.StorageKey) {
		t.Fatalf("storage leaked: %s", response.Body.String())
	}
}

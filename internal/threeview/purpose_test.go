package threeview

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// picturePNG encodes one picture the way a stored asset would return it.
func picturePNG(t *testing.T, width, height int, box [4]int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			if x >= box[0] && x < box[2] && y >= box[1] && y < box[3] {
				picture.SetNRGBA(x, y, color.NRGBA{R: 40, G: 90, B: 160, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// frameAssets and checkAssets are the two asset readers the frame rule and the
// consistency check use; neither touches the network or a repository.
type frameAssets struct{ picture []byte }

func (a frameAssets) OpenOwned(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	return generation.InputAsset{}, io.NopCloser(bytes.NewReader(a.picture)), nil
}
func (a frameAssets) OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error) {
	return generation.AssetContent{}, ErrConflict
}

type checkAssets struct{ pictures map[generation.AssetID][]byte }

func (a checkAssets) OpenOwned(_ context.Context, _ generation.OwnerID, asset generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	return generation.InputAsset{}, io.NopCloser(bytes.NewReader(a.pictures[asset])), nil
}
func (a checkAssets) OpenAsset(_ context.Context, _ generation.OwnerID, _ generation.TaskID,
	asset generation.AssetID,
) (generation.AssetContent, error) {
	return generation.AssetContent{Body: io.NopCloser(bytes.NewReader(a.pictures[asset]))}, nil
}

func designSheetSchema() catalog.ChatThreeViewSchema {
	return catalog.ChatThreeViewSchema{
		RecipeID: "image-three-view", DefaultPurpose: "model_input",
		Frame: catalog.ChatThreeViewFrameSchema{Enabled: true, SubjectThreshold: 32,
			MinSubjectRatio: 0.02, MarginRatio: 0.08},
		SubjectTypes: []catalog.ChatThreeViewSubjectType{
			{ID: "humanoid", FrameMinimumRatio: 0.66, FrameMaximumRatio: 1},
		},
		Purposes: []catalog.ChatThreeViewPurpose{
			{ID: "model_input", RecipeID: "image-three-view"},
			{ID: "design_sheet", RecipeID: "image-design-sheet",
				Frame: &catalog.ChatThreeViewFrameSchema{Enabled: true, SourceRatio: true}},
		},
	}
}

// A design sheet takes the source picture's own width over height and no
// subject-type bound; the shared rule still measures the subject (#409).
func TestAPurposeFrameTakesTheWholeSourcePicture(t *testing.T) {
	service := &Service{
		schema: designSheetSchema(),
		assets: frameAssets{picture: picturePNG(t, 90, 160, [4]int{20, 20, 60, 140})},
	}
	ctx := context.Background()
	source := generation.AssetID("source")
	shared, ok := service.sourceShape(ctx, "owner", source, service.schema.Frame)
	if !ok {
		t.Fatal("the shared frame could not read the source")
	}
	purpose, ok := service.sourceShape(ctx, "owner", source, service.frameForPurpose("design_sheet"))
	if !ok {
		t.Fatal("the design-sheet frame could not read the source")
	}
	if purpose != 90.0/160.0 {
		t.Fatalf("design-sheet ratio = %v, want the whole picture %v", purpose, 90.0/160.0)
	}
	if shared == purpose {
		t.Fatalf("the purpose must replace the shared subject measurement: %v", shared)
	}
	if got := service.frameForPurpose("model_input"); got.SourceRatio || got.SubjectThreshold != 32 {
		t.Fatalf("a purpose without an override keeps the shared frame: %+v", got)
	}
	if got := service.frameForPurpose("no_such_use"); got.SourceRatio {
		t.Fatalf("an unknown use keeps the shared frame: %+v", got)
	}
}

// designSheetRun is one run of the design-sheet recipe: the front view is
// accepted, the left view is being judged.
func designSheetRun() skillrun.Run {
	return skillrun.Run{RecipeID: "image-design-sheet", Steps: []skillrun.Step{
		{Spec: skillrun.StepSpec{Key: "front"}, Phase: skillrun.OutputReady, Review: skillrun.Accepted,
			OutputAssetID: "front-output"},
		{Spec: skillrun.StepSpec{Key: "left", SourceStepKey: "front", IncludeSourceAsset: true},
			Phase: skillrun.OutputReady, OutputAssetID: "left-output"},
	}}
}

// A later view is the same subject again, so it must stay inside the frame as
// the front view is. The front view already checked this (#409).
func TestALaterViewMustNotTouchTheFrame(t *testing.T) {
	service := &Service{
		schema: catalog.ChatThreeViewSchema{Consistency: catalog.ChatThreeViewConsistencySchema{
			MaxBBoxHeightDelta: 0.12,
			Reasons:            map[string]string{"edge": "the subject is cropped ({actual})"},
			ReasonSeparator:    "；",
		}},
		assets: checkAssets{pictures: map[generation.AssetID][]byte{
			"front-output": picturePNG(t, 64, 64, [4]int{10, 10, 40, 60}),
			"left-output":  picturePNG(t, 64, 64, [4]int{0, 10, 30, 60}),
		}},
	}
	run := designSheetRun()
	checks, err := service.viewChecks(context.Background(), "owner", run, run.Steps[1])
	if err != nil {
		t.Fatal(err)
	}
	edge := false
	for _, check := range checks {
		if check.Name == "edge" {
			edge = true
			if check.Status != imageinspect.StatusFail {
				t.Fatalf("a cropped later view passed the edge check: %+v", check)
			}
		}
	}
	if !edge {
		t.Fatalf("a later view has no edge check: %+v", checks)
	}
}

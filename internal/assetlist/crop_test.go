package assetlist_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// fakeCropper is the crop port of the #331 edit tests. It cuts real pixels out
// of a synthetic picture with the production geometry rules, then registers the
// crop in the shared reference map as a list_crop with provenance, which is
// what the asset-list validation reads next.
type fakeCropper struct {
	picture       *image.NRGBA
	width, height int
	minimumSide   int
	maximumPixels int64
	assets        map[generation.AssetID]assetlist.InputAssetReference
	crops         map[generation.AssetID]*image.NRGBA
	requests      []listcrop.Request
	// fail, when set, is returned before any region is checked.
	fail error
	next int
}

func (fake *fakeCropper) CropRegions(_ context.Context,
	request listcrop.Request,
) ([]listcrop.Result, error) {
	fake.requests = append(fake.requests, request)
	if fake.fail != nil {
		return nil, fake.fail
	}
	if len(request.Regions) != 1 {
		return nil, fmt.Errorf("fake crop: %d regions", len(request.Regions))
	}
	if _, found := fake.assets[request.Source]; !found {
		return nil, fmt.Errorf("%w: %s", listcrop.ErrNotFound, request.Source)
	}
	region := request.Regions[0]
	rect, err := listcrop.PixelRectForRegion(region, fake.width, fake.height)
	if err != nil {
		return nil, &listcrop.RegionFailure{Index: 0, Err: err}
	}
	if rect.Width() < fake.minimumSide || rect.Height() < fake.minimumSide {
		return nil, &listcrop.RegionFailure{Index: 0, Err: &listcrop.TooSmallError{
			Width: rect.Width(), Height: rect.Height(), MinimumSide: fake.minimumSide,
		}}
	}
	if int64(rect.Width())*int64(rect.Height()) > fake.maximumPixels {
		return nil, &listcrop.RegionFailure{Index: 0, Err: &listcrop.TooLargeError{
			Reason: "fake region is over the pixel limit",
		}}
	}
	fake.next++
	id := generation.AssetID(fmt.Sprintf("cut_%d", fake.next))
	cut := image.NewNRGBA(image.Rect(0, 0, rect.Width(), rect.Height()))
	draw.Draw(cut, cut.Bounds(), fake.picture, image.Pt(rect.X0, rect.Y0), draw.Src)
	if fake.crops == nil {
		fake.crops = map[generation.AssetID]*image.NRGBA{}
	}
	fake.crops[id] = cut
	fake.assets[id] = assetlist.InputAssetReference{
		MediaType: "image/png", IsCrop: true, CropSourceAssetID: request.Source,
	}
	return []listcrop.Result{{
		InputAssetID: id, NormalizedRegion: region, PixelRect: rect,
		SourceWidth: fake.width, SourceHeight: fake.height,
	}}, nil
}

// cropTestPicture is a 100x80 picture whose every pixel has a distinct colour,
// so a misplaced rectangle shows up in the pixel comparison.
func cropTestPicture() *image.NRGBA {
	picture := image.NewNRGBA(image.Rect(0, 0, 100, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 2), G: uint8(y * 3), B: uint8((x*7 + y*11) % 251), A: 255,
			})
		}
	}
	return picture
}

type cropFixture struct {
	service    *assetlist.Service
	repository *memoryRepository
	cropper    *fakeCropper
	assets     map[generation.AssetID]assetlist.InputAssetReference
}

func newCropFixture(t *testing.T, mutate func(*assetlist.Config)) *cropFixture {
	t.Helper()
	assets := map[generation.AssetID]assetlist.InputAssetReference{
		"scene_1":      imageReference("scene_1"),
		"scene_2":      imageReference("scene_2"),
		"input_1":      imageReference("input_1"),
		"model_1":      {MediaType: "model/gltf-binary"},
		"old_crop":     cropReference("old_crop", "scene_1"),
		"crop_a":       cropReference("crop_a", "scene_1"),
		"crop_b":       cropReference("crop_b", "scene_1"),
		"scene_2_crop": cropReference("scene_2_crop", "scene_2"),
	}
	picture := cropTestPicture()
	cropper := &fakeCropper{
		picture: picture, width: picture.Bounds().Dx(), height: picture.Bounds().Dy(),
		minimumSide: 4, maximumPixels: 4_000_000, assets: assets,
	}
	fixture := &cropFixture{assets: assets, cropper: cropper}
	fixture.service, fixture.repository, _ = newService(t, func(config *assetlist.Config) {
		config.References = memoryReferenceAssets{assets: assets}
		config.Crops = cropper
		config.Schema.Decompose = &catalog.AssetListDecomposeSchema{
			MaximumItemsPerImage: 24,
			Crop: catalog.AssetListDecomposeCropSchema{
				MinimumShortEdge: 4, MaximumRegionPixels: 4_000_000,
			},
		}
		if mutate != nil {
			mutate(config)
		}
	})
	return fixture
}

func (fixture *cropFixture) proposeForEdit(t *testing.T, title string,
	items []assetlist.ItemInput,
) assetlist.List {
	t.Helper()
	input := proposal()
	input.RequestKey = "req-crop-" + title
	input.Title = title
	input.Items = items
	stored, err := fixture.service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose %q: %v", title, err)
	}
	return stored
}

func cropInstruction(source string, region listcrop.Region) *assetlist.CropInstruction {
	return &assetlist.CropInstruction{SourceInputAssetID: source, Region: region}
}

// A new item may carry a crop instruction directly (补项): the server cuts the
// region, stores it as an input asset with provenance and appends its reference
// to that item only. An existing item re-boxed in the same edit keeps its other
// references and its previous crop reference is replaced.
func TestEditCropInstructionAddsAndReplacesReferences(t *testing.T) {
	t.Parallel()
	fixture := newCropFixture(t, nil)
	first := listcrop.Region{X: 0.1, Y: 0.2, Width: 0.4, Height: 0.5}
	second := listcrop.Region{X: 0.5, Y: 0.5, Width: 0.25, Height: 0.25}
	stored := fixture.proposeForEdit(t, "Draft", []assetlist.ItemInput{
		{Kind: "character", Name: "Hero", References: inputReferences("old_crop", "input_1")},
	})
	edited, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Edited",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero", References: inputReferences("input_1"),
				Crop: cropInstruction("scene_1", first)},
			{Kind: "image", Name: "Icon", Crop: cropInstruction("scene_1", second)},
		},
	})
	if err != nil {
		t.Fatalf("edit with crop instructions: %v", err)
	}
	if len(edited.Items) != 2 {
		t.Fatalf("items = %+v", edited.Items)
	}
	// The old crop reference is replaced; the plain picture reference stays in
	// place and the new crop is appended.
	hero := edited.Items[0].References
	if len(hero) != 2 || hero[0] != (project.Reference{InputAssetID: "input_1"}) ||
		hero[1].InputAssetID != "cut_1" {
		t.Fatalf("hero references = %+v", hero)
	}
	if icon := edited.Items[1].References; len(icon) != 1 || icon[0].InputAssetID != "cut_2" {
		t.Fatalf("icon references = %+v", icon)
	}
	if _, found := fixture.assets["old_crop"]; !found {
		t.Fatal("the replaced crop asset must stay stored")
	}
	if len(fixture.cropper.requests) != 2 ||
		fixture.cropper.requests[0].Source != "scene_1" ||
		fixture.cropper.requests[0].Regions[0] != first ||
		fixture.cropper.requests[1].Regions[0] != second {
		t.Fatalf("crop requests = %+v", fixture.cropper.requests)
	}
}

// Re-boxing an item cuts a new asset and replaces its previous crop reference;
// the old crop asset is neither deleted nor reused.
func TestEditCropInstructionReplacesOldCrop(t *testing.T) {
	t.Parallel()
	fixture := newCropFixture(t, nil)
	stored := fixture.proposeForEdit(t, "Draft", []assetlist.ItemInput{
		{Kind: "character", Name: "Hero", References: inputReferences("old_crop")},
	})
	edited, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Reboxed",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero",
				Crop: cropInstruction("scene_1", listcrop.Region{X: 0.2, Y: 0.2, Width: 0.5, Height: 0.5})},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	references := edited.Items[0].References
	if len(references) != 1 || references[0].InputAssetID != "cut_1" {
		t.Fatalf("references = %+v", references)
	}
	if _, found := fixture.assets["old_crop"]; !found {
		t.Fatal("the replaced crop asset must stay stored")
	}
}

// Merging two items is a client-side union of their normalized regions sent as
// one crop instruction. The server cuts that exact rectangle; the decoded
// pixels equal the source picture's union rectangle (真实 PNG 语义，fake 端用
// listcrop.PixelRectForRegion 换算像素矩形).
func TestEditCropUnionRegionCutsSourcePixels(t *testing.T) {
	t.Parallel()
	fixture := newCropFixture(t, nil)
	regionA := listcrop.Region{X: 0.1, Y: 0.1, Width: 0.4, Height: 0.4}
	regionB := listcrop.Region{X: 0.4, Y: 0.3, Width: 0.4, Height: 0.5}
	union := unionRegion(regionA, regionB)
	stored := fixture.proposeForEdit(t, "Merge", []assetlist.ItemInput{
		{Kind: "character", Name: "Hero", References: inputReferences("crop_a")},
		{Kind: "image", Name: "Icon", References: inputReferences("crop_b")},
	})
	// The person merges Icon into Hero: one item with the union region and the
	// other item deleted.
	edited, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Merged",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero", Crop: cropInstruction("scene_1", union)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(edited.Items) != 1 || len(edited.Items[0].References) != 1 ||
		edited.Items[0].References[0].InputAssetID != "cut_1" {
		t.Fatalf("merged items = %+v", edited.Items)
	}
	rect, err := listcrop.PixelRectForRegion(union, fixture.cropper.width, fixture.cropper.height)
	if err != nil {
		t.Fatal(err)
	}
	cut, found := fixture.cropper.crops["cut_1"]
	if !found {
		t.Fatalf("crops = %v", fixture.cropper.crops)
	}
	if cut.Bounds().Dx() != rect.Width() || cut.Bounds().Dy() != rect.Height() {
		t.Fatalf("crop size = %dx%d; want %dx%d",
			cut.Bounds().Dx(), cut.Bounds().Dy(), rect.Width(), rect.Height())
	}
	for y := 0; y < rect.Height(); y++ {
		for x := 0; x < rect.Width(); x++ {
			want := fixture.cropper.picture.NRGBAAt(rect.X0+x, rect.Y0+y)
			if got := cut.NRGBAAt(x, y); got != want {
				t.Fatalf("pixel (%d,%d) = %+v; want source (%d,%d) = %+v",
					x, y, got, rect.X0+x, rect.Y0+y, want)
			}
		}
	}
}

// unionRegion is the client-side merge rule: the bounding box of both regions.
func unionRegion(a, b listcrop.Region) listcrop.Region {
	x := math.Min(a.X, b.X)
	y := math.Min(a.Y, b.Y)
	right := math.Max(a.X+a.Width, b.X+b.Width)
	bottom := math.Max(a.Y+a.Height, b.Y+b.Height)
	return listcrop.Region{X: x, Y: y, Width: right - x, Height: bottom - y}
}

// The crop instruction reuses the tool's refusal vocabulary: an unusable
// source is a reference refusal, a malformed region is a region refusal, an
// exhausted per-source crop budget is the image item limit, and a store failure
// stays a service error.
func TestEditCropInstructionRefusals(t *testing.T) {
	t.Parallel()
	valid := listcrop.Region{X: 0.2, Y: 0.2, Width: 0.5, Height: 0.5}
	cases := []struct {
		name      string
		crop      *assetlist.CropInstruction
		mutate    func(*fakeCropper)
		want      []error
		notWanted []error
	}{
		{
			name: "missing source", crop: cropInstruction("scene_missing", valid),
			want: []error{assetlist.ErrReferenceInvalid, assetlist.ErrInvalid},
		},
		{
			name: "foreign source is missing too", crop: cropInstruction("", valid),
			want: []error{assetlist.ErrReferenceInvalid},
		},
		{
			name: "not a picture", crop: cropInstruction("model_1", valid),
			want: []error{assetlist.ErrReferenceInvalid},
		},
		{
			name: "region extends past the picture",
			crop: cropInstruction("scene_1", listcrop.Region{X: 0.8, Y: 0.1, Width: 0.5, Height: 0.2}),
			want: []error{assetlist.ErrCropRegionInvalid, assetlist.ErrInvalid},
		},
		{
			name: "region has no area",
			crop: cropInstruction("scene_1", listcrop.Region{X: 0.1, Y: 0.1, Width: 0, Height: 0.2}),
			want: []error{assetlist.ErrCropRegionInvalid},
		},
		{
			name: "region is too small",
			crop: cropInstruction("scene_1", listcrop.Region{X: 0.1, Y: 0.1, Width: 0.01, Height: 0.01}),
			want: []error{assetlist.ErrCropRegionTooSmall, assetlist.ErrInvalid},
		},
		{
			name: "source crop budget is exhausted",
			crop: cropInstruction("scene_1", valid),
			mutate: func(cropper *fakeCropper) {
				cropper.fail = &listcrop.LimitError{Limit: 24}
			},
			want:      []error{assetlist.ErrImageItemLimit, assetlist.ErrInvalid},
			notWanted: []error{assetlist.ErrReferenceInvalid},
		},
		{
			name: "crop store is unavailable",
			crop: cropInstruction("scene_1", valid),
			mutate: func(cropper *fakeCropper) {
				cropper.fail = errors.New("database is gone")
			},
			notWanted: []error{assetlist.ErrInvalid, assetlist.ErrReferenceInvalid,
				assetlist.ErrImageItemLimit, assetlist.ErrCropRegionInvalid},
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCropFixture(t, nil)
			if testCase.mutate != nil {
				testCase.mutate(fixture.cropper)
			}
			stored := fixture.proposeForEdit(t, "Draft", []assetlist.ItemInput{
				{Kind: "character", Name: "Hero"},
			})
			_, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
				Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
				ExpectedVersion: stored.Version, Title: "Refused",
				Items: []assetlist.ItemInput{{Kind: "character", Name: "Hero", Crop: testCase.crop}},
			})
			if err == nil {
				t.Fatal("edit accepted a refused crop instruction")
			}
			for _, target := range testCase.want {
				if !errors.Is(err, target) {
					t.Fatalf("error = %v; want %v", err, target)
				}
			}
			for _, target := range testCase.notWanted {
				if errors.Is(err, target) {
					t.Fatalf("error = %v; did not want %v", err, target)
				}
			}
		})
	}
}

// Crop instructions participate in the list-wide crop rules: the per-image
// budget counts them and the same-source rule spans items.
func TestEditCropInstructionsFollowListCropRules(t *testing.T) {
	t.Parallel()
	valid := listcrop.Region{X: 0.2, Y: 0.2, Width: 0.5, Height: 0.5}
	limited := newCropFixture(t, func(config *assetlist.Config) {
		config.Schema.Decompose.MaximumItemsPerImage = 1
	})
	stored := limited.proposeForEdit(t, "Limited", []assetlist.ItemInput{
		{Kind: "character", Name: "Hero"},
		{Kind: "image", Name: "Icon"},
	})
	_, err := limited.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Over the limit",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero", Crop: cropInstruction("scene_1", valid)},
			{Kind: "image", Name: "Icon", Crop: cropInstruction("scene_1", listcrop.Region{
				X: 0.2, Y: 0.7, Width: 0.5, Height: 0.25,
			})},
		},
	})
	if !errors.Is(err, assetlist.ErrImageItemLimit) || !errors.Is(err, assetlist.ErrInvalid) {
		t.Fatalf("over the limit error = %v", err)
	}

	mixed := newCropFixture(t, nil)
	stored = mixed.proposeForEdit(t, "Mixed", []assetlist.ItemInput{
		{Kind: "character", Name: "Hero"},
		{Kind: "image", Name: "Icon"},
	})
	_, err = mixed.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Cross source",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero", Crop: cropInstruction("scene_1", valid)},
			{Kind: "image", Name: "Icon", References: inputReferences("scene_2_crop")},
		},
	})
	if !errors.Is(err, assetlist.ErrReferenceInvalid) {
		t.Fatalf("cross-source error = %v", err)
	}
}

// A proposal cannot carry crop instructions: the Agent cuts with the tool and
// references the returned ids.
func TestProposalRefusesCropInstruction(t *testing.T) {
	t.Parallel()
	fixture := newCropFixture(t, nil)
	input := proposal()
	input.Items = []assetlist.ItemInput{{
		Kind: "character", Name: "Hero",
		Crop: cropInstruction("scene_1", listcrop.Region{X: 0.2, Y: 0.2, Width: 0.5, Height: 0.5}),
	}}
	_, err := fixture.service.Propose(context.Background(), input)
	if !errors.Is(err, assetlist.ErrInvalid) || errors.Is(err, assetlist.ErrReferenceInvalid) {
		t.Fatalf("Propose error = %v; want a plain invalid refusal", err)
	}
	if len(fixture.cropper.requests) != 0 {
		t.Fatalf("a proposal must not cut: %+v", fixture.cropper.requests)
	}
}

// The edit CAS and the terminal-state rule are unchanged by crop instructions.
func TestCropEditKeepsCASAndTerminalRules(t *testing.T) {
	t.Parallel()
	fixture := newCropFixture(t, nil)
	region := listcrop.Region{X: 0.2, Y: 0.2, Width: 0.5, Height: 0.5}
	stored := fixture.proposeForEdit(t, "Draft", []assetlist.ItemInput{
		{Kind: "character", Name: "Hero"},
	})
	items := []assetlist.ItemInput{{Kind: "character", Name: "Hero", Crop: cropInstruction("scene_1", region)}}
	if _, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version + 1, Title: "Stale", Items: items,
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("stale version error = %v; want ErrConflict", err)
	}
	edited, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Edited", Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Decide(context.Background(), assetlist.DecideInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: edited.ID,
		Decision: assetlist.DecisionConfirm,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: edited.ID,
		ExpectedVersion: edited.Version, Title: "Too late", Items: items,
	}); !errors.Is(err, assetlist.ErrConflict) {
		t.Fatalf("confirmed edit error = %v; want ErrConflict", err)
	}
}

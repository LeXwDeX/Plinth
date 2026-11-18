package assetlist_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// memoryReferenceAssets is the reference port of the #331 tests. It answers
// owner-a only: another owner's id is indistinguishable from a missing one.
type memoryReferenceAssets struct {
	assets map[generation.AssetID]assetlist.InputAssetReference
	err    error
}

func (port memoryReferenceAssets) ResolveInputAsset(_ context.Context,
	ownerID generation.OwnerID, id generation.AssetID,
) (assetlist.InputAssetReference, bool, error) {
	if port.err != nil {
		return assetlist.InputAssetReference{}, false, port.err
	}
	asset, found := port.assets[id]
	if !found || ownerID != "owner-a" {
		return assetlist.InputAssetReference{}, false, nil
	}
	return asset, true, nil
}

func imageReference(id generation.AssetID) assetlist.InputAssetReference {
	return assetlist.InputAssetReference{MediaType: "image/png"}
}

func cropReference(id, source generation.AssetID) assetlist.InputAssetReference {
	return assetlist.InputAssetReference{
		MediaType:         "image/png",
		IsCrop:            true,
		CropSourceAssetID: source,
	}
}

func referencePort(mutate func(*memoryReferenceAssets)) func(*assetlist.Config) {
	return func(config *assetlist.Config) {
		port := &memoryReferenceAssets{assets: map[generation.AssetID]assetlist.InputAssetReference{
			"input_1": imageReference("input_1"),
			"crop_1":  cropReference("crop_1", "scene_1"),
			"crop_2":  cropReference("crop_2", "scene_1"),
			"crop_3":  cropReference("crop_3", "scene_2"),
		}}
		if mutate != nil {
			mutate(port)
		}
		config.References = *port
	}
}

func inputReferences(ids ...string) []project.Reference {
	references := make([]project.Reference, 0, len(ids))
	for _, id := range ids {
		references = append(references, project.Reference{InputAssetID: id})
	}
	return references
}

func TestProposeStoresResolvedInputAssetAndCropReferences(t *testing.T) {
	t.Parallel()
	service, repository, _ := newService(t, referencePort(nil))
	input := proposal()
	input.Items = []assetlist.ItemInput{
		{Kind: "character", Name: "Hero", References: inputReferences("input_1", "crop_1")},
		{Kind: "image", Name: "Icon", References: inputReferences("crop_2")},
	}
	stored, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Items) != 2 {
		t.Fatalf("items = %+v", stored.Items)
	}
	// The references land untouched: a crop stays one project.Reference with
	// its input asset id, so StartItemTurn (#328) picks it up automatically.
	hero := stored.Items[0].References
	if len(hero) != 2 || hero[0] != (project.Reference{InputAssetID: "input_1"}) ||
		hero[1] != (project.Reference{InputAssetID: "crop_1"}) {
		t.Fatalf("hero references = %+v", hero)
	}
	if icon := stored.Items[1].References; len(icon) != 1 ||
		icon[0] != (project.Reference{InputAssetID: "crop_2"}) {
		t.Fatalf("icon references = %+v", icon)
	}
	if len(repository.lists) != 1 {
		t.Fatalf("stored lists = %d", len(repository.lists))
	}
}

func TestProposeRefusesInvalidReferences(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		references []project.Reference
		second     []project.Reference
		mutate     func(*memoryReferenceAssets)
	}{
		{
			name:       "missing asset",
			references: inputReferences("missing"),
		},
		{
			name: "not a picture",
			mutate: func(port *memoryReferenceAssets) {
				port.assets["input_1"] = assetlist.InputAssetReference{MediaType: "model/gltf-binary"}
			},
			references: inputReferences("input_1"),
		},
		{
			name:       "two crops in one item",
			references: inputReferences("crop_1", "crop_2"),
		},
		{
			name:       "crops from different sources",
			references: inputReferences("crop_1"),
			second:     inputReferences("crop_3"),
		},
		{
			name: "crop without provenance",
			mutate: func(port *memoryReferenceAssets) {
				port.assets["crop_1"] = assetlist.InputAssetReference{MediaType: "image/png", IsCrop: true}
			},
			references: inputReferences("crop_1"),
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service, _, _ := newService(t, referencePort(testCase.mutate))
			input := proposal()
			input.Items = []assetlist.ItemInput{
				{Kind: "character", Name: "Hero", References: testCase.references},
				{Kind: "image", Name: "Icon", References: testCase.second},
			}
			_, err := service.Propose(context.Background(), input)
			if !errors.Is(err, assetlist.ErrReferenceInvalid) || !errors.Is(err, assetlist.ErrInvalid) {
				t.Fatalf("Propose error = %v; want ErrReferenceInvalid and ErrInvalid", err)
			}
		})
	}
}

// The edit path runs the same validation: a bare references list cannot bypass
// the one-crop and same-source rules.
func TestEditValidatesBareReferences(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, referencePort(nil))
	stored, err := service.Propose(context.Background(), proposal())
	if err != nil {
		t.Fatal(err)
	}
	edit := assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Edited",
		Items: []assetlist.ItemInput{
			{Kind: "character", Name: "Hero", References: inputReferences("crop_1", "crop_2")},
		},
	}
	if _, err := service.Edit(context.Background(), edit); !errors.Is(err, assetlist.ErrReferenceInvalid) {
		t.Fatalf("Edit error = %v; want ErrReferenceInvalid", err)
	}
	edit.Items[0].References = inputReferences("input_1", "crop_1")
	edited, err := service.Edit(context.Background(), edit)
	if err != nil {
		t.Fatal(err)
	}
	if len(edited.Items[0].References) != 2 {
		t.Fatalf("edited references = %+v", edited.Items[0].References)
	}
}

// A store failure while resolving a reference is a service error, not an
// invalid reference: the person sees "unavailable", not "your list is wrong".
func TestReferencePortFailureIsNotAnInvalidRefusal(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, referencePort(func(port *memoryReferenceAssets) {
		port.err = errors.New("database is gone")
	}))
	input := proposal()
	input.Items[0].References = inputReferences("input_1")
	_, err := service.Propose(context.Background(), input)
	if err == nil {
		t.Fatal("Propose accepted a reference although the port failed")
	}
	if errors.Is(err, assetlist.ErrInvalid) || errors.Is(err, assetlist.ErrReferenceInvalid) {
		t.Fatalf("Propose error = %v; want a service error", err)
	}
}

// Without the port only the shape is checked; that is the #327 behavior a
// deployment without the port keeps.
func TestNilReferencePortKeepsShapeChecks(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, nil)
	input := proposal()
	input.Items[0].References = inputReferences("anything", "crop_1")
	stored, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("Propose error = %v", err)
	}
	if len(stored.Items[0].References) != 2 {
		t.Fatalf("references = %+v", stored.Items[0].References)
	}
}

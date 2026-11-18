package assetlist_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// referenceRouteConfig wires the reference port and the decompose section that
// declares the reference route (#331).
func referenceRouteConfig(config *assetlist.Config) {
	referencePort(nil)(config)
	config.Schema.Decompose = &catalog.AssetListDecomposeSchema{
		MaximumItemsPerImage:      24,
		DefaultRouteWithReference: "reference-to-3d",
	}
}

func routeCapabilities(item assetlist.Item) []string {
	capabilities := make([]string, 0, len(item.Route))
	for _, step := range item.Route {
		capabilities = append(capabilities, step.Capability)
	}
	return capabilities
}

// #331: an item that references a crop and omits route_id takes
// default_route_with_reference: redraw, multiview, image to 3D, remesh. The
// two 2D steps count as planned steps but not as 3D steps.
func TestProposeCropReferenceTakesTheDefaultReferenceRoute(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, referenceRouteConfig)
	input := proposal()
	input.Items = []assetlist.ItemInput{{
		Kind: "character", Name: "Knight", References: inputReferences("crop_1"),
	}}
	list, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	item := list.Items[0]
	if item.RouteID != "reference-to-3d" {
		t.Fatalf("route id = %q; want reference-to-3d", item.RouteID)
	}
	want := []string{"image-to-image", "image-to-multiview", "image-to-3d", "remesh"}
	got := routeCapabilities(item)
	if len(got) != len(want) {
		t.Fatalf("route = %v; want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("route = %v; want %v", got, want)
		}
	}
	if item.PlannedStepCount != 4 || item.PlannedModel3DStepCount != 2 {
		t.Fatalf("item plan = %d/%d; want 4/2", item.PlannedStepCount, item.PlannedModel3DStepCount)
	}
	if list.PlannedStepCount != 4 || list.PlannedModel3DStepCount != 2 {
		t.Fatalf("list plan = %d/%d; want 4/2", list.PlannedStepCount, list.PlannedModel3DStepCount)
	}
}

// An explicit route_id stays authoritative even when the item carries a crop.
func TestProposeExplicitRouteWinsOverTheReferenceRoute(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, referenceRouteConfig)
	input := proposal()
	input.Items = []assetlist.ItemInput{{
		Kind: "character", Name: "Knight", RouteID: "text-to-3d-rig",
		References: inputReferences("crop_1"),
	}}
	list, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	item := list.Items[0]
	if item.RouteID != "text-to-3d-rig" || len(item.Route) != 2 {
		t.Fatalf("route = %q (%d steps); want text-to-3d-rig (2 steps)", item.RouteID, len(item.Route))
	}
}

// A picture that is not a crop, or a Catalog without the decompose section,
// keeps the kind's default route.
func TestProposeWithoutCropKeepsTheKindDefaultRoute(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*assetlist.Config)
	}{{
		name:   "plain picture with decompose declared",
		mutate: referenceRouteConfig,
	}, {
		name:   "crop without a decompose section",
		mutate: referencePort(nil),
	}}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service, _, _ := newService(t, testCase.mutate)
			input := proposal()
			reference := "input_1"
			if testCase.name == "crop without a decompose section" {
				reference = "crop_1"
			}
			input.Items = []assetlist.ItemInput{{
				Kind: "character", Name: "Knight", References: inputReferences(reference),
			}}
			list, err := service.Propose(context.Background(), input)
			if err != nil {
				t.Fatalf("propose: %v", err)
			}
			if item := list.Items[0]; item.RouteID != "text-to-3d" || len(item.Route) != 1 {
				t.Fatalf("route = %q (%d steps); want text-to-3d (1 step)", item.RouteID, len(item.Route))
			}
		})
	}
}

// The reference route only applies to the kinds it serves; a kind outside it
// keeps its own default route.
func TestProposeCropForAnUnservedKindKeepsTheKindDefaultRoute(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, referenceRouteConfig)
	input := proposal()
	input.Items = []assetlist.ItemInput{{
		Kind: "image", Name: "Icon", References: inputReferences("crop_2"),
	}}
	list, err := service.Propose(context.Background(), input)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if item := list.Items[0]; item.RouteID != "text-to-image" {
		t.Fatalf("route id = %q; want text-to-image", item.RouteID)
	}
}

// An edit rebuilds the plan through the same rules: adding a crop without a
// route switches the item to the reference route.
func TestEditWithCropReferenceTakesTheDefaultReferenceRoute(t *testing.T) {
	t.Parallel()
	service, _, _ := newService(t, referenceRouteConfig)
	stored, err := service.Propose(context.Background(), proposal())
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	edited, err := service.Edit(context.Background(), assetlist.EditInput{
		Owner: "owner-a", ProjectID: "prj_1", ListID: stored.ID,
		ExpectedVersion: stored.Version, Title: "Edited",
		Items: []assetlist.ItemInput{{
			Kind: "character", Name: "Knight", References: inputReferences("crop_1"),
		}},
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	item := edited.Items[0]
	if item.RouteID != "reference-to-3d" || item.PlannedStepCount != 4 || item.PlannedModel3DStepCount != 2 {
		t.Fatalf("edited item = %q %d steps (%d 3D); want reference-to-3d 4 (2)",
			item.RouteID, item.PlannedStepCount, item.PlannedModel3DStepCount)
	}
}

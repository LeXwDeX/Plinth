package lineage

import (
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

func TestSplatLineagePreservesImageAndRepresentation(t *testing.T) {
	image := task("image", 0, imageOffering, input(t, map[string]any{"prompt": "source"}), imageAsset("original", "hash-image"))
	splat := task("splat", 1, modelOffering, input(t, map[string]any{"image": "original"}), generation.Asset{ID: "splat-file", Role: schema.GaussianSplatRole, Variant: schema.GaussianSplatVariant, MediaType: schema.GaussianSplatMediaType, DeliveryProfile: schema.DeliveryProfileGaussianSplat32V1})
	splat.Capability = schema.ImageToSplat
	graph, err := service(t, []generation.Task{image, splat}, nil, fakeInputs{}).Session(t.Context(), "owner-a", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 || len(graph.Nodes[1].Assets) != 1 || graph.Nodes[1].Assets[0].DeliveryProfile != schema.DeliveryProfileGaussianSplat32V1 || graph.Nodes[1].Assets[0].Role != schema.GaussianSplatRole {
		t.Fatalf("representation lost: %+v", graph.Nodes)
	}
	edges := edgeOf(graph, "splat")
	if len(edges) != 1 || edges[0].From != "image" || edges[0].AssetID != "original" || edges[0].Kind != EdgeReference {
		t.Fatalf("source image lost: %+v", edges)
	}
}

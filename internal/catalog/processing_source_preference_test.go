package catalog

import (
	"slices"
	"testing"
)

// A processing plan binds later steps with one variant order; every shipped
// processing tool must declare that same order, or a plan step could take a
// different file than the tool that planned it (#334).
func TestProcessingToolsShareOneSourcePreference(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	tools := product.Application().Chat.Tools
	shared := ProcessingSourcePreference(tools)
	if len(shared) == 0 || shared[0] != "glb" {
		t.Fatalf("processing source preference = %v, want GLB first", shared)
	}
	for _, tool := range tools {
		if tool.Processing == nil || len(tool.Processing.SourceVariantPreference) == 0 {
			continue
		}
		if !slices.Equal(tool.Processing.SourceVariantPreference, shared) {
			t.Errorf("tool %s source_variant_preference = %v, want %v", tool.Name, tool.Processing.SourceVariantPreference, shared)
		}
	}
	if got := ProcessingSourcePreference(nil); got != nil {
		t.Fatalf("no tools = %v", got)
	}
}

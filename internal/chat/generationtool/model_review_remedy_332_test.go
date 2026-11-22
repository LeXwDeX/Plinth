package generationtool

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// #332: a failed check carries the fix as a structured remedy beside the
// sentence, so an outside Agent can decide on it instead of parsing the text.
// The remedy's code is the check, its message is the hint already rendered, its
// capabilities are the ones the Schema declares for the chosen option, and its
// steps count only the capabilities the step policy counts.
func TestModelReviewUsageFailCarriesAStructuredRemedy(t *testing.T) {
	production := usageProduction(t)
	report := usageBaseReport()
	report.Triangles = 99
	// Over the 1–2 m scale bound, so the plausible_scale fix has a remedy too.
	report.BBox.Size = [3]float64{3, 0.5, 0.5}
	check := newModelReviewUsageCheckFor(report, nil, usageBaseUsage(), production, chat.ProductionBrief{}, "",
		usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true }, nil,
		[]schemacontract.Capability{"remesh", "model-split", "model-merge"})
	triangles := usageItem(t, check, catalog.ModelReviewUsageCheckTriangles)
	if len(triangles.Remedies) != 1 {
		t.Fatalf("triangles remedies = %+v, want one", triangles.Remedies)
	}
	remedy := triangles.Remedies[0]
	if remedy.Code != catalog.ModelReviewUsageCheckTriangles || remedy.Message != triangles.FixHint ||
		remedy.Steps != 1 || len(remedy.Capabilities) != 1 || remedy.Capabilities[0] != "remesh" {
		t.Fatalf("triangles remedy = %+v", remedy)
	}
	// A fix made only of local tools is a 0-step remedy, not a missing one.
	scale := usageItem(t, check, catalog.ModelReviewUsageCheckPlausibleScale)
	if scale.Status != modelReviewUsageStatusFail || len(scale.Remedies) != 1 ||
		scale.Remedies[0].Steps != 0 || scale.Remedies[0].Capabilities[0] != "resize" {
		t.Fatalf("scale remedies = %+v", scale.Remedies)
	}
}

// #332: a fix the caller cannot run yields no remedy, because the sentence then
// names something the caller has no tool for.
func TestModelReviewUsageWithoutAnExecutableFixHasNoRemedy(t *testing.T) {
	production := usageProduction(t)
	report := usageBaseReport()
	report.Triangles = 99
	check := newModelReviewUsageCheckFor(report, nil, usageBaseUsage(), production, chat.ProductionBrief{}, "",
		usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return false },
		[]generation.Capability{"retexture"}, nil)
	item := usageItem(t, check, catalog.ModelReviewUsageCheckTriangles)
	if item.FixHint == "" || len(item.Remedies) != 0 {
		t.Fatalf("triangles = %+v, want a hint and no remedy", item)
	}
}

// #332: a report that fails nothing carries no remedy anywhere.
func TestModelReviewUsagePassingReportCarriesNoRemedy(t *testing.T) {
	production := usageProduction(t)
	check := newModelReviewUsageCheckFor(usageBaseReport(), nil, usageBaseUsage(), production, chat.ProductionBrief{}, "",
		usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true }, nil,
		[]schemacontract.Capability{"remesh"})
	for _, item := range check.Checks {
		if len(item.Remedies) != 0 {
			t.Fatalf("%s remedy = %+v, want none", item.Check, item.Remedies)
		}
	}
}

// #332: the message is the hint, including the one a sized deliverable's
// export bound replaces the general fix with.
func TestModelReviewUsageRemedyMessageFollowsTheSizedExportHint(t *testing.T) {
	production := usageProduction(t)
	report := usageBaseReport()
	report.Materials = []modelinspect.MaterialReport{{Slots: []modelinspect.MaterialSlotReport{
		{Slot: "base_color", Present: true, Width: 8192, Height: 8192},
	}}}
	sized := &sizedDeliverable{Edge: 4096, Variants: []string{"glb"}}
	check := newModelReviewUsageCheckFor(report, sized, usageBaseUsage(), production, chat.ProductionBrief{}, "",
		usageIdentityText, func(catalog.ChatModelInspectFixCapabilitySchema) bool { return true }, nil,
		[]schemacontract.Capability{"format-conversion"})
	item := usageItem(t, check, catalog.ModelReviewUsageCheckTextureSize)
	if item.Status != modelReviewUsageStatusFail || len(item.Remedies) != 1 {
		t.Fatalf("texture_size = %+v", item)
	}
	if item.Remedies[0].Message != item.FixHint ||
		!strings.Contains(item.Remedies[0].Message, "导出贴图尺寸") {
		t.Fatalf("texture_size remedy = %+v, hint = %q", item.Remedies[0], item.FixHint)
	}
}

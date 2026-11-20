package budget_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #417: the plan estimate is the quote a step freezes when it names no
// parameters — the base amount plus the components its defaults already pay
// for. Quoting the bare base amount budgeted 20 Meshy credits for a step that
// actually froze 40.
func TestEstimateOfferingQuotesTheComponentsItsDefaultsPayFor(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !ok {
		t.Fatal("missing Meshy multiview Offering")
	}
	quote, err := offering.DefaultNativeQuote(nil)
	if err != nil || quote == nil || quote.BaseAmount != "20" || quote.ExtraAmount != "20" || quote.TotalAmount != "40" {
		t.Fatalf("default quote = %+v err=%v, want 20 + 20 = 40", quote, err)
	}
	want := budget.Amount{Unit: "meshy.credit", UnitLabel: "Meshy credits", Micros: 40000000}
	if got := budget.EstimateOffering(offering); got != want {
		t.Fatalf("plan estimate = %+v, want %+v", got, want)
	}
	// The plan quote is the same amount a prepared input freezes from the same
	// default values, so an approved limit covers the step's reservation.
	if frozen := budget.QuoteAmount(quote, 0); frozen != want {
		t.Fatalf("frozen quote amount = %+v, want the plan estimate %+v", frozen, want)
	}
}

// A component whose condition does not hold at the Offering's defaults is not
// part of the estimate, and the fixed values of an Offering count exactly as
// its defaults do.
func TestEstimateOfferingCountsOnlyTheComponentsItsDefaultsSatisfy(t *testing.T) {
	// textured builds the same Offering with its texture default turned on or
	// off; texture_resolution is a fixed parameter, so its declared default is
	// the value the step runs with.
	textured := func(defaultTexture bool) schema.Offering {
		return schema.Offering{
			ID: "test/multiview/image-to-3d", CreditsMetered: true,
			NativePricing: &schema.NativePricing{
				Version: 1, Unit: "test.credit", UnitLabel: "Test credits", BaseAmount: "20",
				Components: []schema.NativePriceComponent{
					{ID: "texture", Label: "Texture", Amount: "10",
						When: &schema.ParameterCondition{Parameter: "should_texture", Operator: "equals", Value: json.RawMessage(`true`)}},
					{ID: "texture_8k", Label: "8K texture", Amount: "5",
						When: &schema.ParameterCondition{Parameter: "texture_resolution", Operator: "equals", Value: json.RawMessage(`"8k"`)}},
				},
				ConsentParameter: "authorize_extra_credits", MaxAuthorizedExtraAmount: "15",
			},
			Parameters: []schema.Parameter{
				{Name: "should_texture", Type: "boolean", Default: json.RawMessage(`true`)},
				{Name: "texture_resolution", Type: "select", Fixed: true, Default: json.RawMessage(`"8k"`),
					ForcedWhen: []schema.ForcedValue{{When: schema.ParameterCondition{
						Parameter: "should_texture", Operator: "equals", Value: json.RawMessage(`false`)},
						Value: json.RawMessage(`"2k"`)}}},
				{Name: "authorize_extra_credits", Type: "boolean", Default: json.RawMessage(`false`)},
			},
		}
	}
	on := textured(true)
	// Both the default and the fixed value satisfy their components: 20+10+5.
	if got := budget.EstimateOffering(on); got.Micros != 35000000 {
		t.Fatalf("default estimate = %+v, want 35", got)
	}
	off := textured(false)
	for index := range off.Parameters {
		if off.Parameters[index].Name == "should_texture" {
			off.Parameters[index].Default = json.RawMessage(`false`)
		}
	}
	// Without texture the forced value is 2k, so neither texture component is
	// charged: 20.
	if got := budget.EstimateOffering(off); got.Micros != 20000000 {
		t.Fatalf("no-texture estimate = %+v, want 20", got)
	}
}

// An Offering priced in legacy credits keeps quoting its historical integer
// estimate; only native Offerings take the default native quote.
func TestEstimateOfferingKeepsTheLegacyPath(t *testing.T) {
	offering := schema.Offering{ID: "legacy/offering", EstimatedCredits: 12}
	if got := budget.EstimateOffering(offering); got != budget.Legacy(12) {
		t.Fatalf("legacy estimate = %+v", got)
	}
	// A legacy Offering with a fixed extra keeps its declared extras (#120).
	fixed := schema.Offering{ID: "legacy/extra", EstimatedCredits: 12,
		Cost: &schema.CostDeclaration{ExtraCredits: 5, ExtraWhen: []schema.ParameterCondition{
			{Parameter: "quad", Operator: "truthy"}}},
		Parameters: []schema.Parameter{{Name: "quad", Type: "boolean", Fixed: true, Default: json.RawMessage(`true`)}}}
	if got := budget.EstimateOffering(fixed); got != budget.Legacy(17) {
		t.Fatalf("legacy fixed-extra estimate = %+v", got)
	}
}

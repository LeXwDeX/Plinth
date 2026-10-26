package schema

import (
	"encoding/json"
	"testing"
)

func TestEstimateTotalCreditsIncludesExtraCreditsOfTheChosenOptions(t *testing.T) {
	offering := Offering{EstimatedCredits: 5, Cost: &CostDeclaration{BaseCredits: 5, ExtraCredits: 5,
		ExtraWhen: []ParameterCondition{{Parameter: "texture_format", Operator: "present"}}}}
	for input, want := range map[string]int{
		`{"format":"FBX"}`:                        5,
		`{"format":"FBX","texture_format":"PNG"}`: 10,
		``:         5,
		`not json`: 5, // undecodable input never invents an amount
	} {
		if got := offering.EstimateTotalCredits(json.RawMessage(input)); got != want {
			t.Errorf("EstimateTotalCredits(%q) = %d; want %d", input, got, want)
		}
	}
	plain := Offering{EstimatedCredits: 3}
	if plain.EstimateTotalCredits(json.RawMessage(`{"a":1}`)) != 3 {
		t.Fatal("an Offering without a cost declaration must keep its base credits")
	}
}

func TestEvaluateExtraCreditsSelectionFormCountsEachSelectedItem(t *testing.T) {
	cost := CostDeclaration{BaseCredits: 1, ExtraCredits: 2, SelectionParameter: "target_formats"}
	got, err := EvaluateExtraCredits(cost, json.RawMessage(`{"target_formats":["glb","fbx"]}`))
	if err != nil || got != 4 {
		t.Fatalf("selection extra = %d, %v", got, err)
	}
	if _, err := EvaluateExtraCredits(cost, json.RawMessage(`[1]`)); err == nil {
		t.Fatal("a non-object selection must fail closed")
	}
}

func TestMaximumEstimatedCreditsIsDerivedAndZeroWithoutBilledChoices(t *testing.T) {
	if (Offering{EstimatedCredits: 3}).MaximumEstimatedCredits() != 0 {
		t.Fatal("an Offering without billed choices has no separate maximum")
	}
	scalar := Offering{EstimatedCredits: 5, Cost: &CostDeclaration{BaseCredits: 5, ExtraCredits: 5}}
	if scalar.MaximumEstimatedCredits() != 10 {
		t.Fatalf("scalar maximum = %d", scalar.MaximumEstimatedCredits())
	}
	multi := Offering{EstimatedCredits: 1, Cost: &CostDeclaration{BaseCredits: 1, ExtraCredits: 2, SelectionParameter: "target_formats"},
		Parameters: []Parameter{{Name: "target_formats", Options: []ParameterOption{{Value: "a"}, {Value: "b"}, {Value: "c"}}}}}
	if multi.MaximumEstimatedCredits() != 7 {
		t.Fatalf("selection maximum = %d", multi.MaximumEstimatedCredits())
	}
}

func TestExtraCreditDetailNamesWhatTheExtraCreditsPayFor(t *testing.T) {
	selection := Offering{EstimatedCredits: 20,
		Cost: &CostDeclaration{ExtraCredits: 5, SelectionParameter: "output_formats", ConsentParameter: "ok", ExtraLabel: "附加导出格式"},
		Parameters: []Parameter{{Name: "output_formats", Label: "formats", Options: []ParameterOption{
			{Value: "FBX", Label: "FBX 文件"}, {Value: "USDZ"}}}}}
	detail := selection.ExtraCreditDetail(json.RawMessage(`{"output_formats":["FBX","USDZ"]}`))
	if detail.Credits != 10 || detail.Label != "附加导出格式" || len(detail.Items) != 2 || detail.Items[0] != "FBX 文件" || detail.Items[1] != "USDZ" {
		t.Fatalf("selection detail = %+v", detail)
	}
	// Without a declared label the billed parameter's own label stands in.
	selection.Cost.ExtraLabel = ""
	if got := selection.ExtraCreditDetail(json.RawMessage(`{"output_formats":["FBX"]}`)); got.Label != "formats" || got.Credits != 5 {
		t.Fatalf("fallback label = %+v", got)
	}
	if got := selection.ExtraCreditDetail(json.RawMessage(`{"output_formats":[]}`)); got.Credits != 0 || got.Items != nil {
		t.Fatalf("no formats chosen = %+v", got)
	}

	scalar := Offering{EstimatedCredits: 5, Cost: &CostDeclaration{ExtraCredits: 5, ExtraLabel: "附加转换选项", ConsentParameter: "ok",
		ExtraWhen: []ParameterCondition{{Parameter: "quad", Operator: "truthy"}, {Parameter: "texture_format", Operator: "present"}}},
		Parameters: []Parameter{{Name: "quad", Label: "四边面"}, {Name: "texture_format", Label: "纹理格式"}}}
	detail = scalar.ExtraCreditDetail(json.RawMessage(`{"quad":true,"texture_format":"PNG"}`))
	if detail.Credits != 5 || detail.Label != "附加转换选项" || len(detail.Items) != 2 || detail.Items[0] != "四边面" || detail.Items[1] != "纹理格式" {
		t.Fatalf("scalar detail = %+v", detail)
	}
	if got := scalar.ExtraCreditDetail(json.RawMessage(`{"quad":false}`)); got.Credits != 0 {
		t.Fatalf("nothing billed = %+v", got)
	}
	// An undecodable input or an Offering without extra pricing yields nothing.
	if got := scalar.ExtraCreditDetail(json.RawMessage(`[`)); got.Credits != 0 {
		t.Fatalf("undecodable input = %+v", got)
	}
	if got := (Offering{EstimatedCredits: 3}).ExtraCreditDetail(json.RawMessage(`{}`)); got.Credits != 0 {
		t.Fatalf("plain offering = %+v", got)
	}
}

// #324: a billed selection the Schema fixes (Tripo's complete export delivery) is not a choice,
// so the quoted price already contains its extra credits and the quote is a single number.
func TestQuotedCreditsIncludeTheExtrasOfFixedValues(t *testing.T) {
	fixed := Offering{EstimatedCredits: 40,
		Cost: &CostDeclaration{BaseCredits: 40, ExtraCredits: 10, SelectionParameter: "output_formats", ConsentParameter: "ok"},
		Parameters: []Parameter{{Name: "output_formats", Fixed: true, Default: json.RawMessage(`["FBX","OBJ","STL","USDZ","3MF"]`),
			Options: []ParameterOption{{Value: "FBX"}, {Value: "OBJ"}, {Value: "STL"}, {Value: "USDZ"}, {Value: "3MF"}}}}}
	if got := fixed.QuotedCredits(); got != 90 {
		t.Fatalf("fixed selection quoted = %d; want 90", got)
	}
	if got := fixed.MaximumEstimatedCredits(); got != 90 {
		t.Fatalf("fixed selection maximum = %d; want the same 90", got)
	}
	optional := fixed
	optional.Parameters = []Parameter{{Name: "output_formats", Options: fixed.Parameters[0].Options}}
	if got := optional.QuotedCredits(); got != 40 {
		t.Fatalf("optional selection quoted = %d; want the base 40", got)
	}
	if (Offering{EstimatedCredits: 7}).QuotedCredits() != 7 {
		t.Fatal("an Offering without a cost declaration is quoted at its base")
	}
}

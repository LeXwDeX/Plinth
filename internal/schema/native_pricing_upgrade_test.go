package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// #278: a Tripo conversion costs the basic price until an export option leaves
// its upstream default, then every chosen conversion costs the advanced price. A
// component cannot say that (its amount does not grow with the number of chosen
// items), so a selection can declare an upgrade: a dearer per-item amount while
// any of its conditions holds.
func upgradeFixture() Offering {
	return Offering{
		NativePricing: &NativePricing{
			Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits", BaseAmount: "20",
			Selection: &NativePriceSelection{
				ID: "conversions", Label: "Conversions", Amount: "5", Parameter: "formats", MaxItems: 3,
				Upgrade: &NativePriceUpgrade{Amount: "10", WhenAny: []ParameterCondition{
					{Parameter: "texture", Operator: "not-equals", Value: json.RawMessage(`"JPEG"`)},
					{Parameter: "pivot", Operator: "truthy"},
				}},
			},
			ConsentParameter: "consent", MaxAuthorizedExtraAmount: "30",
		},
		Parameters: []Parameter{{Name: "formats", Type: "string-array", MinItems: 1, MaxItems: 3,
			Options: []ParameterOption{{Value: "A"}, {Value: "B"}, {Value: "C"}}}},
	}
}

func TestSelectionUpgradePricesEveryChosenItemOnceAnyConditionHolds(t *testing.T) {
	for name, test := range map[string]struct {
		input string
		extra string
		total string
	}{
		"no condition holds":     {`{"formats":["A","B"],"texture":"JPEG","pivot":false}`, "10", "30"},
		"one item":               {`{"formats":["A"],"texture":"JPEG","pivot":false}`, "5", "25"},
		"the first condition":    {`{"formats":["A","B"],"texture":"PNG","pivot":false}`, "20", "40"},
		"the second condition":   {`{"formats":["A","B"],"texture":"JPEG","pivot":true}`, "20", "40"},
		"both conditions":        {`{"formats":["A","B"],"texture":"PNG","pivot":true}`, "20", "40"},
		"three items upgraded":   {`{"formats":["A","B","C"],"texture":"PNG","pivot":false}`, "30", "50"},
		"three items not":        {`{"formats":["A","B","C"],"texture":"JPEG","pivot":false}`, "15", "35"},
		"an extra, unused input": {`{"formats":["A","B"],"texture":"JPEG","pivot":false,"other":1}`, "10", "30"},
	} {
		t.Run(name, func(t *testing.T) {
			quote, err := upgradeFixture().EvaluateNativeQuote(json.RawMessage(test.input))
			if err != nil || quote.ExtraAmount != test.extra || quote.TotalAmount != test.total || len(quote.Components) != 1 {
				t.Fatalf("quote = %+v err %v, want extra %s total %s in one charge", quote, err, test.extra, test.total)
			}
			if err := quote.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSelectionUpgradeNeverPricesTheBaseAmountForAMissingInput(t *testing.T) {
	// A condition input that is not there must not silently fall back to the plain
	// amount, even when an earlier condition already holds.
	for _, input := range []string{
		`{"formats":["A"],"pivot":false}`,
		`{"formats":["A"],"texture":"PNG"}`,
	} {
		if _, err := upgradeFixture().EvaluateNativeQuote(json.RawMessage(input)); err == nil {
			t.Fatalf("priced %s without every upgrade input", input)
		}
	}
}

func TestSelectionUpgradeUpperQuotePricesTheDearestCase(t *testing.T) {
	quote, err := upgradeFixture().UpperNativeQuote()
	if err != nil || quote.TotalAmount != "50" || quote.ExtraAmount != "30" {
		t.Fatalf("upper quote = %+v err %v, want base 20 plus three advanced conversions", quote, err)
	}
	// The pricing declaration itself is not touched by the projection.
	if upgradeOffering := upgradeFixture(); upgradeOffering.NativePricing.Selection.Upgrade == nil {
		t.Fatal("fixture lost its upgrade")
	}
}

func TestSelectionUpgradeDeclarationIsBounded(t *testing.T) {
	mutate := func(change func(selection *NativePriceSelection, pricing *NativePricing)) error {
		offering := upgradeFixture()
		change(offering.NativePricing.Selection, offering.NativePricing)
		return offering.NativePricing.Validate()
	}
	if err := mutate(func(*NativePriceSelection, *NativePricing) {}); err != nil {
		t.Fatalf("a valid upgrade was refused: %v", err)
	}
	for name, change := range map[string]func(*NativePriceSelection, *NativePricing){
		"an upgrade that is not dearer":      func(s *NativePriceSelection, _ *NativePricing) { s.Upgrade.Amount = "5" },
		"a cheaper upgrade":                  func(s *NativePriceSelection, _ *NativePricing) { s.Upgrade.Amount = "4" },
		"an unparsable amount":               func(s *NativePriceSelection, _ *NativePricing) { s.Upgrade.Amount = "ten" },
		"no conditions":                      func(s *NativePriceSelection, _ *NativePricing) { s.Upgrade.WhenAny = nil },
		"a condition without a parameter":    func(s *NativePriceSelection, _ *NativePricing) { s.Upgrade.WhenAny[0].Parameter = "" },
		"a condition with an unknown test":   func(s *NativePriceSelection, _ *NativePricing) { s.Upgrade.WhenAny[0].Operator = "maybe" },
		"the consent bound misses the upper": func(_ *NativePriceSelection, p *NativePricing) { p.MaxAuthorizedExtraAmount = "29" },
		"too many conditions": func(s *NativePriceSelection, _ *NativePricing) {
			for len(s.Upgrade.WhenAny) <= maxNativeUpgradeConditions {
				s.Upgrade.WhenAny = append(s.Upgrade.WhenAny, ParameterCondition{Parameter: "pivot", Operator: "truthy"})
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := mutate(change); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if err := mutate(func(_ *NativePriceSelection, p *NativePricing) { p.MaxAuthorizedExtraAmount = "30" }); err != nil {
		t.Fatalf("the exact consent bound was refused: %v", err)
	}
}

// The declaration is plain JSON in the Schema: it round-trips and a selection
// without an upgrade keeps pricing flat.
func TestSelectionUpgradeIsPlainSchemaJSON(t *testing.T) {
	encoded, err := json.Marshal(upgradeFixture().NativePricing)
	if err != nil || !strings.Contains(string(encoded), `"upgrade":{"amount":"10","when_any":[`) {
		t.Fatalf("encoded = %s err %v", encoded, err)
	}
	var decoded NativePricing
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Selection.Upgrade == nil || len(decoded.Selection.Upgrade.WhenAny) != 2 {
		t.Fatalf("decoded = %+v err %v", decoded, err)
	}
	flat := upgradeFixture()
	flat.NativePricing.Selection.Upgrade = nil
	quote, err := flat.EvaluateNativeQuote(json.RawMessage(`{"formats":["A","B"]}`))
	if err != nil || quote.TotalAmount != "30" {
		t.Fatalf("flat selection = %+v err %v", quote, err)
	}
	if strings.Contains(mustEncode(t, flat.NativePricing), "upgrade") {
		t.Fatal("an absent upgrade was encoded")
	}
}

func mustEncode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativePublicPricesNeverAdvertiseLegacyZero(t *testing.T) {
	t.Parallel()
	q := &schema.NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0.5"}
	for name, obj := range map[string]any{"offering": offeringResponse{PriceMode: "native", DefaultNativeQuote: q, UpperNativeQuote: q}, "pending": pendingapproval.Item{NativeQuote: q}, "processing": processing.Step{NativeQuote: q}, "conversion": modeldelivery.Conversion{NativeQuote: q}, "coverage": modeldelivery.Coverage{PriceTotals: []schema.NativeAmount{{Unit: q.Unit, UnitLabel: q.UnitLabel, Amount: q.TotalAmount}}}, "harness": harnessView{PriceMode: "native", NativeQuote: q}, "account": accountUsageResponse{PriceTotals: []generation.NativeAmount{{Unit: q.Unit, UnitLabel: q.UnitLabel, Amount: q.TotalAmount}}}} {
		b, e := json.Marshal(obj)
		if e != nil {
			t.Fatal(e)
		}
		for _, forbidden := range []string{`"estimated_credits":`, `"credits_charged":`, `"base_credits":`, `"extra_credits":`, `"completion_credits":`} {
			if strings.Contains(string(b), forbidden) {
				t.Fatalf("%s misleading zero %s", name, b)
			}
		}
		if !strings.Contains(string(b), `"0.5"`) {
			t.Fatalf("%s lost fraction %s", name, b)
		}
	}
	for name, obj := range map[string]any{"pending": pendingapproval.Item{}, "processing": processing.Step{}, "account": accountUsageResponse{}, "harness": harnessView{}} {
		b, e := json.Marshal(obj)
		if e != nil || !strings.Contains(string(b), `"estimated_credits":0`) {
			t.Fatalf("%s legacy zero omitted %s %v", name, b, e)
		}
	}
	task := generation.Task{State: generation.TaskSucceeded, Offering: generation.Offering{CreditsMetered: true, NativePricing: &schema.NativePricing{}}, NativeQuote: q}
	if publicCreditsCharged(task) != nil {
		t.Fatal("native estimated charge projected as integer")
	}
	task.NativeQuote = nil
	if publicCreditsCharged(task) != nil {
		t.Fatal("missing quote projected as free")
	}
}
func TestNativeDefaultUpperAndFrozenQuotesStayDistinct(t *testing.T) {
	t.Parallel()
	o := generation.Offering{NativePricing: &schema.NativePricing{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", Components: []schema.NativePriceComponent{{ID: "texture", Label: "Texture", Amount: "2", When: &schema.ParameterCondition{Parameter: "quality", Operator: "equals", Value: json.RawMessage(`"high"`)}}}, ConsentParameter: "consent", MaxAuthorizedExtraAmount: "2"}, Parameters: []schema.Parameter{{Name: "quality", Type: "text", Default: json.RawMessage(`"low"`)}}}
	if defaultNativePrice(o).TotalAmount != "0.5" || upperNativePrice(o).TotalAmount != "2.5" {
		t.Fatal("preview price distinction lost")
	}
	o.NativePricing.BaseAmount = "invalid"
	if nativePriceMode(o) != "native" || defaultNativePrice(o) != nil || upperNativePrice(o) != nil {
		t.Fatal("bad price became legacy zero")
	}
}

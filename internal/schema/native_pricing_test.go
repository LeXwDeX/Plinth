package schema

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func nativeFixture() Offering {
	return Offering{NativePricing: &NativePricing{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", Components: []NativePriceComponent{{ID: "geometry", Label: "Geometry", Amount: "0.5"}, {ID: "texture", Label: "Texture", Amount: "2", When: &ParameterCondition{Parameter: "quality", Operator: "equals", Value: json.RawMessage(`"extreme"`)}}}, ConsentParameter: "consent", MaxAuthorizedExtraAmount: "2.5"}}
}
func TestNativeDecimalIsExactAndBounded(t *testing.T) {
	for _, s := range []string{"-1", "+1", "01", " 1", "1e3", "NaN", ".5", "1.", "0.0000001", "9223372036855"} {
		if _, err := ParseNativeAmount(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	a, _ := ParseNativeAmount("0.1")
	b, _ := ParseNativeAmount("0.2")
	if FormatNativeAmount(a+b) != "0.3" {
		t.Fatal("decimal drift")
	}
	if FormatNativeAmount(500000) != "0.5" {
		t.Fatal("rounded native price")
	}
	if _, err := AddNativeAmounts(math.MaxInt64, 1); err == nil {
		t.Fatal("overflow")
	}
}
func TestNativeComponentsFreezeActualPrice(t *testing.T) {
	o := nativeFixture()
	q, err := o.EvaluateNativeQuote(json.RawMessage(`{"quality":"extreme"}`))
	if err != nil || q.TotalAmount != "3" || q.ExtraAmount != "2.5" || len(q.Components) != 2 {
		t.Fatalf("%+v %v", q, err)
	}
	if err = q.Validate(); err != nil {
		t.Fatal(err)
	}
	q.TotalAmount = "2.5"
	if q.Validate() == nil {
		t.Fatal("inconsistent total")
	}
	q, err = o.EvaluateNativeQuote(json.RawMessage(`{"quality":"standard"}`))
	if err != nil || q.TotalAmount != "1" || len(q.Components) != 1 {
		t.Fatalf("%+v %v", q, err)
	}
	if _, err = o.EvaluateNativeQuote(json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown conditional price became base")
	}
	o.NativePricing.Components[1].ID = "geometry"
	if _, err = o.EvaluateNativeQuote(json.RawMessage(`{"quality":"extreme"}`)); err == nil {
		t.Fatal("duplicate charge")
	}
}
func TestNativeTotalsNeverMixUnitsOrSkipBadQuote(t *testing.T) {
	o := nativeFixture()
	q, _ := o.EvaluateNativeQuote(json.RawMessage(`{"quality":"extreme"}`))
	other := *q
	other.Unit = "other.credit"
	other.UnitLabel = "Other credits"
	totals, err := SumNativeQuotes([]*NativeQuote{q, &other, q})
	if err != nil || len(totals) != 2 || totals[0].Amount != "6" || totals[1].Amount != "3" {
		t.Fatalf("%+v %v", totals, err)
	}
	q.TotalAmount = "bad"
	if _, err = SumNativeQuotes([]*NativeQuote{q, &other}); err == nil {
		t.Fatal("bad quote silently skipped")
	}
}

func TestNativeProjectionLegacyZeroAndAggregateOverflow(t *testing.T) {
	type wire struct {
		Credits int          `json:"estimated_credits"`
		Quote   *NativeQuote `json:"native_quote,omitempty"`
	}
	legacy, e := MarshalPriceProjection(wire{}, false, "estimated_credits")
	if e != nil || string(legacy) != `{"estimated_credits":0}` {
		t.Fatalf("legacy zero drift %s %v", legacy, e)
	}
	q := &NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0.5"}
	native, e := MarshalPriceProjection(wire{Quote: q}, true, "estimated_credits")
	if e != nil || strings.Contains(string(native), "estimated_credits") {
		t.Fatal("native advertised legacy zero")
	}
	var a PriceAccumulator
	large := *q
	large.BaseAmount = "1000000000"
	large.TotalAmount = large.BaseAmount
	overflow := false
	for i := 0; i < 10000; i++ {
		if e := a.Add(0, &large); e != nil {
			overflow = true
			break
		}
	}
	if !overflow {
		t.Fatal("aggregate overflow")
	}
	var b PriceAccumulator
	if e := b.Add(math.MaxInt, nil); e != nil {
		t.Fatal(e)
	}
	if e := b.Add(1, nil); e == nil {
		t.Fatal("legacy aggregate overflow")
	}
	if e := b.Add(0, &NativeQuote{}); e == nil {
		t.Fatal("bad quote silently skipped")
	}
}

func TestNativeDefaultQuoteUsesSharedForcedRules(t *testing.T) {
	o := nativeFixture()
	o.Parameters = []Parameter{{Name: "quality", Type: "text", Default: json.RawMessage(`"standard"`), ForcedWhen: []ForcedValue{{When: ParameterCondition{Parameter: "premium", Operator: "truthy"}, Value: json.RawMessage(`"extreme"`)}}}, {Name: "premium", Type: "boolean", Default: json.RawMessage(`true`)}}
	q, e := o.DefaultNativeQuote(nil)
	if e != nil || q.TotalAmount != "3" {
		t.Fatalf("forced default price %+v %v", q, e)
	}
}

func TestNativeMixedAggregationRejectsDoubleModeAndUnrepresentableLegacy(t *testing.T) {
	q := &NativeQuote{Version: 1, Unit: "provider.credit", UnitLabel: "Provider credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0.5"}
	var a PriceAccumulator
	if a.Add(7, q) == nil {
		t.Fatal("native/legacy ambiguous estimate silently dropped legacy")
	}
	if e := a.Add(math.MaxInt, nil); e != nil {
		t.Fatal(e)
	}
	if totals, e := a.Totals(); e != nil || totals != nil {
		t.Fatal("pure legacy contract changed")
	}
	if e := a.Add(0, q); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Totals(); e == nil {
		t.Fatal("mixed legacy amount outside decimal aggregate bound")
	}
}

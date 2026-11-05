package generation

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

// #139: the recommendation of a parameter with maximum_when is the default
// normalization gives the chosen combination, so a converged default needs
// no reason while a value below it still does.
func TestAdvisedRecommendationFollowsTheConditionalRange(t *testing.T) {
	maximum := 2000000.0
	presentation := &schema.ParameterPresentation{Surface: schema.SurfaceReadOnly}
	o := Offering{Parameters: []Parameter{
		{Name: "face_limit", Label: "面数上限", Type: "number", Default: json.RawMessage(`2000000`), Maximum: &maximum,
			AdjustmentPolicy: "advised", AdjustmentReason: "默认最高", Presentation: presentation,
			MaximumWhen: []schema.ConditionalMaximum{{When: schema.ParameterCondition{Parameter: "smart_low_poly", Operator: "truthy"}, Maximum: 20000}}},
		{Name: "smart_low_poly", Label: "智能低模", Type: "boolean", Default: json.RawMessage(`false`),
			AdjustmentPolicy: "advised", AdjustmentReason: "默认关闭", Presentation: presentation},
	}}
	reasons := map[string]string{"smart_low_poly": "低模用途"}
	got, err := freezeParameterAdjustments(o, json.RawMessage(`{"face_limit":20000,"smart_low_poly":true}`), reasons)
	if err != nil || len(got) != 1 || got[0].Name != "smart_low_poly" {
		t.Fatalf("converged face_limit needed a reason: %+v %v", got, err)
	}
	if _, err := freezeParameterAdjustments(o, json.RawMessage(`{"face_limit":5000,"smart_low_poly":true}`), reasons); err == nil {
		t.Fatal("a face_limit below the converged default was frozen without a reason")
	}
}

func TestAdvisedAdjustmentFreezesActualValueAndReason(t *testing.T) {
	o := Offering{Parameters: []Parameter{{Name: "quality", Label: "质量", Type: "select", Default: json.RawMessage(`"high"`), AdjustmentPolicy: "advised", AdjustmentReason: "默认最高质量", Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceReadOnly}}}}
	input := json.RawMessage(`{"quality":"low"}`)
	if _, err := freezeParameterAdjustments(o, input, nil); err == nil {
		t.Fatal("missing reason accepted")
	}
	got, err := freezeParameterAdjustments(o, input, map[string]string{"quality": "draft"})
	if err != nil || len(got) != 1 || string(got[0].Value) != `"low"` || got[0].Reason != "draft" {
		t.Fatalf("%+v %v", got, err)
	}
	p := PreparedInput{NormalizedInput: input}
	old, _ := hashPreparedInput(p)
	p.ParameterAdjustments = got
	now, _ := hashPreparedInput(p)
	if old == now {
		t.Fatal("reason/value not bound to grant hash")
	}
	got[0].Reason = "changed"
	changed, _ := hashPreparedInput(p)
	if changed == now {
		t.Fatal("reason mutation does not change hash")
	}
	p.ParameterAdjustments = nil
	again, _ := hashPreparedInput(p)
	if old != again {
		t.Fatal("empty metadata changed old hashes")
	}
}

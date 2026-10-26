package generation

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

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

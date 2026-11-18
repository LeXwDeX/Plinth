package catalog

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

func TestAdjustmentPolicyKeepsAuthoritySeparateFromPresentation(t *testing.T) {
	p := schema.Parameter{Name: "quality", Type: "select", Default: json.RawMessage(`"high"`), Options: []schema.ParameterOption{{Value: "high"}, {Value: "low"}}, AdjustmentPolicy: "advised", AdjustmentReason: "最高质量默认值", Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceReadOnly, Control: schema.ControlSelect}}
	o := schema.Offering{Parameters: []schema.Parameter{p}}
	if o.UserParameterAllowed(p) || !o.AgentParameterAllowed(p) {
		t.Fatal("advised authority confused")
	}
	if _, err := ValidateParameterPatch(o, map[string]json.RawMessage{"quality": json.RawMessage(`"low"`)}, false, nil); err == nil {
		t.Fatal("human changed advised value")
	}
	if _, err := ValidateParameterPatch(o, map[string]json.RawMessage{"quality": json.RawMessage(`"low"`)}, true, nil); err == nil {
		t.Fatal("missing reason accepted")
	}
	if _, err := ValidateParameterPatch(o, map[string]json.RawMessage{"quality": json.RawMessage(`"low"`)}, true, map[string]string{"quality": "先做低成本草稿"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateParameterPatch(o, map[string]json.RawMessage{"quality": json.RawMessage(`"invented"`)}, true, map[string]string{"quality": "reason"}); err == nil {
		t.Fatal("illegal value accepted")
	}
	p.Presentation.Surface = schema.SurfaceHidden
	if o.AgentParameterAllowed(p) || o.UserParameterAllowed(p) {
		t.Fatal("hidden field editable")
	}
	p.Presentation.Surface = schema.SurfaceTask
	p.Fixed = true
	if o.AgentParameterAllowed(p) {
		t.Fatal("fixed field editable")
	}
}

// #360: a "user" parameter keeps the person's authority and removes the
// Agent's, without becoming backend-fixed.
func TestUserPolicyIsAdjustableByThePersonOnly(t *testing.T) {
	p := schema.Parameter{Name: "n", Type: "number", Minimum: floatPointer(1), Maximum: floatPointer(10), AdjustmentPolicy: "user"}
	o := schema.Offering{Parameters: []schema.Parameter{p}}
	if !o.UserParameterAllowed(p) || o.AgentParameterAllowed(p) {
		t.Fatal("user policy authority confused")
	}
	if !o.OverrideParameterAllowed(p) {
		t.Fatal("a user-only parameter cannot be fixed by a Schema override")
	}
	if _, err := ValidateParameterPatch(o, map[string]json.RawMessage{"n": json.RawMessage(`2`)}, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateParameterPatch(o, map[string]json.RawMessage{"n": json.RawMessage(`2`)}, true, nil); err == nil {
		t.Fatal("Agent changed a user-only value")
	}
	p.Fixed = true
	if o.OverrideParameterAllowed(p) || o.UserParameterAllowed(p) {
		t.Fatal("a fixed parameter was overridden")
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestAdvisedNormalizationAcceptsLegalValueAndStillRejectsUnknown(t *testing.T) {
	o := schema.Offering{Parameters: []schema.Parameter{{Name: "quality", Type: "select", Default: json.RawMessage(`"high"`), Options: []schema.ParameterOption{{Value: "high"}, {Value: "low"}}, AdjustmentPolicy: "advised", AdjustmentReason: "推荐最高质量"}}}
	got, err := normalizeInput(o, json.RawMessage(`{"quality":"low"}`))
	if err != nil || string(got) != `{"quality":"low"}` {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := normalizeInput(o, json.RawMessage(`{"quality":"bad"}`)); err == nil {
		t.Fatal("invalid value accepted")
	}
}

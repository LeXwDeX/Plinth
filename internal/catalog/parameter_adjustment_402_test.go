package catalog

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #402: an Agent that sends a reason for a parameter that needs none (a normal
// setting such as background) used to lose the whole set_params call to
// `unexpected adjustment reason`, and had to send the same call again. The
// extra reason does no harm, so it is ignored: it is neither an error nor part
// of what the patch hands on. What stays refused is a reason that names a
// parameter the Offering does not declare, a reason without a value in the
// same call, and a reason over the bound.

func reasonOffering402() schema.Offering {
	return schema.Offering{Parameters: []schema.Parameter{
		{
			Name: "quality", Type: "select", Default: json.RawMessage(`"high"`),
			Options:          []schema.ParameterOption{{Value: "high"}, {Value: "low"}},
			AdjustmentPolicy: "advised", AdjustmentReason: "最高质量默认值",
			Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceReadOnly, Control: schema.ControlSelect},
		},
		{
			Name: "background", Type: "select", Default: json.RawMessage(`"auto"`),
			Options:      []schema.ParameterOption{{Value: "auto"}, {Value: "opaque"}},
			Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceProvider, Control: schema.ControlSelect},
		},
	}}
}

func TestSetParamsIgnoresAReasonForAParameterThatNeedsNone_402(t *testing.T) {
	o := reasonOffering402()
	values := map[string]json.RawMessage{"background": json.RawMessage(`"opaque"`)}

	without, err := ValidateParameterPatch(o, values, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	with, err := ValidateParameterPatch(o, values, true, map[string]string{"background": "白底更干净"})
	if err != nil {
		t.Fatalf("an extra reason failed the call: %v", err)
	}
	if !maps.EqualFunc(with, without, func(a, b json.RawMessage) bool { return string(a) == string(b) }) {
		t.Fatalf("the reason changed the result: %s vs %s", with, without)
	}

	// The reasons the patch hands on to the settings store never carry it.
	_, reasons, err := NormalizeParameterPatch(o, values, true, map[string]string{"background": "白底更干净"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 0 {
		t.Fatalf("an ignored reason was handed on: %v", reasons)
	}
}

func TestSetParamsKeepsTheReasonOfAnAdvisedParameterAndDropsTheOthers_402(t *testing.T) {
	o := reasonOffering402()
	values := map[string]json.RawMessage{
		"quality":    json.RawMessage(`"low"`),
		"background": json.RawMessage(`"opaque"`),
	}
	_, reasons, err := NormalizeParameterPatch(o, values, true, map[string]string{
		"quality":    "先做低成本草稿",
		"background": "白底更干净",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 1 || reasons["quality"] != "先做低成本草稿" {
		t.Fatalf("reasons = %v, want only the advised parameter's", reasons)
	}
	// An advised value that differs from its default still needs its own reason;
	// a reason for another parameter does not stand in for it.
	if _, err := ValidateParameterPatch(o, values, true, map[string]string{"background": "白底更干净"}); err == nil ||
		!strings.Contains(err.Error(), "adjustment reason") {
		t.Fatalf("a missing reason for quality passed: %v", err)
	}
}

func TestSetParamsStillRefusesTheOtherBadReasons_402(t *testing.T) {
	o := reasonOffering402()
	tooLong := strings.Repeat("a", 4097)
	for _, test := range []struct {
		name    string
		values  map[string]json.RawMessage
		reasons map[string]string
	}{
		{
			"a parameter the Offering does not declare",
			map[string]json.RawMessage{"background": json.RawMessage(`"opaque"`)},
			map[string]string{"invented": "不存在的参数"},
		},
		{
			"a reason without a value in the same call",
			map[string]json.RawMessage{"quality": json.RawMessage(`"low"`)},
			map[string]string{"quality": "先做低成本草稿", "background": "没给值"},
		},
		{
			"a reason over the bound for an advised parameter",
			map[string]json.RawMessage{"quality": json.RawMessage(`"low"`)},
			map[string]string{"quality": tooLong},
		},
		{
			"a reason over the bound for a parameter that needs none",
			map[string]json.RawMessage{"background": json.RawMessage(`"opaque"`)},
			map[string]string{"background": tooLong},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateParameterPatch(o, test.values, true, test.reasons); err == nil {
				t.Fatal("the call was accepted")
			}
			if _, _, err := NormalizeParameterPatch(o, test.values, true, test.reasons); err == nil {
				t.Fatal("the normalizing call was accepted")
			}
		})
	}
}

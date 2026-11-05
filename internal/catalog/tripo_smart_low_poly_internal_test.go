package catalog

// #139: Tripo's H-series generation endpoints (text, image and multiview to
// model) limit face_limit by the combination of the other switches. The
// official pages (developers.tripo3d.ai generation-*-to-model/standard) give:
// triangle up to 1,500,000 at standard and 2,000,000 at Ultra (detailed)
// geometry quality, quad up to 150,000, and with smart_low_poly a triangle
// mesh 500–20,000 and a quad mesh 500–10,000. dev-test sent the two-million
// default with smart_low_poly on and Tripo refused it with code 1004.

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

var tripoHSeriesOfferings = []string{
	"tripo/tripo-v3.1-full/text-to-3d",
	"tripo/tripo-v3.1-full/image-to-3d",
	"tripo/tripo-v3.1-multiview/image-to-3d",
	"tripo/tripo-v3.1/text-to-3d",
	"tripo/tripo-v3.1/image-to-3d",
}

func TestTripoHSeriesDeclaresTheFaceLimitCombinations(t *testing.T) {
	quad := schema.ParameterCondition{Parameter: "quad", Operator: "truthy"}
	smart := schema.ParameterCondition{Parameter: "smart_low_poly", Operator: "truthy"}
	want := []schema.ConditionalMaximum{
		{When: schema.ParameterCondition{Parameter: "geometry_quality", Operator: "equals", Value: json.RawMessage(`"standard"`)}, Maximum: 1500000},
		{When: quad, Maximum: 150000},
		{When: smart, Maximum: 20000},
		{When: smart, AlsoWhen: []schema.ParameterCondition{quad}, Maximum: 10000},
	}
	for _, id := range tripoHSeriesOfferings {
		offering := loadP2Offering(t, id)
		faceLimit, found := offeringParameter(offering.Parameters, "face_limit")
		if !found || !reflect.DeepEqual(faceLimit.MaximumWhen, want) {
			t.Fatalf("%s face_limit maximum_when = %+v", id, faceLimit.MaximumWhen)
		}
	}
}

func TestTripoHSeriesDefaultFaceLimitFollowsTheCombination(t *testing.T) {
	for _, id := range tripoHSeriesOfferings {
		offering := loadP2Offering(t, id)
		for _, test := range []struct {
			name    string
			changes map[string]any
			want    float64
		}{
			{"smart low poly off keeps the Ultra default", map[string]any{}, 2000000},
			{"smart low poly with a triangle mesh", map[string]any{"smart_low_poly": true}, 20000},
			{"smart low poly with a quad mesh", map[string]any{"smart_low_poly": true, "quad": true}, 10000},
			{"a quad mesh without smart low poly", map[string]any{"quad": true}, 150000},
			{"standard geometry quality", map[string]any{"geometry_quality": "standard"}, 1500000},
			{"standard geometry quality with smart low poly", map[string]any{"geometry_quality": "standard", "smart_low_poly": true}, 20000},
		} {
			values, err := normalizeHSeries(t, offering, test.changes)
			if err != nil {
				t.Fatalf("%s %s: %v", id, test.name, err)
			}
			if values["face_limit"] != test.want {
				t.Fatalf("%s %s: face_limit = %v, want %v", id, test.name, values["face_limit"], test.want)
			}
		}
	}
}

func TestTripoHSeriesRefusesAGivenFaceLimitOutsideTheCombination(t *testing.T) {
	offering := loadP2Offering(t, "tripo/tripo-v3.1-full/text-to-3d")
	for _, test := range []struct {
		changes map[string]any
		rangeOf string
	}{
		{map[string]any{"smart_low_poly": true, "face_limit": 2000000}, "1000–20000"},
		{map[string]any{"smart_low_poly": true, "quad": true, "face_limit": 15000}, "1000–10000"},
		{map[string]any{"quad": true, "face_limit": 200000}, "1000–150000"},
		{map[string]any{"geometry_quality": "standard", "face_limit": 2000000}, "1000–1500000"},
	} {
		_, err := normalizeHSeries(t, offering, test.changes)
		var refusal *schema.Error
		if !errors.As(err, &refusal) || !refusal.UserFacing || refusal.DetailTemplate != offering.ExecutionMessages.InputParameterRange {
			t.Fatalf("%v: refusal = %#v, want the Schema's range text", test.changes, err)
		}
		if !strings.Contains(refusal.Detail, test.rangeOf) || !strings.Contains(refusal.Detail, "面数上限") {
			t.Fatalf("%v: detail %q does not name the label and the range %s", test.changes, refusal.Detail, test.rangeOf)
		}
	}
	for _, changes := range []map[string]any{
		{"smart_low_poly": true, "face_limit": float64(15000)},
		{"smart_low_poly": true, "quad": true, "face_limit": float64(8000)},
		{"face_limit": float64(2000000)},
	} {
		if values, err := normalizeHSeries(t, offering, changes); err != nil || values["face_limit"] != changes["face_limit"] {
			t.Fatalf("%v: legal face_limit refused or rewritten: %v %v", changes, values, err)
		}
	}
}

func TestMaximumWhenAlsoWhenDeclarationValidation(t *testing.T) {
	for _, test := range []struct {
		name      string
		condition map[string]any
		want      string
	}{
		{"an unknown parameter", map[string]any{"parameter": "missing", "operator": "truthy"}, "引用了未知参数"},
		{"the parameter itself", map[string]any{"parameter": "face_limit", "operator": "truthy"}, "不能引用自身"},
		{"a value of the wrong type", map[string]any{"parameter": "quad", "operator": "equals", "value": float64(5)}, "value 不符合参数"},
		{"an empty parameter", map[string]any{"parameter": "", "operator": "truthy"}, "also_when 缺少条件参数"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := mutateDefaultOffering(t, "tripo/tripo-v3.1-full/text-to-3d", func(parameter map[string]any) {
				entries, _ := parameter["maximum_when"].([]any)
				entry, _ := entries[len(entries)-1].(map[string]any)
				entry["also_when"] = []any{test.condition}
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// normalizeHSeries normalizes like the Agent path: the step's extra-credit
// consent is always set (agentConsentInput), the rest comes from changes.
func normalizeHSeries(t *testing.T, offering schema.Offering, changes map[string]any) (map[string]any, error) {
	t.Helper()
	input := map[string]any{}
	for key, value := range changes {
		input[key] = value
	}
	if consent := offering.ExtraConsentParameter(); consent != "" {
		input[consent] = true
	}
	return normalizeP2(t, offering, input)
}

// mutateDefaultOffering loads the default Catalog with one face_limit
// declaration changed and returns the load error.
func mutateDefaultOffering(t *testing.T, id string, mutate func(parameter map[string]any)) error {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(defaultCatalog, &document); err != nil {
		t.Fatal(err)
	}
	offerings, _ := document["offerings"].([]any)
	for _, item := range offerings {
		offering, _ := item.(map[string]any)
		if offering["id"] != id {
			continue
		}
		parameters, _ := offering["parameters"].([]any)
		for _, rawParameter := range parameters {
			parameter, _ := rawParameter.(map[string]any)
			if parameter["name"] == "face_limit" {
				mutate(parameter)
			}
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(strings.NewReader(string(encoded)))
	return err
}

package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// brief.parameter_defaults belongs to the production brief, which needs purposes
// (#103): a Catalog that declares the mapping without them is refused instead of
// carrying a mapping nothing reads.
func TestBriefParameterDefaultsNeedThePurposesTheBriefDeclares(t *testing.T) {
	schema := ChatSchema{Model3DUsage: ChatModel3DUsageSchema{Brief: ChatProductionBriefSchema{
		ParameterDefaults: []ChatProductionBriefDefault{{
			Field: "target_engine", Value: "unreal",
			ParameterOverrides: map[string]map[string]json.RawMessage{"tripo/processing/rigging-v3": {"spec": json.RawMessage(`"unreal"`)}},
		}},
	}}}
	err := validateModel3DUsageShape(schema, nil)
	if err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("a mapping without purposes was accepted: %v", err)
	}
	schema.Model3DUsage.Brief.ParameterDefaults = nil
	if err := validateModel3DUsageShape(schema, nil); err != nil {
		t.Fatalf("a Catalog without a brief is valid: %v", err)
	}
}

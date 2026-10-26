package catalog_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// 带 options 的 boolean 是二选一呈现：必须恰为 false/true，且每项有 label。
func TestBooleanParameterOptionsMustBeFalseAndTrue(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	option := func(value, label string) map[string]any { return map[string]any{"value": value, "label": label} }
	cases := map[string][]any{
		"one option":    {option("true", "分部件")},
		"wrong values":  {option("yes", "是"), option("no", "否")},
		"duplicate":     {option("true", "甲"), option("true", "乙")},
		"three options": {option("false", "a"), option("true", "b"), option("maybe", "c")},
		"missing label": {option("false", ""), option("true", "分部件")},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			mutated := false
			for _, item := range doc["offerings"].([]any) {
				offering := item.(map[string]any)
				if offering["id"] != "tripo/tripo-v3.1/text-to-3d" {
					continue
				}
				for _, entry := range offering["parameters"].([]any) {
					if parameter := entry.(map[string]any); parameter["name"] == "generate_parts" {
						parameter["options"] = options
						mutated = true
					}
				}
			}
			if !mutated {
				t.Fatal("generate_parts not found")
			}
			encoded, _ := json.Marshal(doc)
			if _, err := catalog.Load(strings.NewReader(string(encoded))); err == nil ||
				!strings.Contains(err.Error(), "boolean") {
				t.Fatalf("load error = %v, want boolean options rejection", err)
			}
		})
	}
}

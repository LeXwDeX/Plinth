package catalog_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func catalogOffering(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range document["offerings"].([]any) {
		if offering, ok := raw.(map[string]any); ok && offering["id"] == id {
			return offering
		}
	}
	t.Fatalf("offering %q missing", id)
	return nil
}

func loadMutatedCatalog(t *testing.T, change func(document map[string]any)) (*catalog.Catalog, error) {
	t.Helper()
	document := readDefaultCatalogDocument(t)
	change(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.Load(bytes.NewReader(encoded))
}

// The source-model ceiling is declared once on the Offering (#121): the
// requirement copy, its translation and the Adapter all read that number.
func TestSourceModelLimitIsDeclaredOnceAndFillsRequirementCopy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rigging, _ := product.Get("meshy/processing/rigging")
	if rigging.SourceModelLimit == nil || rigging.SourceModelLimit.MaxTriangles != 320000 {
		t.Fatalf("rigging limit = %+v", rigging.SourceModelLimit)
	}
	requirements := func(c *catalog.Catalog) string {
		offering, _ := c.Get("meshy/processing/rigging")
		for _, parameter := range offering.Parameters {
			if parameter.UploadedModel != nil {
				return parameter.UploadedModel.Requirements
			}
		}
		return ""
	}
	if text := requirements(product); !strings.Contains(text, "最多 320000 个三角面") || strings.Contains(text, "{limit}") {
		t.Fatalf("requirements = %q", text)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	if english := texts.Text("en", requirements(product)); !strings.Contains(english, "at most 320000 triangle faces") || strings.Contains(english, "{limit}") {
		t.Fatalf("english requirements = %q", english)
	}
	// Changing the one declaration changes the copy and its translation, and the
	// stale-translation check still holds with the new number.
	changed, err := loadMutatedCatalog(t, func(document map[string]any) {
		catalogOffering(t, document, "meshy/processing/rigging")["source_model_limit"].(map[string]any)["max_triangles"] = json.Number("250000")
	})
	if err != nil {
		t.Fatal(err)
	}
	if text := requirements(changed); !strings.Contains(text, "最多 250000 个三角面") {
		t.Fatalf("requirements after change = %q", text)
	}
	changedTexts, err := catalog.NewTextLocalizer(changed.Application())
	if err != nil {
		t.Fatal(err)
	}
	if english := changedTexts.Text("en", requirements(changed)); !strings.Contains(english, "at most 250000 triangle faces") {
		t.Fatalf("english requirements after change = %q", english)
	}
	// Requirement copy never spells the number itself.
	raw := readDefaultCatalogDocument(t)
	encoded, _ := json.Marshal(catalogOffering(t, raw, "meshy/processing/rigging"))
	if strings.Contains(string(encoded), "320000 个") {
		t.Fatalf("rigging offering still spells the ceiling in copy")
	}
}

func TestCatalogLoadRejectsInvalidModelLimitDeclarations(t *testing.T) {
	cases := map[string]func(document map[string]any){
		"limit placeholder without a declaration": func(document map[string]any) {
			delete(catalogOffering(t, document, "meshy/processing/rigging"), "source_model_limit")
		},
		"zero ceiling": func(document map[string]any) {
			catalogOffering(t, document, "meshy/processing/rigging")["source_model_limit"].(map[string]any)["max_triangles"] = json.Number("0")
		},
		"unsupported count basis": func(document map[string]any) {
			catalogOffering(t, document, "meshy/processing/rigging")["source_model_limit"].(map[string]any)["count_basis"] = "quads"
		},
		"limit without a source parameter": func(document map[string]any) {
			offering := catalogOffering(t, document, "meshy/processing/rigging")
			offering["parameters"] = []any{}
			offering["input_rules"] = []any{}
		},
		"estimate names a missing parameter": func(document map[string]any) {
			catalogOffering(t, document, "meshy/processing/remesh")["output_triangles"].(map[string]any)["count_parameter"] = "nope"
		},
		"estimate topology is not a select": func(document map[string]any) {
			catalogOffering(t, document, "meshy/processing/remesh")["output_triangles"].(map[string]any)["topology_parameter"] = "target_polycount"
		},
		"estimate misses a topology option": func(document map[string]any) {
			delete(catalogOffering(t, document, "meshy/processing/remesh")["output_triangles"].(map[string]any)["triangles_per_face"].(map[string]any), "triangle")
		},
		"estimate with a non-positive factor": func(document map[string]any) {
			catalogOffering(t, document, "meshy/processing/remesh")["output_triangles"].(map[string]any)["triangles_per_face"].(map[string]any)["quad"] = json.Number("0")
		},
		"estimate on a generation offering": func(document map[string]any) {
			catalogOffering(t, document, "meshy/meshy-7.1/text-to-3d")["output_triangles"] = catalogOffering(t, document, "meshy/processing/remesh")["output_triangles"]
		},
		"plan refusal text lost": func(document map[string]any) {
			messages := document["application"].(map[string]any)["runtime"].(map[string]any)["generation_messages"].(map[string]any)
			delete(messages, "plan_source_face_limit")
		},
		"plan refusal text without its choices": func(document map[string]any) {
			messages := document["application"].(map[string]any)["runtime"].(map[string]any)["generation_messages"].(map[string]any)
			messages["plan_source_face_limit"] = "计划被拒绝：{source_service} {service} {estimate} {limit}"
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMutatedCatalog(t, change); err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
}

// A message matcher that names an HTTP status applies only under it; the
// "credit limit" wording must not reclassify a task_error or other statuses.
func TestMessageMatcherHTTPStatusRangeIsValidated(t *testing.T) {
	for name, status := range map[string]map[string]any{
		"inverted": {"min": json.Number("402"), "max": json.Number("401")},
		"too low":  {"min": json.Number("99"), "max": json.Number("402")},
		"too high": {"min": json.Number("402"), "max": json.Number("600")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadMutatedCatalog(t, func(document map[string]any) {
				connections := document["application"].(map[string]any)["runtime"].(map[string]any)["provider_connections"].([]any)
				for _, raw := range connections {
					connection := raw.(map[string]any)
					if connection["id"] != "meshy" {
						continue
					}
					matchers := connection["error_mapping"].(map[string]any)["message_matchers"].([]any)
					matchers[0].(map[string]any)["http_status"] = status
				}
			})
			if err == nil {
				t.Fatal("invalid http_status accepted")
			}
		})
	}
}

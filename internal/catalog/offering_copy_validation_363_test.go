package catalog

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #363: user-visible Offering copy names what users choose. It must not
// expose the Schema fields that implement normalization or machine-only wire
// identifiers. The validator is general; these fixtures only exercise the two
// reported leaks and the input-rule message that used to be copied from the
// wire name.
func TestOfferingCopyValidationRejectsInternalFieldLeaks(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(document map[string]any)
		want   string
	}{
		{
			name: "normalization field in a description",
			mutate: func(document map[string]any) {
				keychain := offeringDocumentByID(t, document, "meshy/creative-lab/keychain/build")
				size := parameterDocument(t, keychain, "size_mm")
				size["description"] = size["description"].(string) + " forced_when"
			},
			want: "forced_when",
		},
		{
			name: "wire name in a description",
			mutate: func(document map[string]any) {
				tripo := offeringDocumentByID(t, document, "tripo/tripo-v3.1/text-to-3d")
				seed := parameterDocument(t, tripo, "texture_seed")
				seed["description"] = seed["description"].(string) + " model_seed"
			},
			want: "model_seed",
		},
		{
			name: "wire name in an input rule message",
			mutate: func(document map[string]any) {
				keychain := offeringDocumentByID(t, document, "meshy/creative-lab/keychain/build")
				keychain["input_rules"] = []any{map[string]any{
					"parameters": []any{},
					"forbidden_when": []any{map[string]any{
						"parameter": "size_mm",
						"operator":  "equals",
						"value":     0,
					}},
					"message": "size_mm 必须大于 0",
				}}
			},
			want: "size_mm",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, testCase.mutate)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("internal field leak accepted or not named: %v", err)
			}
		})
	}
}

// The zero-default explanation belongs in each parameter description and
// names the user-facing parameter label, not the wire field.
// A single common word is valid natural copy and a valid API-style label,
// even when it is also the parameter's wire_name. Only machine-only
// snake_case identifiers are rejected.
func TestOfferingCopyAllowsCommonWordWireName(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		offering := offeringDocumentByID(t, document, "openai/gpt-image-2/text-to-image")
		quality := parameterDocument(t, offering, "quality")
		quality["wire_name"] = "quality"
		quality["label"] = "Quality"
		quality["description"] = "Choose image quality"
	})
	if err != nil {
		t.Fatalf("natural English copy rejected: %v", err)
	}
}

// The translated Offering copy is user-visible too. Leaks in the English
// offering_texts values must fail load, not only leaks in the default copy.
func TestOfferingTextCopyRejectsInternalIdentifiers(t *testing.T) {
	for _, leaked := range []string{"model_seed", "forced_when"} {
		t.Run(leaked, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				englishOfferingTexts(document)["纹理随机种子"] = "Texture seed; see " + leaked
			})
			if err == nil || !strings.Contains(err.Error(), leaked) {
				t.Fatalf("translated copy leak accepted or not named: %v", err)
			}
		})
	}
}

func TestDefaultCatalogReliefDefaultsUseParameterDescriptions(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet"} {
		o, _ := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
		if len(o.InputRules) != 0 {
			t.Fatalf("%s still rejects zero through input rules", product)
		}
		for _, name := range []string{"size_mm", "curve_param", "relief_scale"} {
			parameter, ok := offeringParameter(o.Parameters, name)
			if !ok {
				t.Fatalf("%s lacks %s", product, name)
			}
			if !strings.Contains(parameter.Description, "0") || !strings.Contains(parameter.Description, "默认") {
				t.Fatalf("%s %s description does not explain 0 as default: %q", product, name, parameter.Description)
			}
			if strings.Contains(parameter.Description, name) {
				t.Fatalf("%s %s description repeats the wire name: %q", product, name, parameter.Description)
			}
		}
	}
}

func TestDefaultCatalogOfferingTranslationsAvoidInternalFieldLeaks(t *testing.T) {
	english := defaultApplicationForLocalization(t).Localization.Bundles["en"].OfferingTexts
	for source, translated := range english {
		for _, leaked := range []string{"forced_when", "model_seed"} {
			if strings.Contains(source, leaked) || strings.Contains(translated, leaked) {
				t.Fatalf("offering translation still leaks %q: %q => %q", leaked, source, translated)
			}
		}
	}
	for _, stale := range []string{"size_mm 必须大于 0", "curve_param 必须大于 0", "relief_scale 必须大于 0"} {
		if _, exists := english[stale]; exists {
			t.Fatalf("stale zero-rejection translation remains: %q", stale)
		}
	}
}

func offeringDocumentByID(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	offerings, ok := document["offerings"].([]any)
	if !ok {
		t.Fatal("document has no offerings")
	}
	for _, raw := range offerings {
		offering, ok := raw.(map[string]any)
		if ok && offering["id"] == id {
			return offering
		}
	}
	t.Fatalf("offering %q not found", id)
	return nil
}

func parameterDocument(t *testing.T, offering map[string]any, name string) map[string]any {
	t.Helper()
	parameters, ok := offering["parameters"].([]any)
	if !ok {
		t.Fatalf("offering %v has no parameters", offering["id"])
	}
	for _, raw := range parameters {
		parameter, ok := raw.(map[string]any)
		if ok && parameter["name"] == name {
			return parameter
		}
	}
	t.Fatalf("parameter %q not found", name)
	return nil
}

// #363 review: the Offering's execution and cancellation texts are shown to
// users and share the same internal-field guard as the rest of its copy.
func TestOfferingExecutionAndCancellationCopyRejectInternalFields(t *testing.T) {
	cases := []struct {
		name     string
		offering schema.Offering
		want     string
	}{
		{
			name: "execution message",
			offering: schema.Offering{
				ID: "test",
				ExecutionMessages: schema.ExecutionMessages{
					ReferenceLimit: "Use forced_when here",
				},
			},
			want: "forced_when",
		},
		{
			name: "cancellation message with parameter name",
			offering: schema.Offering{
				ID:         "test",
				Parameters: []schema.Parameter{{Name: "size_mm", WireName: "size_mm"}},
				Cancellation: schema.CancellationPolicy{
					Requesting: "Use size_mm here",
				},
			},
			want: "size_mm",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateOfferingCopy(testCase.offering)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("internal name in execution/cancellation copy accepted or not named: %v", err)
			}
		})
	}
}

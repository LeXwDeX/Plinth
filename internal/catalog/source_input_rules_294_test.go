package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #294: Meshy Rigging refuses finger bones for a model made with quad topology
// (the real API answered HTTP 400 on 2026-11-12). The topology is a parameter of
// the Task that made the model, not of the rigging request, so the Schema states
// the limit on the rigging step's source task parameter, and the kernel refuses
// the combination before anything is paid (internal/generation).

const meshyRiggingOfferingID = "meshy/processing/rigging"

func meshyRiggingOffering(t *testing.T, product *catalog.Catalog) generation.Offering {
	t.Helper()
	rigging, found := product.Get(meshyRiggingOfferingID)
	if !found {
		t.Fatalf("%s is not in the Catalog", meshyRiggingOfferingID)
	}
	return rigging
}

func TestMeshyRiggingDeclaresTheQuadFingersSourceRule(t *testing.T) {
	product := shippedProduct(t)
	rigging := meshyRiggingOffering(t, product)
	if rigging.Revision != "2026-11-12.294.1" {
		t.Fatalf("revision = %q: the declaration changed with #294", rigging.Revision)
	}
	source := sourceTaskParameter(t, rigging)
	if len(source.SourceInputRules) != 1 {
		t.Fatalf("source rules = %+v, want the one fingers rule", source.SourceInputRules)
	}
	rule := source.SourceInputRules[0]
	if len(rule.SourceConditions) != 1 || rule.SourceConditions[0].Parameter != "topology" ||
		rule.SourceConditions[0].Operator != "equals" || string(rule.SourceConditions[0].Value) != `"quad"` {
		t.Fatalf("source conditions = %+v, want topology equals quad", rule.SourceConditions)
	}
	if len(rule.RequestConditions) != 1 || rule.RequestConditions[0].Parameter != "include_fingers" ||
		rule.RequestConditions[0].Operator != "truthy" {
		t.Fatalf("request conditions = %+v, want include_fingers truthy", rule.RequestConditions)
	}

	// The reason names the parameter by its label, says nothing was sent or spent,
	// and has its English text; every word of the refusal is Schema copy.
	var fingersLabel string
	for _, parameter := range rigging.Parameters {
		if parameter.Name == "include_fingers" {
			fingersLabel = parameter.Label
		}
	}
	if fingersLabel == "" || !strings.Contains(rule.Message, "「"+fingersLabel+"」") ||
		!strings.Contains(rule.Message, "这一步没有提交，也没有扣积分") {
		t.Fatalf("message %q should name the label 「%s」 and say nothing was submitted or spent", rule.Message, fingersLabel)
	}
	english := product.Application().Localization.Bundles["en"].OfferingTexts[rule.Message]
	for _, want := range []string{"finger bones", "quad topology", "no credits were spent", "Include finger bones"} {
		if !strings.Contains(english, want) {
			t.Errorf("English message %q does not say %q", english, want)
		}
	}

	// The rule can only see a parameter the source Task's own Offering declares:
	// the sources that name a topology are the Meshy 7.1 and 7 generations and the
	// remesh. A Smart Topology model (T2.5) delivers triangles and has no topology
	// parameter; a retexture does not carry its source's topology, so a retexture of
	// a quad model is not recognised and the upstream decides.
	var seeing []string
	for _, id := range source.TaskOfferings {
		offering, found := product.Get(id)
		if !found {
			t.Fatalf("source %s is not in the Catalog", id)
		}
		for _, parameter := range offering.Parameters {
			if parameter.Name == "topology" {
				seeing = append(seeing, string(id))
			}
		}
	}
	slices.Sort(seeing)
	want := []string{"meshy/meshy-7.1/image-to-3d", "meshy/meshy-7.1/text-to-3d", "meshy/meshy-7/image-to-3d", "meshy/meshy-7/text-to-3d", "meshy/processing/remesh"}
	if !slices.Equal(seeing, want) {
		t.Fatalf("sources that name a topology = %v, want %v", seeing, want)
	}
}

func TestSourceInputRulesAreValidated(t *testing.T) {
	// The rule of the shipped rigging step, edited in place. A changed message
	// moves its translation with it: Load rejects a missing or an unused one before
	// it reaches the check under test.
	edit := func(document map[string]any, change func(parameter, rule map[string]any)) {
		for _, raw := range catalogOffering(t, document, meshyRiggingOfferingID)["parameters"].([]any) {
			if entry := raw.(map[string]any); entry["name"] == "source_task" {
				change(entry, entry["source_input_rules"].([]any)[0].(map[string]any))
				return
			}
		}
		t.Fatal("source_task missing")
	}
	setMessage := func(text string) func(document map[string]any) {
		return func(document map[string]any) {
			edit(document, func(_, rule map[string]any) { rule["message"] = text })
			if text != "" {
				translation := "Not a quad source."
				if strings.Contains(text, "{label}") {
					translation = "Not a quad {label} source." // the placeholders of both languages must agree
				}
				bundle := document["application"].(map[string]any)["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)
				bundle["offering_texts"].(map[string]any)[text] = translation
			}
			catalogtest.DropUnusedOfferingTexts(document)
		}
	}
	conditionsOf := func(rule map[string]any, key string) map[string]any {
		return rule[key].([]any)[0].(map[string]any)
	}
	for name, tc := range map[string]struct {
		mutate func(document map[string]any)
		want   string // "" accepts the Catalog
	}{
		"the shipped rule": {func(map[string]any) {}, ""},
		"a rule without source conditions": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) { rule["source_conditions"] = []any{} })
		}, "source_conditions"},
		"a source condition on a parameter no source declares": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) {
				conditionsOf(rule, "source_conditions")["parameter"] = "no_such_parameter"
			})
		}, "没有任何来源 Offering 声明"},
		"a source condition with a value the parameter does not take": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) { conditionsOf(rule, "source_conditions")["value"] = "hex" })
		}, "无效"},
		"a source condition with an unknown operator": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) { conditionsOf(rule, "source_conditions")["operator"] = "roughly" })
		}, "未知 operator"},
		"a request condition on a parameter the step lacks": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) {
				conditionsOf(rule, "request_conditions")["parameter"] = "no_such_parameter"
			})
		}, "请求条件引用未知参数"},
		"a rule that only the source decides": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) { delete(rule, "request_conditions") })
		}, ""},
		"a rule on a parameter that is not a task": {func(document map[string]any) {
			var rules any
			edit(document, func(parameter, _ map[string]any) { rules = parameter["source_input_rules"] })
			for _, raw := range catalogOffering(t, document, meshyRiggingOfferingID)["parameters"].([]any) {
				if entry := raw.(map[string]any); entry["name"] == "include_fingers" {
					entry["source_input_rules"] = rules
				}
			}
		}, "source_input_rules 只能写在"},
		"a rule on a task parameter that lists no Offerings": {func(document map[string]any) {
			edit(document, func(parameter, _ map[string]any) { delete(parameter, "task_offerings") })
		}, "source_input_rules 只能写在"},
		"a message with surrounding white space": {setMessage(" 不能用四边面来源。"), "message"},
		"a message with a placeholder brace":     {setMessage("不能用 {label} 四边面来源。"), "message"},
		"a message longer than a refusal":        {setMessage(strings.Repeat("不能用四边面来源。", 60)), "message"},
		"a message that is empty":                {setMessage(""), "message"},
		"a plain message":                        {setMessage("不能用四边面来源。"), ""},
		"a message without an English text": {func(document map[string]any) {
			edit(document, func(_, rule map[string]any) {
				bundle := document["application"].(map[string]any)["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)
				delete(bundle["offering_texts"].(map[string]any), rule["message"].(string))
			})
		}, "Meshy 绑骨不支持"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadMutatedCatalog(t, tc.mutate)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("a valid declaration was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("an invalid declaration was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

// Finger bones are the person's call: nothing the Schema defaults for a purpose,
// a platform or a target engine turns include_fingers on, so a step that nobody
// asked for fingers never meets the quad limit by itself.
func TestNothingTurnsOnFingerBonesByDefault(t *testing.T) {
	product := shippedProduct(t)
	usage := product.Application().Chat.Model3DUsage
	for _, profile := range usage.Profiles {
		for id, values := range profile.ParameterOverrides {
			if _, set := values["include_fingers"]; set {
				t.Errorf("purpose %q overrides include_fingers of %s", profile.ID, id)
			}
		}
		for _, variant := range profile.Platforms {
			for id, values := range variant.ParameterOverrides {
				if _, set := values["include_fingers"]; set {
					t.Errorf("a platform variant of purpose %q overrides include_fingers of %s", profile.ID, id)
				}
			}
		}
	}
	for _, option := range usage.Brief.ParameterDefaults {
		for id, values := range option.ParameterOverrides {
			if _, set := values["include_fingers"]; set {
				t.Errorf("the brief default of %s=%s sets include_fingers of %s", option.Field, option.Value, id)
			}
		}
	}
	rigging := meshyRiggingOffering(t, product)
	for _, parameter := range rigging.Parameters {
		if parameter.Name == "include_fingers" && string(parameter.Default) != "false" {
			t.Errorf("include_fingers defaults to %s, want false", parameter.Default)
		}
	}
}

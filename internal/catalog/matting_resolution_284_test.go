package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const mattingOfferingID = "matting/default/background-removal"

func mattingParameter(t *testing.T, name string) schema.Parameter {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := loaded.Get(mattingOfferingID)
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	for _, parameter := range offering.Parameters {
		if parameter.Name == name {
			return parameter
		}
	}
	t.Fatalf("background-removal has no parameter %q", name)
	return schema.Parameter{}
}

// #284, #299: the Schema lets the two working resolutions go as high as the
// service accepts, but both default to the static 1024 Plinth sent before #284,
// whatever the source picture. On 2026-11-13 the matting service's shared GPU ran
// out of memory on a 2048x2048 picture twice: with both resolutions at 2048 and,
// with SDMatte held at 1024, with BiRefNet at 2048 (out of memory in the coarse
// stage). So neither parameter follows the source picture, and a larger value is
// only what a caller asks for. Matting stays free.
func TestMattingResolutionsReachTheServiceRangeAndDefaultTo1024(t *testing.T) {
	for _, test := range []struct {
		name    string
		maximum float64
	}{{"birefnet_process_res", 4096}, {"sdmatte_process_res", 2048}} {
		parameter := mattingParameter(t, test.name)
		if parameter.Minimum == nil || *parameter.Minimum != 512 || parameter.Maximum == nil || *parameter.Maximum != test.maximum {
			t.Fatalf("%s range = %v..%v, want 512..%v", test.name, parameter.Minimum, parameter.Maximum, test.maximum)
		}
		if !parameter.Integer {
			t.Fatalf("%s is an integer on the service", test.name)
		}
		if string(parameter.Default) != "1024" {
			t.Fatalf("%s static default = %s, want 1024 whatever the picture", test.name, parameter.Default)
		}
		if parameter.DefaultBySourceSide != nil {
			t.Fatalf("%s follows the source picture again: %+v", test.name, parameter.DefaultBySourceSide)
		}
	}
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, _ := loaded.Get(mattingOfferingID)
	if offering.EstimatedCredits != 0 {
		t.Fatalf("matting is self-hosted and free, estimated_credits = %d", offering.EstimatedCredits)
	}
}

// #299: the two descriptions tell the caller what the default is now (1024,
// whatever the source picture) and what a larger value costs (more GPU memory,
// and it can fail on the shared service), in both languages. The English text is
// the bundle's translation of the Chinese one, which Load requires to exist.
func TestMattingResolutionDescriptionsSayTheDefaultAndTheMemoryCost(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	localization := loaded.Application().Localization
	if localization == nil {
		t.Fatal("the default catalog declares no localization")
	}
	english := localization.Bundles["en"].OfferingTexts
	for _, name := range []string{"birefnet_process_res", "sdmatte_process_res"} {
		parameter := mattingParameter(t, name)
		for _, want := range []string{"不指定时用 1024", "不随来源图片放大", "更高的值需要更多显存", "共享显卡"} {
			if !strings.Contains(parameter.Description, want) {
				t.Errorf("%s description %q does not say %q", name, parameter.Description, want)
			}
		}
		if strings.Contains(parameter.Description, "按来源图片") {
			t.Errorf("%s description still says the default follows the source picture: %q", name, parameter.Description)
		}
		translated, ok := english[parameter.Description]
		if !ok {
			t.Errorf("%s description has no English translation", name)
			continue
		}
		for _, want := range []string{"when not set it is 1024", "does not grow with the source picture", "Higher values need more GPU memory", "shared GPU"} {
			if !strings.Contains(translated, want) {
				t.Errorf("%s English description %q does not say %q", name, translated, want)
			}
		}
		if strings.Contains(translated, "follows the long side") {
			t.Errorf("%s English description still says the default follows the source picture: %q", name, translated)
		}
	}
}

// The Offering the rule's tests run on is a test-local one: its document loads
// as it stands and carries the rule on SourceSideParameter alone, while the
// shipped Offering it is copied from still declares none.
func TestSourceSideTestOfferingCarriesTheRuleTheShippedOneDoesNot(t *testing.T) {
	loaded := catalogtest.LoadWithSourceSideOffering(t)
	offering, ok := loaded.Get(catalogtest.SourceSideOfferingID)
	if !ok {
		t.Fatal("the test-local Offering is not in the catalog")
	}
	shipped, ok := loaded.Get(mattingOfferingID)
	if !ok {
		t.Fatal("background-removal offering missing")
	}
	if offering.Default || !shipped.Default {
		t.Fatalf("default flags: test-local %v, shipped %v", offering.Default, shipped.Default)
	}
	declared := map[string]*schema.SourceSideDefault{}
	for _, parameter := range offering.Parameters {
		if parameter.DefaultBySourceSide != nil {
			declared[parameter.Name] = parameter.DefaultBySourceSide
		}
	}
	rule := declared[catalogtest.SourceSideParameter]
	if len(declared) != 1 || rule == nil || rule.Source != "source_assets" || len(rule.Tiers) != 2 {
		t.Fatalf("the test-local Offering declares %+v, want the one rule on %s", declared, catalogtest.SourceSideParameter)
	}
	for side, want := range map[int64]string{1: "1024", 1024: "1024", 1025: "2048", 4096: "2048", 20000: "2048"} {
		if got, ok := rule.ValueFor(side); !ok || string(got) != want {
			t.Fatalf("for a long side of %d the rule gives %s, want %s", side, got, want)
		}
	}
	for _, parameter := range shipped.Parameters {
		if parameter.DefaultBySourceSide != nil {
			t.Fatalf("the shipped matting parameter %s declares a rule: %+v", parameter.Name, parameter.DefaultBySourceSide)
		}
	}
}

// default_by_source_side is validated where the Schema loads. No shipped
// Offering declares it (#299), so the declarations refused here are made on the
// test-local Offering, whose own declaration loads as it stands.
func TestDefaultBySourceSideDeclarationIsValidated(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(parameter map[string]any, offering map[string]any)
		want   string
	}{
		"unknown source": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["source"] = "no_such_parameter"
		}, "必须是本 Offering 的资产参数"},
		"source is not an asset parameter": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["source"] = "expand"
		}, "必须是本 Offering 的资产参数"},
		"no tiers": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["tiers"] = []any{}
		}, "缺少 tiers"},
		"the last tier has an upper bound": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["tiers"] = []any{
				map[string]any{"max_source_side": 1024, "value": 1024},
				map[string]any{"max_source_side": 2048, "value": 2048},
			}
		}, "最后一档不能声明 max_source_side"},
		"an earlier tier has no upper bound": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["tiers"] = []any{
				map[string]any{"value": 1024},
				map[string]any{"value": 2048},
			}
		}, "tiers[0].max_source_side 必须为正且严格递增"},
		"upper bounds do not ascend": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["tiers"] = []any{
				map[string]any{"max_source_side": 2048, "value": 1024},
				map[string]any{"max_source_side": 1024, "value": 1536},
				map[string]any{"value": 2048},
			}
		}, "tiers[1].max_source_side 必须为正且严格递增"},
		"a tier value is outside the parameter's range": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["tiers"] = []any{
				map[string]any{"max_source_side": 1024, "value": 1024},
				map[string]any{"value": 8192},
			}
		}, "tiers[1] value 无效"},
		"a tier value is missing": {func(p map[string]any, _ map[string]any) {
			p["default_by_source_side"].(map[string]any)["tiers"] = []any{
				map[string]any{"max_source_side": 1024},
				map[string]any{"value": 2048},
			}
		}, "tiers[0] 缺少有效 value"},
		"no static default for pictures that cannot be measured": {func(p map[string]any, _ map[string]any) {
			delete(p, "default")
		}, "需要同时声明 default"},
		"a fixed parameter takes no caller value to follow": {func(p map[string]any, _ map[string]any) {
			p["fixed"] = true
		}, "只有可由调用方给值的 number 参数可以声明"},
		"only a number parameter declares it": {func(p map[string]any, _ map[string]any) {
			p["type"] = "text"
		}, "只有可由调用方给值的 number 参数可以声明"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := catalogtest.SourceSideDocument(t)
			mutated := false
			for _, item := range doc["offerings"].([]any) {
				offering := item.(map[string]any)
				if offering["id"] != catalogtest.SourceSideOfferingID {
					continue
				}
				for _, entry := range offering["parameters"].([]any) {
					if parameter := entry.(map[string]any); parameter["name"] == catalogtest.SourceSideParameter {
						test.mutate(parameter, offering)
						mutated = true
					}
				}
			}
			if !mutated {
				t.Fatalf("the document has no %s parameter on %s", catalogtest.SourceSideParameter, catalogtest.SourceSideOfferingID)
			}
			encoded, _ := json.Marshal(doc)
			if _, err := catalog.Load(strings.NewReader(string(encoded))); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load error = %v, want a rejection naming %q", err, test.want)
			}
		})
	}
}

// ValueFor picks by the long side; a side of zero (nothing was measured) or a
// rule without an open-ended last tier gives nothing.
func TestSourceSideDefaultValueFor(t *testing.T) {
	rule := schema.SourceSideDefault{Source: "pictures", Tiers: []schema.SourceSideTier{
		{MaxSide: 512, Value: json.RawMessage(`512`)},
		{MaxSide: 1024, Value: json.RawMessage(`1024`)},
		{Value: json.RawMessage(`2048`)},
	}}
	for side, want := range map[int64]string{1: "512", 512: "512", 513: "1024", 1024: "1024", 1025: "2048", 1 << 20: "2048"} {
		if got, ok := rule.ValueFor(side); !ok || string(got) != want {
			t.Fatalf("ValueFor(%d) = %s %v, want %s", side, got, ok, want)
		}
	}
	for _, side := range []int64{0, -5} {
		if got, ok := rule.ValueFor(side); ok {
			t.Fatalf("ValueFor(%d) = %s, want nothing", side, got)
		}
	}
	bounded := schema.SourceSideDefault{Tiers: []schema.SourceSideTier{{MaxSide: 512, Value: json.RawMessage(`512`)}}}
	if got, ok := bounded.ValueFor(1024); ok {
		t.Fatalf("a rule without an open-ended tier covered 1024: %s", got)
	}
}

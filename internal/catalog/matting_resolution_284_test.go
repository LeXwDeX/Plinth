package catalog_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
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
// service accepts. BiRefNet's default follows the source picture, up to the
// service's own GPU default of 2048. SDMatte's does not: the service's default is
// 1024 even on a GPU, and the live run of 2026-11-13 ran the GPU out of memory
// with it raised to 2048 for a 2048x2048 picture, so it stays 1024 whatever the
// picture and a larger value is only what a caller asks for. Both keep a static
// default for pictures that cannot be measured. Matting stays free.
func TestMattingResolutionsReachTheServiceRangeAndSDMatteStaysAtItsDefault(t *testing.T) {
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
			t.Fatalf("%s static default = %s, want 1024 for pictures that cannot be measured", test.name, parameter.Default)
		}
	}
	rule := mattingParameter(t, "birefnet_process_res").DefaultBySourceSide
	if rule == nil || rule.Source != "source_assets" || len(rule.Tiers) != 2 {
		t.Fatalf("birefnet_process_res rule = %+v", rule)
	}
	for side, want := range map[int64]string{1: "1024", 1024: "1024", 1025: "2048", 4096: "2048", 20000: "2048"} {
		if got, ok := rule.ValueFor(side); !ok || string(got) != want {
			t.Fatalf("birefnet_process_res for a long side of %d = %s, want %s", side, got, want)
		}
	}
	if rule := mattingParameter(t, "sdmatte_process_res").DefaultBySourceSide; rule != nil {
		t.Fatalf("sdmatte_process_res follows the source picture again: %+v", rule)
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

// The tiers must be values the parameter itself accepts, or a default the
// Schema supplies would be refused by its own validation.
func TestMattingTierValuesAreWithinTheDeclaredRange(t *testing.T) {
	parameter := mattingParameter(t, "birefnet_process_res")
	for _, tier := range parameter.DefaultBySourceSide.Tiers {
		var value float64
		if err := json.Unmarshal(tier.Value, &value); err != nil {
			t.Fatal(err)
		}
		if value < *parameter.Minimum || value > *parameter.Maximum {
			t.Fatalf("birefnet_process_res tier value %v is outside %v..%v", value, *parameter.Minimum, *parameter.Maximum)
		}
	}
}

// default_by_source_side is validated where the Schema loads.
func TestDefaultBySourceSideDeclarationIsValidated(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
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
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for _, item := range doc["offerings"].([]any) {
				offering := item.(map[string]any)
				if offering["id"] != mattingOfferingID {
					continue
				}
				for _, entry := range offering["parameters"].([]any) {
					if parameter := entry.(map[string]any); parameter["name"] == "birefnet_process_res" {
						test.mutate(parameter, offering)
					}
				}
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

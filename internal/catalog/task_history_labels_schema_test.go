package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestTaskHistoryLabelsExposeDisplayOnlyModelNames 锁定 W3 历史标签契约：
// application.task_history.labels.models 只承载展示名，三条历史模型标签
// 原文来自旧前端 provider-config，且不激活任何 Offering（无 enabled 记录）。
func TestTaskHistoryLabelsExposeDisplayOnlyModelNames(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	history := taskCatalog.Application().TaskHistory
	want := map[string]string{
		"gpt-image-2":                "GPT Image 2 (OpenAI)",
		"nano-banana":                "Nano Banana (快速)",
		"doubao-seedream-4-0-250828": "即梦 Seedream 4.0 (标准)",
	}
	if len(history.Labels.Models) != len(want) {
		t.Fatalf(
			"labels.models 数量 = %d, want %d (%v)",
			len(history.Labels.Models),
			len(want),
			history.Labels.Models,
		)
	}
	for modelID, label := range want {
		if history.Labels.Models[modelID] != label {
			t.Errorf(
				"labels.models[%q] = %q, want %q",
				modelID,
				history.Labels.Models[modelID],
				label,
			)
		}
		for _, offering := range taskCatalog.List("") {
			if offering.ModelID == modelID {
				t.Errorf(
					"历史标签 %q 不得对应任何 Offering 模型（仅展示）",
					modelID,
				)
			}
		}
	}
}

// TestTaskHistoryLabelsAndHiddenFieldsReachPublicProjection 锁定公开投影：
// /api/v1/schema 必须携带 labels 与 hidden_fields，前端 presenter 据此渲染。
func TestTaskHistoryLabelsAndHiddenFieldsReachPublicProjection(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		TaskHistory struct {
			Labels struct {
				Models     map[string]string            `json:"models"`
				Parameters map[string]map[string]string `json:"parameters"`
			} `json:"labels"`
			HiddenFields map[string][]string `json:"hidden_fields"`
		} `json:"task_history"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if document.TaskHistory.Labels.Models["gpt-image-2"] != "GPT Image 2 (OpenAI)" {
		t.Errorf("公开投影缺失历史模型标签: %+v", document.TaskHistory.Labels.Models)
	}
	if document.TaskHistory.Labels.Parameters["retexture"]["prompt"] != "纹理提示词" {
		t.Errorf(
			"公开投影缺失 retexture 参数标签: %+v",
			document.TaskHistory.Labels.Parameters["retexture"],
		)
	}
	hidden := document.TaskHistory.HiddenFields["background-removal"]
	if len(hidden) != 2 || hidden[0] != "batch_status" || hidden[1] != "total_count" {
		t.Errorf("background-removal hidden_fields = %v", hidden)
	}
}

// TestTaskHistoryLabelsRejectBlankEntries 锁定加载期校验：空键/空标签/空
// hidden_fields 拒绝，防止出现"声明了但没有内容"的第二真源。
func TestTaskHistoryLabelsRejectBlankEntries(t *testing.T) {
	base := `"values":{"all_capability":"all","enabled":"1"}`
	cases := map[string]string{
		"blank model label": `"values":{"all_capability":"all","enabled":"1"},
		"labels":{"models":{"ghost":""}}`,
		"blank parameter label": `"values":{"all_capability":"all","enabled":"1"},
		"labels":{"parameters":{"text-to-image":{"size":"  "}}}`,
		"empty parameter scope": `"values":{"all_capability":"all","enabled":"1"},
		"labels":{"parameters":{"text-to-image":{}}}`,
		"empty hidden fields": `"values":{"all_capability":"all","enabled":"1"},
		"hidden_fields":{"background-removal":[]}`,
		"blank hidden field": `"values":{"all_capability":"all","enabled":"1"},
		"hidden_fields":{"background-removal":[""]}`,
	}
	for name, patch := range cases {
		document := strings.Replace(
			string(testProductDocument(`[]`)),
			base,
			patch,
			1,
		)
		if _, err := catalog.Load(strings.NewReader(document)); err == nil {
			t.Errorf("%s: 加载应失败", name)
		}
	}
}

// TestPresentationPrimitivesHaveDefaultCatalogDeclarations 锁定 W2 遗留的
// visible_when 与 slider 原语在默认 Catalog 有真实声明（W3 前端消费者依赖）：
// matting sensitivity 声明 slider + visible_when(mask_refine truthy)；
// tripo 两 Offering 的 generate_parts 互斥参数全部声明
// visible_when(generate_parts falsy) 且保留 forced_when 服务端裁决。
func TestPresentationPrimitivesHaveDefaultCatalogDeclarations(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	matting, exists := taskCatalog.Get("matting/default/background-removal")
	if !exists {
		t.Fatal("missing matting offering")
	}
	sensitivity, exists := testOfferingParameter(matting.Parameters, "sensitivity")
	if !exists {
		t.Fatal("missing sensitivity parameter")
	}
	if sensitivity.Presentation == nil ||
		sensitivity.Presentation.Control != generation.ControlSlider {
		t.Fatalf("sensitivity control = %+v, want slider", sensitivity.Presentation)
	}
	if !parameterVisibleWhenMatches(
		sensitivity,
		"mask_refine",
		"truthy",
	) {
		t.Errorf("sensitivity visible_when 应声明 mask_refine truthy")
	}

	tripoHidden := map[string][]string{
		"tripo/tripo-v3.1/text-to-3d": {
			"texture", "pbr", "auto_size", "texture_quality",
			"geometry_quality", "quad", "smart_low_poly",
		},
		"tripo/tripo-v3.1/image-to-3d": {
			"enable_image_autofix", "texture", "pbr", "auto_size",
			"texture_quality", "texture_alignment", "geometry_quality",
			"orientation", "quad", "smart_low_poly",
		},
	}
	forcedNames := []string{"texture", "pbr", "quad", "geometry_quality"}
	for offeringID, names := range tripoHidden {
		offering, exists := taskCatalog.Get(generation.OfferingID(offeringID))
		if !exists {
			t.Fatalf("missing offering %q", offeringID)
		}
		for _, name := range names {
			parameter, exists := testOfferingParameter(offering.Parameters, name)
			if !exists {
				t.Fatalf("%s 缺少参数 %q", offeringID, name)
			}
			if parameter.Fixed {
				// 质量参数由后端固定，只能隐藏或按 Schema 展示只读值。
				if parameter.Presentation == nil || (parameter.Presentation.Surface != generation.SurfaceHidden && parameter.Presentation.Surface != generation.SurfaceReadOnly) {
					t.Errorf("%s 固定参数 %q 必须是 hidden 或 readonly", offeringID, name)
				}
				continue
			}
			if !parameterVisibleWhenMatches(
				parameter,
				"generate_parts",
				"falsy",
			) {
				t.Errorf(
					"%s 参数 %q 应声明 visible_when generate_parts falsy",
					offeringID,
					name,
				)
			}
		}
		for _, name := range forcedNames {
			parameter, exists := testOfferingParameter(offering.Parameters, name)
			if !exists {
				t.Fatalf("%s 缺少参数 %q", offeringID, name)
			}
			if len(parameter.ForcedWhen) == 0 {
				t.Errorf(
					"%s 参数 %q 应保留 forced_when 服务端裁决",
					offeringID,
					name,
				)
			}
		}
	}
}

func parameterVisibleWhenMatches(
	parameter generation.Parameter,
	name string,
	operator string,
) bool {
	if parameter.Presentation == nil {
		return false
	}
	for _, condition := range parameter.Presentation.VisibleWhen {
		if condition.Parameter == name && string(condition.Operator) == operator {
			return true
		}
	}
	return false
}

func testOfferingParameter(
	parameters []generation.Parameter,
	name string,
) (generation.Parameter, bool) {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

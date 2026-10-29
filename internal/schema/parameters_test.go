package schema_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestSourceParameterRolePrefersDeclaredSemantic(t *testing.T) {
	parameter := schema.Parameter{
		Name:     "input_task_id",
		Type:     "string",
		Semantic: schema.SemanticSourceTask,
	}
	role, ok := schema.SourceParameterRole(parameter)
	if !ok || role != schema.SemanticSourceTask {
		t.Fatalf("role = %q, ok = %v; want source-task", role, ok)
	}
}

func TestSourceParameterRoleFallsBackToLegacyNames(t *testing.T) {
	cases := []struct {
		name      string
		parameter schema.Parameter
		want      schema.ParameterSemantic
	}{
		{"task", schema.Parameter{Name: "source_task", Type: "task"}, schema.SemanticSourceTask},
		{"asset", schema.Parameter{Name: "source_asset", Type: "asset"}, schema.SemanticSourceAsset},
		{"url", schema.Parameter{Name: "source_url", Type: "text"}, schema.SemanticSourceURL},
		{"multi asset", schema.Parameter{Name: "source_assets", Type: "asset-array"}, schema.SemanticSourceAsset},
	}
	for _, testCase := range cases {
		role, ok := schema.SourceParameterRole(testCase.parameter)
		if !ok || role != testCase.want {
			t.Fatalf("%s: role = %q, ok = %v; want %q", testCase.name, role, ok, testCase.want)
		}
	}
}

func TestSourceParameterRoleRejectsNonSourceParameters(t *testing.T) {
	cases := []struct {
		name      string
		parameter schema.Parameter
	}{
		{"regular parameter", schema.Parameter{Name: "topology", Type: "select"}},
		{"wrong type on legacy name", schema.Parameter{Name: "source_url", Type: "task"}},
		{"unrelated semantic", schema.Parameter{Name: "anything", Semantic: schema.SemanticMask}},
		{
			"declared semantic overrides legacy name",
			schema.Parameter{Name: "source_assets", Type: "asset-array", Semantic: schema.SemanticReferenceImage},
		},
	}
	for _, testCase := range cases {
		if role, ok := schema.SourceParameterRole(testCase.parameter); ok {
			t.Fatalf("%s: 意外命中来源角色 %q", testCase.name, role)
		}
	}
}

func TestSourceSemanticTokensAreValid(t *testing.T) {
	for _, semantic := range []schema.ParameterSemantic{
		schema.SemanticSourceTask,
		schema.SemanticSourceAsset,
		schema.SemanticSourceURL,
		schema.SemanticAction,
	} {
		if !semantic.Valid() {
			t.Fatalf("semantic %q 应为合法枚举", semantic)
		}
	}
	if (schema.ParameterSemantic("source-task-invalid")).Valid() {
		t.Fatal("未知 semantic 不应通过校验")
	}
}

func TestSkillSettableClosureIsExplicitAllowlist(t *testing.T) {
	excluded := []schema.ParameterSemantic{
		schema.SemanticSourceTask,
		schema.SemanticSourceAsset,
		schema.SemanticSourceURL,
		schema.SemanticAction,
	}
	for _, semantic := range excluded {
		if !semantic.Valid() {
			t.Fatalf("%q 必须是合法词表成员", semantic)
		}
		if semantic.SkillSettable() {
			t.Fatalf("%q 不得进入 skill 可覆盖闭包", semantic)
		}
	}
	settable := []schema.ParameterSemantic{
		schema.SemanticPrompt,
		schema.SemanticMask,
		schema.SemanticNegativePrompt,
		schema.SemanticReferenceImage,
		schema.SemanticPolycount,
		schema.SemanticOutputSize,
		schema.SemanticQuality,
	}
	for _, semantic := range settable {
		if !semantic.SkillSettable() {
			t.Fatalf("%q 应属于 skill 可覆盖闭包", semantic)
		}
	}
	// 词表里的每个合法语义都必须对 skill 闭包表态：新增语义若只加进
	// Valid() 而忘记 SkillSettable，本用例与 SkillSettable 的白名单
	// 实现共同保证"默认不被覆盖"。
	if schema.ParameterSemantic("unknown").SkillSettable() {
		t.Fatal("词表外语义不得进入任何闭包")
	}
}

// A2 (#121): the conversation reference-image rule is one function; every
// projection that asks "does the Agent take this picture from the chat" uses it.
func TestConversationReferenceImageIsOneSharedRule(t *testing.T) {
	image := func(change func(*schema.Parameter)) schema.Parameter {
		parameter := schema.Parameter{Name: "reference_image", Type: "asset", Semantic: schema.SemanticReferenceImage}
		change(&parameter)
		return parameter
	}
	cases := map[string]struct {
		parameter schema.Parameter
		step      bool
		chat      bool
	}{
		"asset reference image":       {image(func(*schema.Parameter) {}), true, true},
		"asset array reference image": {image(func(p *schema.Parameter) { p.Type = "asset-array" }), true, true},
		"fixed by the Schema":         {image(func(p *schema.Parameter) { p.Fixed = true }), false, false},
		"hidden from the person": {image(func(p *schema.Parameter) {
			p.Presentation = &schema.ParameterPresentation{Surface: schema.SurfaceHidden}
		}), false, false},
		"source asset":            {schema.Parameter{Name: "source_asset", Type: "asset", Semantic: schema.SemanticSourceAsset}, false, false},
		"reference text":          {image(func(p *schema.Parameter) { p.Type = "text" }), true, false},
		"not a reference image":   {image(func(p *schema.Parameter) { p.Semantic = schema.SemanticMask }), true, false},
		"plain prompt":            {schema.Parameter{Name: "prompt", Type: "text", Semantic: schema.SemanticPrompt}, true, false},
		"visible presentation ok": {image(func(p *schema.Parameter) { p.Presentation = &schema.ParameterPresentation{Surface: schema.SurfaceTask} }), true, true},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if got := schema.StepInputParameter(test.parameter); got != test.step {
				t.Errorf("StepInputParameter = %v, want %v", got, test.step)
			}
			if got := schema.ConversationReferenceImage(test.parameter); got != test.chat {
				t.Errorf("ConversationReferenceImage = %v, want %v", got, test.chat)
			}
		})
	}
}

func TestTriangleEstimateAndCompatibleBudgets(t *testing.T) {
	offering := schema.Offering{
		Parameters: []schema.Parameter{
			{Name: "topology", Type: "select", Default: []byte(`"quad"`)},
			{Name: "target_polycount", Type: "number", Default: []byte(`300000`)},
		},
		OutputTriangles: &schema.OutputTriangleEstimate{CountParameter: "target_polycount", TopologyParameter: "topology",
			TrianglesPerFace: map[string]int64{"quad": 2, "triangle": 1}},
	}
	for name, test := range map[string]struct {
		input string
		want  int64
		ok    bool
	}{
		"defaults":        {`{}`, 600000, true},
		"no input":        {``, 600000, true},
		"triangle":        {`{"topology":"triangle","target_polycount":320000}`, 320000, true},
		"quad override":   {`{"target_polycount":160000}`, 320000, true},
		"unknown shape":   {`{"topology":"hexagon"}`, 0, false},
		"non numeric":     {`{"target_polycount":"many"}`, 0, false},
		"zero":            {`{"target_polycount":0}`, 0, false},
		"input not JSON":  {`nope`, 0, false},
		"input not a map": {`[1]`, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := offering.EstimateOutputTriangles([]byte(test.input))
			if got != test.want || ok != test.ok {
				t.Fatalf("estimate = %d, %v; want %d, %v", got, ok, test.want, test.ok)
			}
		})
	}
	if _, ok := (schema.Offering{}).EstimateOutputTriangles([]byte(`{}`)); ok {
		t.Fatal("an Offering without the declaration produced an estimate")
	}
	budgets := schema.FormatTopologyBudgets(offering.OutputTriangles.CompatibleFaceBudgets(schema.SourceModelLimit{MaxTriangles: 320000, CountBasis: "triangles"}))
	if budgets != "quad ≤ 160000 / triangle ≤ 320000" {
		t.Fatalf("budgets = %q", budgets)
	}
}

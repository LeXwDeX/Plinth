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

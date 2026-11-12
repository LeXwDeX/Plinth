package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #65/#279: the settings sentence of a tool description lists the parameters
// the Agent may adjust, in Schema order. An Offering whose parameters are all
// the prompt, assets, hidden consent or fixed values has none to name, so the
// sentence is left out. Every shipped image-to-3D Offering now declares
// settings, so this case is built here instead of read from the Catalog.
func TestDeclaredSettingNamesAreEmptyWithoutUserSettings(t *testing.T) {
	executor := &Executor{}
	command := chat.ToolCommand{}
	hidden := &schema.ParameterPresentation{Surface: schema.SurfaceHidden, Control: schema.ControlBoolean}
	readOnly := &schema.ParameterPresentation{Surface: schema.SurfaceReadOnly, Control: schema.ControlSelect}
	offering := generation.Offering{Parameters: []generation.Parameter{
		{Name: "prompt", Label: "提示词", Type: "text", Semantic: schema.SemanticPrompt},
		{Name: "reference_assets", Label: "参考图", Type: "asset-array"},
		{Name: "consent", Label: "同意", Type: "boolean", Presentation: hidden},
		{Name: "locked", Label: "锁定", Type: "select", Fixed: true, Presentation: readOnly},
		{Name: "shown", Label: "只读", Type: "select", Presentation: readOnly},
	}}
	if names := executor.declaredSettingNames(command, offering); names != "" {
		t.Fatalf("settings named without a user setting: %q", names)
	}

	offering.Parameters = append(offering.Parameters,
		generation.Parameter{Name: "mesh_mode", Label: "拓扑类型", Type: "select"},
		generation.Parameter{Name: "advised", Label: "目标面数", Type: "number", AdjustmentPolicy: "advised",
			Presentation: &schema.ParameterPresentation{Surface: schema.SurfaceReadOnly, Control: schema.ControlNumber}},
	)
	if names := executor.declaredSettingNames(command, offering); names != "拓扑类型, 目标面数" {
		t.Fatalf("settings = %q, want the adjustable ones in Schema order", names)
	}
}

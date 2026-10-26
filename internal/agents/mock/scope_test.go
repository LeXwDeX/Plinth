package mock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

const scopeRefusal = "mode refuses: {tool}"

var (
	scope2DOffered  = []string{"refine_image", "remove_background", "create_three_view", "update_session_notes"}
	scope3DOffered  = []string{"create_model_3d", "create_model_3d_from_image", "retexture_model", "process_model", "update_session_notes"}
	scope2DWithheld = []string{"create_model_3d", "create_model_3d_from_image", "retexture_model", "process_model"}
	scope3DWithheld = []string{"refine_image", "remove_background", "create_three_view"}
)

func scopedRequest(text string, offered, withheld []string) agents.Request {
	tools := make([]agents.Tool, 0, len(offered))
	for _, name := range offered {
		tools = append(tools, agents.Tool{Name: name})
	}
	return agents.Request{
		Messages:      []agents.Message{{Role: "system", Content: "你是 Agent。"}, {Role: "user", Content: text}},
		Tools:         tools,
		ScopeWithheld: withheld,
	}
}

// The mock Agent is a stand-in for a model that knows its scope: a request the
// scope forbids is answered with the refusal, never with a tool call (and never
// with a different tool that is still on offer).
func TestMockAgentAnswersARequestTheScopeForbidsInsteadOfCallingATool(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "done",
		UserRole: "user", ToolRole: "tool", ScopeRefusal: scopeRefusal,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name            string
		offered         []string
		withheld        []string
		text            string
		wantTool        string
		wantRefusedTool string
	}{
		{name: "2d: a picture", offered: scope2DOffered, withheld: scope2DWithheld, text: "赛博朋克女刺客，全身立绘", wantTool: "refine_image"},
		{name: "2d: remove background", offered: scope2DOffered, withheld: scope2DWithheld, text: "用 @img6 去背景", wantTool: "remove_background"},
		{name: "2d: a 3D model is refused", offered: scope2DOffered, withheld: scope2DWithheld, text: "做一个中世纪骑士的 3D 模型", wantRefusedTool: "create_model_3d"},
		{name: "2d: image to 3D is refused", offered: scope2DOffered, withheld: scope2DWithheld, text: "把 @img3 转成 3D 模型", wantRefusedTool: "create_model_3d_from_image"},
		{name: "2d: retexture is refused", offered: scope2DOffered, withheld: scope2DWithheld, text: "给 @model1 换贴图", wantRefusedTool: "retexture_model"},
		{name: "2d: rigging is refused", offered: scope2DOffered, withheld: scope2DWithheld, text: "给 @model1 绑骨", wantRefusedTool: "process_model"},
		{name: "2d: notes still work", offered: scope2DOffered, withheld: scope2DWithheld, text: "记住：主角是红发女剑士", wantTool: "update_session_notes"},
		{name: "3d: a model", offered: scope3DOffered, withheld: scope3DWithheld, text: "做一个中世纪骑士的 3D 模型", wantTool: "create_model_3d"},
		{name: "3d: rigging", offered: scope3DOffered, withheld: scope3DWithheld, text: "给 @model1 绑骨", wantTool: "process_model"},
		{name: "3d: a Creative Lab keychain", offered: append([]string{"create_creative_lab_design"}, scope3DOffered...), withheld: scope3DWithheld, text: "用 @img2 做一个钥匙扣", wantTool: "create_creative_lab_design"},
		{name: "2d: a Creative Lab keychain is refused", offered: scope2DOffered, withheld: append([]string{"create_creative_lab_design"}, scope2DWithheld...), text: "用 @img2 做一个钥匙扣", wantRefusedTool: "create_creative_lab_design"},
		{name: "3d: a plain picture is refused", offered: scope3DOffered, withheld: scope3DWithheld, text: "赛博朋克女刺客，全身立绘", wantRefusedTool: "refine_image"},
		{name: "3d: remove background is refused", offered: scope3DOffered, withheld: scope3DWithheld, text: "用 @img6 去背景", wantRefusedTool: "remove_background"},
		{name: "3d: three views are refused", offered: scope3DOffered, withheld: scope3DWithheld, text: "用 @img2 生成三视图", wantRefusedTool: "create_three_view"},
		{name: "3d: four views are refused", offered: scope3DOffered, withheld: append(append([]string{}, scope3DWithheld...), "create_multiview_images", "edit_multiview_images"), text: "用 @img2 生成四视角图片", wantRefusedTool: "create_multiview_images"},
		{name: "3d: editing four views is refused", offered: scope3DOffered, withheld: append(append([]string{}, scope3DWithheld...), "create_multiview_images", "edit_multiview_images"), text: "编辑四视角：背面换成绿色", wantRefusedTool: "edit_multiview_images"},
		{name: "2d: four views", offered: append(append([]string{}, scope2DOffered...), "create_multiview_images", "edit_multiview_images"), withheld: scope2DWithheld, text: "用 @img2 生成四视角图片", wantTool: "create_multiview_images"},
		{name: "3d: editing a picture is refused", offered: scope3DOffered, withheld: scope3DWithheld, text: "@img4 改成红色披风", wantRefusedTool: "refine_image"},
		{name: "auto: nothing is withheld", offered: append(append([]string{}, scope2DOffered...), scope3DOffered...), text: "做一个中世纪骑士的 3D 模型", wantTool: "create_model_3d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			completion, err := adapter.Stream(context.Background(), scopedRequest(tc.text, tc.offered, tc.withheld), nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantRefusedTool != "" {
				want := strings.ReplaceAll(scopeRefusal, "{tool}", tc.wantRefusedTool)
				if len(completion.ToolCalls) != 0 || completion.Content != want {
					t.Fatalf("completion = %+v, want the refusal %q", completion, want)
				}
				return
			}
			if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != tc.wantTool {
				t.Fatalf("completion = %+v, want a call of %s", completion, tc.wantTool)
			}
		})
	}
}

func TestMockAgentWithoutAScopeKeepsItsRouting(t *testing.T) {
	adapter := newRoutingAdapter(t)
	completion, err := adapter.Stream(context.Background(), scopedRequest("做一个中世纪骑士的 3D 模型", scope2DOffered, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "refine_image" {
		t.Fatalf("without scope information the old fallback must stay: %+v", completion)
	}
}

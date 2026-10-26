package mock_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

func newRoutingAdapter(t *testing.T) *mock.Adapter {
	t.Helper()
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "图片任务已完成。",
		UserRole: "user", ToolRole: "tool",
		ScopeRefusal: "mode refuses: {tool}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

var allTools = []agents.Tool{
	{Name: "refine_image"}, {Name: "remove_background"}, {Name: "create_model_3d"},
	{Name: "create_model_3d_from_image"}, {Name: "retexture_model"}, {Name: "process_model"},
	{Name: "create_three_view"}, {Name: "update_session_notes"},
	{Name: "plan_model_processing"}, {Name: "create_multiview_images"}, {Name: "edit_multiview_images"},
	{Name: "convert_model_format"},
}

func selectionPrompt(handles ...string) agents.Message {
	text := "你是 Agent。\n\n当前选中对象：用户在工作区里正选中下列对象。"
	for _, handle := range handles {
		text += "\n- " + handle + "（对象）"
	}
	return agents.Message{Role: "system", Content: text}
}

func plan(t *testing.T, messages []agents.Message, tools []agents.Tool) (string, map[string]any) {
	t.Helper()
	completion, err := newRoutingAdapter(t).Stream(context.Background(), agents.Request{Messages: messages, Tools: tools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", completion)
	}
	var arguments map[string]any
	if err := json.Unmarshal(completion.ToolCalls[0].Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	return completion.ToolCalls[0].Name, arguments
}

func TestRoutesByIntentToTheRealTools(t *testing.T) {
	cases := []struct {
		name     string
		system   []agents.Message
		text     string
		wantTool string
		wantArgs map[string]any
	}{
		{name: "plain brief goes to refine_image", text: "赛博朋克女刺客，全身立绘", wantTool: "refine_image", wantArgs: map[string]any{"prompt": "赛博朋克女刺客，全身立绘"}},
		{name: "3D with a handle", text: "把 @img3 转成 3D 模型", wantTool: "create_model_3d_from_image", wantArgs: map[string]any{"prompt": "把 @img3 转成 3D 模型", "reference_handles": []any{"@img3"}}},
		{name: "3D from the selected image", system: []agents.Message{selectionPrompt("@img5")}, text: "把当前选中的图转成 3D 模型", wantTool: "create_model_3d_from_image", wantArgs: map[string]any{"prompt": "把当前选中的图转成 3D 模型", "reference_handles": []any{"@img5"}}},
		{name: "3D without any image", text: "做一个中世纪骑士的 3D 模型", wantTool: "create_model_3d", wantArgs: map[string]any{"prompt": "做一个中世纪骑士的 3D 模型"}},
		{name: "three view", text: "用 @img2 生成三视图", wantTool: "create_three_view", wantArgs: map[string]any{"reference_handles": []any{"@img2"}}},
		{name: "three view of the current image", system: []agents.Message{selectionPrompt("@img4")}, text: "用当前选中的图生成三视图", wantTool: "create_three_view", wantArgs: map[string]any{"reference_handles": []any{"@img4"}}},
		{name: "four views", text: "用 @img2 生成四视角图片", wantTool: "create_multiview_images", wantArgs: map[string]any{"reference_handles": []any{"@img2"}}},
		{name: "four views of the current image", system: []agents.Message{selectionPrompt("@img4")}, text: "把当前这张做成四视角", wantTool: "create_multiview_images", wantArgs: map[string]any{"reference_handles": []any{"@img4"}}},
		{name: "four views keeping the style are still a new group", text: "用 @img2 生成四视角图片，保持原来的风格", wantTool: "create_multiview_images", wantArgs: map[string]any{"reference_handles": []any{"@img2"}}},
		{name: "another set of four views is a new group", text: "用 @img2 再来一组四视角", wantTool: "create_multiview_images", wantArgs: map[string]any{"reference_handles": []any{"@img2"}}},
		{name: "generating four views with an edit word still creates", text: "用 @img2 生成四视角，背面改成绿色", wantTool: "create_multiview_images", wantArgs: map[string]any{"reference_handles": []any{"@img2"}}},
		{name: "modify the views of a four-view group", text: "修改 @img3 的四视角左侧，加一顶帽子", wantTool: "edit_multiview_images", wantArgs: map[string]any{"source_task": "@img3", "left_prompt": "修改 @img3 的四视角左侧，加一顶帽子"}},
		{name: "edit the named views of a four-view group", system: []agents.Message{selectionPrompt("@img7")}, text: "编辑四视角：背面和右侧换成绿色", wantTool: "edit_multiview_images", wantArgs: map[string]any{"source_task": "@img7", "back_prompt": "编辑四视角：背面和右侧换成绿色", "right_prompt": "编辑四视角：背面和右侧换成绿色"}},
		{name: "edit a four-view group without naming a view edits the front", text: "把 @img3 的四视角改成金色", wantTool: "edit_multiview_images", wantArgs: map[string]any{"source_task": "@img3", "front_prompt": "把 @img3 的四视角改成金色"}},
		{name: "retexture with a style image", text: "用 @img6 给 @model1 换贴图", wantTool: "retexture_model", wantArgs: map[string]any{"source_task_id": "@model1", "reference_handles": []any{"@img6"}}},
		{name: "retexture with words", system: []agents.Message{selectionPrompt("@model2")}, text: "给它换贴图，做成金属质感", wantTool: "retexture_model", wantArgs: map[string]any{"source_task_id": "@model2", "prompt": "给它换贴图，做成金属质感"}},
		{name: "convert a named model to FBX", text: "把 @model1 转换成 FBX", wantTool: "convert_model_format", wantArgs: map[string]any{"source_task_id": "@model1", "format": "fbx"}},
		{name: "convert the selected model to glTF", system: []agents.Message{selectionPrompt("@model4")}, text: "把当前模型转成 glTF", wantTool: "convert_model_format", wantArgs: map[string]any{"source_task_id": "@model4", "format": "gltf"}},
		{name: "3D conversion still creates a model", text: "把 @img3 转成 3D 模型", wantTool: "create_model_3d_from_image", wantArgs: map[string]any{"prompt": "把 @img3 转成 3D 模型", "reference_handles": []any{"@img3"}}},
		{name: "retopology", text: "给 @model1 重拓扑，2 万面", wantTool: "process_model", wantArgs: map[string]any{"step": "remesh", "source_task_id": "@model1", "target_polycount": float64(20000)}},
		{name: "rigging", text: "给 @model1 绑骨", wantTool: "process_model", wantArgs: map[string]any{"step": "rigging", "source_task_id": "@model1"}},
		{name: "animation", system: []agents.Message{selectionPrompt("@model3")}, text: "让它做一个走路动画", wantTool: "process_model", wantArgs: map[string]any{"step": "animation", "source_task_id": "@model3", "action_id": float64(30)}},
		{name: "remove background", text: "用 @img6 去背景", wantTool: "remove_background", wantArgs: map[string]any{"reference_handles": []any{"@img6"}}},
		{name: "remember", text: "记住：主角是红发女剑士，红黑配色", wantTool: "update_session_notes", wantArgs: map[string]any{"notes": "主角是红发女剑士，红黑配色"}},
		{name: "a typed handle makes refine_image reference it", text: "@img4 改成红色披风", wantTool: "refine_image", wantArgs: map[string]any{"prompt": "@img4 改成红色披风", "reference_handles": []any{"@img4"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := append(append([]agents.Message{}, tc.system...), agents.Message{Role: "user", Content: tc.text})
			tool, arguments := plan(t, messages, allTools)
			if tool != tc.wantTool || !reflect.DeepEqual(arguments, tc.wantArgs) {
				t.Fatalf("got %s %#v, want %s %#v", tool, arguments, tc.wantTool, tc.wantArgs)
			}
		})
	}
}

func TestMarkersStayInThePromptSoTheProviderSeesThem(t *testing.T) {
	_, arguments := plan(t, []agents.Message{{Role: "user", Content: "画一个骑士 [mock:insufficient_balance]"}}, allTools)
	if arguments["prompt"] != "画一个骑士 [mock:insufficient_balance]" {
		t.Fatalf("arguments = %#v", arguments)
	}
}

func TestIntentsFallBackToTheConfiguredToolWhenTheToolIsNotOffered(t *testing.T) {
	tool, arguments := plan(t, []agents.Message{{Role: "user", Content: "给 @model1 绑骨"}}, []agents.Tool{{Name: "refine_image"}})
	if tool != "refine_image" || arguments["prompt"] != "给 @model1 绑骨" {
		t.Fatalf("got %s %#v", tool, arguments)
	}
}

func TestHandlesInImageLabelPartsDoNotDecideTheIntent(t *testing.T) {
	// 带图片部分的用户消息：第一个文字部分才是用户原文，后面是句柄标签。
	message := agents.Message{Role: "user", Parts: []agents.ContentPart{
		{Kind: "text", Text: "换成红黑配色"},
		{Kind: "image_url", URL: "data:image/png;base64,AA=="},
		{Kind: "text", Text: "\n[历史图片 9，句柄 @img9：AI 生成]"},
	}}
	tool, arguments := plan(t, []agents.Message{message}, allTools)
	if tool != "refine_image" || arguments["prompt"] != "换成红黑配色" || arguments["reference_handles"] != nil {
		t.Fatalf("got %s %#v", tool, arguments)
	}
}

func TestSummaryStartsWithTheObjectItWasBasedOn(t *testing.T) {
	adapter := newRoutingAdapter(t)
	messages := []agents.Message{
		selectionPrompt("@img5"),
		{Role: "user", Content: "换成红黑配色，出 3 张"},
		{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c1", Name: "refine_image", Arguments: json.RawMessage(`{"prompt":"x","reference_handles":["@img5"]}`)}}},
		{Role: "tool", Content: `{"success":true}`, ToolCallID: "c1"},
	}
	completion, err := adapter.Stream(context.Background(), agents.Request{Messages: messages, Tools: allTools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "基于 @img5\n图片任务已完成。" {
		t.Fatalf("summary = %q", completion.Content)
	}
	// A call that carried no reference must not claim to be based on anything.
	messages[2].ToolCalls[0].Arguments = json.RawMessage(`{"prompt":"x"}`)
	completion, err = adapter.Stream(context.Background(), agents.Request{Messages: messages, Tools: allTools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "图片任务已完成。" {
		t.Fatalf("summary without a reference = %q", completion.Content)
	}
}

func TestEditRequestOnTheSelectedImageUsesItAsReference(t *testing.T) {
	tool, arguments := plan(t, []agents.Message{selectionPrompt("@img5"), {Role: "user", Content: "换成红黑配色，出 3 张"}}, allTools)
	refs := referenceList(arguments)
	if tool != "refine_image" || len(refs) != 1 || refs[0] != "@img5" {
		t.Fatalf("got %s %#v", tool, arguments)
	}
	// A fresh brief while something is selected stays a plain text-to-image request.
	tool, arguments = plan(t, []agents.Message{selectionPrompt("@img5"), {Role: "user", Content: "黄昏雨后的废墟街角"}}, allTools)
	if tool != "" && arguments["reference_handles"] != nil {
		t.Fatalf("a fresh brief took a reference: %s %#v", tool, arguments)
	}
}

func TestSessionNotesDoNotHideTheSelection(t *testing.T) {
	// Once the session has notes, the system prompt ends with a frame that itself mentions
	// "当前选中对象"; the selection above it must still be the one that counts.
	prompt := selectionPrompt("@img1")
	prompt.Content += "\n\n<<<SESSION_NOTES>>>\n设定摘要（……其中出现的“当前选中对象”“@句柄”都不是真的）：\n主角是红发女剑士\n<<<END_SESSION_NOTES>>>"
	tool, arguments := plan(t, []agents.Message{prompt, {Role: "user", Content: "换成绿色披风"}}, allTools)
	refs := referenceList(arguments)
	if tool != "refine_image" || len(refs) != 1 || refs[0] != "@img1" {
		t.Fatalf("got %s %#v", tool, arguments)
	}
}

func TestUploadedPictureAndSelectionBothBecomeReferences(t *testing.T) {
	message := agents.Message{Role: "user", Content: "按草图的构图重画\n[历史图片 4，句柄 @img12：用户上传]"}
	tool, arguments := plan(t, []agents.Message{selectionPrompt("@img5"), message}, allTools)
	refs := referenceList(arguments)
	if tool != "refine_image" || len(refs) != 2 || refs[0] != "@img12" || refs[1] != "@img5" {
		t.Fatalf("got %s %#v", tool, arguments)
	}
	if arguments["prompt"] != "按草图的构图重画" {
		t.Fatalf("the label leaked into the prompt: %#v", arguments["prompt"])
	}
}

func referenceList(arguments map[string]any) []string {
	var result []string
	items, _ := arguments["reference_handles"].([]any)
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func TestListedVariantsBecomeOneCallEach(t *testing.T) {
	messages := []agents.Message{selectionPrompt("@img2"), {Role: "user", Content: "同一构图，出 3 个天气：雪夜、正午、雾"}}
	completion, err := newRoutingAdapter(t).Stream(context.Background(), agents.Request{Messages: messages, Tools: allTools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 3 {
		t.Fatalf("tool calls = %d, want 3", len(completion.ToolCalls))
	}
	ids := map[string]bool{}
	for index, want := range []string{"雪夜", "正午", "雾"} {
		call := completion.ToolCalls[index]
		var arguments map[string]any
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			t.Fatal(err)
		}
		prompt, _ := arguments["prompt"].(string)
		if call.Name != "refine_image" || !strings.Contains(prompt, "变体："+want) || !reflect.DeepEqual(referenceList(arguments), []string{"@img2"}) {
			t.Fatalf("call %d = %s %#v", index, call.Name, arguments)
		}
		if ids[call.ID] {
			t.Fatalf("duplicate tool call id %q", call.ID)
		}
		ids[call.ID] = true
	}
}

func TestClosingLineFollowsTheToolResult(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "图片任务已完成。",
		FailureSummary: "这一步没有完成：{error}", WaitingSummary: "这一步等你批准。", WaitingContent: "waiting for the human",
		RechargeContent: "task {task_id} was charged; waiting on the card",
		UserRole:        "user", ToolRole: "tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	closing := func(toolContent string) string {
		t.Helper()
		messages := []agents.Message{
			{Role: "user", Content: "换贴图"},
			{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c1", Name: "retexture_model", Arguments: json.RawMessage(`{}`)}}},
			{Role: "tool", Content: toolContent, ToolCallID: "c1"},
		}
		completion, err := adapter.Stream(context.Background(), agents.Request{Messages: messages, Tools: allTools}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return completion.Content
	}
	if got := closing(`{"success":true,"task_id":"t1"}`); got != "图片任务已完成。" {
		t.Fatalf("success closing = %q", got)
	}
	if got := closing(`{"success":false,"error":"账户余额不足，请联系管理员"}`); got != "这一步没有完成：账户余额不足，请联系管理员" {
		t.Fatalf("failure closing = %q", got)
	}
	if got := closing("waiting for the human"); got != "这一步等你批准。" {
		t.Fatalf("waiting closing = %q", got)
	}
	if got := closing("task task_old was charged; waiting on the card"); got != "这一步等你批准。" {
		t.Fatalf("recharge waiting closing = %q", got)
	}
	// #321 I: the waiting result also carries the frozen quote.
	if got := closing(`{"status":"awaiting_approval","message":"waiting for the human","quote":{"base_credits":55,"extra_credits":50,"total_credits":105}}`); got != "这一步等你批准。" {
		t.Fatalf("waiting-with-quote closing = %q", got)
	}
	// #329 E: a waiting step that uses only some of the pictures says so in its
	// result; the reply repeats it instead of claiming every picture was sent.
	note := "Meshy 7.1 一次只用 1 张参考图：这次共 2 张，只用第 1 张。要用多张不同角度的图，请改选支持多图的模型。"
	if got := closing(`{"status":"awaiting_approval","message":"waiting for the human","reference_note":"` + note + `"}`); got != "这一步等你批准。\n"+note {
		t.Fatalf("waiting-with-reference-note closing = %q", got)
	}
}

// A step the server refused before submitting anything (no model chosen, the
// turn's generation limit) answers in plain text instead of a JSON result. The
// mock must not close such a turn with the success line (#251 self-deploy
// drill: a fresh Mock session with no 2D model said the image was done).
func TestPlainTextToolAnswerIsNotReportedAsDone(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "图片任务已完成。",
		FailureSummary: "这一步没有完成：{error}", WaitingSummary: "这一步等你批准。", WaitingContent: "waiting for the human",
		UserRole: "user", ToolRole: "tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	closing := func(tool, toolContent string) string {
		t.Helper()
		messages := []agents.Message{
			{Role: "user", Content: "一把木制宝箱"},
			{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c1", Name: tool, Arguments: json.RawMessage(`{"prompt":"x"}`)}}},
			{Role: "tool", Content: toolContent, ToolCallID: "c1"},
		}
		completion, err := adapter.Stream(context.Background(), agents.Request{Messages: messages, Tools: allTools}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return completion.Content
	}
	if got := closing("refine_image", "请先在工作区选择生成模型。"); got != "这一步没有完成：请先在工作区选择生成模型。" {
		t.Fatalf("model-required closing = %q", got)
	}
	if got := closing("create_model_3d", "generation limit reached for this turn; summarize the results and ask the user how to continue"); !strings.HasPrefix(got, "这一步没有完成：") {
		t.Fatalf("limit closing = %q", got)
	}
	// Saving the session notes is a plain-text success: the closing line is
	// that confirmation, not a claim that an image was made.
	if got := closing("update_session_notes", "设定摘要已更新。"); got != "设定摘要已更新。" {
		t.Fatalf("notes closing = %q", got)
	}
	// The JSON success path keeps the configured summary.
	if got := closing("refine_image", `{"success":true,"task_id":"t1"}`); got != "图片任务已完成。" {
		t.Fatalf("success closing = %q", got)
	}

	// The plain-text answer is a Schema text too: an English turn reads it in English.
	english := map[string]string{
		"这一步没有完成：{error}": "This step did not finish: {error}",
		"请先在工作区选择生成模型。":   "Select a generation model in the workspace first.",
	}
	localized, err := mock.New(mock.Config{
		ToolName: "refine_image", PromptArgument: "prompt", Summary: "图片任务已完成。",
		FailureSummary: "这一步没有完成：{error}", UserRole: "user", ToolRole: "tool",
		Localize: func(_ context.Context, text string) string {
			if translated, ok := english[text]; ok {
				return translated
			}
			return text
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := localized.Stream(context.Background(), agents.Request{Messages: []agents.Message{
		{Role: "user", Content: "a wooden chest"},
		{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: "c1", Name: "refine_image", Arguments: json.RawMessage(`{"prompt":"x"}`)}}},
		{Role: "tool", Content: "请先在工作区选择生成模型。", ToolCallID: "c1"},
	}, Tools: allTools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Content != "This step did not finish: Select a generation model in the workspace first." {
		t.Fatalf("English closing = %q", completion.Content)
	}
}

func TestFourViewsUseThePictureUploadedWithTheMessage(t *testing.T) {
	message := agents.Message{Role: "user", Content: "用这张图生成四视角图片\n[历史图片 1，句柄 @img1：用户上传]"}
	tool, arguments := plan(t, []agents.Message{message}, allTools)
	if refs := referenceList(arguments); tool != "create_multiview_images" || len(refs) != 1 || refs[0] != "@img1" {
		t.Fatalf("got %s %#v", tool, arguments)
	}
}

// #189: Creative Lab template words pick create_creative_lab_design with the
// template's model_id; asking to make the preview printable picks the build.
func TestRoutesCreativeLabTemplatesAndBuilds(t *testing.T) {
	tools := append(append([]agents.Tool{}, allTools...), agents.Tool{Name: "create_creative_lab_design"}, agents.Tool{Name: "build_creative_lab_item"})
	cases := []struct {
		name     string
		system   []agents.Message
		text     string
		wantTool string
		wantArgs map[string]any
	}{
		{name: "keychain from a handle", text: "用 @img2 做一个钥匙扣", wantTool: "create_creative_lab_design",
			wantArgs: map[string]any{"template": "creative-lab-keychain-prototype", "reference_handles": []any{"@img2"}}},
		{name: "vinyl wins over figure", system: []agents.Message{selectionPrompt("@img5")}, text: "把它做成潮玩手办", wantTool: "create_creative_lab_design",
			wantArgs: map[string]any{"template": "creative-lab-vinyl-figure-prototype", "reference_handles": []any{"@img5"}}},
		{name: "brick figure", text: "用 @img1 做积木人仔", wantTool: "create_creative_lab_design",
			wantArgs: map[string]any{"template": "creative-lab-brick-figure-prototype", "reference_handles": []any{"@img1"}}},
		{name: "figure even though the words say 3D model", text: "把 @img3 做成手办 3D 模型", wantTool: "create_creative_lab_design",
			wantArgs: map[string]any{"template": "creative-lab-figure-prototype", "reference_handles": []any{"@img3"}}},
		{name: "collapsible fidget", text: "用 @img4 做一个折叠玩具", wantTool: "create_creative_lab_design",
			wantArgs: map[string]any{"template": "creative-lab-fidget-collapsible-generate", "reference_handles": []any{"@img4"}}},
		{name: "pixel art board", text: "用 @img4 做像素拼图", wantTool: "create_creative_lab_design",
			wantArgs: map[string]any{"template": "creative-lab-fidget-pixel-prototype", "reference_handles": []any{"@img4"}}},
		{name: "build the selected preview", system: []agents.Message{selectionPrompt("@img8")}, text: "把这个预览做成成品", wantTool: "build_creative_lab_item",
			wantArgs: map[string]any{"source_task": "@img8"}},
		{name: "build a named preview", text: "把 @img6 做成可打印的模型", wantTool: "build_creative_lab_item",
			wantArgs: map[string]any{"source_task": "@img6"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := append(append([]agents.Message{}, tc.system...), agents.Message{Role: "user", Content: tc.text})
			tool, arguments := plan(t, messages, tools)
			if tool != tc.wantTool || !reflect.DeepEqual(arguments, tc.wantArgs) {
				t.Fatalf("got %s %#v, want %s %#v", tool, arguments, tc.wantTool, tc.wantArgs)
			}
		})
	}
	// Without the Creative Lab tools on offer the same words keep their old route.
	if tool, _ := plan(t, []agents.Message{{Role: "user", Content: "把 @img3 做成手办 3D 模型"}}, allTools); tool != "create_model_3d_from_image" {
		t.Fatalf("without Creative Lab the figure request went to %s", tool)
	}
}

// #189 (dev-test note 98826): a build whose source did not pair is refused
// with the handles of this session's previews that would. Like a real Agent,
// the mock calls the build again once with the newest of them; a second
// refusal ends the turn with that refusal instead of looping.
func TestRetriesACreativeLabBuildWithTheNamedPreviewOnce(t *testing.T) {
	adapter := newRoutingAdapter(t)
	tools := append(append([]agents.Tool{}, allTools...), agents.Tool{Name: "build_creative_lab_item"})
	refusal := `{"error":"本轮的输入是画板快照……本会话中可以用作来源的：@img2, @img7。","success":false,"task_id":""}`
	call := func(id, source string) agents.Message {
		return agents.Message{Role: "assistant", ToolCalls: []agents.ToolCall{{ID: id, Name: "build_creative_lab_item",
			Arguments: json.RawMessage(`{"source_task":"` + source + `"}`)}}}
	}
	messages := []agents.Message{
		{Role: "user", Content: "把预览做成可打印的成品"},
		call("call-1", "@img3"),
		{Role: "tool", ToolCallID: "call-1", Content: refusal},
	}
	completion, err := adapter.Stream(context.Background(), agents.Request{Messages: messages, Tools: tools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "build_creative_lab_item" ||
		string(completion.ToolCalls[0].Arguments) != `{"source_task":"@img2"}` {
		t.Fatalf("retry = %+v", completion)
	}
	messages = append(messages, call("call-2", "@img2"), agents.Message{Role: "tool", ToolCallID: "call-2", Content: refusal})
	completion, err = adapter.Stream(context.Background(), agents.Request{Messages: messages, Tools: tools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 0 || completion.Content == "" {
		t.Fatalf("second refusal must end the turn: %+v", completion)
	}
}

// #321 D: asked to segment "again" while the previous result is selected, the
// mock goes back to the session's first model, as the real Agent did on
// dev-test; the processing tool then asks before that paid step.
func TestSegmentAgainGoesBackToTheFirstModel(t *testing.T) {
	messages := []agents.Message{
		selectionPrompt("@model2"),
		{Role: "user", Content: "把这个模型分割成部件"},
		{Role: "assistant", Content: "[@model1 生成的 3D 模型]"},
		{Role: "assistant", Content: "已完成。[@model2 分件结果]"},
		{Role: "user", Content: "再分割一次"},
	}
	name, arguments := plan(t, messages, allTools)
	if name != "process_model" || arguments["step"] != "mesh-segmentation" || arguments["source_task_id"] != "@model1" {
		t.Fatalf("again = %s %v", name, arguments)
	}
	// A model the person names is used as named.
	messages[len(messages)-1].Content = "用 @model2 再分割一次"
	if _, arguments := plan(t, messages, allTools); arguments["source_task_id"] != "@model2" {
		t.Fatalf("named = %v", arguments)
	}
	// #321 H: "用原模型" answers the tool's proposal without the handle; the
	// mock passes the session's first model and names no service.
	messages[len(messages)-1].Content = "用原模型，用 Bang"
	name, arguments = plan(t, messages, allTools)
	if name != "process_model" || arguments["source_task_id"] != "@model1" || arguments["offering_id"] != nil {
		t.Fatalf("original model = %s %v", name, arguments)
	}
}

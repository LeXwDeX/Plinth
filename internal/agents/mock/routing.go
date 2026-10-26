package mock

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// 本文件让 mock Agent 按用户这句话的意图选工具，而不是每次都调 refine_image。
// 关键词是测试用词表（写死在 mock 里，不进 Product Schema）；工具名与参数
// 沿用 Schema 声明的真实工具契约，所以后面的批准、冻结、提交、结果卡都走
// 真实路径。请求里没有某个工具时，对应意图退回默认路径。
// 仅 AGENT_MODE=mock 会构造本适配器。

const (
	toolRefineImage          = "refine_image"
	toolRemoveBackground     = "remove_background"
	toolCreateModel3D        = "create_model_3d"
	toolCreateModelFromImg   = "create_model_3d_from_image"
	toolRetextureModel       = "retexture_model"
	toolProcessModel         = "process_model"
	toolConvertModelFormat   = "convert_model_format"
	toolPlanProcessing       = "plan_model_processing"
	toolCreateThreeView      = "create_three_view"
	toolCreateMultiview      = "create_multiview_images"
	toolEditMultiview        = "edit_multiview_images"
	toolUpdateSessionNotes   = "update_session_notes"
	toolCreativeLabDesign    = "create_creative_lab_design"
	toolCreativeLabBuild     = "build_creative_lab_item"
	selectionMarker          = "当前选中对象"
	sessionNotesOpenMarker   = "<<<SESSION_NOTES>>>"
	selectedAlias            = "@selected"
	defaultAnimationActionID = 0
)

var (
	imageHandlePattern = regexp.MustCompile(`@img\d+`)
	modelHandlePattern = regexp.MustCompile(`@model\d+`)
	rememberPrefix     = regexp.MustCompile(`^[\s，,：:]*(请)?(帮我)?(记住|设定)[\s，,：:]*`)
	polycountPattern   = regexp.MustCompile(`(\d+)\s*(万)?\s*面`)
	uploadedPattern    = regexp.MustCompile(`句柄\s*(@img\d+)[：:]\s*用户上传`)
	// 只有明确的编辑动词才把四视角请求当作编辑；“保持/再来/继续”是新的一组。
	multiviewEditPattern = regexp.MustCompile(`编辑|修改|改成|换成|改为`)
	editVerbPattern      = regexp.MustCompile(`换成|改成|改为|改一下|调成|调整|重画|重绘|同一|保持|按.*(重画|画)|再来|继续`)
	formatWordPattern    = regexp.MustCompile(`(?i)(gltf|glb|fbx|obj|usdz|stl|3mf)`)
)

// multiviewPrompts 是四视角编辑里视角词到 edit_multiview_images 参数的对应，按 front、left、back、right 排列。
var multiviewPrompts = []struct {
	word      string
	parameter string
}{
	{"正面", "front_prompt"}, {"左", "left_prompt"}, {"背面", "back_prompt"}, {"右", "right_prompt"},
}

// animationActions 是常见动作词到 Meshy 官方动作库 action_id 的对应（见 Offering 的
// action_id 选项）：0 Idle、33 Chair_Sit_Idle_M、290 Wave_One_Hand、30 Casual_Walk、14 Run_02。
var animationActions = []struct {
	word string
	id   int
}{
	{"待机", 0}, {"坐", 33}, {"挥手", 290}, {"走", 30}, {"跑", 14},
}

// creativeLabTemplates 是 Creative Lab 模板词到 create_creative_lab_design 的
// template 取值（Offering model_id）的对应；“潮玩”“积木”排在“手办”前，
// 让“潮玩手办”“积木手办”落到各自模板。
var creativeLabTemplates = []struct {
	word     string
	template string
}{
	{"钥匙扣", "creative-lab-keychain-prototype"},
	{"冰箱贴", "creative-lab-fridge-magnet-prototype"},
	{"潮玩", "creative-lab-vinyl-figure-prototype"},
	{"积木", "creative-lab-brick-figure-prototype"},
	{"手办", "creative-lab-figure-prototype"},
	{"灯罩", "creative-lab-lamp-prototype"},
	{"键帽", "creative-lab-keycap-prototype"},
	{"像素拼图", "creative-lab-fidget-pixel-prototype"},
	{"像素解压", "creative-lab-fidget-pixel-prototype"},
	{"折叠玩具", "creative-lab-fidget-collapsible-generate"},
	{"折叠解压", "creative-lab-fidget-collapsible-generate"},
}

// creativeLabBuildPattern 是“把预览做成可打印成品”的说法。
var creativeLabBuildPattern = regexp.MustCompile(`做成成品|构建成品|生成成品|打印模型|做成可打印`)

type selection struct {
	images []string
	models []string
}

// currentSelection 取出系统说明里“当前选中对象”一节列出的句柄。
func (adapter *Adapter) currentSelection(messages []agents.Message) selection {
	var found selection
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role == adapter.userRole || message.Role == adapter.toolRole {
			continue
		}
		// The session notes follow the selection and their frame warns about the very
		// phrase that marks it, so the notes are cut off before the marker is looked for.
		content := message.Content
		if cut := strings.Index(content, sessionNotesOpenMarker); cut >= 0 {
			content = content[:cut]
		}
		position := strings.LastIndex(content, selectionMarker)
		if position < 0 {
			continue
		}
		section := content[position:]
		found.images = uniqueMatches(imageHandlePattern, section)
		found.models = uniqueMatches(modelHandlePattern, section)
		return found
	}
	return found
}

// earlierModel is the first model handle the conversation shows that is not
// selected now (system text excluded).
func (adapter *Adapter) earlierModel(messages []agents.Message, selected []string) string {
	for _, message := range messages {
		if message.Role != adapter.userRole && message.Role != "assistant" {
			continue
		}
		for _, handle := range modelHandlePattern.FindAllString(message.Content, -1) {
			if !slices.Contains(selected, handle) {
				return handle
			}
		}
	}
	return ""
}

// uploadedHandles 取出用户这一轮上传的图片句柄（标签里写着“用户上传”）。
func (adapter *Adapter) uploadedHandles(messages []agents.Message) []string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != adapter.userRole {
			continue
		}
		var combined strings.Builder
		combined.WriteString(message.Content)
		for _, part := range message.Parts {
			combined.WriteString("\n")
			combined.WriteString(part.Text)
		}
		var handles []string
		for _, match := range uploadedPattern.FindAllStringSubmatch(combined.String(), -1) {
			handles = append(handles, match[1])
		}
		return handles
	}
	return nil
}

func uniqueMatches(pattern *regexp.Regexp, text string) []string {
	var result []string
	seen := map[string]bool{}
	for _, match := range pattern.FindAllString(text, -1) {
		if !seen[match] {
			seen[match] = true
			result = append(result, match)
		}
	}
	return result
}

func (adapter *Adapter) hasTool(request agents.Request, name string) bool {
	for _, tool := range request.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// withheld reports whether the session's Agent scope hides the tool from this
// turn: the tool exists, but this turn's Agent has not been offered it.
func (adapter *Adapter) withheld(request agents.Request, name string) bool {
	for _, withheld := range request.ScopeWithheld {
		if withheld == name {
			return true
		}
	}
	return false
}

// wants is the intent test of route: a keyword picks its tool whether the tool
// is offered or only withheld by the scope, so a request the scope forbids is
// recognised (and refused by Stream) instead of falling through to another
// tool that happens to be offered.
func (adapter *Adapter) wants(request agents.Request, name string) bool {
	return adapter.hasTool(request, name) || adapter.withheld(request, name)
}

func hasImagePart(messages []agents.Message, userRole string) bool {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != userRole {
			continue
		}
		for _, part := range messages[index].Parts {
			if part.URL != "" {
				return true
			}
		}
		return false
	}
	return false
}

// variantPattern 匹配“出 3 个天气：雪夜、正午、雾”这类要几个变体的说法。
var variantPattern = regexp.MustCompile(`[:：]\s*([^，。,.:：\n]+(?:[、，,][^，。,.:：\n]+)+)\s*$`)

type plannedCall struct {
	name      string
	arguments map[string]any
}

// routeCalls 在 route 的基础上把“列了几个变体”的改图请求拆成每个变体一次
// 工具调用，每次的提示词带上自己的变体名，结果因此各不相同。
func (adapter *Adapter) routeCalls(request agents.Request, text string) []plannedCall {
	name, arguments := adapter.route(request, text)
	if name == "" {
		return nil
	}
	if name == toolRefineImage {
		if match := variantPattern.FindStringSubmatch(strings.TrimSpace(text)); match != nil {
			variants := splitVariants(match[1])
			if len(variants) >= 2 && len(variants) <= 6 {
				calls := make([]plannedCall, 0, len(variants))
				for _, variant := range variants {
					copied := map[string]any{}
					for key, value := range arguments {
						copied[key] = value
					}
					copied["prompt"] = text + "（变体：" + variant + "）"
					calls = append(calls, plannedCall{name: name, arguments: copied})
				}
				return calls
			}
		}
	}
	return []plannedCall{{name: name, arguments: arguments}}
}

func splitVariants(list string) []string {
	var variants []string
	for _, piece := range regexp.MustCompile(`[、，,]`).Split(list, -1) {
		if piece = strings.TrimSpace(piece); piece != "" {
			variants = append(variants, piece)
		}
	}
	return variants
}

// route 返回 (工具名, 参数)；空工具名表示没有命中关键词，走默认路径。
func (adapter *Adapter) route(request agents.Request, text string) (string, map[string]any) {
	lower := strings.ToLower(text)
	chosen := selection{}
	if len(request.Messages) > 0 {
		chosen = adapter.currentSelection(request.Messages)
	}
	imageHandle := firstOr(imageHandlePattern.FindString(text), "")
	modelHandle := firstOr(modelHandlePattern.FindString(text), "")
	if modelHandle == "" && len(chosen.models) > 0 {
		modelHandle = chosen.models[0]
	}
	uploads := adapter.uploadedHandles(request.Messages)
	imageFromSelection := ""
	if len(chosen.images) > 0 {
		imageFromSelection = chosen.images[0]
	}
	switch {
	case adapter.wants(request, toolUpdateSessionNotes) &&
		(strings.Contains(text, "记住") || strings.HasPrefix(strings.TrimSpace(text), "设定")):
		notes := strings.TrimSpace(rememberPrefix.ReplaceAllString(text, ""))
		if notes == "" {
			notes = strings.TrimSpace(text)
		}
		return toolUpdateSessionNotes, map[string]any{"notes": notes}

	case adapter.wants(request, toolEditMultiview) && strings.Contains(text, "四视角") &&
		!strings.Contains(text, "生成") && multiviewEditPattern.MatchString(text):
		// 来源是该四视角组里的一张图（句柄或当前选中），服务端解析为它的公开 Task；
		// 句中点名的视角各带一条说明，没点名时只改正面。
		args := map[string]any{"source_task": firstOr(imageHandle, firstOr(imageFromSelection, selectedAlias))}
		for _, view := range multiviewPrompts {
			if strings.Contains(text, view.word) {
				args[view.parameter] = text
			}
		}
		if len(args) == 1 {
			args["front_prompt"] = text
		}
		return toolEditMultiview, args

	case adapter.wants(request, toolCreateMultiview) && strings.Contains(text, "四视角"):
		// 恰好一张参考图：点名的句柄、当前选中，或随这句话上传的图。
		upload := ""
		if len(uploads) > 0 {
			upload = uploads[len(uploads)-1]
		}
		handle := firstOr(imageHandle, firstOr(imageFromSelection, firstOr(upload, selectedAlias)))
		return toolCreateMultiview, map[string]any{"reference_handles": []string{handle}}

	case adapter.wants(request, toolCreateThreeView) && strings.Contains(text, "三视图"):
		handle := firstOr(imageHandle, firstOr(imageFromSelection, selectedAlias))
		return toolCreateThreeView, map[string]any{"reference_handles": []string{handle}}

	case adapter.wants(request, toolRetextureModel) &&
		(strings.Contains(text, "换贴图") || strings.Contains(text, "重贴图") || strings.Contains(lower, "retexture")):
		args := map[string]any{"source_task_id": firstOr(modelHandle, selectedAlias)}
		if imageHandle != "" {
			// 风格参考图与文字描述二选一：有图用图。
			args["reference_handles"] = []string{imageHandle}
		} else {
			args["prompt"] = text
		}
		return toolRetextureModel, args

	case adapter.wants(request, toolPlanProcessing) &&
		(strings.Contains(text, "重拓扑") || strings.Contains(lower, "retopo") || strings.Contains(lower, "remesh")) &&
		(strings.Contains(text, "绑骨") || strings.Contains(lower, "rig")) &&
		(strings.Contains(text, "动画") || strings.Contains(lower, "animat")):
		return toolPlanProcessing, map[string]any{
			"source_task_id": firstOr(modelHandle, selectedAlias),
			"steps": []map[string]any{
				{"capability": "remesh", "parameters": map[string]any{}},
				{"capability": "rigging", "parameters": map[string]any{}},
				{"capability": "animation", "parameters": map[string]any{"action_id": animationAction(text)}},
			},
		}

	case adapter.wants(request, toolProcessModel) && strings.Contains(text, "原模型") &&
		modelHandlePattern.FindString(text) == "" && adapter.earlierModel(request.Messages, chosen.models) != "":
		// "用原模型": the answer to the tool's proposal of the model the selected one was made from
		// (#321 H), without its handle and without a service; the tool keeps to the service named.
		return toolProcessModel, map[string]any{"step": "mesh-segmentation", "source_task_id": adapter.earlierModel(request.Messages, chosen.models)}

	case adapter.wants(request, toolProcessModel) && strings.Contains(text, "再分割一次") &&
		modelHandlePattern.FindString(text) == "" && adapter.earlierModel(request.Messages, chosen.models) != "":
		// "再分割一次" with the previous result selected: like the real Agent on dev-test, the mock
		// goes back to the session's first model; the tool asks before that paid step (#321 D).
		return toolProcessModel, map[string]any{"step": "mesh-segmentation", "source_task_id": adapter.earlierModel(request.Messages, chosen.models)}

	case adapter.wants(request, toolProcessModel) &&
		(strings.Contains(text, "模型分割") || strings.Contains(text, "分割") || strings.Contains(text, "分件") || strings.Contains(lower, "segment")):
		// The service is not named here: with several compatible ones the tool asks on a card (#321).
		return toolProcessModel, map[string]any{"step": "mesh-segmentation", "source_task_id": firstOr(modelHandle, selectedAlias)}

	case adapter.wants(request, toolProcessModel) &&
		(strings.Contains(text, "动画") || strings.Contains(text, "绑骨") || strings.Contains(text, "重拓扑") ||
			strings.Contains(lower, "retopo") || strings.Contains(lower, "remesh")):
		args := map[string]any{"source_task_id": firstOr(modelHandle, selectedAlias)}
		switch {
		case strings.Contains(text, "动画"):
			args["step"] = "animation"
			args["action_id"] = animationAction(text)
		case strings.Contains(text, "绑骨"):
			args["step"] = "rigging"
		default:
			args["step"] = "remesh"
			if match := polycountPattern.FindStringSubmatch(text); match != nil {
				count := atoi(match[1])
				if match[2] != "" {
					count *= 10000
				}
				if count > 0 {
					args["target_polycount"] = count
				}
			}
		}
		return toolProcessModel, args

	case adapter.wants(request, toolRemoveBackground) &&
		(strings.Contains(text, "去背景") || strings.Contains(text, "抠图")):
		args := map[string]any{}
		if imageHandle != "" {
			args["reference_handles"] = []string{imageHandle}
		}
		return toolRemoveBackground, args

	case adapter.wants(request, toolCreativeLabBuild) && creativeLabBuildPattern.MatchString(text):
		// 来源是同一会话里的设计预览图（句柄或当前选中），服务端据它配对成品构建。
		return toolCreativeLabBuild, map[string]any{"source_task": firstOr(imageHandle, firstOr(imageFromSelection, selectedAlias))}

	case adapter.wants(request, toolCreativeLabDesign) && creativeLabTemplate(text) != "":
		upload := ""
		if len(uploads) > 0 {
			upload = uploads[len(uploads)-1]
		}
		args := map[string]any{"template": creativeLabTemplate(text)}
		if handle := firstOr(imageHandle, firstOr(imageFromSelection, upload)); handle != "" {
			args["reference_handles"] = []string{handle}
		}
		return toolCreativeLabDesign, args

	case adapter.wants(request, toolConvertModelFormat) &&
		(strings.Contains(text, "格式转换") || strings.Contains(text, "转换格式") ||
			strings.Contains(text, "转换成") || strings.Contains(text, "转换为") ||
			strings.Contains(text, "转成") || strings.Contains(lower, "convert format") ||
			strings.Contains(lower, "convert to") || strings.Contains(lower, "convert ")) &&
		formatWordPattern.MatchString(lower):
		return toolConvertModelFormat, map[string]any{
			"source_task_id": firstOr(modelHandle, selectedAlias),
			"format":         formatWordPattern.FindString(lower),
		}

	case strings.Contains(lower, "3d") || strings.Contains(text, "模型") || strings.Contains(text, "建模"):
		reference := firstOr(imageHandle, imageFromSelection)
		if adapter.wants(request, toolCreateModelFromImg) &&
			(reference != "" || hasImagePart(request.Messages, adapter.userRole)) {
			args := map[string]any{"prompt": text}
			if reference != "" {
				args["reference_handles"] = []string{reference}
			}
			return toolCreateModelFromImg, args
		}
		if adapter.wants(request, toolCreateModel3D) {
			return toolCreateModel3D, map[string]any{"prompt": text}
		}

	case adapter.wants(request, toolRefineImage) && imageHandle != "":
		return toolRefineImage, map[string]any{"prompt": text, "reference_handles": []string{imageHandle}}

	case adapter.wants(request, toolRefineImage) && (len(uploads) > 0 || (imageFromSelection != "" && editVerbPattern.MatchString(text))):
		// 用户上传了图，或在选中的图上提了修改要求：上传图和选中图一起当参考。
		references := append([]string(nil), uploads...)
		if imageFromSelection != "" {
			references = append(references, imageFromSelection)
		}
		return toolRefineImage, map[string]any{"prompt": text, "reference_handles": references}
	}
	return "", nil
}

func creativeLabTemplate(text string) string {
	for _, entry := range creativeLabTemplates {
		if strings.Contains(text, entry.word) {
			return entry.template
		}
	}
	return ""
}

func firstOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func atoi(text string) int {
	value := 0
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func animationAction(text string) int {
	for _, action := range animationActions {
		if strings.Contains(text, action.word) {
			return action.id
		}
	}
	return defaultAnimationActionID
}

// basedOnHandle 找出上一次工具调用所基于的对象句柄，用于总结第一行的“基于 @imgN”。
func (adapter *Adapter) basedOnHandle(messages []agents.Message) string {
	chosen := adapter.currentSelection(messages)
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if len(message.ToolCalls) == 0 {
			continue
		}
		var arguments struct {
			ReferenceHandles []string `json:"reference_handles"`
			SourceTaskID     string   `json:"source_task_id"`
		}
		_ = json.Unmarshal(message.ToolCalls[0].Arguments, &arguments)
		handle := ""
		if len(arguments.ReferenceHandles) > 0 {
			handle = arguments.ReferenceHandles[0]
		} else if strings.HasPrefix(arguments.SourceTaskID, "@") {
			handle = arguments.SourceTaskID
		}
		// 参考里有当前选中的图，就以它为“基于”的对象；否则取第一个参考。
		if len(chosen.images) > 0 {
			for _, reference := range arguments.ReferenceHandles {
				if reference == chosen.images[0] {
					return reference
				}
			}
		}
		if handle == selectedAlias {
			switch {
			case len(chosen.images) > 0 && message.ToolCalls[0].Name != toolProcessModel && message.ToolCalls[0].Name != toolRetextureModel:
				return chosen.images[0]
			case len(chosen.models) > 0:
				return chosen.models[0]
			}
			return ""
		}
		if handle == "" {
			// 工具调用没带任何参考：不能说“基于”某个对象。
			if name := message.ToolCalls[0].Name; (name == toolProcessModel || name == toolRetextureModel) && len(chosen.models) > 0 {
				return chosen.models[0]
			}
			return ""
		}
		return handle
	}
	return ""
}

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

// 提示词一致性 lint（#383）。
//
// 系统提示词由二十多段拼起来：多数段落是 Catalog 里的固定文案，只有少数按当次工具门控。
// 模型只看得见拼好的文本和当次工具表，所以文本点名的工具、界面名和参数必须与当次工具表、
// 前端文案键和参数表一致。不一致时模型会去调一个不存在的工具、让用户去一个前端没有的
// 设置，或按已经去掉的参数调用。这些矛盾在真实流量里表现为“模型照着提示词做错事”，
// 而各段的单测只断言自己那一段的渲染，于是没人发现。
//
// 这里不新建平行管线：每种组合都走 compileToolRegistry → harnessRegistry → agentRequest
// 这条线上路径，断言的是真实请求。
//
// 四组断言：
//
//	工具名：范围块（【Agent 范围】）点名的工具、Harness 段快照的 executable_tools 都等于
//	        当次工具表；点名不可用的工具、或有工具没告诉模型，都算失败。
//	界面名：文案里用 「」/“” 引用的界面名是这套文案的键（默认语言查 content.messages，
//	        其它语言查该语言的 bundle），且 src/ 真的在用；点名界面位置的句子
//	        （在…设置/面板/页签/顶栏里）也必须落在真实键上。
//	参数名：工具描述点名的参数是这次调用真正接受的参数。Harness 收窄参数表后描述若原样
//	        保留，模型会按已经去掉的参数调用。
//	批准方式：描述一旦列举批准方式，就要把 Catalog 声明的每一档都列上。
//
// 静态提示词（chat.agent.system_prompt）与各 craft 段目前不按工具门控，仍会点名当次不
// 可用的工具。把它们纳入断言要改提示词组装（26 段里只有 8 段门控），不在本 Issue 范围，
// 所以这里只打印缺口、不假装已经守住。
type promptConsistencyTurn struct {
	label    string
	scope    string
	op       string
	snapshot *resolve.Snapshot
	request  agents.Request
	registry *toolRegistry
}

// promptConsistencyTurns 覆盖每个 Harness 操作 × 每个 Agent 范围，外加每个范围下的自由模式。
func promptConsistencyTurns(t *testing.T, orchestrator *Orchestrator, schema catalog.ChatSchema) []promptConsistencyTurn {
	t.Helper()
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	orchestrator.registry = registry
	orchestrator.agentScope = agentscope.FromSchema(schema.AgentScope)
	turns := make([]promptConsistencyTurn, 0, len(schema.Harness.Operations)+len(schema.AgentScope.Modes))
	for _, mode := range schema.AgentScope.Modes {
		turns = append(turns, orchestrator.promptConsistencyTurn(schema, mode.ID, promptConsistencyTurnLabel{}))
		for _, operation := range schema.Harness.Operations {
			turns = append(turns, orchestrator.promptConsistencyTurn(schema, mode.ID,
				promptConsistencyTurnLabel{id: operation.ID, text: operation.Label}))
		}
	}
	return turns
}

// promptConsistencyTurnLabel 是一个 Harness 操作在问题清单里的名字。
type promptConsistencyTurnLabel struct {
	id   string
	text string
}

// promptConsistencyTurn 渲染一种组合：Harness 选定一个操作（id 为空则自由模式）。
func (orchestrator *Orchestrator) promptConsistencyTurn(
	schema catalog.ChatSchema,
	scope string,
	operation promptConsistencyTurnLabel,
) promptConsistencyTurn {
	options := TurnOptions{AgentScope: scope}
	if operation.id != "" {
		declared, found := promptConsistencyHarnessOperation(schema.Harness, operation.id)
		if !found {
			panic("harness 声明里没有操作 " + operation.id)
		}
		options.Harness = promptConsistencySnapshot(declared)
	}
	label := "自由模式"
	if operation.id != "" {
		label = operation.text
	}
	// 走真实请求的那条路：Harness 段与范围块读的是同一个收窄后的、带请求缓存的注册表。
	ctx := context.Background()
	if options.Harness != nil {
		ctx = context.WithValue(ctx, harnessContextKey{}, options.Harness)
	}
	registry, err := orchestrator.registryForRequest(ctx)
	if err != nil {
		panic(err)
	}
	return promptConsistencyTurn{
		label:    fmt.Sprintf("范围=%s 操作=%s", scope, label),
		scope:    scope,
		op:       operation.id,
		snapshot: options.Harness,
		request:  orchestrator.agentRequest(ctx, nil, options, "owner", ""),
		registry: registry,
	}
}

// promptConsistencySnapshot 是一个就绪的 Harness 选择：参数在真实请求里由服务端填，
// 这里只需要工具表与参数表跟着收窄。
func promptConsistencySnapshot(operation catalog.HarnessOperationSchema) *resolve.Snapshot {
	return &resolve.Snapshot{
		Selection: harness.Selection{
			OperationID:        operation.ID,
			OfferingID:         "prompt-consistency/offering",
			OfferingRevision:   "v1",
			EnabledOperations:  []string{operation.ID},
			FixedParameters:    map[string]json.RawMessage{},
			FillableParameters: []string{},
		},
		OperationID:    operation.ID,
		ToolName:       operation.ToolName,
		ToolParameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Status:         resolve.StatusReady,
		Input:          map[string]json.RawMessage{},
	}
}

func promptConsistencyHarnessOperation(schema catalog.ChatHarnessSchema, id string) (catalog.HarnessOperationSchema, bool) {
	for _, operation := range schema.Operations {
		if operation.ID == id {
			return operation, true
		}
	}
	return catalog.HarnessOperationSchema{}, false
}

// promptConsistencyFinding 是一条问题：哪一段文案、点了什么名字。
type promptConsistencyFinding struct {
	Kind   string
	Where  string
	Name   string
	Detail string
}

func (finding promptConsistencyFinding) String() string {
	return fmt.Sprintf("[%s] %s: %s %s", finding.Kind, finding.Where, finding.Name, finding.Detail)
}

// TestPromptConsistencyToolNames 断言提示词里点名可用工具的两段与当次工具表一致。
func TestPromptConsistencyToolNames(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	orchestrator := &Orchestrator{schema: schema}
	findings := []promptConsistencyFinding{}
	for _, turn := range promptConsistencyTurns(t, orchestrator, schema) {
		offered := map[string]bool{}
		for _, tool := range turn.request.Tools {
			offered[tool.Name] = true
		}
		prompt := turn.request.Messages[0].Content
		// 范围块自称列出本轮可用与不可用的工具：可用段必须就是工具表，不可用段不许有工具表里的名字。
		scope := orchestrator.scopePrompt(turn.registry, turn.scope)
		if scope == "" || !strings.Contains(prompt, scope) {
			t.Errorf("%s: 范围块没有渲染进提示词", turn.label)
			continue
		}
		allowed, blocked, ok := promptConsistencyScopeLists(scope, schema.AgentScope.ContextTemplate)
		if !ok {
			t.Fatalf("%s: 范围块里没有 {allowed_tools}/{blocked_tools} 两段", turn.label)
		}
		allowedNames := promptConsistencyListNames(allowed, schema.AgentScope.EmptyListText)
		for _, name := range allowedNames {
			if !offered[name] {
				findings = append(findings, promptConsistencyFinding{"工具名", turn.label + " 范围块可用段", name, "点名了当次不可用的工具"})
			}
		}
		for name := range offered {
			if !containsString(allowedNames, name) {
				findings = append(findings, promptConsistencyFinding{"工具名", turn.label + " 范围块可用段", name, "在工具表里但范围块没列"})
			}
		}
		for _, name := range promptConsistencyListNames(blocked, schema.AgentScope.EmptyListText) {
			if offered[name] {
				findings = append(findings, promptConsistencyFinding{"工具名", turn.label + " 范围块不可用段", name, "在工具表里却被列成不可用"})
			}
		}
		if turn.op == "" {
			continue
		}
		// Harness 段：快照 JSON 的 executable_tools 是工具表在文本里的投影。
		block := orchestrator.harnessPrompt(turn.snapshot, turn.registry, turn.scope)
		if !strings.Contains(prompt, block) {
			t.Errorf("%s: Harness 段没有渲染进提示词", turn.label)
			continue
		}
		listed := promptConsistencyExecutableTools(t, block)
		for _, name := range listed {
			if !offered[name] {
				findings = append(findings, promptConsistencyFinding{"工具名", turn.label + " Harness 段", name, "executable_tools 里有不可执行的工具"})
			}
		}
		for name := range offered {
			if !containsString(listed, name) {
				findings = append(findings, promptConsistencyFinding{"工具名", turn.label + " Harness 段", name, "在工具表里但 executable_tools 没列"})
			}
		}
	}
	reportPromptConsistency(t, "工具名", findings)
	reportStaticPromptToolNames(t, orchestrator, schema)
}

// TestPromptConsistencyCopyNames 断言提示词与工具描述引用的界面名、批准方式是真的。
func TestPromptConsistencyCopyNames(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	live := promptConsistencyLiveKeys(t, application)
	findings := []promptConsistencyFinding{}
	for _, copy := range promptConsistencyCopyStrings(application) {
		for _, name := range promptConsistencyQuotedNames(copy.text) {
			keys := promptConsistencyLabelKeys(name, copy.locale, application)
			if len(keys) == 0 {
				// 引号也用来引一句原话或一个反面例子（“不要把它说成手动模式”），
				// 只有能落到文案键的名字才当界面名校验。
				continue
			}
			if !promptConsistencyAnyLive(keys, live) {
				findings = append(findings, promptConsistencyFinding{"界面名", copy.path, name,
					"文案键在 src/ 里没有引用: " + strings.Join(keys, ", ")})
			}
		}
		for _, place := range promptConsistencyPlaces(copy.text) {
			keys := promptConsistencyLabelKeys(place, copy.locale, application)
			if len(keys) == 0 || !promptConsistencyAnyLive(keys, live) {
				findings = append(findings, promptConsistencyFinding{"界面名", copy.path, place, "让用户去一个前端没有的界面位置"})
			}
		}
	}
	// 批准方式：列举就必须列全，漏一档模型就会按不存在的档位回答。
	words := promptConsistencyModeWords(application)
	for _, copy := range promptConsistencyApprovalModeCopy(application) {
		mentioned := promptConsistencyNamedModes(copy.text, words)
		// 括号里列档位是最明确的“只列这几档”，单独按括号里的内容判一次。
		if listed := promptConsistencyModeEnumeration(copy.text, words); len(listed) > 0 {
			mentioned = listed
		}
		if len(mentioned) >= 2 && len(mentioned) < len(application.Chat.ApprovalPolicy.Modes) {
			findings = append(findings, promptConsistencyFinding{"批准方式", copy.path, strings.Join(mentioned, "/"),
				"只列了 " + strings.Join(application.Chat.ApprovalPolicy.Modes, "/") + " 中的一部分"})
		}
	}
	reportPromptConsistency(t, "文案", findings)
}

// TestPromptConsistencyToolParameters 断言工具描述点名的参数是这次调用真正接受的参数。
func TestPromptConsistencyToolParameters(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	orchestrator := &Orchestrator{schema: schema}
	declared := map[string]map[string]bool{}
	for _, tool := range schema.Tools {
		declared[tool.Name] = promptConsistencyParameterNames(t, tool.Parameters)
	}
	for _, tool := range schema.AgentTools {
		declared[tool.Name] = promptConsistencyParameterNames(t, tool.Parameters)
	}
	findings := []promptConsistencyFinding{}
	for _, turn := range promptConsistencyTurns(t, orchestrator, schema) {
		for _, tool := range turn.request.Tools {
			accepted := promptConsistencyParameterNames(t, tool.Parameters)
			for name := range declared[tool.Name] {
				if accepted[name] || !mentionsToolName(tool.Description, name) {
					continue
				}
				findings = append(findings, promptConsistencyFinding{"参数名", turn.label + " " + tool.Name, name,
					"描述在教一个这次调用不接受的名字"})
			}
		}
	}
	reportPromptConsistency(t, "参数名", findings)
}

func reportPromptConsistency(t *testing.T, kind string, findings []promptConsistencyFinding) {
	t.Helper()
	for _, finding := range findings {
		t.Errorf("%s", finding)
	}
	t.Logf("%s一致性：%d 条问题", kind, len(findings))
}

// reportStaticPromptToolNames 打印静态提示词与 craft 段点名了当次不可用工具的数量。
// 这是本 Issue 记下的缺口（26 段里只有 8 段按工具门控），不是断言：把它纳入断言要改
// 提示词组装，不在本分支范围。
func reportStaticPromptToolNames(t *testing.T, orchestrator *Orchestrator, schema catalog.ChatSchema) {
	t.Helper()
	declared := promptConsistencyDeclaredTools(schema)
	worst, worstLabel := 0, ""
	for _, turn := range promptConsistencyTurns(t, orchestrator, schema) {
		offered := map[string]bool{}
		for _, tool := range turn.request.Tools {
			offered[tool.Name] = true
		}
		// 范围块本来就要列出不可用的工具，缺口只数剩下的段落（含 Harness 段的说明文字）。
		static := turn.request.Messages[0].Content
		if scope := orchestrator.scopePrompt(turn.registry, turn.scope); scope != "" {
			static = strings.Replace(static, scope, "", 1)
		}
		missing := []string{}
		for _, name := range declared {
			if !offered[name] && mentionsToolName(static, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > worst {
			worst, worstLabel = len(missing), turn.label
		}
	}
	t.Logf("静态提示词缺口：最多的一种组合（%s）在静态段落里点名了 %d 个当次不可用的工具", worstLabel, worst)
}

// promptConsistencyScopeLists 把范围块切成可用与不可用两段：{allowed_tools} 前面那段
// 字面文字是可用段的抬头、两个占位符之间的文字是分隔线、{blocked_tools} 后面的第一个
// 标点（列表的句号）收尾。只取字面文字，所以 {mode}、{description} 渲染成什么都不影响。
func promptConsistencyScopeLists(block, template string) (string, string, bool) {
	const allowedPlaceholder, blockedPlaceholder = "{allowed_tools}", "{blocked_tools}"
	iAllowed := strings.Index(template, allowedPlaceholder)
	iBlocked := strings.Index(template, blockedPlaceholder)
	if iAllowed < 0 || iBlocked < iAllowed {
		return "", "", false
	}
	lead := promptConsistencyLiteralBefore(template, iAllowed)
	between := template[iAllowed+len(allowedPlaceholder) : iBlocked]
	tail := promptConsistencyLiteralAfter(template, iBlocked+len(blockedPlaceholder))
	if lead == "" || between == "" || tail == "" {
		return "", "", false
	}
	// 工具名里不会出现句号，所以列表以第一个标点收尾。
	closing := tail[:1]
	start := strings.Index(block, lead)
	if start < 0 {
		return "", "", false
	}
	rest := block[start+len(lead):]
	split := strings.Index(rest, between)
	if split < 0 {
		return "", "", false
	}
	allowed, blocked := rest[:split], rest[split+len(between):]
	end := strings.Index(blocked, closing)
	if end < 0 {
		return "", "", false
	}
	return allowed, blocked[:end], true
}

// promptConsistencyLiteralBefore 是一个占位符前面那段文字：从上一个占位符之后开始，
// 因为上一个占位符渲染后不再是花括号。
func promptConsistencyLiteralBefore(template string, position int) string {
	start := strings.LastIndex(template[:position], "}") + 1
	return template[start:position]
}

// promptConsistencyLiteralAfter 是一个占位符后面那段文字：到下一个占位符为止。
func promptConsistencyLiteralAfter(template string, position int) string {
	end := strings.Index(template[position:], "{")
	if end < 0 {
		return template[position:]
	}
	return template[position : position+end]
}

// promptConsistencyListNames 是范围块里逗号分隔的工具名列表。
func promptConsistencyListNames(list, empty string) []string {
	if strings.TrimSpace(list) == "" || strings.TrimSpace(list) == empty {
		return nil
	}
	return strings.Split(list, ", ")
}

// promptConsistencyDeclaredTools 是 Catalog 声明的全部工具名。
func promptConsistencyDeclaredTools(schema catalog.ChatSchema) []string {
	names := map[string]bool{}
	for _, tool := range schema.Tools {
		names[tool.Name] = true
	}
	for _, tool := range schema.AgentTools {
		names[tool.Name] = true
	}
	return sortedNames(names)
}

func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsString(list []string, want string) bool {
	for _, entry := range list {
		if entry == want {
			return true
		}
	}
	return false
}

// mentionsToolName 按整词匹配：工具名都是小写下划线，前后不接同样的字符才算点名，
// 否则 create_model_3d 会命中 create_model_3d_from_views。
func mentionsToolName(text, name string) bool {
	for offset := 0; ; {
		index := strings.Index(text[offset:], name)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(name)
		offset = start + 1
		if start > 0 && isToolNameByte(text[start-1]) {
			continue
		}
		if end < len(text) && isToolNameByte(text[end]) {
			continue
		}
		return true
	}
}

func isToolNameByte(char byte) bool {
	return char == '_' || (char >= '0' && char <= '9') || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

// promptConsistencyExecutableTools 读 Harness 段快照 JSON 里的 executable_tools。
// 快照是一个 JSON 对象，后面还跟着说明文字，所以只解码第一个值。
func promptConsistencyExecutableTools(t *testing.T, block string) []string {
	t.Helper()
	start := strings.Index(block, "{")
	if start < 0 {
		t.Fatalf("Harness 段里没有快照 JSON: %q", block)
	}
	var data struct {
		Tools []string `json:"executable_tools"`
	}
	if err := json.NewDecoder(strings.NewReader(block[start:])).Decode(&data); err != nil {
		t.Fatalf("Harness 段快照不是 JSON: %v", err)
	}
	return data.Tools
}

// promptConsistencyCopy 是一段带出处的文案：locale 决定去哪个 bundle 查界面名。
type promptConsistencyCopy struct {
	path   string
	locale string
	text   string
}

// promptConsistencyCopyStrings 收集提示词会用到、或提示词要求模型照说的文案：
// 工具与 Agent 工具描述、系统提示与 craft 段、Agent 范围、Harness、批准方式、3D 步数。
// 默认语言取 Catalog 的结构体，其它语言取 localization bundle 的 fields/messages。
func promptConsistencyCopyStrings(application catalog.ApplicationSchema) []promptConsistencyCopy {
	schema := application.Chat
	copies := []promptConsistencyCopy{}
	add := func(path, text string) {
		if strings.TrimSpace(text) != "" {
			copies = append(copies, promptConsistencyCopy{path, "default", text})
		}
	}
	for _, tool := range schema.Tools {
		add("chat.tools[name="+tool.Name+"].description", tool.Description)
	}
	for _, tool := range schema.AgentTools {
		add("chat.agent_tools[name="+tool.Name+"].description", tool.Description)
	}
	add("chat.agent.system_prompt", schema.Agent.SystemPrompt)
	for index, prompt := range schema.Agent.CraftPrompts {
		add(fmt.Sprintf("chat.agent.craft_prompts[%d].text", index), prompt.Text)
	}
	add("chat.agent_scope.context_template", schema.AgentScope.ContextTemplate)
	add("chat.agent_scope.rejection_template", schema.AgentScope.RejectionTemplate)
	add("chat.agent_scope.mock_refusal", schema.AgentScope.MockRefusal)
	for _, mode := range schema.AgentScope.Modes {
		add("chat.agent_scope.modes[id="+mode.ID+"].label", mode.Label)
		add("chat.agent_scope.modes[id="+mode.ID+"].description", mode.Description)
	}
	add("chat.harness.context_template", schema.Harness.ContextTemplate)
	add("chat.harness.proposal_message", schema.Harness.ProposalMessage)
	add("chat.harness.missing_parameters_message", schema.Harness.MissingParametersMessage)
	add("chat.harness.missing_conversation_input_message", schema.Harness.MissingConversationInputMessage)
	add("chat.harness.unavailable_message", schema.Harness.UnavailableMessage)
	for _, operation := range schema.Harness.Operations {
		add("chat.harness.operations[id="+operation.ID+"].label", operation.Label)
		add("chat.harness.operations[id="+operation.ID+"].description", operation.Description)
	}
	add("chat.approval_policy.prompt.context_template", schema.ApprovalPolicy.Prompt.ContextTemplate)
	for _, mode := range schema.ApprovalPolicy.Prompt.Modes {
		add("chat.approval_policy.prompt.modes[id="+mode.ID+"].label", mode.Label)
		add("chat.approval_policy.prompt.modes[id="+mode.ID+"].description", mode.Description)
	}
	if application.StepPolicy != nil {
		prompt := application.StepPolicy.Prompt
		add("application.step_policy.prompt.context_template", prompt.ContextTemplate)
		add("application.step_policy.prompt.mode_unknown_rule", prompt.ModeUnknownRule)
		add("application.step_policy.prompt.guidance", prompt.Guidance)
		for _, rule := range prompt.ModeRules {
			add("application.step_policy.prompt.mode_rules[id="+rule.ID+"].rule", rule.Rule)
		}
		messages := application.StepPolicy.Messages
		add("application.step_policy.messages.cap_reached", messages.CapReached)
		add("application.step_policy.messages.extra_steps_limit", messages.ExtraStepsLimit)
		add("application.step_policy.messages.extra_steps_single_limit", messages.ExtraStepsSingleLimit)
		add("application.step_policy.messages.extra_steps_unavailable", messages.ExtraStepsUnavailable)
	}
	if application.Localization == nil {
		return copies
	}
	prefixes := []string{"chat.tools", "chat.agent_tools", "chat.agent.", "chat.agent_scope",
		"chat.harness", "chat.approval_policy", "step_policy."}
	for _, locale := range application.Localization.Locales {
		bundle, ok := application.Localization.Bundles[locale.ID]
		if !ok {
			continue
		}
		for path, text := range bundle.Fields {
			matched := false
			for _, prefix := range prefixes {
				if strings.HasPrefix(path, prefix) {
					matched = true
					break
				}
			}
			if !matched || strings.TrimSpace(text) == "" {
				continue
			}
			copies = append(copies, promptConsistencyCopy{"localization." + locale.ID + " " + path, locale.ID, text})
		}
		for key, text := range bundle.Messages {
			// 界面名只有界面文案键：3D 步数与批准方式的界面名也在里面。
			if !strings.HasPrefix(key, "ui.step_policy.") || strings.TrimSpace(text) == "" {
				continue
			}
			copies = append(copies, promptConsistencyCopy{"localization." + locale.ID + " " + key, locale.ID, text})
		}
	}
	return copies
}

var (
	promptConsistencyQuote     = regexp.MustCompile(`「([^」]+)」|“([^”]+)”`)
	promptConsistencyPlace     = regexp.MustCompile(`(?:在|到|去)([^，。；：\n「」“”]{1,16})(?:里|中|面板|页签)`)
	promptConsistencyLabelLike = regexp.MustCompile(`^[^\s，。、+→（）()“”"'/|？?]+$`)
	// promptConsistencyModeParenthesis 是 "approval mode (auto or manual, ...)" 里的括号。
	promptConsistencyModeParenthesis = regexp.MustCompile(`(?i)approval modes? \(([^)]*)\)`)
)

// promptConsistencySurfaceWords 是文案里点名界面位置时的方位词：出现这些词才算在说界面，
// 否则“在同一次回复里”这类句子会被误判成界面名。
var promptConsistencySurfaceWords = []string{"设置", "面板", "页签", "顶栏", "菜单", "弹窗", "界面"}

// promptConsistencyQuotedNames 是文案里用 「」/“” 引用的名字（占位符不算）。
func promptConsistencyQuotedNames(text string) []string {
	names := []string{}
	for _, match := range promptConsistencyQuote.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if name == "" {
			name = match[2]
		}
		if name == "" || strings.ContainsAny(name, "{}") || !promptConsistencyLabelLike.MatchString(name) {
			continue
		}
		names = append(names, name)
	}
	return names
}

// promptConsistencyPlaces 是文案让用户去的界面位置。带 「」/“” 引用的位置由
// promptConsistencyQuotedNames 单独检查，这里只看没有标出界面名的句子。
func promptConsistencyPlaces(text string) []string {
	places := []string{}
	for _, match := range promptConsistencyPlace.FindAllString(text, -1) {
		if strings.ContainsAny(match, "「」“”") {
			continue
		}
		place := promptConsistencyPlace.FindStringSubmatch(match)[1]
		surface := false
		for _, word := range promptConsistencySurfaceWords {
			if strings.Contains(place, word) {
				surface = true
				break
			}
		}
		if !surface || strings.ContainsAny(place, "{}") {
			continue
		}
		places = append(places, place)
	}
	return places
}

// promptConsistencyApprovalModeCopy 是可能列举批准方式的描述。
func promptConsistencyApprovalModeCopy(application catalog.ApplicationSchema) []promptConsistencyCopy {
	copies := []promptConsistencyCopy{}
	for _, tool := range application.Chat.Tools {
		copies = append(copies, promptConsistencyCopy{"chat.tools[name=" + tool.Name + "].description", "default", tool.Description})
	}
	for _, tool := range application.Chat.AgentTools {
		copies = append(copies, promptConsistencyCopy{"chat.agent_tools[name=" + tool.Name + "].description", "default", tool.Description})
	}
	if application.Localization != nil {
		for locale, bundle := range application.Localization.Bundles {
			for path, text := range bundle.Fields {
				if strings.HasSuffix(path, "].description") {
					copies = append(copies, promptConsistencyCopy{"localization." + locale + " " + path, locale, text})
				}
			}
		}
	}
	return copies
}

// promptConsistencyModeEnumeration 是文案里紧跟 "approval mode" 的括号列举：描述一旦用
// 括号列档位，那串括号里就要列全，否则模型会以为只有括号里那几档。
func promptConsistencyModeEnumeration(text string, words map[string][]string) []string {
	for _, match := range promptConsistencyModeParenthesis.FindAllStringSubmatch(text, -1) {
		return promptConsistencyNamedModes(match[1], words)
	}
	return nil
}

// promptConsistencyNamedModes 是一段文字按整词点到的档位。
func promptConsistencyNamedModes(text string, words map[string][]string) []string {
	mentioned := []string{}
	for mode, vocabulary := range words {
		for _, word := range vocabulary {
			// 描述写的是英文（如 "strict mode"），标签却可能是中文，所以两种写法都认。
			if mentionsToolName(text, word) || mentionsToolName(text, strings.ToLower(word)) {
				mentioned = append(mentioned, mode)
				break
			}
		}
	}
	sort.Strings(mentioned)
	return mentioned
}

// promptConsistencyModeWords 是一个批准方式档位的说法：档位 id，加上它在提示词文案里
// 的标签和界面上那个档位按钮的标签，各语言的都算。英文描述里写的是 "strict mode"，
// 对应的档位 id 是 manual，只认 id 会把已经列全的描述判成漏写。
func promptConsistencyModeWords(application catalog.ApplicationSchema) map[string][]string {
	words := map[string][]string{}
	add := func(id, label string) {
		if strings.TrimSpace(label) == "" {
			return
		}
		words[id] = append(words[id], strings.Fields(label)[0])
	}
	label := map[string]string{}
	for _, mode := range application.Chat.ApprovalPolicy.Prompt.Modes {
		label[mode.ID] = mode.Label
	}
	if application.Localization != nil {
		for _, bundle := range application.Localization.Bundles {
			for id, text := range bundle.Messages {
				if strings.HasPrefix(id, "ui.workspace.approval.mode_") {
					label[strings.TrimPrefix(id, "ui.workspace.approval.mode_")] = text
				}
			}
		}
	}
	for id, text := range application.Content.Messages {
		if strings.HasPrefix(id, "ui.workspace.approval.mode_") {
			if _, ok := label[strings.TrimPrefix(id, "ui.workspace.approval.mode_")]; !ok {
				label[strings.TrimPrefix(id, "ui.workspace.approval.mode_")] = text
			}
		}
	}
	for _, mode := range application.Chat.ApprovalPolicy.Modes {
		words[mode] = append(words[mode], mode)
		add(mode, label[mode])
	}
	return words
}

// promptConsistencyLabelKeys 是这套文案里值等于 name 的文案键：默认语言查
// content.messages，其它语言查该语言的 bundle（messages 与 fields 都算）。
func promptConsistencyLabelKeys(name, locale string, application catalog.ApplicationSchema) []string {
	keys := []string{}
	if locale == "default" {
		for key, text := range application.Content.Messages {
			if text == name {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		return keys
	}
	if application.Localization == nil {
		return keys
	}
	bundle, ok := application.Localization.Bundles[locale]
	if !ok {
		return keys
	}
	for key, text := range bundle.Messages {
		if text == name {
			keys = append(keys, key)
		}
	}
	for path, text := range bundle.Fields {
		if text == name {
			keys = append(keys, path)
		}
	}
	sort.Strings(keys)
	return keys
}

func promptConsistencyAnyLive(keys []string, live map[string]bool) bool {
	for _, key := range keys {
		if live[key] {
			return true
		}
	}
	return false
}

// promptConsistencyLiveKeys 是 src/ 真的在用的前端文案键。
//
// src 有三种引用方式：字符串字面量、Schema 里的 *_key 字段（label_key 这类间接引用，
// 组件按字段值取文案），以及模板串拼出来的键（`ui.account.${key}`）。少认一种就会把
// 在用的键判成死键，所以三种都认。
func promptConsistencyLiveKeys(t *testing.T, application catalog.ApplicationSchema) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	walkErr := filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range promptConsistencyKeyLiteral.FindAllStringSubmatch(string(source), -1) {
			live[match[1]] = true
		}
		for _, match := range promptConsistencyKeyTemplate.FindAllStringSubmatch(string(source), -1) {
			live[match[1]] = true
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	// *_key 字段：Catalog 用它们指向文案键，组件按字段值取文案。
	for _, key := range promptConsistencyIndirectKeys(application) {
		live[key] = true
	}
	return live
}

var (
	promptConsistencyKeyLiteral  = regexp.MustCompile("['\"`]([a-z][a-z0-9_]*(?:\\.[a-z0-9_]+)+)['\"`]")
	promptConsistencyKeyTemplate = regexp.MustCompile("`([a-z][a-z0-9_]*(?:\\.[a-z0-9_]+)+)\\.\\$\\{")
)

// promptConsistencyIndirectKeys 收集 Catalog 里 *_key 字段的取值（label_key 这类）。
func promptConsistencyIndirectKeys(application catalog.ApplicationSchema) []string {
	keys := []string{}
	encoded, err := json.Marshal(application)
	if err != nil {
		return keys
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return keys
	}
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, entry := range typed {
				if strings.HasSuffix(key, "_key") {
					if name, ok := entry.(string); ok {
						keys = append(keys, name)
					}
				}
				walk(entry)
			}
		case []any:
			for _, entry := range typed {
				walk(entry)
			}
		}
	}
	walk(document)
	return keys
}

// promptConsistencyParameterNames 是一个参数表声明的参数名。
func promptConsistencyParameterNames(t *testing.T, parameters json.RawMessage) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	if len(parameters) == 0 {
		return names
	}
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(parameters, &document); err != nil {
		t.Fatalf("参数表不是 JSON: %v", err)
	}
	for name := range document.Properties {
		names[name] = true
	}
	return names
}
package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

type Config struct {
	ToolName       string
	PromptArgument string
	Summary        string
	// FailureSummary closes a turn whose tool failed; {error} is replaced by
	// the tool's own error text. WaitingSummary closes a turn whose step now
	// waits for the person's approval (the tool answered WaitingContent). Both
	// are optional: without them the closing line is Summary.
	FailureSummary string
	WaitingSummary string
	WaitingContent string
	// RechargeContent is the tool result of a step that waits on its card
	// because it repeats a charged attempt whose result was not stored (#294);
	// it names that attempt at {task_id}, so it is matched up to there.
	RechargeContent string
	// DeniedContent is the tool result of a step the person refused on its
	// approval card; DeniedSummary closes such a turn by saying the step was
	// not done and proposing an alternative. Both are optional.
	DeniedContent string
	DeniedSummary string
	// ScopeRefusal is the answer to a request whose tool the session's Agent
	// scope withholds; {tool} is the tool the request needed. Optional.
	ScopeRefusal string
	// ChoiceSummary closes a turn whose tool opened an in-chat choice card
	// (#321): one line asking the person to choose on the card. Optional.
	ChoiceSummary string
	// SummaryTemplate is the answer to a context compaction's summary call
	// (#322); {handles} lists the attachment handles the summarized text
	// names. Optional: without it the summary is the plain Summary.
	SummaryTemplate string
	UserRole        string
	ToolRole        string
	// Localize gives a Schema text in the UI locale of the turn in ctx. Nil
	// keeps the default-locale copy.
	Localize func(context.Context, string) string
}

type Adapter struct {
	toolName       string
	promptArgument string
	summary        string
	failureSummary string
	waitingSummary string
	waitingContent string
	rechargePrefix string
	deniedContent  string
	deniedSummary  string
	scopeRefusal   string
	choiceSummary  string
	summaryTmpl    string
	userRole       string
	toolRole       string
	localize       func(context.Context, string) string
	nextCall       atomic.Uint64
}

func New(config Config) (*Adapter, error) {
	if strings.TrimSpace(config.ToolName) == "" ||
		strings.TrimSpace(config.PromptArgument) == "" ||
		strings.TrimSpace(config.Summary) == "" ||
		strings.TrimSpace(config.UserRole) == "" || strings.TrimSpace(config.ToolRole) == "" {
		return nil, errors.New("mock agent: incomplete schema config")
	}
	return &Adapter{
		toolName:       config.ToolName,
		promptArgument: config.PromptArgument,
		summary:        config.Summary,
		failureSummary: config.FailureSummary,
		waitingSummary: config.WaitingSummary,
		waitingContent: config.WaitingContent,
		rechargePrefix: strings.Split(config.RechargeContent, "{task_id}")[0],
		deniedContent:  config.DeniedContent,
		deniedSummary:  config.DeniedSummary,
		scopeRefusal:   config.ScopeRefusal,
		choiceSummary:  config.ChoiceSummary,
		summaryTmpl:    config.SummaryTemplate,
		userRole:       config.UserRole,
		toolRole:       config.ToolRole,
		localize:       config.Localize,
	}, nil
}

func (adapter *Adapter) Stream(
	ctx context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	if err := refusesPictures(request); err != nil {
		return agents.Completion{}, err
	}
	completion, err := adapter.stream(ctx, request, emit)
	if err != nil {
		return completion, err
	}
	completion.Usage = adapter.usage(request, completion)
	return completion, nil
}

// mockTextOnlyModel in a model name makes the mock refuse pictures like an
// upstream without image input (#322); only the mock reads it.
const mockTextOnlyModel = "text-only"

func refusesPictures(request agents.Request) error {
	if !strings.Contains(request.Model, mockTextOnlyModel) {
		return nil
	}
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			if part.URL != "" {
				return fmt.Errorf("%w: mock model %s reads text only", agents.ErrImagesRejected, request.Model)
			}
		}
	}
	return nil
}

func (adapter *Adapter) stream(
	ctx context.Context,
	request agents.Request,
	emit func(agents.Delta) error,
) (agents.Completion, error) {
	// Follow-up requests keep tool definitions available. A tool result in the
	// conversation is the mock's signal to summarize this operation instead
	// of planning an unrelated second paid generation.
	if len(request.Tools) > 0 && (len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != adapter.toolRole) {
		prompt := adapter.latestUserText(request.Messages)
		if markdownTurn(prompt) {
			return streamMarkdown(ctx, prompt, emit)
		}
		// 意图路由：命中关键词就换成对应的真实工具；没命中沿用历史行为。
		// 本轮在回答选择卡时，先按卡片给出的下一次调用执行（#321）。
		calls, answered := adapter.choiceCalls(request.Messages)
		if !answered {
			calls = adapter.routeCalls(request, prompt)
		}
		if len(calls) == 0 {
			calls = []plannedCall{{name: adapter.toolName, arguments: map[string]any{adapter.promptArgument: prompt}}}
		}
		for index, call := range calls {
			calls[index] = adapter.harnessCall(request, call, prompt)
		}
		// An Agent that knows its scope does not call a tool it was not
		// offered: it says the current mode does not allow the request.
		for _, call := range calls {
			if adapter.scopeRefusal != "" && adapter.withheld(request, call.name) {
				answer := strings.ReplaceAll(adapter.text(ctx, adapter.scopeRefusal), "{tool}", call.name)
				if emit != nil {
					if err := emit(agents.Delta{Content: answer}); err != nil {
						return agents.Completion{}, err
					}
				}
				return agents.Completion{Content: answer}, nil
			}
		}
		toolCalls := make([]agents.ToolCall, 0, len(calls))
		for _, call := range calls {
			arguments, err := json.Marshal(call.arguments)
			if err != nil {
				return agents.Completion{}, err
			}
			toolCalls = append(toolCalls, agents.ToolCall{
				ID:        "mock-call-" + strconv.FormatUint(adapter.nextCall.Add(1), 10),
				Name:      call.name,
				Arguments: arguments,
			})
		}
		return agents.Completion{ToolCalls: toolCalls}, nil
	}
	if retry, ok := adapter.creativeLabBuildRetry(request); ok {
		return agents.Completion{ToolCalls: []agents.ToolCall{retry}}, nil
	}
	summary := adapter.closingSummary(ctx, request.Messages)
	if handle := adapter.basedOnHandle(request.Messages); handle != "" {
		summary = "基于 " + handle + "\n" + summary
	}
	if emit != nil {
		if err := emit(agents.Delta{Content: summary}); err != nil {
			return agents.Completion{}, err
		}
	}
	return agents.Completion{Content: summary}, nil
}

// creativeLabBuildRetry calls the Creative Lab build once more when its
// source did not pair and the refusal names this session's previews that
// would (#189): the newest one is the preview the person means, like a real
// Agent reads it. A turn retries at most once.
func (adapter *Adapter) creativeLabBuildRetry(request agents.Request) (agents.ToolCall, bool) {
	messages := request.Messages
	if len(messages) == 0 || !adapter.wants(request, toolCreativeLabBuild) {
		return agents.ToolCall{}, false
	}
	last := messages[len(messages)-1]
	names := turnToolNames(messages, adapter.userRole)
	if last.Role != adapter.toolRole || names[last.ToolCallID] != toolCreativeLabBuild {
		return agents.ToolCall{}, false
	}
	builds := 0
	for _, name := range names {
		if name == toolCreativeLabBuild {
			builds++
		}
	}
	var result struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if builds != 1 || json.Unmarshal([]byte(last.Content), &result) != nil || result.Success == nil || *result.Success {
		return agents.ToolCall{}, false
	}
	handle := imageHandlePattern.FindString(result.Error)
	if handle == "" {
		return agents.ToolCall{}, false
	}
	arguments, err := json.Marshal(map[string]any{"source_task": handle})
	if err != nil {
		return agents.ToolCall{}, false
	}
	return agents.ToolCall{ID: "mock-call-" + strconv.FormatUint(adapter.nextCall.Add(1), 10),
		Name: toolCreativeLabBuild, Arguments: arguments}, true
}

// closingSummary is the line that ends the turn: the failure when a tool of
// this turn failed, the wait when a step now awaits approval, else the plain
// summary. A real agent reads the same tool results.
//
// Generation and processing tools answer a step they ran with a JSON result.
// A plain-text answer that is not the waiting, recharge or denied text means
// the server refused the step before submitting anything (no model chosen,
// the turn's limit, a missing consent), so it closes the turn as not done.
// Saving the session notes is the one plain-text success: a turn that only
// did that closes with the tool's own confirmation.
func (adapter *Adapter) closingSummary(ctx context.Context, messages []agents.Message) string {
	failure, waiting, denied := "", false, false
	// referenceNote is what a waiting step says about the pictures it uses
	// (#329 E); the reply repeats it.
	referenceNote := ""
	notesSaved, ranStep := "", false
	toolNames := turnToolNames(messages, adapter.userRole)
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role == adapter.userRole {
			break
		}
		if message.Role != adapter.toolRole {
			continue
		}
		if adapter.waitingContent != "" && message.Content == adapter.waitingContent ||
			adapter.rechargePrefix != "" && strings.HasPrefix(message.Content, adapter.rechargePrefix) {
			waiting = true
			continue
		}
		if awaitingApproval(message.Content) {
			// #321 I: a step on its approval card, with the frozen quote.
			waiting = true
			if note := awaitingReferenceNote(message.Content); note != "" && referenceNote == "" {
				referenceNote = note
			}
			continue
		}
		if adapter.deniedContent != "" && message.Content == adapter.deniedContent {
			denied = true
			continue
		}
		if adapter.choiceSummary != "" && choiceOpened(message.Content) {
			// The tool asked the person on a choice card; nothing ran.
			return adapter.text(ctx, adapter.choiceSummary)
		}
		var result struct {
			Success *bool  `json:"success"`
			Error   string `json:"error"`
			PlanID  string `json:"plan_id"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(message.Content), &result) != nil {
			content := strings.TrimSpace(message.Content)
			// The answer is a Schema text (a refusal or the notes
			// confirmation), so it is read in the turn's UI locale.
			switch {
			case toolNames[message.ToolCallID] == toolUpdateSessionNotes:
				if notesSaved == "" {
					notesSaved = adapter.text(ctx, content)
				}
			case content != "" && failure == "":
				failure = adapter.text(ctx, content)
			}
			continue
		}
		ranStep = true
		if result.PlanID != "" && result.Message != "" {
			// A persisted proposal is not a finished model. Its Schema result
			// text tells the person how approval and observation will proceed.
			return result.Message
		}
		if result.Success != nil && !*result.Success && failure == "" {
			failure = result.Error
			if failure == "" {
				failure = message.Content
			}
		}
	}
	switch {
	case failure != "" && adapter.failureSummary != "":
		return strings.ReplaceAll(adapter.text(ctx, adapter.failureSummary), "{error}", failure)
	case denied && adapter.deniedSummary != "":
		return adapter.text(ctx, adapter.deniedSummary)
	case waiting && adapter.waitingSummary != "":
		if referenceNote != "" {
			return adapter.text(ctx, adapter.waitingSummary) + "\n" + referenceNote
		}
		return adapter.text(ctx, adapter.waitingSummary)
	case notesSaved != "" && !ranStep:
		return notesSaved
	}
	return adapter.text(ctx, adapter.summary)
}

// awaitingApproval reports the result of a step that waits on its approval
// card and carries its frozen quote (#321 I).
func awaitingApproval(content string) bool {
	var result struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(content), &result) == nil && result.Status == "awaiting_approval"
}

// awaitingReferenceNote is the waiting result's note that the step uses only
// the first of the pictures given (#329 E); "" when it has none.
func awaitingReferenceNote(content string) string {
	var result struct {
		ReferenceNote string `json:"reference_note"`
	}
	if json.Unmarshal([]byte(content), &result) != nil {
		return ""
	}
	return result.ReferenceNote
}

// turnToolNames maps each tool call of the current turn (after the latest
// user message) to the tool it called.
func turnToolNames(messages []agents.Message, userRole string) map[string]string {
	names := map[string]string{}
	for index := len(messages) - 1; index >= 0 && messages[index].Role != userRole; index-- {
		for _, call := range messages[index].ToolCalls {
			names[call.ID] = call.Name
		}
	}
	return names
}

func (adapter *Adapter) text(ctx context.Context, text string) string {
	if adapter.localize == nil {
		return text
	}
	return adapter.localize(ctx, text)
}

func (adapter *Adapter) Complete(
	_ context.Context,
	request agents.Request,
) (agents.Completion, error) {
	if err := refusesPictures(request); err != nil {
		return agents.Completion{}, err
	}
	if request.Purpose == agents.PurposeSummary {
		return adapter.summarize(request)
	}
	content := adapter.latestUserContent(request.Messages)
	if content == "" {
		content = adapter.summary
	}
	completion := agents.Completion{Content: content}
	completion.Usage = adapter.usage(request, completion)
	return completion, nil
}

// mockUsagePattern lets a user message name the prompt tokens the mock
// reports, so a test can fill the context window on purpose (#322).
var (
	mockUsagePattern  = regexp.MustCompile(`\[mock:usage=(\d+)\]`)
	summaryHandleText = regexp.MustCompile(`@(?:img|model)\d+`)
)

const (
	mockNoUsageMarker     = "[mock:no_usage]"
	mockCompactFailMarker = "[mock:compact_fail]"
)

// usage is what the mock upstream reports for a request: about one token per
// two characters of everything it was sent and answered, like a CJK-heavy
// conversation. [mock:usage=N] in the newest user message reports N prompt
// tokens; [mock:no_usage] reports nothing, like an upstream without usage.
func (adapter *Adapter) usage(request agents.Request, completion agents.Completion) *agents.Usage {
	latest := adapter.latestUserContent(request.Messages)
	if strings.Contains(latest, mockNoUsageMarker) {
		return nil
	}
	sent := 0
	for _, message := range request.Messages {
		sent += utf8.RuneCountInString(message.Content)
		for _, part := range message.Parts {
			sent += utf8.RuneCountInString(part.Text)
		}
		for _, call := range message.ToolCalls {
			sent += utf8.RuneCount(call.Arguments)
		}
	}
	answered := utf8.RuneCountInString(completion.Content)
	for _, call := range completion.ToolCalls {
		answered += utf8.RuneCount(call.Arguments)
	}
	usage := &agents.Usage{PromptTokens: (sent + 1) / 2, CompletionTokens: max(1, (answered+1)/2)}
	if match := mockUsagePattern.FindStringSubmatch(latest); match != nil {
		if value, err := strconv.Atoi(match[1]); err == nil {
			usage.PromptTokens = value
		}
	}
	return usage
}

// summarize answers a compaction's summary call with the Schema template and
// the handles the summarized text names, in first-seen order. A transcript
// with [mock:compact_fail] fails like an upstream error, so the failure path
// can be exercised.
func (adapter *Adapter) summarize(request agents.Request) (agents.Completion, error) {
	var source strings.Builder
	for _, message := range request.Messages {
		if message.Role == adapter.userRole {
			source.WriteString(message.Content)
		}
	}
	if strings.Contains(source.String(), mockCompactFailMarker) {
		return agents.Completion{}, errors.New("mock agent: summary failed on request")
	}
	seen := map[string]bool{}
	var handles []string
	for _, handle := range summaryHandleText.FindAllString(source.String(), -1) {
		if !seen[handle] {
			seen[handle] = true
			handles = append(handles, handle)
		}
	}
	content := adapter.summary
	if adapter.summaryTmpl != "" {
		content = strings.ReplaceAll(adapter.summaryTmpl, "{handles}", strings.Join(handles, " "))
	}
	completion := agents.Completion{Content: content}
	completion.Usage = adapter.usage(request, completion)
	return completion, nil
}

func (adapter *Adapter) latestUserContent(messages []agents.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != adapter.userRole {
			continue
		}
		if strings.TrimSpace(message.Content) != "" {
			return message.Content
		}
		var text strings.Builder
		for _, part := range message.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
		}
		return text.String()
	}
	return ""
}

// latestUserText 是用户这一轮自己写的话：消息带图片部分时，第一个文字部分
// 是用户原文，后面跟的是图片句柄标签和上传提示，不属于意图判断的范围。
func (adapter *Adapter) latestUserText(messages []agents.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != adapter.userRole {
			continue
		}
		if strings.TrimSpace(message.Content) != "" || len(message.Parts) == 0 {
			return ownText(message.Content)
		}
		for _, part := range message.Parts {
			if part.Text != "" {
				return ownText(part.Text)
			}
		}
		return ""
	}
	return ""
}

// ownText 去掉编排器追加在用户原文后面的图片句柄标签。
func ownText(text string) string {
	for _, marker := range []string{"\n[历史图片", "\n[图片 句柄", "\n[3D 模型 句柄"} {
		if cut := strings.Index(text, marker); cut >= 0 {
			text = text[:cut]
		}
	}
	return text
}

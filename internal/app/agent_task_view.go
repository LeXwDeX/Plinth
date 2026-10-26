package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// agentTaskView 是 get_task/observe_task 回给对话 LLM 的公开 Task 事实。
// 只包含平台 Task 身份、Offering 路由、状态、进度、公开失败分类与已落盘
// Asset；上游 remote ID、Provider Opaque、取证字段和输入参数一律不出面。
type agentTaskView struct {
	TaskID     string              `json:"task_id"`
	SessionID  string              `json:"session_id,omitempty"`
	Capability string              `json:"capability"`
	OfferingID string              `json:"offering_id"`
	ProviderID string              `json:"provider_id"`
	ModelID    string              `json:"model_id"`
	State      string              `json:"state"`
	Progress   int                 `json:"progress"`
	Failure    *agentFailureView   `json:"failure,omitempty"`
	Assets     []agentAssetView    `json:"assets"`
	Resources  []agentResourceView `json:"resources,omitempty"`
	// ResultRecovery tells the Agent whether a failed Task's accepted upstream
	// result can be fetched again at no charge with recover_task_result (#295).
	// Absent for a Task that did not fail after its upstream result.
	ResultRecovery *agentResultRecoveryView `json:"result_recovery,omitempty"`
}

type agentResultRecoveryView struct {
	Available      bool   `json:"available"`
	Reason         string `json:"reason,omitempty"`
	RecoveryTaskID string `json:"recovery_task_id,omitempty"`
}

// agentTaskRefetcher is the kernel's upstream refetch (#295) as the Agent's
// task tools use it.
type agentTaskRefetcher interface {
	RefetchEligibility(context.Context, generation.OwnerID, generation.TaskID) (generation.RefetchEligibility, error)
	RefetchUpstreamResult(context.Context, generation.OwnerID, generation.TaskID, generation.RefetchRequester) (generation.RefetchResult, error)
}

// agentRecoveryView projects refetch eligibility for the Agent, with the
// Schema text of a reason in the turn's locale; nil when there is nothing to
// fetch again.
func agentRecoveryView(view generation.RefetchEligibility, chatSchema catalog.ChatSchema, messages map[string]string,
	texts *catalog.TextLocalizer, locale string) *agentResultRecoveryView {
	if view.Reason == generation.RefetchNotApplicable {
		return nil
	}
	result := &agentResultRecoveryView{Available: view.Eligible, RecoveryTaskID: string(view.RefetchTaskID)}
	if !view.Eligible {
		result.Reason = refetchUnavailableText(view, chatSchema, messages, texts, locale)
	}
	return result
}

// refetchUnavailableText is the Agent's Schema text for a refetch reason. For
// repeated_failure, {cause} is the public Schema text of the repeated failure
// class (content message failure.<class>), never the internal detail. The
// template and each Schema value are translated before interpolation.
func refetchUnavailableText(view generation.RefetchEligibility, chatSchema catalog.ChatSchema,
	messages map[string]string, texts *catalog.TextLocalizer, locale string) string {
	text := chatSchema.Agent.RefetchUnavailableContent[string(view.Reason)]
	if texts != nil {
		text = texts.Text(locale, text)
	}
	if view.Reason == generation.RefetchDelivered {
		// #330: the refetch already delivered; the text names that Task.
		return strings.ReplaceAll(text, "{task_id}", string(view.RefetchTaskID))
	}
	if view.Reason != generation.RefetchRepeatedFailure {
		return text
	}
	cause := messages["failure."+string(view.FailureClass)]
	if cause == "" {
		cause = string(view.FailureClass)
	}
	if texts != nil {
		cause = texts.Text(locale, cause)
	}
	return strings.ReplaceAll(text, "{cause}", cause)
}

// newAgentTaskRecoverer runs recover_task_result: only a Task of the command's
// own owner and session, never another session's (reported as not readable).
func newAgentTaskRecoverer(tasks interface {
	agentTaskReader
	agentTaskRefetcher
}, chatSchema catalog.ChatSchema, messages map[string]string,
	texts *catalog.TextLocalizer) func(context.Context, string, string, string) (json.RawMessage, error) {
	return func(ctx context.Context, ownerID, sessionID, taskID string) (json.RawMessage, error) {
		owner, id := generation.OwnerID(ownerID), generation.TaskID(taskID)
		task, err := tasks.Get(ctx, owner, id)
		if err != nil {
			return nil, err
		}
		if sessionID == "" || task.SessionID != sessionID {
			return nil, &generation.Error{Code: generation.ErrNotFound, Operation: "upstream_refetch"}
		}
		result, err := tasks.RefetchUpstreamResult(ctx, owner, id, generation.RefetchByAgent)
		if err != nil && !generation.IsCode(err, generation.ErrConflict) {
			return nil, err
		}
		locale := catalog.LocaleFrom(ctx)
		view := map[string]any{"source_task_id": taskID}
		switch {
		case result.Refetch.Reason == generation.RefetchDelivered:
			// The earlier refetch already delivered (#330): nothing started.
			view["started"] = false
			view["recovery_task_id"] = string(result.Refetch.RefetchTaskID)
			view["reason"] = refetchUnavailableText(result.Refetch, chatSchema, messages, texts, locale)
		case result.TaskID != "":
			view["started"] = true
			view["recovery_task_id"] = string(result.TaskID)
			view["state"] = string(result.Refetch.RefetchState)
			note := chatSchema.Agent.RefetchStartedContent
			if texts != nil {
				note = texts.Text(locale, note)
			}
			view["note"] = strings.ReplaceAll(note, "{task_id}", string(result.TaskID))
		default:
			view["started"] = false
			view["reason"] = refetchUnavailableText(result.Refetch, chatSchema, messages, texts, locale)
		}
		return json.Marshal(view)
	}
}

type agentFailureView struct {
	Class     string `json:"class"`
	Retryable bool   `json:"retryable"`
	Ambiguous bool   `json:"ambiguous,omitempty"`
	// UserDecisionRequired marks a failure the Agent must not retry on its own: the
	// Offering bills in credits and the Schema declares the class as one where the upstream
	// may already have finished and charged. Retryable is then false for the Agent; the
	// person can still retry from the task list. Reason is the Schema text to relay.
	UserDecisionRequired bool   `json:"user_decision_required,omitempty"`
	Reason               string `json:"reason,omitempty"`
}

type agentAssetView struct {
	AssetID   string `json:"asset_id"`
	Role      string `json:"role"`
	Variant   string `json:"variant,omitempty"`
	MediaType string `json:"media_type"`
	// Size is the file size as the page writes it (schema.SizeText, #327), for
	// the Agent to quote instead of converting bytes.
	Size string `json:"size"`
}

type agentResourceView struct {
	Slot  string `json:"slot"`
	State string `json:"state"`
}

type agentTaskReader interface {
	Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error)
}

// newAgentTaskReader 以 owner 作用域读取 Task；跨 owner 与不存在的 Task
// 同样返回错误，工具层统一收敛为 "task not readable"。
func newAgentTaskReader(
	tasks agentTaskReader,
	chatSchema catalog.ChatSchema,
	messages map[string]string,
	texts *catalog.TextLocalizer,
) func(context.Context, string, string) (json.RawMessage, error) {
	return func(ctx context.Context, ownerID, taskID string) (json.RawMessage, error) {
		task, err := tasks.Get(ctx, generation.OwnerID(ownerID), generation.TaskID(taskID))
		if err != nil {
			return nil, err
		}
		locale := catalog.LocaleFrom(ctx)
		view := buildAgentTaskViewIn(task, chatSchema, texts, locale)
		if refetcher, ok := tasks.(agentTaskRefetcher); ok && task.State == generation.TaskFailed {
			if eligibility, err := refetcher.RefetchEligibility(ctx, task.OwnerID, task.ID); err == nil {
				view.ResultRecovery = agentRecoveryView(eligibility, chatSchema, messages, texts, locale)
			}
		}
		return json.Marshal(view)
	}
}

// buildAgentTaskViewIn writes the failure reason in the turn's locale. The
// metered decision is made on the canonical text before this translation.
func buildAgentTaskViewIn(task generation.Task, chatSchema catalog.ChatSchema,
	texts *catalog.TextLocalizer, locale string) agentTaskView {
	view := buildAgentTaskView(task, chatSchema)
	if view.Failure != nil && view.Failure.Reason != "" && texts != nil {
		view.Failure.Reason = texts.Text(locale, view.Failure.Reason)
	}
	return view
}

func buildAgentTaskView(task generation.Task, chatSchema catalog.ChatSchema) agentTaskView {
	view := agentTaskView{
		TaskID:     string(task.ID),
		SessionID:  task.SessionID,
		Capability: string(task.Capability),
		OfferingID: string(task.Offering.ID),
		ProviderID: task.Offering.ProviderID,
		ModelID:    task.Offering.ModelID,
		State:      string(task.State),
		Progress:   task.Progress,
		Assets:     make([]agentAssetView, 0, len(task.Assets)),
	}
	if task.Failure != nil {
		view.Failure = &agentFailureView{
			Class:     string(task.Failure.Class),
			Retryable: task.Failure.Retryable,
			Ambiguous: task.Failure.Ambiguous,
		}
		if reason := generationtool.MeteredFailureMessage(chatSchema, task.Offering.CreditsMetered, task.Failure); reason != "" {
			view.Failure.Retryable = false
			view.Failure.UserDecisionRequired = true
			view.Failure.Reason = reason
		}
	}
	for _, asset := range task.Assets {
		view.Assets = append(view.Assets, agentAssetView{
			AssetID:   string(asset.ID),
			Role:      asset.Role,
			Variant:   asset.Variant,
			MediaType: asset.MediaType,
			Size:      schema.SizeText(asset.Size, true),
		})
	}
	slots := make([]string, 0, len(task.Resources))
	for slot := range task.Resources {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	for _, slot := range slots {
		view.Resources = append(view.Resources, agentResourceView{
			Slot:  slot,
			State: string(task.Resources[slot].State),
		})
	}
	return view
}

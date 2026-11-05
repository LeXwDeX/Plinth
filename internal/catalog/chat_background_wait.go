package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// ChatBackgroundWaitSchema declares "stop waiting, the Task keeps running in
// the background" for a turn that waits on single generation steps (#137).
// The turn ends without cancelling its Tasks; their approvals stay settled as
// submitted and a later turn tells the Agent which of them still run.
type ChatBackgroundWaitSchema struct {
	// Tools are the chat.tools whose wait on their one Task may continue in
	// the background. A Processing Plan tool and every agent tool (a SkillRun
	// such as the three-view group) are never eligible.
	Tools []string `json:"tools"`
	// AfterSeconds is how long a submitted Task must have run before its
	// wait qualifies; a Task the Provider holds in its own queue qualifies at
	// once.
	AfterSeconds int `json:"after_seconds"`
	// MaxListedTasks bounds how many of the session's newest background calls
	// a later turn reads to list the Tasks still running for the Agent.
	MaxListedTasks int `json:"max_listed_tasks"`
	// TurnContent is what the ended turn says to the person.
	TurnContent string `json:"turn_content"`
	// UnavailableMessage answers a request the turn does not qualify for.
	UnavailableMessage string `json:"unavailable_message"`
	// ContextTemplate is the system context a later turn adds while such a
	// Task still runs; {tasks} is one TaskLineTemplate per Task. It tells the
	// Agent to look a Task up with get_task or observe_task before submitting
	// the same generation again.
	ContextTemplate string `json:"context_template"`
	// TaskLineTemplate names one running Task: {task_id}, {capability},
	// {model} and {status}.
	TaskLineTemplate string `json:"task_line_template"`
	// UpstreamQueuedStatus follows the state of a Task the Provider still
	// holds in its own queue.
	UpstreamQueuedStatus string `json:"upstream_queued_status"`
}

// Allows reports whether the tool's single Task wait may continue in the
// background. A nil policy allows nothing.
func (policy *ChatBackgroundWaitSchema) Allows(tool string) bool {
	if policy == nil {
		return false
	}
	for _, name := range policy.Tools {
		if name == tool {
			return true
		}
	}
	return false
}

func validateBackgroundWait(schema ChatSchema, runtime RuntimeSchema) error {
	policy := schema.Agent.BackgroundWait
	route := runtime.HTTP.LegacyRoutes.ChatTurnBackground
	if policy == nil {
		if route != "" {
			return errors.New("http.legacy_routes.chat_turn_background 需要 chat.agent.background_wait")
		}
		return nil
	}
	if len(policy.Tools) == 0 {
		return errors.New("chat.agent.background_wait.tools 不能为空")
	}
	tools := make(map[string]ChatToolSchema, len(schema.Tools))
	for _, tool := range schema.Tools {
		tools[tool.Name] = tool
	}
	seen := map[string]struct{}{}
	for _, name := range policy.Tools {
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("chat.agent.background_wait.tools 重复声明 %q", name)
		}
		seen[name] = struct{}{}
		tool, exists := tools[name]
		if !exists {
			return fmt.Errorf("chat.agent.background_wait.tools 引用了不是生成工具的 %q", name)
		}
		if tool.Processing != nil && tool.Processing.Plan != nil {
			return fmt.Errorf("chat.agent.background_wait.tools 不能包含加工计划工具 %q", name)
		}
		paid := false
		for _, effect := range tool.Execution.Effects {
			if effect == "PAID_TASK" {
				paid = true
			}
		}
		if !paid {
			return fmt.Errorf("chat.agent.background_wait.tools 的 %q 不是付费任务工具", name)
		}
	}
	if policy.AfterSeconds <= 0 || policy.MaxListedTasks <= 0 {
		return errors.New("chat.agent.background_wait 的 after_seconds 与 max_listed_tasks 必须为正数")
	}
	for name, text := range map[string]string{
		"turn_content":           policy.TurnContent,
		"unavailable_message":    policy.UnavailableMessage,
		"context_template":       policy.ContextTemplate,
		"task_line_template":     policy.TaskLineTemplate,
		"upstream_queued_status": policy.UpstreamQueuedStatus,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("chat.agent.background_wait.%s 不能为空", name)
		}
	}
	if !strings.Contains(policy.ContextTemplate, "{tasks}") {
		return errors.New("chat.agent.background_wait.context_template 必须包含 {tasks}")
	}
	for _, placeholder := range []string{"{task_id}", "{capability}", "{model}", "{status}"} {
		if !strings.Contains(policy.TaskLineTemplate, placeholder) {
			return fmt.Errorf("chat.agent.background_wait.task_line_template 必须包含 %s", placeholder)
		}
	}
	if route == "" {
		return nil
	}
	if strings.TrimSpace(schema.API.Fields.TurnBackgrounded) == "" ||
		strings.TrimSpace(schema.API.Fields.TurnBackgroundAvailable) == "" ||
		schema.API.Fields.TurnBackgrounded == schema.API.Fields.TurnBackgroundAvailable {
		return errors.New("http.legacy_routes.chat_turn_background 需要两个不同的 api.fields.turn_backgrounded 与 turn_background_available")
	}
	if schema.Frontend.BackgroundWaitPollMilliseconds <= 0 {
		return errors.New("http.legacy_routes.chat_turn_background 需要正数的 chat.frontend.background_wait_poll_milliseconds")
	}
	if strings.TrimSpace(schema.Audit.TurnBackgroundAction) == "" {
		return errors.New("http.legacy_routes.chat_turn_background 需要 chat.audit.turn_background_action")
	}
	return nil
}

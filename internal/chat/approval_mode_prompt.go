package chat

import (
	"context"
	"strings"
)

// approvalModePrompt tells the Agent the session's real paid-step approval
// mode (#7), so it states the mode from a fact instead of guessing it from
// the conversation. The mode is the session's stored one, normalized by the
// approval policy exactly as the execution grant reads it, and is read on
// every model request, so a switch made between rounds shows up in the next
// one. The words come from application.chat.approval_policy.prompt.
//
// When the mode cannot be read (no session yet, or the lookup fails) the block
// is left out: an Agent that is told nothing can only say it does not know,
// while a defaulted mode could be wrong and send it into the confusion this
// block exists to prevent.
func (orchestrator *Orchestrator) approvalModePrompt(ctx context.Context, ownerID, sessionID string) string {
	prompt := orchestrator.schema.ApprovalPolicy.Prompt
	if orchestrator.service == nil || sessionID == "" || strings.TrimSpace(prompt.ContextTemplate) == "" {
		return ""
	}
	mode, err := orchestrator.service.SessionApprovalMode(ctx, ownerID, sessionID)
	if err != nil {
		if orchestrator.logger != nil {
			orchestrator.logger.Warn("读取会话批准方式失败，本轮提示词不带批准方式", "error", err)
		}
		return ""
	}
	text, ok := prompt.Mode(mode)
	if !ok {
		return ""
	}
	return strings.NewReplacer(
		"{mode}", text.Label,
		"{description}", text.Description,
	).Replace(prompt.ContextTemplate)
}

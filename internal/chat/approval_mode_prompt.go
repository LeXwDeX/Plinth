package chat

import (
	"context"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
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
	block := strings.NewReplacer(
		"{mode}", text.Label,
		"{description}", text.Description,
	).Replace(prompt.ContextTemplate)
	if mode == approvalpolicy.ModeBudget {
		// The budget mode's own fact: whether a plan is active, what it allows
		// and how much of it is left (#103). The mode text alone cannot say
		// that, and an Agent that guesses would either skip a card or open one
		// the budget already covers.
		block += orchestrator.productionBudgetPrompt(ctx, ownerID, sessionID)
	}
	return block
}

// productionBudgetPrompt renders the session's production budget through the
// Schema's context_template. It shows the active (or exhausted) plan; a plan
// that is still waiting for the person is shown instead, with no spending yet.
// Without either, the Schema's no-plan state and text tell the Agent to
// propose a plan before a multi-step paid job.
func (orchestrator *Orchestrator) productionBudgetPrompt(ctx context.Context, ownerID, sessionID string) string {
	config := orchestrator.schema.ApprovalPolicy.Budget
	if orchestrator.service == nil || strings.TrimSpace(config.ContextTemplate) == "" {
		return ""
	}
	state, limit, spent, reserved, remaining := config.NoPlanState, 0, 0, 0, 0
	planText := config.NoPlanText
	if summary, found, err := orchestrator.service.ProductionBudgetSummary(ctx, ownerID, sessionID); err == nil && found {
		state = summary.State
		limit, spent, reserved, remaining = summary.LimitCredits, summary.SpentCredits, summary.ReservedCredits, summary.RemainingCredits
		planText = summary.Plan.PromptLine()
	} else if pending, found, err := orchestrator.service.PendingProductionBudget(ctx, ownerID, sessionID); err == nil && found {
		state = pending.State
		limit, remaining = pending.LimitCredits, pending.RemainingCredits
		planText = pending.Plan.PromptLine()
	}
	label := state
	for _, declared := range config.States {
		if declared.ID == state {
			label = declared.Label
			break
		}
	}
	return strings.NewReplacer(
		"{state}", label,
		"{limit}", strconv.Itoa(limit),
		"{spent}", strconv.Itoa(spent),
		"{reserved}", strconv.Itoa(reserved),
		"{remaining}", strconv.Itoa(remaining),
		"{plan}", planText,
	).Replace(config.ContextTemplate)
}

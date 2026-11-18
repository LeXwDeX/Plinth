package chat

import (
	"context"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// StepBalance is one 3D provider's balance as the Agent's step context shows
// it (#330). A balance that could not be read is Known == false and is written
// as unknown, never as zero.
type StepBalance struct {
	Label string
	Known bool
	// Amount is the exact amount in the provider's own unit when UnitLabel is
	// set, otherwise the plain balance.
	Amount    string
	UnitLabel string
}

// StepCost is one service's approximate cost of one step, in its provider's
// own unit (#330). The application derives it from the asset's Tasks, the same
// way the external MCP status block does.
type StepCost struct {
	OfferingID string
	Capability string
	Amount     string
	UnitLabel  string
}

// stepPolicyPrompt renders the 「3D 步数」segment (#330). It appears only in a
// session whose derived approval mode is the internal steps mode; agent,
// unlimited, budget and manual sessions render nothing. A number that cannot
// be read is written as unknown and never blocks the turn.
func (orchestrator *Orchestrator) stepPolicyPrompt(
	ctx context.Context,
	ownerID string,
	sessionID string,
) string {
	policy := orchestrator.stepPolicy
	if policy == nil || orchestrator.service == nil || sessionID == "" ||
		strings.TrimSpace(policy.Prompt.ContextTemplate) == "" {
		return ""
	}
	mode, err := orchestrator.service.SessionApprovalMode(ctx, ownerID, sessionID)
	if err != nil {
		if orchestrator.logger != nil {
			orchestrator.logger.Warn("读取会话批准方式失败，本轮提示词不带 3D 步数段", "error", err)
		}
		return ""
	}
	if mode != approvalpolicy.ModeSteps {
		return ""
	}
	prompt := policy.Prompt
	text := func(value string) string { return orchestrator.text(ctx, value) }
	rule, usage := text(prompt.ModeUnknownRule), text(prompt.UsageUnknown)
	summary, read := orchestrator.stepStanding(ctx, ownerID, sessionID)
	if read {
		rule = orchestrator.stepModeRule(ctx, policy, summary)
		usage = orchestrator.stepUsageLine(ctx, prompt, summary)
	}
	replace := func(template string, pairs ...string) string {
		return strings.NewReplacer(pairs...).Replace(template)
	}
	return replace(text(prompt.ContextTemplate),
		"{mode_rule}", rule,
		"{usage}", usage,
		"{balances}", orchestrator.stepBalancesLine(ctx, prompt),
		"{costs}", orchestrator.stepCostsLine(ctx, ownerID, sessionID, prompt),
		"{guidance}", text(prompt.Guidance),
	)
}

// stepStanding reads the session's 3D step standing. The second result is
// false when the standing is unknown; nothing is guessed in its place.
func (orchestrator *Orchestrator) stepStanding(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (executiongrant.StepSummary, bool) {
	if orchestrator.stepUsage == nil {
		return executiongrant.StepSummary{}, false
	}
	summary, err := orchestrator.stepUsage(ctx, ownerID, sessionID)
	if err != nil {
		if orchestrator.logger != nil {
			orchestrator.logger.Warn("读取 3D 已用步数失败，本轮按未知处理", "error", err)
		}
		return executiongrant.StepSummary{}, false
	}
	return summary, true
}

// stepModeRule is the rule sentence of the person's current step mode.
func (orchestrator *Orchestrator) stepModeRule(
	ctx context.Context,
	policy *catalog.StepPolicySchema,
	summary executiongrant.StepSummary,
) string {
	prompt := policy.Prompt
	rule, ok := prompt.Rule(summary.Mode)
	if !ok {
		return orchestrator.text(ctx, prompt.ModeUnknownRule)
	}
	label := summary.Mode
	for _, mode := range policy.Modes {
		if mode.Value == summary.Mode {
			label = orchestrator.text(ctx, mode.Label)
			break
		}
	}
	return strings.NewReplacer(
		"{mode}", label,
		"{cap}", strconv.Itoa(summary.Cap),
	).Replace(orchestrator.text(ctx, rule))
}

// stepUsageLine states the asset's used steps and, under the cap mode, the cap
// and the steps left.
func (orchestrator *Orchestrator) stepUsageLine(
	ctx context.Context,
	prompt catalog.StepPolicyPromptSchema,
	summary executiongrant.StepSummary,
) string {
	used := strconv.Itoa(summary.Used)
	if summary.Mode != approvalpolicy.StepModeCap {
		return strings.NewReplacer("{used}", used).Replace(orchestrator.text(ctx, prompt.UsageLineNoCap))
	}
	return strings.NewReplacer(
		"{used}", used,
		"{cap}", strconv.Itoa(summary.Cap),
		"{remaining}", strconv.Itoa(max(summary.Cap-summary.Used, 0)),
	).Replace(orchestrator.text(ctx, prompt.UsageLine))
}

// stepBalancesLine lists the three 3D providers' balances in their own units.
func (orchestrator *Orchestrator) stepBalancesLine(
	ctx context.Context,
	prompt catalog.StepPolicyPromptSchema,
) string {
	text := func(value string) string { return orchestrator.text(ctx, value) }
	if orchestrator.stepBalances == nil {
		return text(prompt.BalancesUnknown)
	}
	balances := orchestrator.stepBalances(ctx)
	if len(balances) == 0 {
		return text(prompt.BalancesUnknown)
	}
	items := make([]string, 0, len(balances))
	for _, balance := range balances {
		if !balance.Known || strings.TrimSpace(balance.Amount) == "" {
			items = append(items, strings.NewReplacer("{label}", balance.Label).
				Replace(text(prompt.BalanceUnknown)))
			continue
		}
		if strings.TrimSpace(balance.UnitLabel) == "" {
			items = append(items, strings.NewReplacer(
				"{label}", balance.Label, "{amount}", balance.Amount,
			).Replace(text(prompt.BalanceValueItem)))
			continue
		}
		items = append(items, strings.NewReplacer(
			"{label}", balance.Label, "{amount}", balance.Amount, "{unit_label}", balance.UnitLabel,
		).Replace(text(prompt.BalanceItem)))
	}
	return strings.NewReplacer("{balances}", strings.Join(items, text(prompt.BalanceSeparator))).
		Replace(text(prompt.BalancesLine))
}

// stepCostsLine lists what one step of each service the asset already used
// costs, roughly, in the provider's own unit.
func (orchestrator *Orchestrator) stepCostsLine(
	ctx context.Context,
	ownerID string,
	sessionID string,
	prompt catalog.StepPolicyPromptSchema,
) string {
	text := func(value string) string { return orchestrator.text(ctx, value) }
	if orchestrator.stepCosts == nil {
		return text(prompt.CostsUnknown)
	}
	costs := orchestrator.stepCosts(ctx, ownerID, sessionID)
	if len(costs) == 0 {
		return text(prompt.CostsUnknown)
	}
	items := make([]string, 0, len(costs))
	for _, cost := range costs {
		items = append(items, strings.NewReplacer(
			"{capability}", cost.Capability, "{offering_id}", cost.OfferingID,
			"{amount}", cost.Amount, "{unit_label}", cost.UnitLabel,
		).Replace(text(prompt.CostItem)))
	}
	return strings.NewReplacer("{costs}", strings.Join(items, text(prompt.CostSeparator))).
		Replace(text(prompt.CostsLine))
}

package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The budget approval mode (#103): the Agent proposes one production plan with
// a total credit budget, the person decides it once, and executiongrant then
// approves the plan's steps inside the budget without a card per step. This
// file owns the proposal tool's validation; the plan store, the reservation
// and the decision stay in executiongrant.

var (
	// ErrProductionPlanUnavailable refuses a proposal when the Schema declares
	// no budget configuration (a historical Catalog).
	ErrProductionPlanUnavailable = errors.New("chat: production plans are not declared")
)

// ProductionPlanStore is the part of executiongrant.Service the proposal tool
// and the Agent's budget context read.
type ProductionPlanStore interface {
	CreateProductionPlan(ctx context.Context, ownerID, sessionID, requestKey, messageID, callID string,
		plan budget.Plan, limitCredits int) (budget.ProductionBudget, error)
	BudgetSummary(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error)
	PendingBudgets(ctx context.Context, ownerID, sessionID string) ([]budget.ProductionBudget, error)
}

// ProductionPlanPolicy wires the proposal tool: the Schema's bounds and texts,
// the runtime Offerings the server quotes against, and the plan store.
type ProductionPlanPolicy struct {
	Budget            catalog.ChatApprovalBudgetSchema
	Offerings         func() []generation.Offering
	OfferingAvailable func(generation.OfferingID) bool
	Store             ProductionPlanStore
}

// SetProductionPlans installs the budget-mode proposal writer. It is called
// during application assembly, after the execution grant service exists (the
// grant service itself reads the session mode from the chat service).
func (service *Service) SetProductionPlans(policy ProductionPlanPolicy) {
	if policy.Store == nil || policy.Budget.MaxCredits <= 0 || policy.Offerings == nil {
		service.productionPlans = nil
		return
	}
	service.productionPlans = &policy
}

// ProductionPlanEnabled reports whether the proposal tool is available.
func (service *Service) ProductionPlanEnabled() bool {
	return service.productionPlans != nil
}

// ProductionBudgetSummary is the session's active (or exhausted) plan with its
// settled spent and reserved totals.
func (service *Service) ProductionBudgetSummary(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error) {
	if service.productionPlans == nil || ownerID == "" || sessionID == "" {
		return budget.Summary{}, false, nil
	}
	return service.productionPlans.Store.BudgetSummary(ctx, ownerID, sessionID)
}

// PendingProductionBudget is the session's unexpired undecided plan, if any.
// It carries no reservations: a pending plan never spent.
func (service *Service) PendingProductionBudget(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error) {
	if service.productionPlans == nil || ownerID == "" || sessionID == "" {
		return budget.Summary{}, false, nil
	}
	pending, err := service.productionPlans.Store.PendingBudgets(ctx, ownerID, sessionID)
	if err != nil || len(pending) == 0 {
		return budget.Summary{}, false, err
	}
	return budget.SummaryOf(pending[0], 0, 0), true, nil
}

// proposedPlanArguments is the tool's declared object. estimated_credits is
// accepted because the Schema declares it, and deliberately ignored: the
// server quotes every step itself.
type proposedPlanArguments struct {
	Title string `json:"title"`
	Steps []struct {
		Capability       string `json:"capability"`
		Model            string `json:"model"`
		Purpose          string `json:"purpose"`
		EstimatedCredits *int   `json:"estimated_credits"`
	} `json:"steps"`
	BudgetCredits          int      `json:"budget_credits"`
	CorrectionCapabilities []string `json:"correction_capabilities"`
}

// ProposeProductionPlan validates and stores one pending plan for the Agent's
// own session. The command's owner, session, message and call come from the
// server-held tool call; the arguments only describe the plan.
func (service *Service) ProposeProductionPlan(ctx context.Context, command ToolCommand) (ToolResult, error) {
	policy := service.productionPlans
	if policy == nil {
		return planRefusal("production plans are unavailable"), nil
	}
	var arguments proposedPlanArguments
	decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
	decoder.DisallowUnknownFields()
	if len(command.Call.Arguments) == 0 || decoder.Decode(&arguments) != nil ||
		decoder.Decode(new(any)) != io.EOF {
		return planRefusal("arguments must match the declared plan object"), nil
	}
	plan, limit, refusal := policy.buildPlan(arguments)
	if refusal != "" {
		return planRefusal(refusal), nil
	}
	key := productionPlanRequestKey(command)
	stored, err := policy.Store.CreateProductionPlan(ctx, command.OwnerID, command.SessionID, key,
		command.AssistantMessageID, command.Call.ID, plan, limit)
	if err != nil {
		switch {
		case errors.Is(err, executiongrant.ErrInvalid):
			return planRefusal("the plan is invalid"), nil
		case errors.Is(err, executiongrant.ErrConflict):
			return planRefusal("a different plan was already proposed for this tool call"), nil
		case errors.Is(err, ErrSessionNotFound):
			return planRefusal("session not found"), nil
		default:
			return ToolResult{NoSubmission: true}, err
		}
	}
	content, err := json.Marshal(planProposedContent(stored, policy.Budget.ToolMessages))
	if err != nil {
		return ToolResult{NoSubmission: true}, err
	}
	return ToolResult{AgentContent: string(content), NoSubmission: true}, nil
}

// productionPlanRequestKey is the proposal's stable identity. It includes the
// turn so a repeated model call ID in a later turn cannot replay an old plan.
func productionPlanRequestKey(command ToolCommand) string {
	identity, _ := json.Marshal([]string{
		command.SessionID, command.AssistantMessageID, command.Call.ID, command.IdempotencyKey(),
	})
	digest := sha256.Sum256(identity)
	return "chat-budget:" + hex.EncodeToString(digest[:])
}

// buildPlan validates the arguments against the Schema and quotes every step
// from the runtime Catalog. A step's Agent-supplied estimate is never used.
func (policy *ProductionPlanPolicy) buildPlan(arguments proposedPlanArguments) (budget.Plan, int, string) {
	config := policy.Budget
	title := strings.TrimSpace(arguments.Title)
	if title == "" {
		return budget.Plan{}, 0, "title is required"
	}
	titleLimit := config.MaxTitleCharacters
	if titleLimit <= 0 || titleLimit > budget.MaxTitleCharacters {
		titleLimit = budget.MaxTitleCharacters
	}
	if utf8.RuneCountInString(title) > titleLimit {
		return budget.Plan{}, 0, "title is too long"
	}
	maxSteps := config.MaxSteps
	if maxSteps <= 0 {
		maxSteps = len(arguments.Steps)
	}
	if len(arguments.Steps) == 0 || len(arguments.Steps) > maxSteps {
		return budget.Plan{}, 0, "steps must be between 1 and " + strconv.Itoa(maxSteps)
	}
	if arguments.BudgetCredits <= 0 {
		return budget.Plan{}, 0, "budget_credits is required"
	}
	if arguments.BudgetCredits > config.MaxCredits {
		return budget.Plan{}, 0, "budget_credits exceeds the Schema maximum of " + strconv.Itoa(config.MaxCredits)
	}
	plan := budget.Plan{Title: title}
	seenCorrections := map[string]bool{}
	for _, raw := range arguments.CorrectionCapabilities {
		capability := strings.TrimSpace(raw)
		if capability == "" || seenCorrections[capability] {
			continue
		}
		if !policy.capabilityKnown(capability) {
			return budget.Plan{}, 0, "correction capability " + capability + " is unknown"
		}
		seenCorrections[capability] = true
		plan.CorrectionCapabilities = append(plan.CorrectionCapabilities, capability)
	}
	for _, step := range arguments.Steps {
		capability := strings.TrimSpace(step.Capability)
		purpose := strings.TrimSpace(step.Purpose)
		if capability == "" {
			return budget.Plan{}, 0, "every step needs a capability"
		}
		if purpose == "" {
			return budget.Plan{}, 0, "every step needs a purpose"
		}
		if config.MaxPurposeCharacters > 0 && utf8.RuneCountInString(purpose) > config.MaxPurposeCharacters {
			return budget.Plan{}, 0, "step purpose is too long"
		}
		offering, ok := policy.stepOffering(capability, strings.TrimSpace(step.Model))
		if !ok {
			return budget.Plan{}, 0, "no available offering for capability " + capability
		}
		estimated := budget.EstimateOffering(offering)
		if offering.CreditsMetered && estimated <= 0 {
			// A metered step the server cannot quote cannot be covered by a
			// total budget: reserving zero would let it spend outside the plan
			// (#103 review B1). The Agent must pick a priceable model or let the
			// person approve the step directly.
			return budget.Plan{}, 0, "step " + capability + " has no server credit estimate and cannot be budgeted"
		}
		plan.Steps = append(plan.Steps, budget.PlanStep{
			Capability:       capability,
			OfferingID:       strings.TrimSpace(step.Model),
			Purpose:          purpose,
			EstimatedCredits: estimated,
		})
	}
	estimated := plan.EstimatedTotal()
	if arguments.BudgetCredits < estimated {
		return budget.Plan{}, 0, "budget_credits must cover the server estimate of " + strconv.Itoa(estimated) + " credits"
	}
	return plan, arguments.BudgetCredits, ""
}

// stepOffering finds the runtime Offering a step is quoted at: the pinned one
// when the person chose a model, else the capability's default available
// Offering. A capability without an available Offering cannot be planned.
func (policy *ProductionPlanPolicy) stepOffering(capability, pinned string) (generation.Offering, bool) {
	var candidates []generation.Offering
	for _, offering := range policy.Offerings() {
		if !offering.Enabled || string(offering.Capability) != capability {
			continue
		}
		if policy.OfferingAvailable != nil && !policy.OfferingAvailable(offering.ID) {
			continue
		}
		candidates = append(candidates, offering)
	}
	if len(candidates) == 0 {
		return generation.Offering{}, false
	}
	chosen := candidates[0]
	if pinned == "" {
		for _, offering := range candidates {
			if offering.Default {
				chosen = offering
				break
			}
		}
		return chosen, true
	}
	for _, offering := range candidates {
		if string(offering.ID) == pinned {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

// capabilityKnown reports whether the runtime Catalog declares the capability
// at all; a correction capability need not be available right now.
func (policy *ProductionPlanPolicy) capabilityKnown(capability string) bool {
	for _, offering := range policy.Offerings() {
		if string(offering.Capability) == capability {
			return true
		}
	}
	return false
}

// planProposedContent is the Agent-facing result of the plan tool. Its state
// messages come from the Schema's approval_policy.budget.tool_messages, with
// an English fallback for a historical Catalog (#103 review B4).
func planProposedContent(stored budget.ProductionBudget, messages catalog.ChatApprovalBudgetToolMessages) map[string]any {
	steps := make([]map[string]any, 0, len(stored.Plan.Steps))
	for _, step := range stored.Plan.Steps {
		item := map[string]any{
			"capability":        step.Capability,
			"purpose":           step.Purpose,
			"estimated_credits": step.EstimatedCredits,
		}
		if step.OfferingID != "" {
			item["offering_id"] = step.OfferingID
		}
		steps = append(steps, item)
	}
	message := planToolMessage(messages.Proposed,
		"Production plan proposed; wait for the person to approve or deny it. Do not propose another plan before this one is decided.")
	switch stored.State {
	case budget.StateActive:
		message = planToolMessage(messages.Active, "Production plan is approved; its steps may run within the budget.")
	case budget.StateExhausted:
		message = planToolMessage(messages.Exhausted,
			"Production plan is approved but its credit budget is used up; a step beyond it needs the person's approval.")
	case budget.StateDenied, budget.StateClosed:
		message = planToolMessage(messages.Closed, "This production plan is no longer pending.")
	}
	return map[string]any{
		"success":                 true,
		"budget_id":               stored.ID,
		"state":                   stored.State,
		"title":                   stored.Plan.Title,
		"limit_credits":           stored.LimitCredits,
		"estimated_credits":       stored.Plan.EstimatedTotal(),
		"steps":                   steps,
		"correction_capabilities": stored.Plan.CorrectionCapabilities,
		"message":                 message,
	}
}

func planToolMessage(declared, fallback string) string {
	if strings.TrimSpace(declared) == "" {
		return fallback
	}
	return declared
}

func planRefusal(reason string) ToolResult {
	content, _ := json.Marshal(map[string]any{"success": false, "error": reason})
	return ToolResult{AgentContent: string(content), Error: reason, NoSubmission: true}
}

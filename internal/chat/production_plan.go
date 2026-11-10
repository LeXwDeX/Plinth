package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The budget approval mode (#103): the Agent proposes one production plan with
// a credit budget per unit, the person decides it once, and executiongrant then
// approves the plan's steps inside the budget of their own unit without a card
// per step. Different provider units are limited and counted separately and
// never added (#120). This file owns the proposal tool's validation; the plan
// store, the reservation and the decision stay in executiongrant.

var (
	// ErrProductionPlanUnavailable refuses a proposal when the Schema declares
	// no budget configuration (a historical Catalog).
	ErrProductionPlanUnavailable = errors.New("chat: production plans are not declared")
	// ErrPlanNotActive refuses a continuation of a plan that is not the
	// session's active plan.
	ErrPlanNotActive = errors.New("chat: production plan is not the session's active plan")
)

// StartPlanContinuation starts the turn that runs a plan the person just
// approved (#134), like the turn that carries a choice card's answer (#321):
// its user message is the Schema's go-ahead, and the Agent's request follows
// it with the Schema's continuation text. The turn is an ordinary one: the
// service freezes the scope, brief, model lock and chat model as for any turn,
// and it takes the workspace choices (domain, picked models) and the locale of
// the turn that proposed the plan. Only the session's active plan continues;
// an active turn of the session refuses it with ErrTurnActive. It returns the
// assistant message and the options the caller schedules the turn with.
func (service *Service) StartPlanContinuation(ctx context.Context, ownerID, sessionID, budgetID, locale string) (Message, TurnOptions, error) {
	policy := service.productionPlans
	if policy == nil || strings.TrimSpace(policy.Budget.Continuation.Message) == "" {
		return Message{}, TurnOptions{}, ErrProductionPlanUnavailable
	}
	summary, found, err := policy.Store.BudgetSummary(ctx, ownerID, sessionID)
	if err != nil {
		return Message{}, TurnOptions{}, err
	}
	if !found || summary.ID != budgetID || summary.State != budget.StateActive {
		return Message{}, TurnOptions{}, ErrPlanNotActive
	}
	options := TurnOptions{Locale: locale}
	if production, err := policy.Store.GetBudget(ctx, ownerID, budgetID); err == nil && production.MessageID != "" {
		if proposing, err := service.repository.GetTurn(ctx, ownerID, sessionID, production.MessageID); err == nil {
			options.CreativeDomain = proposing.Options.CreativeDomain
			options.AIModel = proposing.Options.AIModel
			options.DomainModels = proposing.Options.DomainModels
			if proposing.Options.Locale != "" {
				options.Locale = proposing.Options.Locale
			}
		}
	}
	content := strings.ReplaceAll(policy.Budget.Continuation.Message, "{title}", summary.Plan.Title)
	assistant, err := service.startTurn(ctx, StartTurn{OwnerID: ownerID, SessionID: sessionID,
		Content: content, Options: options}, budgetID)
	if err != nil {
		return Message{}, TurnOptions{}, err
	}
	options.PlanContinuation = budgetID
	return assistant, options, nil
}

// ProductionPlanStore is the part of executiongrant.Service the proposal tool
// and the Agent's budget context read.
type ProductionPlanStore interface {
	CreateProductionPlan(ctx context.Context, ownerID, sessionID, requestKey, messageID, callID string,
		plan budget.Plan, limits []budget.Amount) (budget.ProductionBudget, error)
	BudgetSummary(ctx context.Context, ownerID, sessionID string) (budget.Summary, bool, error)
	PendingBudgets(ctx context.Context, ownerID, sessionID string) ([]budget.ProductionBudget, error)
	// GetBudget reads one plan of the owner; the continuation turn takes the
	// workspace choices of the turn that proposed it (#134).
	GetBudget(ctx context.Context, ownerID, budgetID string) (budget.ProductionBudget, error)
}

// ProductionPlanPolicy wires the proposal tool: the Schema's bounds and texts,
// the runtime Offerings the server quotes against, and the plan store.
type ProductionPlanPolicy struct {
	Budget            catalog.ChatApprovalBudgetSchema
	Offerings         func() []generation.Offering
	OfferingAvailable func(generation.OfferingID) bool
	Store             ProductionPlanStore
	// PurposeBudgetNote words the values a planned step would run with that
	// are over the session purpose's budget (#129), or "" when they fit. It is
	// optional: without it a plan carries no purpose budget note.
	PurposeBudgetNote func(command ToolCommand, offering generation.Offering) string
}

// SetProductionPlanPurposeBudget installs the purpose budget check of planned
// steps (#129). Application assembly calls it once the generation tools that
// own the purpose budget exist; it does nothing without production plans.
func (service *Service) SetProductionPlanPurposeBudget(note func(ToolCommand, generation.Offering) string) {
	if service.productionPlans != nil {
		service.productionPlans.PurposeBudgetNote = note
	}
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
	return budget.SummaryOf(pending[0], nil), true, nil
}

// proposedPlanArguments is the tool's declared object. estimated_credits is
// accepted because the Schema declares it, and deliberately ignored: the
// server quotes every step itself. budgets holds one amount per credit unit.
type proposedPlanArguments struct {
	Title string `json:"title"`
	Steps []struct {
		Capability       string `json:"capability"`
		Model            string `json:"model"`
		Purpose          string `json:"purpose"`
		EstimatedCredits *int   `json:"estimated_credits"`
	} `json:"steps"`
	Budgets []struct {
		Unit   string      `json:"unit"`
		Amount json.Number `json:"amount"`
	} `json:"budgets"`
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
	plan, limits, refusal := policy.buildPlan(command, arguments)
	if refusal != "" {
		return planRefusal(refusal), nil
	}
	key := productionPlanRequestKey(command)
	stored, err := policy.Store.CreateProductionPlan(ctx, command.OwnerID, command.SessionID, key,
		command.AssistantMessageID, command.Call.ID, plan, limits)
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
// from the runtime Catalog in the step's own unit. A step's Agent-supplied
// estimate is never used, and every unit the steps are quoted in needs its own
// limit that covers them; units are never added or converted (#120).
func (policy *ProductionPlanPolicy) buildPlan(command ToolCommand, arguments proposedPlanArguments) (budget.Plan, []budget.Amount, string) {
	config := policy.Budget
	title := strings.TrimSpace(arguments.Title)
	if title == "" {
		return budget.Plan{}, nil, "title is required"
	}
	titleLimit := config.MaxTitleCharacters
	if titleLimit <= 0 || titleLimit > budget.MaxTitleCharacters {
		titleLimit = budget.MaxTitleCharacters
	}
	if utf8.RuneCountInString(title) > titleLimit {
		return budget.Plan{}, nil, "title is too long"
	}
	maxSteps := config.MaxSteps
	if maxSteps <= 0 {
		maxSteps = len(arguments.Steps)
	}
	if len(arguments.Steps) == 0 || len(arguments.Steps) > maxSteps {
		return budget.Plan{}, nil, "steps must be between 1 and " + strconv.Itoa(maxSteps)
	}
	limits, refusal := policy.planLimits(arguments)
	if refusal != "" {
		return budget.Plan{}, nil, refusal
	}
	plan := budget.Plan{Title: title}
	seenCorrections := map[string]bool{}
	for _, raw := range arguments.CorrectionCapabilities {
		capability := strings.TrimSpace(raw)
		if capability == "" || seenCorrections[capability] {
			continue
		}
		if !policy.capabilityKnown(capability) {
			return budget.Plan{}, nil, "correction capability " + capability + " is unknown"
		}
		seenCorrections[capability] = true
		plan.CorrectionCapabilities = append(plan.CorrectionCapabilities, capability)
	}
	for _, step := range arguments.Steps {
		capability := strings.TrimSpace(step.Capability)
		purpose := strings.TrimSpace(step.Purpose)
		if capability == "" {
			return budget.Plan{}, nil, "every step needs a capability"
		}
		if purpose == "" {
			return budget.Plan{}, nil, "every step needs a purpose"
		}
		if config.MaxPurposeCharacters > 0 && utf8.RuneCountInString(purpose) > config.MaxPurposeCharacters {
			return budget.Plan{}, nil, "step purpose is too long"
		}
		offering, locked, refusal := policy.stepOffering(capability, strings.TrimSpace(step.Model),
			strings.TrimSpace(command.Options.ExplicitModel3D), strings.TrimSpace(command.Options.ModelFor("image")))
		if refusal != "" {
			return budget.Plan{}, nil, refusal
		}
		estimated := budget.EstimateOffering(offering)
		if offering.CreditsMetered && estimated.Micros <= 0 {
			// A metered step the server cannot quote cannot be covered by a
			// budget: reserving zero would let it spend outside the plan (#103
			// review B1). The Agent must pick a priceable model or let the
			// person approve the step directly.
			return budget.Plan{}, nil, "step " + capability + " has no server credit estimate and cannot be budgeted"
		}
		offeringID := strings.TrimSpace(step.Model)
		if locked {
			offeringID = string(offering.ID)
		}
		planned := budget.PlanStep{
			Capability: capability,
			OfferingID: offeringID,
			Purpose:    purpose,
			Estimate:   estimated,
		}
		if policy.PurposeBudgetNote != nil {
			// A plan step names no parameters, so the check reads the values it
			// would run with; the person sees an over-budget value on the card
			// before approving the plan, not only after the step ran (#129).
			planned.PurposeBudgetNote = policy.PurposeBudgetNote(command, offering)
		}
		plan.Steps = append(plan.Steps, planned)
	}
	if uncovered := plan.Uncovered(limits); len(uncovered) > 0 {
		return budget.Plan{}, nil, "budgets must give every unit the steps are quoted in at least the server estimate of that unit (" +
			unitAmountsText(plan.EstimatedTotals()) + "); missing or too low: " + unitAmountsText(uncovered) +
			". Units are never added or converted"
	}
	return plan, limits, ""
}

// planLimits reads the per-unit budgets the Agent proposed. A unit must be
// the legacy bucket or a provider unit some Offering of the Catalog is priced
// in; each appears once with a positive amount within the Schema maximum.
func (policy *ProductionPlanPolicy) planLimits(arguments proposedPlanArguments) ([]budget.Amount, string) {
	if len(arguments.Budgets) == 0 {
		return nil, "budgets is required: one amount per credit unit"
	}
	labels := policy.unitLabels()
	maximum := strconv.Itoa(policy.Budget.MaxCredits)
	limits := make([]budget.Amount, 0, len(arguments.Budgets))
	seen := map[string]bool{}
	for _, entry := range arguments.Budgets {
		unit := strings.TrimSpace(entry.Unit)
		label, known := labels[unit]
		if !known {
			return nil, "budget unit " + unit + " is unknown; use the unit of a step quote (" + unitList(labels) + ")"
		}
		if seen[unit] {
			return nil, "budget unit " + unit + " is listed twice"
		}
		seen[unit] = true
		limit, err := budget.ParseAmount(unit, label, entry.Amount.String())
		if err != nil || limit.Micros <= 0 {
			return nil, "budget for unit " + unit + " must be a positive decimal amount" + legacyWholeHint(unit)
		}
		ceiling, err := budget.ParseAmount(unit, label, maximum)
		if err == nil && limit.Micros > ceiling.Micros {
			return nil, "budget for unit " + unit + " exceeds the Schema maximum of " + maximum
		}
		limits = append(limits, limit)
	}
	normalized, err := budget.NormalizeLimits(limits)
	if err != nil {
		return nil, "budgets are invalid"
	}
	return normalized, ""
}

// unitLabels are the units a plan may budget: the legacy bucket and every
// provider unit an Offering of the Catalog declares, with its label.
func (policy *ProductionPlanPolicy) unitLabels() map[string]string {
	labels := map[string]string{budget.LegacyUnit: budget.LegacyUnit}
	for _, offering := range policy.Offerings() {
		if pricing := offering.NativePricing; pricing != nil && pricing.Unit != "" {
			if _, seen := labels[pricing.Unit]; !seen {
				labels[pricing.Unit] = pricing.UnitLabel
			}
		}
	}
	return labels
}

func unitList(labels map[string]string) string {
	units := make([]string, 0, len(labels))
	for unit := range labels {
		units = append(units, unit)
	}
	sort.Strings(units)
	return strings.Join(units, ", ")
}

func legacyWholeHint(unit string) string {
	if unit == budget.LegacyUnit {
		return " in whole credits"
	}
	return ""
}

// unitAmountsText lists amounts as "<amount> <unit>" for a tool refusal the
// Agent reads; the unit ids are what it sends back in budgets.
func unitAmountsText(amounts []budget.Amount) string {
	parts := make([]string, 0, len(amounts))
	for _, amount := range amounts {
		parts = append(parts, amount.Decimal()+" "+amount.Unit)
	}
	return strings.Join(parts, ", ")
}

// stepOffering finds the runtime Offering a step is quoted at, in the order a
// 3D tool chooses its model. The person's explicit 3D model comes first when
// an Offering of the capability is that model: it is what will run, so the
// step is quoted at it and pinned to it (locked), and a step naming another
// model is refused, as the tool would refuse it (#144). An image-domain step
// uses the workspace's image model for the same reason: create_three_view
// selects it, so a plan that quoted another 2D Offering could never be
// continued by the budget (#243). Otherwise the pinned Offering when the
// Agent named a model, else the capability's default available Offering. A
// capability without an available Offering cannot be planned.
func (policy *ProductionPlanPolicy) stepOffering(capability, pinned, lockedModel, workspaceImageModel string) (offering generation.Offering, locked bool, refusal string) {
	unavailable := "no available offering for capability " + capability
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
		return generation.Offering{}, false, unavailable
	}
	imageDomain := candidates[0].Domain == generation.DomainImage
	if lockedModel != "" {
		for _, offering := range candidates {
			if !offeringIsModel(offering, lockedModel) {
				continue
			}
			if pinned != "" && !offeringIsModel(offering, pinned) {
				return generation.Offering{}, false, "the person locked the 3D model to " + lockedModel + "; step " + capability + " must use it, not " + pinned
			}
			return offering, true, ""
		}
	}
	if imageDomain && workspaceImageModel != "" {
		workspace, matched := matchPlanCandidate(candidates, workspaceImageModel)
		if !matched {
			return generation.Offering{}, false, "the workspace image model " + workspaceImageModel +
				" has no available Offering for capability " + capability
		}
		if pinned == "" {
			// Quote and pin the model create_three_view will actually run.
			return workspace, true, ""
		}
		pinnedOffering, pinnedMatched := matchPlanCandidate(candidates, pinned)
		if !pinnedMatched {
			return generation.Offering{}, false, unavailable
		}
		if !offeringIsModel(pinnedOffering, workspaceImageModel) {
			return generation.Offering{}, false, "the workspace image model is " + workspaceImageModel +
				"; step " + capability + " must use it, not " + pinned
		}
		// Pin the resolved Offering: the plan compares Offering IDs when it
		// decides whether it covers a submitted step.
		return pinnedOffering, true, ""
	}
	if pinned == "" {
		chosen := candidates[0]
		for _, offering := range candidates {
			if offering.Default {
				chosen = offering
				break
			}
		}
		return chosen, false, ""
	}
	for _, offering := range candidates {
		if string(offering.ID) == pinned {
			return offering, false, ""
		}
	}
	return generation.Offering{}, false, unavailable
}

// matchPlanCandidate finds an Offering by any name a tool accepts: model_id,
// Offering ID or provider/model_id.
func matchPlanCandidate(candidates []generation.Offering, name string) (generation.Offering, bool) {
	for _, offering := range candidates {
		if offeringIsModel(offering, name) {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

// offeringIsModel reports whether name names the Offering's model the ways a
// 3D tool accepts it: the model ID, the Offering ID or provider/model.
func offeringIsModel(offering generation.Offering, name string) bool {
	return name == offering.ModelID || name == string(offering.ID) ||
		(offering.ModelID != "" && offering.ProviderID != "" && name == offering.ProviderID+"/"+offering.ModelID)
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
			"capability": step.Capability,
			"purpose":    step.Purpose,
			"estimate":   step.Estimate,
		}
		if step.OfferingID != "" {
			item["offering_id"] = step.OfferingID
		}
		if step.PurposeBudgetNote != "" {
			item["purpose_budget_note"] = step.PurposeBudgetNote
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
		"budgets":                 stored.Limits,
		"estimated_totals":        stored.Plan.EstimatedTotals(),
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

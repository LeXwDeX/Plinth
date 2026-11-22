package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
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
		Content: content, Options: options}, budgetID, nil)
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
	// Usage and Production are the purpose budget and review thresholds the
	// server uses to derive acceptance criteria and checkpoint thresholds
	// (#251). The Agent names checks; these values fix what they mean.
	Usage      catalog.ChatModel3DUsageSchema
	Production catalog.ChatModelInspectProductionSchema
	// Localize translates the acceptance wording into the proposal turn's
	// language. The locale is the turn's own (command.Options.Locale); nil
	// keeps the Schema's default language.
	Localize func(locale, text string) string
	// PurposeBudgetNote words the values a planned step would run with that
	// are over the session purpose's budget (#129), or "" when they fit. It is
	// optional: without it a plan carries no purpose budget note.
	PurposeBudgetNote func(command ToolCommand, offering generation.Offering) string
	// Checkpoints are the check ids a step may declare (#246), from
	// application.chat.self_check.checkpoints. Empty accepts none.
	Checkpoints []string
	// Candidates is application.chat.candidates (#242): how many parallel
	// candidates one step may declare and where they may come from. A zero
	// value refuses candidates.
	Candidates catalog.ChatCandidatesSchema
	// ConfirmsWorkspace says the Schema declares the text that asks the person
	// before a purpose recommendation replaces the model they see (see
	// ModelRequest.ConfirmsWorkspace). It is the same declaration the
	// generation tools read, so a plan quote and the call that runs it agree
	// (#380 step 2).
	ConfirmsWorkspace bool
}

// SetProductionPlanPurposeBudget installs the purpose budget check of planned
// steps (#129). Application assembly calls it once the generation tools that
// own the purpose budget exist; it does nothing without production plans.
func (service *Service) SetProductionPlanPurposeBudget(note func(ToolCommand, generation.Offering) string) {
	if service.productionPlans != nil {
		service.productionPlans.PurposeBudgetNote = note
	}
}

// SetProductionPlanLocalizer installs the text translation used for the
// acceptance criteria frozen at proposal time (#251). It is optional and runs
// after the Schema's localizer exists; the callback receives the proposal
// turn's locale.
func (service *Service) SetProductionPlanLocalizer(localize func(locale, text string) string) {
	if service.productionPlans != nil {
		service.productionPlans.Localize = localize
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
	Title   string             `json:"title"`
	Steps   []proposedPlanStep `json:"steps"`
	Budgets []struct {
		Unit   string      `json:"unit"`
		Amount json.Number `json:"amount"`
	} `json:"budgets"`
	CorrectionCapabilities []string `json:"correction_capabilities"`
	// #251: checkpoints name only check ids; thresholds are server-derived.
	Checkpoints []struct {
		AfterCapability string   `json:"after_capability"`
		Checks          []string `json:"checks"`
	} `json:"checkpoints"`
	Contingencies []struct {
		ID      string   `json:"id"`
		Trigger []string `json:"trigger"`
		Step    struct {
			Capability string `json:"capability"`
			Model      string `json:"model"`
			Purpose    string `json:"purpose"`
		} `json:"step"`
	} `json:"contingencies"`
}

// proposedPlanStep is one declared step. Candidates, when present, asks the
// server to expand the step into a parallel candidate group (#242).
type proposedPlanStep struct {
	Capability       string                  `json:"capability"`
	Model            string                  `json:"model"`
	Purpose          string                  `json:"purpose"`
	EstimatedCredits *int                    `json:"estimated_credits"`
	Check            string                  `json:"check"`
	Candidates       *proposedPlanCandidates `json:"candidates"`
}

// proposedPlanCandidates declares where a step's parallel candidates differ
// and how many there are: either count of one model, or one model each.
type proposedPlanCandidates struct {
	Source string   `json:"source"`
	Count  int      `json:"count"`
	Models []string `json:"models"`
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
	content, err := json.Marshal(planProposedContent(stored, policy.Budget.ToolMessages, policy.localize(command.Options.Locale, policy.Budget.Acceptance.Report)))
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
	for stepIndex, step := range arguments.Steps {
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
		check := strings.TrimSpace(step.Check)
		if check != "" && !slices.Contains(policy.Checkpoints, check) {
			return budget.Plan{}, nil, "step checkpoint " + check + " is not declared"
		}
		planned, refusal := policy.planStepCandidates(command, step, capability, purpose, check, stepIndex+1)
		if refusal != "" {
			return budget.Plan{}, nil, refusal
		}
		plan.Steps = append(plan.Steps, planned...)
	}
	if maxSteps > 0 && len(plan.Steps) > maxSteps {
		return budget.Plan{}, nil, "the plan expands to " + strconv.Itoa(len(plan.Steps)) +
			" steps, over the declared maximum of " + strconv.Itoa(maxSteps) + "; reduce the candidates or steps"
	}
	acceptance, refusal := policy.acceptanceFor(command, plan.Steps)
	if refusal != "" {
		return budget.Plan{}, nil, refusal
	}
	plan.Acceptance = acceptance
	checkpoints, refusal := policy.buildCheckpoints(command, plan.Steps, arguments.Checkpoints)
	if refusal != "" {
		return budget.Plan{}, nil, refusal
	}
	plan.Checkpoints = checkpoints
	contingencies, refusal := policy.buildContingencies(command, arguments, plan)
	if refusal != "" {
		return budget.Plan{}, nil, refusal
	}
	plan.Contingencies = contingencies
	if uncovered := plan.Uncovered(limits); len(uncovered) > 0 {
		return budget.Plan{}, nil, "budgets must give every unit the steps are quoted in at least the server estimate of that unit (" +
			unitAmountsText(plan.EstimatedTotals()) + "); missing or too low: " + unitAmountsText(uncovered) +
			". Units are never added or converted"
	}
	return plan, limits, ""
}

// planStepCandidates quotes one declared step, expanding its candidate group
// when it names one (#242). Every candidate is quoted by the server in its own
// unit, so the plan's uncovered totals and the approved limit cover them all.
func (policy *ProductionPlanPolicy) planStepCandidates(command ToolCommand, step proposedPlanStep,
	capability, purpose, check string, group int) ([]budget.PlanStep, string) {
	models := []string{strings.TrimSpace(step.Model)}
	declared := step.Candidates
	if declared != nil {
		if !policy.Candidates.Enabled {
			return nil, "this Catalog does not declare plan candidates"
		}
		source := strings.TrimSpace(declared.Source)
		if source == "" || !slices.Contains(policy.Candidates.Sources, source) {
			return nil, "candidate source " + source + " is not declared " +
				"(use " + strings.Join(policy.Candidates.Sources, ", ") + ")"
		}
		named := make([]string, 0, len(declared.Models))
		for _, model := range declared.Models {
			if model = strings.TrimSpace(model); model != "" {
				named = append(named, model)
			}
		}
		maxCount := policy.Candidates.MaxCount
		switch {
		case declared.Count > 0 && len(named) > 0:
			return nil, "a step's candidates take count or models, not both"
		case declared.Count > 0:
			if declared.Count < 2 || (maxCount > 0 && declared.Count > maxCount) {
				return nil, "candidate count must be between 2 and " + strconv.Itoa(maxCount)
			}
			// count samples one model (or the default model); the Agent gives
			// each call a different seed or input in the approved continuation.
			models = make([]string, declared.Count)
			for index := range models {
				models[index] = strings.TrimSpace(step.Model)
			}
		case len(named) > 0:
			if len(named) < 2 || (maxCount > 0 && len(named) > maxCount) {
				return nil, "candidate models must list between 2 and " + strconv.Itoa(maxCount) + " Offerings"
			}
			models = named
		default:
			return nil, "a step's candidates need count or models"
		}
	}
	planned := make([]budget.PlanStep, 0, len(models))
	for index, model := range models {
		offering, locked, refusal := policy.stepOffering(command, capability, model)
		if refusal != "" {
			return nil, refusal
		}
		estimated := budget.EstimateOffering(offering)
		if offering.CreditsMetered && estimated.Micros <= 0 {
			// A metered step the server cannot quote cannot be covered by a
			// budget: reserving zero would let it spend outside the plan (#103
			// review B1). The Agent must pick a priceable model or let the
			// person approve the step directly.
			return nil, "step " + capability + " has no server credit estimate and cannot be budgeted"
		}
		offeringID := model
		if locked {
			offeringID = string(offering.ID)
		}
		stepPlan := budget.PlanStep{
			Capability: capability,
			OfferingID: offeringID,
			Purpose:    purpose,
			Estimate:   estimated,
			Check:      check,
		}
		if declared != nil {
			stepPlan.CandidateGroup = group
			stepPlan.CandidateIndex = index + 1
		}
		if policy.PurposeBudgetNote != nil {
			// A plan step names no parameters, so the check reads the values it
			// would run with; the person sees an over-budget value on the card
			// before approving the plan, not only after the step ran (#129).
			stepPlan.PurposeBudgetNote = policy.PurposeBudgetNote(command, offering)
		}
		planned = append(planned, stepPlan)
	}
	return planned, ""
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

// stepOffering resolves the runtime Offering a step is quoted at, through the
// same chat.ResolveModel rule the generation tools choose their model with
// (#380 step 1). The layers, highest first: the person's lock for the
// capability's workspace domain, the model the Agent names in the step, the
// plan's own pin (none while proposing), the purpose's recommendation (3-D
// steps, see purposeRecommendation), the workspace picker and the Offering
// default.
//
// The second result reports whether the plan pins the step to the resolved
// Offering: the plan quoted it, so executiongrant only approves the step on
// that Offering (budget.Plan.Allows). A bare default pins nothing, because a
// capability's other shapes (a face-count variant of a remesh, say) are the
// step planner's to choose and are not this model decision. The third result
// is the Agent-facing refusal; it is empty when a model was resolved.
func (policy *ProductionPlanPolicy) stepOffering(command ToolCommand, capability, pinned string) (generation.Offering, bool, string) {
	candidates := policy.availableOfferings(capability)
	if len(candidates) == 0 {
		return generation.Offering{}, false, planUnavailableRefusal(capability)
	}
	domain := string(candidates[0].Domain)
	if domain == "" {
		// A hand-built Catalog leaves the domain out; every generation
		// capability then follows the 3-D workspace, as the plan always has.
		domain = string(generation.DomainModel3D)
	}
	profile, recommended := policy.purposeRecommendation(command, capability, domain)
	lock, workspace, confirmsWorkspace := policy.planModelDecision(command, capability, domain)
	resolution := ResolveModel(ModelRequest{
		Candidates:  candidates,
		Lock:        lock,
		Named:       strings.TrimSpace(pinned),
		Recommended: recommended,
		Profile:     profile.ID,
		Reason:      profile.Reason,
		// A plan quotes what the executor will run, so it reads the same
		// fallback: an inferred purpose never quotes over the picker's value.
		PurposeIsDefault: profile.Default,
		Workspace:        workspace,
		// The plan asks in its own words, so a recommendation that differs
		// from the workspace picker's value is a confirmation here too: a plan
		// quoted at the recommendation would be the silent switch the tool
		// refuses. The plan and the tools read the same declaration, so a
		// Catalog that declares the confirmation text behaves the same in both
		// (#380 step 2).
		ConfirmsWorkspace: confirmsWorkspace,
	})
	if resolution.Conflict != nil {
		return generation.Offering{}, false, planModelRefusal(capability, domain, lock, pinned, resolution.Conflict)
	}
	return resolution.Offering, resolution.Source != ModelSourceDefault, ""
}

// planModelDecision is the person's lock and the workspace picker's value the
// plan resolves a capability with: the ones the turn's frozen plan computed
// when the turn started, so the plan quotes what the executor will run
// (#380 step 2). A turn without a plan (a hand-built Service, or a Catalog
// wired without one) keeps the policy's own rule.
func (policy *ProductionPlanPolicy) planModelDecision(command ToolCommand, capability, domain string) (string, string, bool) {
	if entry, ok := command.Options.TurnPlan.Domain(domain, capability); ok {
		return entry.Lock, entry.Workspace, policy.ConfirmsWorkspace
	}
	return policy.planLock(command, capability, domain), strings.TrimSpace(command.Options.ModelFor(domain)),
		policy.ConfirmsWorkspace
}

// availableOfferings are the capability's enabled, runtime-available Offerings,
// in Catalog order.
func (policy *ProductionPlanPolicy) availableOfferings(capability string) []generation.Offering {
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
	return candidates
}

// purposeRecommendation is the production purpose of a 3-D step and the models
// it recommends for the step's shape (#103). Only 3-D steps follow it here: an
// image step's purpose recommendation is the composer's 2-D question, while the
// three-view recipe and the image tools in a plan run the session's 2-D model,
// so #243's rule stands and the plan quotes the workspace value. A capability
// without a generation shape (a processing step) has no recommendation either.
func (policy *ProductionPlanPolicy) purposeRecommendation(command ToolCommand, capability, domain string) (PurposeProfile, []string) {
	if domain != string(generation.DomainModel3D) {
		return PurposeProfile{}, nil
	}
	resolver := PurposeParameters{Model3D: policy.Usage}
	profile, found := resolver.Profile(domain, command.Options.ProductionBrief, false)
	if !found {
		return PurposeProfile{}, nil
	}
	return profile, RecommendedOfferings(profile, ModelShapeOfCapability(generation.Capability(capability)))
}

// planLock is the person's lock as it applies to a capability (see
// ApplicableModelLock). A lock of another capability is not this step's lock: a
// plan lists remesh, splat and motion steps the lock was never about, and
// refusing them would make every such plan unproposable. The lock's own rule is
// shared with the executor, so a step the plan quotes is a step the executor
// runs (#380 step 2).
func (policy *ProductionPlanPolicy) planLock(command ToolCommand, capability, domain string) string {
	if domain != string(generation.DomainModel3D) && domain != string(generation.DomainImage) {
		return ""
	}
	return ApplicableModelLock(policy.Offerings(), command.Options.ExplicitModelFor(domain), capability)
}

// planUnavailableRefusal is the Agent's refusal when the capability has no
// available Offering at all.
func planUnavailableRefusal(capability string) string {
	return "no available offering for capability " + capability
}

// planModelRefusal words a refused plan resolution for the Agent. The lock and the
// step's own model are the person's and the Agent's decisions, so a step that
// cannot use them is refused instead of quoted at another model.
func planModelRefusal(capability, domain, lock, pinned string, conflict *ModelConflict) string {
	switch conflict.Kind {
	case ModelConflictLocked:
		return "the person locked the " + domainLabel(domain) + " model to " + lock +
			"; step " + capability + " must use it, not " + pinned
	case ModelConflictWorkspace:
		return "the workspace model is " + conflict.Named + ", but purpose " + conflict.Profile +
			" recommends " + modelNameOf(conflict.Offering) +
			"; confirm with the user which model to keep before planning step " + capability
	case ModelConflictInput:
		return "step " + capability + " cannot run the model the person or the call named (" +
			conflict.Named + "): it cannot take this step's input"
	case ModelConflictUnavailable:
		if conflict.Layer == ModelSourcePlan {
			return "step " + capability + " pins offering " + conflict.Named + ", which is not available"
		}
		if lock != "" && conflict.Named == lock {
			return "the person locked the " + domainLabel(domain) + " model to " + lock +
				", which has no available offering"
		}
		return planUnavailableRefusal(capability)
	default:
		return planUnavailableRefusal(capability)
	}
}

// modelNameOf names an Offering's model in an Agent-facing refusal: its model
// ID, the name the generation tools accept back.
func modelNameOf(offering generation.Offering) string {
	if offering.ModelID != "" {
		return offering.ModelID
	}
	return string(offering.ID)
}

// domainLabel names a workspace domain in an Agent-facing refusal.
func domainLabel(domain string) string {
	if domain == string(generation.DomainImage) {
		return "image"
	}
	return "3D"
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
func planProposedContent(stored budget.ProductionBudget, messages catalog.ChatApprovalBudgetToolMessages, report string) map[string]any {
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
		if step.Check != "" {
			item["check"] = step.Check
		}
		if step.Optional {
			item["optional"] = true
		}
		steps = append(steps, item)
	}
	checkpoints := make([]map[string]any, 0, len(stored.Plan.Checkpoints))
	for _, checkpoint := range stored.Plan.Checkpoints {
		checks := make([]map[string]any, 0, len(checkpoint.Checks))
		for _, check := range checkpoint.Checks {
			item := map[string]any{"check": check.Check}
			if check.Minimum != nil {
				item["minimum"] = *check.Minimum
			}
			if check.Maximum != nil {
				item["maximum"] = *check.Maximum
			}
			if check.MaximumRatio != nil {
				item["maximum_ratio"] = *check.MaximumRatio
			}
			if check.Required {
				item["required"] = true
			}
			checks = append(checks, item)
		}
		checkpoints = append(checkpoints, map[string]any{
			"after_capability": checkpoint.AfterCapability, "checks": checks,
		})
	}
	contingencies := make([]map[string]any, 0, len(stored.Plan.Contingencies))
	for _, contingency := range stored.Plan.Contingencies {
		contingencies = append(contingencies, map[string]any{
			"id": contingency.ID, "trigger": contingency.Trigger,
			"capability": contingency.Step.Capability, "offering_id": contingency.Step.OfferingID,
			"purpose": contingency.Step.Purpose, "estimate": contingency.Step.Estimate,
		})
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
		"step_totals":             stored.Plan.StepTotals(),
		"contingency_totals":      stored.Plan.ContingencyTotals(),
		"steps":                   steps,
		"acceptance":              stored.Plan.Acceptance,
		"checkpoints":             checkpoints,
		"contingencies":           contingencies,
		"correction_capabilities": stored.Plan.CorrectionCapabilities,
		"report_instruction":      report,
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

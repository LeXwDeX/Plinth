package chat

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelcheck"
)

// The plan acceptance contract (#251). The Agent names processing checkpoints
// and declared contingencies; the server turns check ids into frozen
// thresholds from the purpose budget and the production review settings, and
// derives the acceptance criteria from the same purpose plus the frozen
// production brief. The Agent never supplies a number.

func (policy *ProductionPlanPolicy) localize(locale, text string) string {
	if policy.Localize == nil || text == "" {
		return text
	}
	return policy.Localize(locale, text)
}

// acceptanceFor builds the final-delivery criteria the person approves. It
// returns none for a Catalog without usage profiles (a historical plan keeps
// working).
func (policy *ProductionPlanPolicy) acceptanceFor(command ToolCommand) ([]budget.AcceptanceCriterion, string) {
	profile, _, found := policy.purposeProfile(command)
	if !found || len(policy.Budget.Acceptance.Criteria) == 0 {
		return nil, ""
	}
	maximum := policy.Budget.MaxAcceptanceCriteria
	if maximum <= 0 {
		maximum = len(modelcheck.KnownIDs()) + 1
	}
	locale := command.Options.Locale
	purpose := policy.localize(locale, profile.Label)
	criteria := make([]budget.AcceptanceCriterion, 0, maximum)
	if line := strings.TrimSpace(policy.Budget.Acceptance.PurposeLine); line != "" {
		criteria = append(criteria, budget.AcceptanceCriterion{
			ID: "purpose", Check: profile.ID,
			Statement: fillAcceptance(policy.localize(locale, line), purpose, nil),
		})
	}
	for _, check := range modelcheck.KnownIDs() {
		template, declared := policy.Budget.Acceptance.Criteria[check]
		if !declared || len(criteria) >= maximum {
			continue
		}
		planCheck, ok := policy.planCheck(check, profile)
		if !ok {
			continue
		}
		// A check with a lower bound is judged on both bounds; a range
		// template (when declared) words the same standard the checkpoint
		// freezes instead of promising only the upper bound (#251 fix).
		if rangeTemplate := policy.Budget.Acceptance.RangeCriteria[check]; planCheck.Minimum != nil && rangeTemplate != "" {
			template = rangeTemplate
		}
		if !acceptanceApplies(template, planCheck) {
			continue
		}
		criteria = append(criteria, budget.AcceptanceCriterion{
			ID: check, Check: check,
			Statement: fillAcceptance(policy.localize(locale, template), purpose, &planCheck),
		})
	}
	return criteria, ""
}

// acceptanceApplies reports whether a criterion is a condition the server
// actually evaluates for this purpose. A boolean check the purpose does not
// require is unknown, not pass, so promising "需要水密" would freeze a standard
// nobody can meet. A threshold template without the value it interpolates
// would show the literal "{minimum}–{maximum}" to the person, so it is left
// out instead.
func acceptanceApplies(template string, check budget.PlanCheck) bool {
	switch check.Check {
	case modelcheck.CheckUV, modelcheck.CheckWatertight, modelcheck.CheckSkin:
		if !check.Required {
			return false
		}
	case modelcheck.CheckTriangles, modelcheck.CheckTextureSize:
		if check.Maximum == nil || *check.Maximum <= 0 {
			return false
		}
	case modelcheck.CheckPlausibleScale:
		if check.Minimum == nil && check.Maximum == nil {
			return false
		}
	case modelcheck.CheckPivotBottomCenter, modelcheck.CheckYUp,
		modelcheck.CheckDebris, modelcheck.CheckDegenerate:
		if check.MaximumRatio == nil || *check.MaximumRatio <= 0 {
			return false
		}
	}
	if strings.Contains(template, "{minimum}") && check.Minimum == nil {
		return false
	}
	if strings.Contains(template, "{maximum}") && check.Maximum == nil && check.MaximumRatio == nil {
		return false
	}
	return true
}

// buildCheckpoints validates the Agent's checkpoint declarations and freezes
// the server thresholds. Only processing capabilities of this plan may carry
// a checkpoint.
func (policy *ProductionPlanPolicy) buildCheckpoints(command ToolCommand, steps []budget.PlanStep,
	requested []struct {
		AfterCapability string   `json:"after_capability"`
		Checks          []string `json:"checks"`
	}) ([]budget.PlanCheckpoint, string) {
	if len(requested) == 0 {
		return nil, ""
	}
	maximum := policy.Budget.MaxContingencies
	if maximum > 0 && len(requested) > maximum {
		return nil, "at most " + strconv.Itoa(maximum) + " checkpoints may be declared"
	}
	profile, _, found := policy.purposeProfile(command)
	if !found {
		return nil, "checkpoints need a declared purpose budget"
	}
	planCapabilities := map[string]bool{}
	for _, step := range steps {
		planCapabilities[step.Capability] = true
	}
	checkLimit := policy.Budget.MaxChecksPerCheckpoint
	if checkLimit <= 0 {
		checkLimit = len(modelcheck.KnownIDs())
	}
	seenCapabilities := map[string]bool{}
	checkpoints := make([]budget.PlanCheckpoint, 0, len(requested))
	for _, declared := range requested {
		capability := strings.TrimSpace(declared.AfterCapability)
		if capability == "" || !planCapabilities[capability] {
			return nil, "checkpoint capability " + capability + " is not a step of this plan"
		}
		if !policy.processingCapability(capability) {
			return nil, "checkpoint capability " + capability + " is not a processing capability"
		}
		if seenCapabilities[capability] {
			return nil, "capability " + capability + " declares two checkpoints"
		}
		seenCapabilities[capability] = true
		if len(declared.Checks) == 0 || len(declared.Checks) > checkLimit {
			return nil, "checkpoint on " + capability + " needs between 1 and " + strconv.Itoa(checkLimit) + " checks"
		}
		checks := make([]budget.PlanCheck, 0, len(declared.Checks))
		seenChecks := map[string]bool{}
		for _, raw := range declared.Checks {
			check := strings.TrimSpace(raw)
			if !modelcheck.Known(check) || seenChecks[check] {
				return nil, "checkpoint check " + check + " is not declared or is repeated"
			}
			seenChecks[check] = true
			planCheck, ok := policy.planCheck(check, profile)
			if !ok {
				return nil, "checkpoint check " + check + " has no purpose threshold"
			}
			checks = append(checks, planCheck)
		}
		checkpoints = append(checkpoints, budget.PlanCheckpoint{AfterCapability: capability, Checks: checks})
	}
	return checkpoints, ""
}

// buildContingencies validates the declared repair steps and quotes them. A
// repair must be offered by the fix table of a trigger check or be a retained
// correction capability; its estimate counts toward the approved limit.
func (policy *ProductionPlanPolicy) buildContingencies(command ToolCommand,
	arguments proposedPlanArguments, plan budget.Plan) ([]budget.PlanContingency, string) {
	maximum := policy.Budget.MaxContingencies
	if maximum > 0 && len(arguments.Contingencies) > maximum {
		return nil, "at most " + strconv.Itoa(maximum) + " contingencies may be declared"
	}
	corrections := map[string]bool{}
	for _, capability := range plan.CorrectionCapabilities {
		corrections[capability] = true
	}
	declaredChecks := map[string]bool{}
	for _, checkpoint := range plan.Checkpoints {
		for _, check := range checkpoint.Checks {
			declaredChecks[check.Check] = true
		}
	}
	seenIDs := map[string]bool{}
	contingencies := make([]budget.PlanContingency, 0, len(arguments.Contingencies))
	for _, raw := range arguments.Contingencies {
		id := strings.TrimSpace(raw.ID)
		if id == "" || seenIDs[id] {
			return nil, "every contingency needs a unique id"
		}
		seenIDs[id] = true
		capability := strings.TrimSpace(raw.Step.Capability)
		if capability == "" || !policy.processingCapability(capability) {
			return nil, "contingency " + id + " names an unknown processing capability " + capability
		}
		purpose := strings.TrimSpace(raw.Step.Purpose)
		if purpose == "" {
			return nil, "contingency " + id + " needs a purpose"
		}
		if config := policy.Budget.MaxPurposeCharacters; config > 0 && utf8.RuneCountInString(purpose) > config {
			return nil, "contingency purpose is too long"
		}
		if len(raw.Trigger) == 0 {
			return nil, "contingency " + id + " needs a trigger"
		}
		triggers := make([]string, 0, len(raw.Trigger))
		for _, candidate := range raw.Trigger {
			trigger := strings.TrimSpace(candidate)
			switch {
			case trigger == "task_failed", trigger == "checkpoint_failed", declaredChecks[trigger]:
			default:
				return nil, "contingency " + id + " triggers an undeclared check " + trigger
			}
			if !slices.Contains(triggers, trigger) {
				triggers = append(triggers, trigger)
			}
		}
		if !policy.contingencyAllowed(capability, triggers, corrections) {
			return nil, "contingency " + id + " capability " + capability +
				" is neither a declared correction capability nor a declared fix of its trigger"
		}
		offering, _, refusal := policy.stepOffering(capability, strings.TrimSpace(raw.Step.Model),
			strings.TrimSpace(command.Options.ExplicitModel3D), strings.TrimSpace(command.Options.ModelFor("image")))
		if refusal != "" {
			return nil, refusal
		}
		estimated := budget.EstimateOffering(offering)
		if offering.CreditsMetered && estimated.Micros <= 0 {
			return nil, "contingency " + id + " has no server credit estimate and cannot be budgeted"
		}
		contingencies = append(contingencies, budget.PlanContingency{
			ID: id, Trigger: triggers,
			Step: budget.PlanStep{
				Capability: capability,
				OfferingID: string(offering.ID),
				Purpose:    purpose,
				Estimate:   estimated,
			},
		})
	}
	return contingencies, ""
}

// purposeProfile resolves the purpose budget of the proposal turn from the
// frozen brief or the Schema's default profile.
func (policy *ProductionPlanPolicy) purposeProfile(command ToolCommand) (catalog.ChatModel3DUsageProfile, catalog.ChatModel3DUsageBudget, bool) {
	usage := policy.Usage
	if len(usage.Profiles) == 0 {
		return catalog.ChatModel3DUsageProfile{}, catalog.ChatModel3DUsageBudget{}, false
	}
	profile, found := policy.profileFromBrief(command.Options.ProductionBrief, usage)
	if !found {
		for _, candidate := range usage.Profiles {
			if candidate.ID == usage.DefaultProfile {
				profile, found = candidate, true
				break
			}
		}
	}
	if !found {
		profile, found = usage.Profiles[0], true
	}
	budget := profile.Budget
	if value, exists := command.Options.ProductionBrief.Integer(usage.Brief.FieldNameOfType("integer")); exists && value > 0 {
		budget.MaxTriangles = value
	}
	return profile, budget, true
}

// profileFromBrief is the same purpose selection inspect_model uses: the
// brief's profile-typed field names the purpose, if the Schema still declares
// it.
func (policy *ProductionPlanPolicy) profileFromBrief(brief ProductionBrief, usage catalog.ChatModel3DUsageSchema) (catalog.ChatModel3DUsageProfile, bool) {
	if brief.Empty() {
		return catalog.ChatModel3DUsageProfile{}, false
	}
	for _, field := range usage.Brief.Fields {
		if field.Type != "profile" {
			continue
		}
		id := brief.Text(field.Name)
		for _, profile := range usage.Profiles {
			if profile.ID == id {
				return profile, true
			}
		}
	}
	return catalog.ChatModel3DUsageProfile{}, false
}

func (policy *ProductionPlanPolicy) planCheck(check string, profile catalog.ChatModel3DUsageProfile) (budget.PlanCheck, bool) {
	switch check {
	case modelcheck.CheckTriangles:
		maximum := float64(profile.Budget.MaxTriangles)
		planned := budget.PlanCheck{Check: check, Maximum: &maximum}
		if profile.Budget.MinTriangles != nil {
			minimum := float64(*profile.Budget.MinTriangles)
			planned.Minimum = &minimum
		}
		return planned, true
	case modelcheck.CheckTextureSize:
		if profile.Budget.MaxTextureSize == nil {
			return budget.PlanCheck{Check: check}, true
		}
		maximum := float64(*profile.Budget.MaxTextureSize)
		return budget.PlanCheck{Check: check, Maximum: &maximum}, true
	case modelcheck.CheckUV:
		return budget.PlanCheck{Check: check, Required: profile.Budget.RequireUV}, true
	case modelcheck.CheckWatertight:
		return budget.PlanCheck{Check: check, Required: profile.Budget.RequireWatertight}, true
	case modelcheck.CheckSkin:
		return budget.PlanCheck{Check: check, Required: profile.Budget.RequireSkin}, true
	case modelcheck.CheckPivotBottomCenter:
		ratio := policy.Production.PivotBottomCenterRatio
		return budget.PlanCheck{Check: check, MaximumRatio: &ratio}, true
	case modelcheck.CheckYUp:
		ratio := policy.Production.YUpFlatRatio
		upright := slices.Contains(policy.Production.YUpUprightProfiles, profile.ID)
		return budget.PlanCheck{Check: check, MaximumRatio: &ratio, Required: upright}, true
	case modelcheck.CheckPlausibleScale:
		if profile.ScaleRangeM == nil {
			return budget.PlanCheck{Check: check}, true
		}
		minimum, maximum := profile.ScaleRangeM.Min, profile.ScaleRangeM.Max
		return budget.PlanCheck{Check: check, Minimum: &minimum, Maximum: &maximum}, true
	case modelcheck.CheckDebris:
		ratio := policy.Production.DebrisRatio
		return budget.PlanCheck{Check: check, MaximumRatio: &ratio}, true
	case modelcheck.CheckDegenerate:
		ratio := policy.Production.DegenerateRatio
		return budget.PlanCheck{Check: check, MaximumRatio: &ratio}, true
	}
	return budget.PlanCheck{}, false
}

func (policy *ProductionPlanPolicy) processingCapability(capability string) bool {
	for _, offering := range policy.Offerings() {
		if offering.Enabled && offering.Domain == generation.DomainProcessing && string(offering.Capability) == capability {
			return true
		}
	}
	return false
}

func (policy *ProductionPlanPolicy) contingencyAllowed(capability string, triggers []string, corrections map[string]bool) bool {
	if corrections[capability] {
		return true
	}
	for _, fix := range policy.Production.Fixes {
		if !slices.Contains(triggers, fix.Check) {
			continue
		}
		for _, option := range fix.Options {
			for _, candidate := range option.Capabilities {
				if candidate.Capability == capability {
					return true
				}
			}
		}
	}
	return false
}

func fillAcceptance(template, purpose string, check *budget.PlanCheck) string {
	replacements := []string{"{purpose}", purpose}
	if check != nil {
		if check.Minimum != nil {
			replacements = append(replacements, "{minimum}", formatThreshold(*check.Minimum))
		}
		if check.Maximum != nil {
			replacements = append(replacements, "{maximum}", formatThreshold(*check.Maximum))
		}
		if check.MaximumRatio != nil {
			replacements = append(replacements, "{maximum}", formatThreshold(*check.MaximumRatio))
		}
	}
	return strings.NewReplacer(replacements...).Replace(template)
}

func formatThreshold(value float64) string {
	if math.Trunc(value) == value {
		return strconv.FormatFloat(value, 'f', 0, 64)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

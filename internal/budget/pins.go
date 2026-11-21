package budget

// PinResolver resolves one stored Offering pin of a plan step or contingency
// step to the Offering ID of the capability it names. A plan saved before #426
// kept the Agent's model name in PlanStep.OfferingID instead of the Offering
// ID, so the caller supplies the rule that maps a name to an Offering from the
// current Catalog. It reports the Offering ID and true only when the name
// selects exactly one Offering of the capability (#434); the caller then keeps
// the stored value otherwise, so a step whose pin resolves to nothing or to
// more than one Offering still reads as off-plan instead of matching by guess.
type PinResolver func(capability, pinned string) (string, bool)

// ResolvePins returns the plan with every pinned value resolved through
// resolve (#434). Only the returned plan changes: the receiver and its slices
// keep their values, so the stored plan is never rewritten. A pin that does
// not resolve, and a step that pins nothing, keep the value stored with them.
// Steps and contingency steps follow the same rule, because Plan.Allows reads
// both as pins of their capability.
func (p Plan) ResolvePins(resolve PinResolver) Plan {
	if resolve == nil {
		return p
	}
	resolved := p
	if p.Steps != nil {
		resolved.Steps = make([]PlanStep, len(p.Steps))
		for index, step := range p.Steps {
			resolved.Steps[index] = resolveStepPin(step, resolve)
		}
	}
	if p.Contingencies != nil {
		resolved.Contingencies = make([]PlanContingency, len(p.Contingencies))
		for index, contingency := range p.Contingencies {
			item := contingency
			item.Step = resolveStepPin(contingency.Step, resolve)
			resolved.Contingencies[index] = item
		}
	}
	return resolved
}

// resolveStepPin resolves one step's pin, keeping the stored value when the
// resolver does not identify it.
func resolveStepPin(step PlanStep, resolve PinResolver) PlanStep {
	if step.OfferingID == "" {
		return step
	}
	if offeringID, resolved := resolve(step.Capability, step.OfferingID); resolved && offeringID != "" {
		step.OfferingID = offeringID
	}
	return step
}

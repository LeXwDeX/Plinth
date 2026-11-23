// Package approvalpolicy decides whether a paid step needs a human decision.
// It is a pure function of the Product Schema policy, the session approval
// mode and the step capability. It never reads or writes state and never
// approves anything by itself.
package approvalpolicy

import "slices"

const (
	// ModeAuto approves every paid step without waiting for a human.
	ModeAuto = "auto"
	// ModeManual asks a human only for the capabilities the policy lists.
	ModeManual = "manual"
	// ModeBudget approves an Agent step inside an approved production plan and
	// its total credit budget (#103). The budget decision needs the plan, the
	// step estimate and the reservation, so executiongrant resolves this mode
	// before calling RequiresHuman. A step that is not budget eligible (one
	// the browser itself started, and every step when no plan is active) is
	// judged exactly as in manual mode; RequiresHuman therefore treats budget
	// as manual here.
	ModeBudget = "budget"
)

// Policy is the server-side form of application.chat.approval_policy.
// KnownCapabilities is the set of capabilities declared by the same Schema;
// a capability outside it is never auto-approved in manual mode.
type Policy struct {
	DefaultMode                string
	Modes                      []string
	ManualApprovalCapabilities []string
	KnownCapabilities          []string
	// ManualCreditsThreshold is the per-step credit estimate from which manual
	// mode asks a person whatever the capability is (0 disables the rule: the
	// capability list alone then decides, which is how a policy built without
	// WithCreditsThreshold behaves).
	ManualCreditsThreshold int
	// Steps is set by WithSteps when application.step_policy is declared. It
	// enables the internal steps mode (ModeSteps), which is deliberately not in
	// Modes: only the server derives it for a session (#330).
	Steps *StepsPolicy
}

// NormalizeMode maps a stored or requested mode to a usable one. An unknown
// mode is treated as manual: the stricter mode is always the safe default.
func (p Policy) NormalizeMode(mode string) string {
	switch mode {
	case ModeAuto:
		if p.Allows(ModeAuto) {
			return ModeAuto
		}
	case ModeBudget:
		if p.Allows(ModeBudget) {
			return ModeBudget
		}
	case ModeSteps:
		if p.StepsEnabled() {
			return ModeSteps
		}
	}
	return ModeManual
}

// Allows reports whether the Schema offers the mode to sessions.
func (p Policy) Allows(mode string) bool {
	return slices.Contains(p.Modes, mode)
}

// RequiresHuman reports whether a human must decide this step.
//
//   - auto: never.
//   - manual: for capabilities the policy lists, and for any capability the
//     Schema does not know (including an empty one).
//   - budget: like manual here. The budget branch of executiongrant decides
//     Agent steps first; only a step it leaves to a person (no plan, off plan,
//     over budget) or a step the browser started reaches this function, and
//     all of them keep manual's rules.
//   - steps: never. The 3D step cap is decided by executiongrant against the
//     owner's setting, and a step over the cap is refused, not sent to a
//     person (#330).
//   - any other mode is treated as manual.
//
// This is the capability rule alone. RequiresHumanForCredits adds the cost rule
// of manual mode and is what the runtime decides a real step with.
func RequiresHuman(policy Policy, mode, capability string) bool {
	if normalized := policy.NormalizeMode(mode); normalized == ModeAuto || normalized == ModeSteps {
		return false
	}
	if capability == "" || !slices.Contains(policy.KnownCapabilities, capability) {
		return true
	}
	return slices.Contains(policy.ManualApprovalCapabilities, capability)
}

// RequiresHumanForCredits reports whether a human must decide a step that costs
// the given credits: the capability rule of RequiresHuman, and in manual mode
// also every step whose own estimate reaches the policy's
// ManualCreditsThreshold (#490).
//
// The cost rule is deliberately not part of RequiresHuman: the capability list
// stays the answer to "which capabilities does this deployment card", and the
// threshold adds "and which steps are expensive enough to card anyway". A step
// is judged by what it costs, not by which capability it happens to be, so a
// 50-credit processing step no longer slips through because its capability is
// not listed while a cheap 3D step stays on the list it was put on.
//
//   - auto and steps: never, whatever the step costs. Auto already approved
//     every paid step, and money is not the constraint of the 3D step mode.
//   - manual (or any mode treated as manual, budget included): the capability
//     rule, then the threshold. credits is the step's total estimate (base plus
//     extra), the number the approval card shows; a non-positive estimate never
//     reaches a positive threshold.
//
// RequiresHumanForExtraCredits stays the rule for the extra part alone: a step
// with extra credits is the person's to approve even when neither this rule nor
// the capability list asks for it.
func RequiresHumanForCredits(policy Policy, mode, capability string, credits int) bool {
	if RequiresHuman(policy, mode, capability) {
		return true
	}
	if credits <= 0 || policy.ManualCreditsThreshold <= 0 {
		return false
	}
	normalized := policy.NormalizeMode(mode)
	if normalized == ModeAuto || normalized == ModeSteps {
		return false
	}
	return credits >= policy.ManualCreditsThreshold
}

// WithCreditsThreshold returns the policy with the manual cost rule of
// application.chat.approval_policy. It is an additive option: New keeps its
// signature, and a policy without it (or with 0) decides manual mode on the
// capability list alone.
func (p Policy) WithCreditsThreshold(credits int) Policy {
	if credits < 0 {
		credits = 0
	}
	p.ManualCreditsThreshold = credits
	return p
}

// RequiresHumanForExtraCredits reports whether a human must decide a step
// that spends extra credits on top of its base price (extra export formats,
// billed conversion options).
//
//   - auto: never; the session already approves every paid step, the extra
//     part included.
//   - steps: never. Money is not the constraint of the 3D step mode (#330).
//   - manual (or any mode treated as manual): always, whatever the step's
//     capability, because the extra credits are a spend the person has not
//     agreed to until they see it on an approval card.
//
// A step without extra credits is decided by RequiresHuman alone.
func RequiresHumanForExtraCredits(policy Policy, mode string, extraCredits int) bool {
	return RequiresHumanForExtraSpend(policy, mode, extraCredits > 0)
}

// New builds the policy from the declared Schema values. known lists every
// capability the Schema declares.
func New(defaultMode string, modes, manualApproval, known []string) Policy {
	return Policy{
		DefaultMode:                defaultMode,
		Modes:                      slices.Clone(modes),
		ManualApprovalCapabilities: slices.Clone(manualApproval),
		KnownCapabilities:          slices.Clone(known),
	}
}

func RequiresHumanForExtraSpend(policy Policy, mode string, extra bool) bool {
	normalized := policy.NormalizeMode(mode)
	return extra && normalized != ModeAuto && normalized != ModeSteps
}

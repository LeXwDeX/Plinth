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
//   - any other mode is treated as manual.
func RequiresHuman(policy Policy, mode, capability string) bool {
	if policy.NormalizeMode(mode) == ModeAuto {
		return false
	}
	if capability == "" || !slices.Contains(policy.KnownCapabilities, capability) {
		return true
	}
	return slices.Contains(policy.ManualApprovalCapabilities, capability)
}

// RequiresHumanForExtraCredits reports whether a human must decide a step
// that spends extra credits on top of its base price (extra export formats,
// billed conversion options).
//
//   - auto: never; the session already approves every paid step, the extra
//     part included.
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
	return extra && policy.NormalizeMode(mode) != ModeAuto
}

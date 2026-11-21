package approvalpolicy

import "slices"

// ModeSteps is the internal 3D step mode (#330). It is not a Schema approval
// mode: it never appears in Policy.Modes, so a person cannot pick it and the
// session approval-mode API refuses it. A session gets it only from a server
// side derivation (an asset-list sub-session or an external MCP session), and
// only when application.step_policy is declared (Policy.WithSteps).
//
// Under steps nothing waits for a person and money is not the constraint:
// 2D steps and LLM calls are never counted or blocked, and only the 3D steps
// the policy counts are governed by the owner's per-entry setting.
const ModeSteps = "steps"

// The entries a steps session can belong to, and the modes the owner picks
// for each of them. The strings are the Product Schema ids
// (application.step_policy); the semantics of each mode live in the code that
// reads them, like the three approval modes above.
const (
	// StepEntryList is an asset-list sub-session (#328).
	StepEntryList = "list"
	// StepEntryMCP is an external MCP session (#332).
	StepEntryMCP = "mcp"

	// StepModeAgent leaves the budget of 3D steps to the Agent: the platform
	// sets no cap. It is the default.
	StepModeAgent = "agent"
	// StepModeUnlimited sets no cap either; it differs from agent only in what
	// the Agent is told.
	StepModeUnlimited = "unlimited"
	// StepModeCap refuses the 3D step that would be the Cap+1-th of an asset
	// (one chat session). The cap is a hard limit enforced in the decision
	// transaction.
	StepModeCap = "cap"
)

// StepSetting is one entry's 3D step setting. Cap is kept even when Mode is
// not cap, so switching back restores the number the person last chose.
type StepSetting struct {
	Mode string `json:"mode"`
	Cap  int    `json:"cap"`
}

// StepsPolicy is the server-side form of application.step_policy.
type StepsPolicy struct {
	// Counted lists the capabilities whose Tasks are 3D steps. One public Task
	// is one step, whatever number of outputs it delivers. A capability that
	// is not listed (2D images, LLM calls, anything else) is never counted.
	Counted []string
	// DefaultList and DefaultMCP are the setting of an owner who never saved
	// one, per entry.
	DefaultList StepSetting
	DefaultMCP  StepSetting
	// ExtraMaximum is the most one "give it a few more steps" write may add on
	// top of an asset's current grant; ExtraTotalMaximum bounds the grant one
	// asset keeps (#330 step 5C). Both are application.step_policy.extra_steps.
	ExtraMaximum      int
	ExtraTotalMaximum int
}

// Counts reports whether a Task of the capability is a 3D step.
func (s StepsPolicy) Counts(capability string) bool {
	return capability != "" && slices.Contains(s.Counted, capability)
}

// Default is the setting an owner without a stored one has for the entry. The
// second result is false for an entry the policy does not know.
func (s StepsPolicy) Default(entry string) (StepSetting, bool) {
	switch entry {
	case StepEntryList:
		return s.DefaultList, true
	case StepEntryMCP:
		return s.DefaultMCP, true
	}
	return StepSetting{}, false
}

// ValidStepEntry reports whether the entry is one a steps session can have.
func ValidStepEntry(entry string) bool {
	return entry == StepEntryList || entry == StepEntryMCP
}

// WithSteps returns the policy with the internal steps mode enabled. It is an
// additive option: New keeps its signature, and a policy without it treats
// "steps" like any unknown mode (manual, the stricter one).
func (p Policy) WithSteps(steps StepsPolicy) Policy {
	steps.Counted = slices.Clone(steps.Counted)
	p.Steps = &steps
	return p
}

// StepsEnabled reports whether application.step_policy is declared.
func (p Policy) StepsEnabled() bool { return p.Steps != nil }

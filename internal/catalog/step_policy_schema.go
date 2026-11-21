package catalog

import (
	"encoding/json"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// The property names of the steps object a generation tool result carries
// (#330 step 3B). The Schema declares the object as a JSON Schema under
// application.step_policy.result, and validation requires these names in it, so
// the declared contract and the emitted object cannot drift apart.
const (
	StepResultMode      = "mode"
	StepResultUsed      = "used"
	StepResultCap       = "cap"
	StepResultRemaining = "remaining"
)

// StepPolicySchema is application.step_policy (#330): the 3D step mode. Only
// 3D steps are governed; 2D steps and LLM calls are never counted or blocked.
// One public Task is one step, whatever number of outputs it delivers.
//
// Each person has two settings, one for asset lists and one for external MCP.
// A setting is one of three modes: the Agent manages the steps itself (the
// default, no platform cap), unlimited, or a hard cap of N steps per asset (one
// chat session). Credits and money are not a quota here.
//
// The section is optional so older Catalog documents stay valid. Without it
// the internal steps approval mode does not exist and the settings route is
// not registered.
type StepPolicySchema struct {
	// Modes lists the three modes with their labels. The values are the fixed
	// approvalpolicy.StepMode* ids; the Schema owns the labels.
	Modes []ValueLabel `json:"modes"`
	// DefaultMode is the mode of a person who never saved a setting, per
	// entry.
	DefaultMode StepPolicyEntryModes `json:"default_mode"`
	// Cap bounds and defaults the number of 3D steps of one asset.
	Cap StepPolicyCapSchema `json:"cap"`
	// ExtraSteps declares how many additional steps a person may grant one
	// asset (#330 step 5C). The grant is stored per asset (a chat session) and
	// added to the cap only in the cap mode: SetSessionExtraSteps writes the
	// target, Default is what the browser dialog starts with and Maximum what
	// one write may add; TotalMaximum bounds the extra steps one asset keeps.
	ExtraSteps StepPolicyExtraStepsSchema `json:"extra_steps"`
	// CountedCapabilities lists the capabilities whose Tasks count as 3D steps.
	// Server-side only.
	CountedCapabilities []schemacontract.Capability `json:"counted_capabilities"`
	// BalanceCacheSeconds is how long a read of a provider's balance is kept
	// before the provider is asked again, and BalanceReadTimeoutSeconds how
	// long one read may take. A failed read is kept for the same time, so a
	// provider that is down is not asked on every call. Both are read by the
	// shared balance reader the external MCP service (#332) and the platform
	// Agent use; required when application.mcp_public is declared.
	// Server-side only.
	BalanceCacheSeconds       int `json:"balance_cache_seconds,omitempty"`
	BalanceReadTimeoutSeconds int `json:"balance_read_timeout_seconds,omitempty"`
	// Prompt is the platform Agent's 3D step segment. It is appended after the
	// approval-mode section of sessions whose derived approval mode is the
	// internal steps mode, and never of any other session. Server-side only.
	Prompt StepPolicyPromptSchema `json:"prompt"`
	// Result declares the steps object a generation tool result carries beside
	// its budget object (#330 step 3B). Server-side only.
	Result StepPolicyResultSchema `json:"result"`
	// Messages is the copy of the server's answers. Server-side only.
	Messages StepPolicyMessagesSchema `json:"messages"`
	// Audit names the product-audit action of a changed setting.
	Audit StepPolicyAuditSchema `json:"audit"`
}

// StepPolicyEntryModes holds one value per entry a steps session can belong to:
// an asset list (#328) and an external MCP session (#332).
type StepPolicyEntryModes struct {
	List string `json:"list"`
	MCP  string `json:"mcp"`
}

// StepPolicyCapSchema is the number of 3D steps one asset may use under the
// cap mode. Default is the number a person who never saved a setting has;
// Minimum and Maximum bound what a person may save.
type StepPolicyCapSchema struct {
	Default int `json:"default"`
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

// StepPolicyExtraStepsSchema is application.step_policy.extra_steps (#330 step
// 5C): the "give it a few more steps" grant of one asset. Default is the
// dialog's initial amount; Maximum is the most one write may add on top of the
// asset's current grant; TotalMaximum is the most extra steps one asset keeps.
type StepPolicyExtraStepsSchema struct {
	Default      int `json:"default"`
	Maximum      int `json:"maximum"`
	TotalMaximum int `json:"total_maximum"`
}

// StepPolicyPromptSchema is application.step_policy.prompt (#330): the
// platform Agent's 3D step context segment. Every number is a fact the turn
// reads at request time; a read that fails is written as unknown, never as
// zero, and never blocks the turn.
type StepPolicyPromptSchema struct {
	// ContextTemplate is appended after the approval section of a steps
	// session. Placeholders: {mode_rule}, {usage}, {balances}, {costs},
	// {guidance}.
	ContextTemplate string `json:"context_template"`
	// ModeRules is the rule sentence of each step mode. Placeholders: {mode}
	// (the mode's label), {cap} (the hard cap in the cap mode).
	ModeRules []StepPolicyPromptMode `json:"mode_rules"`
	// ModeUnknownRule is the rule shown when the person's step setting could
	// not be read this turn. The segment then says the mode is unknown
	// instead of guessing one.
	ModeUnknownRule string `json:"mode_unknown_rule"`
	// UsageLine is the used/cap line of the cap mode: {used}, {cap},
	// {remaining}.
	UsageLine string `json:"usage_line"`
	// UsageLineNoCap is the used line of the agent and unlimited modes:
	// {used}.
	UsageLineNoCap string `json:"usage_line_no_cap"`
	// UsageUnknown is shown when the step standing could not be read.
	UsageUnknown string `json:"usage_unknown"`
	// BalanceItem is one known balance in its provider's own unit: {label},
	// {amount}, {unit_label}.
	BalanceItem string `json:"balance_item"`
	// BalanceValueItem is one known balance of a reader without a native
	// unit: {label}, {amount}.
	BalanceValueItem string `json:"balance_value_item"`
	// BalanceUnknown is one unreadable balance: {label}.
	BalanceUnknown string `json:"balance_unknown"`
	// BalanceSeparator joins the balance items.
	BalanceSeparator string `json:"balance_separator"`
	// BalancesLine wraps the items: {balances}.
	BalancesLine string `json:"balances_line"`
	// BalancesUnknown is shown when the deployment declares no balance
	// account.
	BalancesUnknown string `json:"balances_unknown"`
	// CostItem is one service's approximate per-step cost: {capability},
	// {offering_id}, {amount}, {unit_label}.
	CostItem string `json:"cost_item"`
	// CostSeparator joins the cost items.
	CostSeparator string `json:"cost_separator"`
	// CostsLine wraps the items: {costs}.
	CostsLine string `json:"costs_line"`
	// CostsUnknown is shown when no per-step cost is cheaply derivable.
	CostsUnknown string `json:"costs_unknown"`
	// Guidance tells the Agent how to decide whether to continue: stop once
	// the result is good enough, count the remedy before continuing, stop and
	// report honestly when the remedy is hopeless or not worth it.
	Guidance string `json:"guidance"`
}

// StepPolicyPromptMode is the rule of one step mode in the Agent's segment.
type StepPolicyPromptMode struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
}

// Rule returns the rule of one step mode.
func (prompt StepPolicyPromptSchema) Rule(id string) (string, bool) {
	for _, mode := range prompt.ModeRules {
		if mode.ID == id {
			return mode.Rule, true
		}
	}
	return "", false
}

// StepPolicyMessagesSchema is the copy the server answers with.
type StepPolicyMessagesSchema struct {
	// CapReached is what the Agent reads when a 3D step is refused because the
	// asset used all its steps. {used} and {cap} are the numbers.
	CapReached string `json:"cap_reached"`
	// ExtraStepsLimit refuses a grant above TotalMaximum: {maximum} is the
	// bound and {extra} the grant the request asked for.
	ExtraStepsLimit string `json:"extra_steps_limit"`
	// ExtraStepsSingleLimit refuses one write that adds more than Maximum in a
	// single call: {maximum} is the per-write bound.
	ExtraStepsSingleLimit string `json:"extra_steps_single_limit"`
	// ExtraStepsUnavailable refuses a grant for an item whose sub-session does
	// not exist yet; there is no asset to extend.
	ExtraStepsUnavailable string `json:"extra_steps_unavailable"`
}

// StepPolicyResultSchema is application.step_policy.result (#330 step 3B): the
// steps object a generation tool result carries in a steps session, beside the
// budget object. The object's key is declared per tool by result_fields.steps;
// its property names are the fixed vocabulary of the code (the StepResult*
// constants).
type StepPolicyResultSchema struct {
	// Schema is the object as a JSON Schema. Validation requires the StepResult*
	// properties, their types and descriptions, and that mode and used are
	// required; cap and remaining are optional because only the cap mode writes
	// them. Every description is server-side copy and is translated in the
	// non-default locale bundles.
	Schema json.RawMessage `json:"schema"`
}

// StepPolicyAuditSchema declares the audit actions recorded when a person
// changes the setting, and when a person grants one asset more steps.
type StepPolicyAuditSchema struct {
	UpdateAction string `json:"update_action"`
	ResourceType string `json:"resource_type"`
	// ExtraStepsAction and ExtraStepsResourceType name the audit entry of one
	// asset's "give it a few more steps" write (#330 step 5C). ResourceID is
	// the list item the person extended.
	ExtraStepsAction       string `json:"extra_steps_action"`
	ExtraStepsResourceType string `json:"extra_steps_resource_type"`
}

// PublicStepPolicySchema is the browser-visible part of application.step_policy:
// the modes with their labels, the defaults, the cap range and the extra-step
// bounds. The counted capabilities and the server's messages stay server-side.
type PublicStepPolicySchema struct {
	Modes       []ValueLabel               `json:"modes"`
	DefaultMode StepPolicyEntryModes       `json:"default_mode"`
	Cap         StepPolicyCapSchema        `json:"cap"`
	ExtraSteps  StepPolicyExtraStepsSchema `json:"extra_steps"`
}

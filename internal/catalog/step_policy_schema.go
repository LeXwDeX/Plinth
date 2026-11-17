package catalog

import schemacontract "github.com/LeXwDeX/Plinth/internal/schema"

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
	// CountedCapabilities lists the capabilities whose Tasks count as 3D steps.
	// Server-side only.
	CountedCapabilities []schemacontract.Capability `json:"counted_capabilities"`
	// BalanceCacheSeconds is how long a read of a provider's balance is kept
	// before the provider is asked again, and BalanceReadTimeoutSeconds how
	// long one read may take. A failed read is kept for the same time, so a
	// provider that is down is not asked on every call. Both are read by the
	// shared balance reader the external MCP service (#332) and later the
	// platform Agent use; required when application.mcp_public is declared.
	// Server-side only.
	BalanceCacheSeconds       int `json:"balance_cache_seconds,omitempty"`
	BalanceReadTimeoutSeconds int `json:"balance_read_timeout_seconds,omitempty"`
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

// StepPolicyMessagesSchema is the copy the server answers with.
type StepPolicyMessagesSchema struct {
	// CapReached is what the Agent reads when a 3D step is refused because the
	// asset used all its steps. {used} and {cap} are the numbers.
	CapReached string `json:"cap_reached"`
}

// StepPolicyAuditSchema declares the audit action recorded when a person
// changes the setting.
type StepPolicyAuditSchema struct {
	UpdateAction string `json:"update_action"`
	ResourceType string `json:"resource_type"`
}

// PublicStepPolicySchema is the browser-visible part of application.step_policy:
// the modes with their labels, the defaults and the cap range. The counted
// capabilities and the server's messages stay server-side.
type PublicStepPolicySchema struct {
	Modes       []ValueLabel         `json:"modes"`
	DefaultMode StepPolicyEntryModes `json:"default_mode"`
	Cap         StepPolicyCapSchema  `json:"cap"`
}

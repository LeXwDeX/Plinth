package agentconfiguration

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// legacyDefaultsJSON is what a version published before format 2 (#525) needs
// to be read against today's Schema: the tool names the v1.8.0 Schema declared.
// It is history, not configuration: it never changes when the Schema does.
// Prompts need no history: a version's stored prompt text is never read
// (#554).
//
//go:embed legacy_defaults.json
var legacyDefaultsJSON []byte

type legacyDefaults struct {
	// ToolBaseline is the tool set a row without a format marker can speak
	// for. A baseline tool missing from its enabled_tool_names was turned off
	// by an administrator; a tool outside the baseline did not exist then.
	ToolBaseline []string `json:"tool_baseline"`
}

func loadLegacyDefaults() (legacyDefaults, error) {
	var result legacyDefaults
	if err := json.Unmarshal(legacyDefaultsJSON, &result); err != nil {
		return legacyDefaults{}, fmt.Errorf("agent configuration: legacy defaults: %w", err)
	}
	if len(result.ToolBaseline) == 0 {
		return legacyDefaults{}, fmt.Errorf("agent configuration: legacy defaults: empty tool baseline")
	}
	return result, nil
}

func (l legacyDefaults) toolBaseline() map[string]bool {
	names := make(map[string]bool, len(l.ToolBaseline))
	for _, name := range l.ToolBaseline {
		names[name] = true
	}
	return names
}

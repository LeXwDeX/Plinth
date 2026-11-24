package agentconfiguration

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// legacyDefaultsJSON is what a version published before format 2 (#525) needs
// to be read against today's Schema: the tool names the v1.8.0 Schema declared
// and fingerprints of the default prompt texts shipped until then. It is
// history, not configuration: it never changes when the Schema does.
//
//go:embed legacy_defaults.json
var legacyDefaultsJSON []byte

type legacyDefaults struct {
	// ToolBaseline is the tool set a row without a format marker can speak
	// for. A baseline tool missing from its enabled_tool_names was turned off
	// by an administrator; a tool outside the baseline did not exist then.
	ToolBaseline []string `json:"tool_baseline"`
	// DefaultTextFingerprints maps a prompt part ("system_prompt",
	// "domain_context.<key>") to the sha256 of every default text it had, with
	// each subset of prompt sections stripped (a deployment strips the sections
	// of capabilities without an enabled Offering when it stores a version).
	DefaultTextFingerprints map[string][]string `json:"default_text_fingerprints"`
}

func loadLegacyDefaults() (legacyDefaults, error) {
	var result legacyDefaults
	if err := json.Unmarshal(legacyDefaultsJSON, &result); err != nil {
		return legacyDefaults{}, fmt.Errorf("agent configuration: legacy defaults: %w", err)
	}
	if len(result.ToolBaseline) == 0 || len(result.DefaultTextFingerprints[promptPartSystem]) == 0 {
		return legacyDefaults{}, fmt.Errorf("agent configuration: legacy defaults: empty tool baseline or prompt fingerprints")
	}
	return result, nil
}

// matchesDefaultText reports whether text is, byte for byte, a default an
// earlier release shipped for the prompt part.
func (l legacyDefaults) matchesDefaultText(part, text string) bool {
	digest := sha256.Sum256([]byte(text))
	want := hex.EncodeToString(digest[:])
	for _, fingerprint := range l.DefaultTextFingerprints[part] {
		if fingerprint == want {
			return true
		}
	}
	return false
}

func (l legacyDefaults) toolBaseline() map[string]bool {
	names := make(map[string]bool, len(l.ToolBaseline))
	for _, name := range l.ToolBaseline {
		names[name] = true
	}
	return names
}

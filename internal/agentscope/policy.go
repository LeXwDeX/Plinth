// Package agentscope decides which Agent tools a session scope exposes. It is
// a pure function of the Product Schema scope policy, the session scope and a
// tool's declared class. It never reads or writes state.
//
// A scope ("2d", "3d" or "auto") is the user's choice of what the Agent may
// do in a session. Every Agent tool declares one class in the Schema
// (chat.tools[].execution.agent_scope and the same member of the control and
// MCP tools): "2d", "3d", "cross" (moves between 2D and 3D) or "utility"
// (neither). The Schema's mode table says which classes each scope allows.
package agentscope

import (
	"slices"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const (
	// Mode ids. "auto" is the only mode the code names: it is the fail-safe
	// choice for an unreadable stored value and keeps every tool.
	Mode2D   = "2d"
	Mode3D   = "3d"
	ModeAuto = "auto"

	// Tool classes.
	Class2D      = "2d"
	Class3D      = "3d"
	ClassCross   = "cross"
	ClassUtility = "utility"
)

// Classes lists every tool class a Schema may declare.
var Classes = []string{Class2D, Class3D, ClassCross, ClassUtility}

// Mode is one scope the Schema offers.
type Mode struct {
	ID    string
	Label string
	// Description tells the Agent what the scope means for this session.
	Description string
	// Allows lists the tool classes the scope exposes.
	Allows []string
}

// Policy is the server-side form of application.chat.agent_scope.
type Policy struct {
	DefaultMode string
	Modes       []Mode
}

// Enabled reports whether the policy declares any scope. A policy without
// modes is the "no harness" configuration of unit tests; the application
// always builds one from the Schema.
func (p Policy) Enabled() bool { return len(p.Modes) > 0 }

// Mode returns the Schema mode with this id.
func (p Policy) Mode(id string) (Mode, bool) {
	for _, mode := range p.Modes {
		if mode.ID == id {
			return mode, true
		}
	}
	return Mode{}, false
}

// Allows reports whether the Schema offers the scope to sessions.
func (p Policy) Allows(id string) bool {
	_, ok := p.Mode(id)
	return ok
}

// Normalize maps a stored or requested scope to one the Schema offers. A
// value the Schema no longer offers (or an empty one, as on a turn frozen
// before scopes existed) becomes the Schema default.
func (p Policy) Normalize(id string) string {
	if p.Allows(id) {
		return id
	}
	if p.Allows(p.DefaultMode) {
		return p.DefaultMode
	}
	return ModeAuto
}

// ToolAllowed reports whether a tool of this class is exposed in the scope.
// A class the Schema does not know (including an empty one) is treated as
// "cross": only a scope that allows moving between 2D and 3D exposes it, so a
// tool without a declaration is never available by accident.
func (p Policy) ToolAllowed(scope, class string) bool {
	mode, ok := p.Mode(p.Normalize(scope))
	if !ok {
		return false
	}
	if !slices.Contains(Classes, class) {
		class = ClassCross
	}
	return slices.Contains(mode.Allows, class)
}

// FromSchema builds the policy from application.chat.agent_scope.
func FromSchema(schema catalog.ChatAgentScopeSchema) Policy {
	modes := make([]Mode, 0, len(schema.Modes))
	for _, mode := range schema.Modes {
		modes = append(modes, Mode{
			ID:          mode.ID,
			Label:       mode.Label,
			Description: mode.Description,
			Allows:      slices.Clone(mode.Allows),
		})
	}
	return Policy{DefaultMode: schema.DefaultMode, Modes: modes}
}

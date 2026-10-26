package chat

import (
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

// toolRegistry compiles the Product Schema's public descriptions and private
// execution policy once. The model supplies only a tool name and arguments;
// it cannot supply an implementation, effect, capability or permission.
type toolRegistry struct {
	ordered []registeredTool
	byName  map[string]registeredTool
	allowed func(capability, domain string) bool
	// offeringAllowed reports whether an enabled, runtime-available Offering
	// with this model_id serves the capability. It narrows a tool's
	// offering_argument choices (#189); nil leaves the declared enum as is.
	offeringAllowed func(capability, modelID string) bool
	// scope maps the session Agent scope to the tool classes it exposes. It
	// is read from the same Schema as the tool declarations.
	scope         agentscope.Policy
	harness       *resolve.Snapshot
	harnessPolicy catalog.ChatHarnessSchema
}

type registeredTool struct {
	name        string
	description string
	parameters  json.RawMessage
	execution   catalog.ChatToolExecutionSchema
	generation  *catalog.ChatToolSchema
}

func paidTool(execution catalog.ChatToolExecutionSchema) bool {
	for _, effect := range execution.Effects {
		if effect == "PAID_TASK" {
			return true
		}
	}
	return false
}

func compileToolRegistry(schema catalog.ChatSchema, allowed func(capability, domain string) bool) (*toolRegistry, error) {
	registry := &toolRegistry{
		byName:  make(map[string]registeredTool),
		allowed: allowed,
		scope:   agentscope.FromSchema(schema.AgentScope),
	}
	add := func(entry registeredTool) error {
		if entry.name == "" || entry.execution.ImplementationKey == "" || len(entry.parameters) == 0 {
			return fmt.Errorf("tool registry: incomplete declaration %q", entry.name)
		}
		if _, exists := registry.byName[entry.name]; exists {
			return fmt.Errorf("tool registry: duplicate name %q", entry.name)
		}
		registry.byName[entry.name] = entry
		registry.ordered = append(registry.ordered, entry)
		return nil
	}
	for index := range schema.Tools {
		tool := &schema.Tools[index]
		if err := add(registeredTool{name: tool.Name, description: tool.Description,
			parameters: tool.Parameters, execution: tool.Execution, generation: tool}); err != nil {
			return nil, err
		}
	}
	for _, tool := range schema.AgentTools {
		if err := add(registeredTool{name: tool.Name, description: tool.Description,
			parameters: tool.Parameters, execution: tool.Execution}); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// permits applies the one availability rule for the model's tool list and for
// execution. The workspace domain in view is only a hint for the prompt: a
// tool of the other domain stays available, as long as the tool can actually
// run (a generation tool needs an available Offering for one of its
// capabilities).
func (registry *toolRegistry) permits(tool registeredTool, bound bool) bool {
	if bound && paidTool(tool.execution) {
		// An already accepted paid Task is only observed; mode changes cannot
		// revoke or silently resubmit that historical Task.
		return true
	}
	if !registry.harnessPermits(tool) {
		return false
	}
	if tool.generation == nil || registry.allowed == nil {
		return true
	}
	if len(tool.execution.Capabilities) == 0 {
		return false
	}
	if choices, narrowed := registry.offeringChoices(tool); narrowed && len(choices) == 0 {
		return false
	}
	// The gate asks about the tool's own workspace domain.
	domain := ""
	if len(tool.execution.Domains) > 0 {
		domain = tool.execution.Domains[0]
	}
	for _, capability := range tool.execution.Capabilities {
		if registry.allowed(string(capability), domain) {
			// A tool with input-dependent capability selection can still run
			// when one branch is available. The selected branch is validated
			// again by Generation before any paid submission.
			return true
		}
	}
	return false
}

// scopeBlocks reports whether the Agent scope refuses a call of this tool.
// It is the one rule behind both the model's tool list and the server-side
// check of a call. An already accepted paid Task is only observed, so a scope
// change never revokes it (the same exception permits makes for bound calls).
// A registry whose Schema declares no scope applies none.
func (registry *toolRegistry) scopeBlocks(tool registeredTool, scope string, bound bool) bool {
	if !registry.scope.Enabled() || (bound && paidTool(tool.execution)) {
		return false
	}
	return !registry.scope.ToolAllowed(scope, tool.execution.AgentScope)
}

// describe is the tool list the model is offered for a turn in this scope.
func (registry *toolRegistry) describe(scope string) []agents.Tool {
	result := make([]agents.Tool, 0, len(registry.ordered))
	for _, tool := range registry.ordered {
		if !registry.permits(tool, false) || registry.scopeBlocks(tool, scope, false) {
			continue
		}
		parameters := append(json.RawMessage(nil), tool.parameters...)
		if choices, narrowed := registry.offeringChoices(tool); narrowed {
			parameters = narrowOfferingArgument(parameters, tool.generation.OfferingArgument, choices)
		}
		result = append(result, agents.Tool{Name: tool.name, Description: tool.description,
			Parameters: parameters})
	}
	return result
}

func (registry *toolRegistry) resolve(name string, bound bool) (catalog.ChatToolSchema, bool) {
	tool, found := registry.byName[name]
	if !found || !registry.permits(tool, bound) {
		return catalog.ChatToolSchema{}, false
	}
	if tool.generation == nil {
		return catalog.ChatToolSchema{Name: name, Execution: tool.execution}, true
	}
	// Resolve the server-owned declaration, never a model-supplied Tool field.
	return *tool.generation, true
}

// withheld names the tools that would run but for the Agent scope, in
// declaration order. The model is told these names so it can explain the
// refusal instead of guessing why a tool is missing.
func (registry *toolRegistry) withheld(scope string) []string {
	var names []string
	for _, tool := range registry.ordered {
		if registry.permits(tool, false) && registry.scopeBlocks(tool, scope, false) {
			names = append(names, tool.name)
		}
	}
	return names
}

// offeringChoices lists the declared offering_argument values whose Offering
// is enabled and available now, in declaration order. narrowed is false when
// the tool has no such argument or the registry has no availability source.
func (registry *toolRegistry) offeringChoices(tool registeredTool) ([]string, bool) {
	if tool.generation == nil || tool.generation.OfferingArgument == "" || registry.offeringAllowed == nil {
		return nil, false
	}
	var parameters struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.parameters, &parameters); err != nil {
		return []string{}, true
	}
	choices := []string{}
	for _, value := range parameters.Properties[tool.generation.OfferingArgument].Enum {
		for _, capability := range tool.execution.Capabilities {
			if registry.offeringAllowed(string(capability), value) {
				choices = append(choices, value)
				break
			}
		}
	}
	return choices, true
}

// narrowOfferingArgument rewrites only the enum of the offering argument, so
// the model is offered exactly the templates it can run this turn.
func narrowOfferingArgument(parameters json.RawMessage, argument string, choices []string) json.RawMessage {
	var document map[string]any
	if err := json.Unmarshal(parameters, &document); err != nil {
		return parameters
	}
	properties, _ := document["properties"].(map[string]any)
	property, _ := properties[argument].(map[string]any)
	if property == nil {
		return parameters
	}
	property["enum"] = choices
	encoded, err := json.Marshal(document)
	if err != nil {
		return parameters
	}
	return encoded
}

// noteTools appends the turn's note to the description of each generation
// tool offered (#329 E'). The note is about the frozen turn options only; it
// never changes which tools are offered or their parameters.
func (orchestrator *Orchestrator) noteTools(registry *toolRegistry, tools []agents.Tool, options TurnOptions) {
	if orchestrator.toolNote == nil {
		return
	}
	for index := range tools {
		registered, known := registry.byName[tools[index].Name]
		if !known || registered.generation == nil {
			continue
		}
		if note := orchestrator.toolNote(*registered.generation, options); note != "" {
			tools[index].Description += " " + note
		}
	}
}

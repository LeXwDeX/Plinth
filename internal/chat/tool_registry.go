package chat

import (
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
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
	// offering_argument choices (#189) and filters the model choices derived
	// from Offering knowledge (#249); nil leaves the declared enum as is.
	offeringAllowed func(capability, modelID string) bool
	// offerings lists the runtime Offerings of a capability, in Catalog
	// order. With a ModelChoices declaration it makes the model enum follow
	// the enabled, available Offerings (#249); nil keeps the declared enum.
	offerings func(capability string) []generation.Offering
	// craftPromptUnknown is the Schema's word for an undeclared Offering
	// knowledge fact in the craft brief (#249).
	craftPromptUnknown string
	// scope maps the session Agent scope to the tool classes it exposes. It
	// is read from the same Schema as the tool declarations.
	scope         agentscope.Policy
	harness       *resolve.Snapshot
	harnessPolicy catalog.ChatHarnessSchema
	// cache is set only on the per-request fork a turn is served from (#413).
	// The Orchestrator's long-lived registry keeps it nil, so nothing derived
	// from the runtime Offerings is shared between requests.
	cache *registryCache
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
		byName:             make(map[string]registeredTool),
		allowed:            allowed,
		scope:              agentscope.FromSchema(schema.AgentScope),
		craftPromptUnknown: schema.Agent.OfferingKnowledge.UnknownText,
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

// bindOfferingSources attaches the runtime Offering sources to a registry
// compiled from the Schema. They are the running server's state, which neither
// the Schema nor a frozen Agent configuration declares, so every registry the
// Orchestrator builds takes them through here: its own, and the one recompiled
// from a frozen configuration (#379). Without them a model enum stays at its
// declared value and the craft brief loses the Offering knowledge (#249).
func (registry *toolRegistry) bindOfferingSources(
	allowed func(capability, modelID string) bool,
	offerings func(capability string) []generation.Offering,
) {
	registry.offeringAllowed = allowed
	registry.offerings = offerings
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
	if choices, narrowed := registry.modelChoices(tool); narrowed && len(choices) == 0 {
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
	return registry.offeredInScope(scope).tools
}

// withheld names the tools that would run but for the Agent scope, in
// declaration order. The model is told these names so it can explain the
// refusal instead of guessing why a tool is missing.
func (registry *toolRegistry) withheld(scope string) []string {
	return registry.offeredInScope(scope).withheld
}

// narrowToPlan is the turn-level narrowing of the offered table (#382): a
// capability the turn's frozen plan has nothing to run takes its tools out of
// the table, so the model is not offered a schema it could not pay for and the
// table shrinks by more than the Agent scope alone removes. The plan's own
// pinned tools (TurnPlan.Tools, the Harness operation of this turn) always stay.
//
// Only a capability with no candidate Offering at all is refused here. A
// capability the plan merely wants the person to confirm (a workspace value
// that differs from the purpose recommendation) keeps its tool: the call is
// how that confirmation is asked for, so removing it would remove the question.
//
// A plan that refuses nothing changes nothing, and a narrowing that would empty
// the table is dropped whole: a turn whose every tool was refused must still be
// able to answer, and the tool executor refuses the call it would not run
// anyway.
func (registry *toolRegistry) narrowToPlan(tools []agents.Tool, plan *TurnPlan) []agents.Tool {
	if plan == nil {
		return tools
	}
	refused := map[generation.Capability]bool{}
	for _, entry := range plan.Domains {
		if entry.Conflict == nil || entry.OfferingID != "" || len(entry.Candidates) > 0 {
			continue
		}
		refused[generation.Capability(entry.Capability)] = true
	}
	pinned := map[string]bool{}
	for _, name := range plan.Tools {
		pinned[name] = true
	}
	if len(refused) == 0 {
		return tools
	}
	kept := make([]agents.Tool, 0, len(tools))
	for _, tool := range tools {
		if pinned[tool.Name] {
			kept = append(kept, tool)
			continue
		}
		if registered, known := registry.byName[tool.Name]; known &&
			toolRefusedBy(registered.execution, refused) {
			continue
		}
		kept = append(kept, tool)
	}
	if len(kept) == 0 {
		return tools
	}
	return kept
}

// toolRefusedBy reports whether one of the tool's capabilities is one the plan
// refused. A tool with no declared capability is not a generation tool and
// stays.
func toolRefusedBy(execution catalog.ChatToolExecutionSchema, refused map[generation.Capability]bool) bool {
	for _, capability := range execution.Capabilities {
		if refused[capability] {
			return true
		}
	}
	return false
}

// offeredInScope evaluates the registry once per scope: every tool is
// permitted once, and the same pass splits the permitted tools into the list
// the model is offered and the names the Agent scope withholds (#413). A turn
// asks for both lists several times (the request, the scope block, each
// prompt block), so the result is memoized on the request-scoped fork.
func (registry *toolRegistry) offeredInScope(scope string) offeredTools {
	if registry.cache != nil {
		if offered, ok := registry.cache.loadOffered(scope); ok {
			return offered
		}
	}
	offered := offeredTools{tools: make([]agents.Tool, 0, len(registry.ordered))}
	for _, tool := range registry.ordered {
		if !registry.permits(tool, false) {
			continue
		}
		if registry.scopeBlocks(tool, scope, false) {
			offered.withheld = append(offered.withheld, tool.name)
			continue
		}
		parameters := append(json.RawMessage(nil), tool.parameters...)
		if choices, narrowed := registry.offeringChoices(tool); narrowed {
			parameters = narrowArgumentEnum(parameters, tool.generation.OfferingArgument, choices)
		}
		if choices, narrowed := registry.modelChoices(tool); narrowed {
			parameters = narrowArgumentEnum(parameters, tool.generation.ModelArgument, choices)
		}
		offered.tools = append(offered.tools, agents.Tool{Name: tool.name, Description: tool.description,
			Parameters: parameters})
	}
	if registry.cache != nil {
		registry.cache.storeOffered(scope, offered)
	}
	return offered
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

// offeringChoices lists the declared offering_argument values whose Offering
// is enabled and available now, in declaration order. narrowed is false when
// the tool has no such argument or the registry has no availability source.
// The result is memoized per tool for the request (#413): the model enum, the
// tool list and the availability gate all ask for it.
func (registry *toolRegistry) offeringChoices(tool registeredTool) ([]string, bool) {
	if tool.generation == nil || tool.generation.OfferingArgument == "" || registry.offeringAllowed == nil {
		return nil, false
	}
	if registry.harness != nil && registry.harness.Selection.OperationID != "" && tool.name == registry.harness.ToolName {
		// A selected operation fixes its Offering in the tool contract, which
		// replaces the open choice of service (and its enum) altogether.
		return nil, false
	}
	if registry.cache != nil {
		if choices, ok := registry.cache.loadOfferingChoices(tool.name); ok {
			return choices.choices, choices.narrowed
		}
	}
	choices, narrowed := registry.declaredOfferingChoices(tool)
	if registry.cache != nil {
		registry.cache.storeOfferingChoices(tool.name, choiceSet{choices: choices, narrowed: narrowed})
	}
	return choices, narrowed
}

func (registry *toolRegistry) declaredOfferingChoices(tool registeredTool) ([]string, bool) {
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

// modelChoices lists the model ids an enabled, runtime-available Offering
// gives a ModelArgument tool this turn (#249), in Catalog order. narrowed is
// false for a historical Catalog whose tool still declares its enum. The
// result is memoized per tool for the request (#413): permits, the tool list
// and the availability gate all ask for it, and it must not refetch the
// Offering directory each time (#379 keeps the frozen path bound to it).
func (registry *toolRegistry) modelChoices(tool registeredTool) ([]string, bool) {
	if tool.generation == nil || tool.generation.ModelArgument == "" ||
		tool.generation.ModelChoices == nil || registry.offerings == nil {
		return nil, false
	}
	if registry.cache != nil {
		if choices, ok := registry.cache.loadModelChoices(tool.name); ok {
			return choices.choices, choices.narrowed
		}
	}
	var candidates []generation.Offering
	for _, capability := range tool.execution.Capabilities {
		for _, offering := range registry.offeringsFor(string(capability)) {
			if offering.Capability != capability {
				continue
			}
			if registry.offeringAllowed != nil && !registry.offeringAllowed(string(capability), offering.ModelID) {
				continue
			}
			candidates = append(candidates, offering)
		}
	}
	choices := catalog.DerivedModelChoices(*tool.generation, candidates, nil)
	if registry.cache != nil {
		registry.cache.storeModelChoices(tool.name, choiceSet{choices: choices, narrowed: true})
	}
	return choices, true
}

// offeringsFor reads the runtime Offerings of a capability once per request
// (#413). The source is the server's wiring of the Catalog; without a
// request-scoped cache (a long-lived registry, a test) it reads straight
// through.
func (registry *toolRegistry) offeringsFor(capability string) []generation.Offering {
	if registry.offerings == nil {
		return nil
	}
	if registry.cache == nil {
		return registry.offerings(capability)
	}
	return registry.cache.offeringsFor(registry.offerings, capability)
}

// narrowArgumentEnum rewrites only the enum of one string argument, so the
// model is offered exactly the choices it can run this turn.
func narrowArgumentEnum(parameters json.RawMessage, argument string, choices []string) json.RawMessage {
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
		// #250: name the session's identity anchors and the argument that
		// selects one. The tool note is about the frozen turn options only.
		if note := orchestrator.anchorToolNote(*registered.generation, options); note != "" {
			tools[index].Description += " " + note
		}
	}
}

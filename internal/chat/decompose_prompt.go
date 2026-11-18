package chat

import (
	"context"
	"strconv"
	"strings"
)

// assetListProposeToolName is the Agent tool the decompose guidance finishes
// with. The tool executor owns the name; this package only checks that the
// turn actually offers it.
const assetListProposeToolName = "propose_asset_list"

// decomposePrompt renders the scene-decomposition guidance (#331) as a system
// prompt segment, like the step policy block. It appears only where the
// guidance can act:
//
//   - the Catalog declares application.asset_list.decompose with its
//     agent_prompt and the deployment wires the list workflow;
//   - the session belongs to a project (a list lives in a project);
//   - this turn carries at least one picture (a text-only turn has nothing to
//     decompose); and
//   - the turn offers propose_asset_list in this Agent scope.
//
// The model cannot be asked to classify the intent ahead of time, so the
// copy itself states when it applies ("several objects, make everything /
// split it up") and leaves the choice to the model. The {maximum} placeholder
// is filled from maximum_items_per_image, the same value the crop tool and
// the proposal check read.
func (orchestrator *Orchestrator) decomposePrompt(
	ctx context.Context,
	options TurnOptions,
	registry *toolRegistry,
	registryErr error,
	scope string,
	indexedByMessage [][]indexedChatImage,
	lastUserMessage int,
) string {
	if orchestrator.service == nil || registryErr != nil || registry == nil {
		return ""
	}
	policy := orchestrator.service.assetLists
	if policy == nil || policy.Service == nil {
		return ""
	}
	decompose := policy.Schema.Decompose
	if decompose == nil || strings.TrimSpace(decompose.AgentPrompt) == "" {
		return ""
	}
	// A production list belongs to a project; a session outside one cannot
	// propose, and the guidance would only mislead.
	if strings.TrimSpace(options.ProjectID) == "" {
		return ""
	}
	if lastUserMessage < 0 || lastUserMessage >= len(indexedByMessage) ||
		len(indexedByMessage[lastUserMessage]) == 0 {
		return ""
	}
	tool, found := registry.byName[assetListProposeToolName]
	if !found || !registry.permits(tool, false) || registry.scopeBlocks(tool, scope, false) {
		return ""
	}
	prompt := strings.ReplaceAll(
		orchestrator.text(ctx, decompose.AgentPrompt),
		"{maximum}", strconv.Itoa(decompose.MaximumItemsPerImage),
	)
	return "\n\n" + prompt
}

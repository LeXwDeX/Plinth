package app

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// newAssetListKernelSubmissionGate builds the third paid-gate layer (#328
// §2.5.5). Every Task admitted through generation.Module consults the same
// session→item→run→presence read as the tool Preflight, so already prepared
// operations and other SubmitPrepared callers cannot bypass a paused list.
// The refusal carries only the reason; transports map it to Catalog copy.
// It is installed only for an execution section that is actually on: without
// one no run exists to pause, so the read would only refuse for a list nothing
// advances (#494).
func newAssetListKernelSubmissionGate(
	service *assetlist.Service,
	schema *catalog.AssetListSchema,
) func(ctx context.Context, ownerID, sessionID string) error {
	if service == nil || schema == nil || schema.Execution == nil || !schema.Execution.Enabled {
		return nil
	}
	return func(ctx context.Context, ownerID, sessionID string) error {
		decision, err := service.SubmissionAllowed(ctx, ownerID, sessionID)
		if err != nil {
			// A read failure is a refusal: guessing that submission is safe
			// could take a paid step during a pause whose state we cannot see.
			return &generation.SubmissionBlockedError{Reason: "unavailable", Cause: err}
		}
		if decision.Allowed {
			return nil
		}
		return &generation.SubmissionBlockedError{Reason: decision.Reason}
	}
}

// newAssetListSubmissionGate builds the first paid-gate layer (#328 §2.5.5)
// for the generation tools' Preflight. It is installed only when the Catalog
// declares application.asset_list.execution with enabled=true; an old Catalog,
// or one that keeps the section off, keeps the tool path unchanged (#494).
func newAssetListSubmissionGate(
	service *assetlist.Service,
	schema *catalog.AssetListSchema,
) func(ctx context.Context, command chat.ToolCommand) string {
	if service == nil || schema == nil || schema.Execution == nil || !schema.Execution.Enabled {
		return nil
	}
	messages := schema.Messages
	return func(ctx context.Context, command chat.ToolCommand) string {
		decision, err := service.SubmissionAllowed(ctx, command.OwnerID, command.SessionID)
		if err != nil {
			// A read failure is a refusal: guessing that submission is safe
			// could take a paid step during a pause whose state we cannot see.
			return assetListGateMessage(messages.ItemReportFailed,
				"the production list is unavailable; this step was not submitted")
		}
		if decision.Allowed {
			return ""
		}
		return assetListSubmissionMessage(messages, decision.Reason)
	}
}

// assetListSubmissionMessage maps the gate's reason to the Catalog's visible
// pause copy. Unknown reasons (a stopped or completed run) share "stopped".
func assetListSubmissionMessage(messages catalog.AssetListMessagesSchema, reason string) string {
	switch reason {
	case assetlist.PausePresence:
		return assetListGateMessage(messages.PausePresence,
			"the person is away; automated production is paused and this step was not submitted")
	case assetlist.PauseUser:
		return assetListGateMessage(messages.PauseUser,
			"automated production is paused by the person; this step was not submitted")
	case assetlist.PauseStepCap:
		return assetListGateMessage(messages.PauseStepCap,
			"the item's 3D step cap was reached; this step was not submitted")
	case assetlist.PauseFailure:
		return assetListGateMessage(messages.PauseFailure,
			"automated production is paused after a failure; this step was not submitted")
	default:
		return assetListGateMessage(messages.Stopped,
			"automated production is stopped; this step was not submitted")
	}
}

// assetListGateMessage keeps an empty historical Catalog copy from turning a
// refusal into an empty (allowed) answer.
func assetListGateMessage(declared, fallback string) string {
	message := strings.TrimSpace(declared)
	if message == "" {
		return fallback
	}
	// The gate knows the pause reason but not the list/item details some
	// historical copies interpolate; blank them so the refusal stays readable
	// instead of leaking template braces to the model.
	return strings.NewReplacer("{name}", "", "{title}", "", "{detail}", "").Replace(message)
}

// withoutAssetListTools is the Agent's injection view of the tool declarations
// when the Catalog keeps application.asset_list.execution off (#494). The three
// tools stay declared in the Catalog and the capability stays wired; only the
// table a turn offers loses them, because without a run no item reaches working
// and the calls could never finish. Dropping them here — rather than at
// execution — also answers a configuration frozen while they were still
// offered: the turn registry no longer resolves the name, so the Orchestrator
// returns the Schema's tool-unavailable copy instead of an unknown-tool error.
func withoutAssetListTools(
	tools []catalog.ChatAgentToolSchema,
	executionEnabled bool,
) []catalog.ChatAgentToolSchema {
	if executionEnabled {
		return tools
	}
	kept := make([]catalog.ChatAgentToolSchema, 0, len(tools))
	for _, tool := range tools {
		if catalog.IsAssetListTool(tool.Name) {
			continue
		}
		kept = append(kept, tool)
	}
	return kept
}

// assetListUnavailableCopy is the Schema's "this tool is not available in this
// turn" copy, with {tool} still in it, for an executor whose Catalog keeps the
// production-list tools out of the table. Empty when the execution section is
// on, which leaves the executor's own unwired-tool fallback in place.
func assetListUnavailableCopy(application catalog.ApplicationSchema) string {
	if application.AssetListExecutionEnabled() {
		return ""
	}
	return application.Chat.Presentation.Messages.ToolUnavailable
}

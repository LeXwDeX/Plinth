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
func newAssetListKernelSubmissionGate(
	service *assetlist.Service,
	schema *catalog.AssetListSchema,
) func(ctx context.Context, ownerID, sessionID string) error {
	if service == nil || schema == nil || schema.Execution == nil {
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
// declares application.asset_list.execution; an old Catalog keeps the tool
// path unchanged.
func newAssetListSubmissionGate(
	service *assetlist.Service,
	schema *catalog.AssetListSchema,
) func(ctx context.Context, command chat.ToolCommand) string {
	if service == nil || schema == nil || schema.Execution == nil {
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

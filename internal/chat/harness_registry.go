package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

type harnessContextKey struct{}

func (o *Orchestrator) harnessRegistry(ctx context.Context, base *toolRegistry) *toolRegistry {
	snapshot, _ := ctx.Value(harnessContextKey{}).(*resolve.Snapshot)
	if snapshot == nil || snapshot.Selection.OperationID == "" {
		return base
	}
	registry := *base
	registry.harness = snapshot
	registry.harnessPolicy = o.schema.Harness
	registry.ordered = append([]registeredTool(nil), base.ordered...)
	registry.byName = make(map[string]registeredTool, len(base.byName))
	for i, tool := range registry.ordered {
		if tool.name == snapshot.ToolName {
			tool.parameters = append(json.RawMessage(nil), snapshot.ToolParameters...)
		}
		if tool.name == o.schema.Harness.PrerequisiteTool {
			tool.description = o.schema.Harness.ProposalMessage
			tool.parameters, _ = json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "enum": snapshot.PrerequisitePaths}}, "required": []string{"path"}, "additionalProperties": false})
		}
		registry.ordered[i] = tool
		registry.byName[tool.name] = tool
	}
	return &registry
}

func (registry *toolRegistry) harnessPermits(tool registeredTool) bool {
	snapshot := registry.harness
	if snapshot == nil || snapshot.Selection.OperationID == "" {
		return true
	}
	if slices.Contains(registry.harnessPolicy.BaseTools, tool.name) && !paidTool(tool.execution) {
		return true
	}
	if tool.name == snapshot.ToolName {
		// A turn that only lacks a parameter keeps its tool: a call is refused
		// with the missing input named, so the Agent can ask for it (#187 D).
		callable := snapshot.Status == resolve.StatusReady || (snapshot.Status == resolve.StatusNeedsInput && snapshot.CallableWithMissing)
		return callable && len(snapshot.ToolParameters) > 0
	}
	return tool.name == registry.harnessPolicy.PrerequisiteTool && snapshot.CanPropose && len(snapshot.PrerequisitePaths) > 0
}

// SwitchableHarnessOperations lists the function operations a choice option may
// switch the session to (#374): wired to a tool, not declared unavailable, and,
// once the turn froze a selection, enabled in it and not the one already fixed.
// The Agent reads the list in its function context and an option's
// switch_operation action may only name one of them, so the person's click maps
// to a selection the resolver can accept; saving it is still checked there
// (offering identity, availability, parameters).
func SwitchableHarnessOperations(operations []catalog.HarnessOperationSchema, snapshot *resolve.Snapshot) []catalog.HarnessOperationSchema {
	var enabled []string
	active := ""
	if snapshot != nil {
		enabled, active = snapshot.Selection.EnabledOperations, snapshot.Selection.OperationID
	}
	var switchable []catalog.HarnessOperationSchema
	for _, operation := range operations {
		if operation.ToolName == "" || operation.UnavailableReason != "" || operation.ID == active ||
			(snapshot != nil && !slices.Contains(enabled, operation.ID)) {
			continue
		}
		switchable = append(switchable, operation)
	}
	return switchable
}

func (o *Orchestrator) harnessPrompt(snapshot *resolve.Snapshot, registry *toolRegistry, scope string) string {
	if snapshot == nil || snapshot.Selection.OperationID == "" {
		return ""
	}
	tools := []string{}
	if registry != nil {
		for _, tool := range registry.describe(scope) {
			tools = append(tools, tool.Name)
		}
	}
	// Explicit projection: never marshal the private resolver snapshot or Offering.
	type sourceChoice struct {
		TaskID    string `json:"task_id"`
		AssetID   string `json:"asset_id"`
		Variant   string `json:"variant"`
		Model     string `json:"model"`
		CreatedAt string `json:"created_at"`
		Preferred bool   `json:"preferred,omitempty"`
	}
	choices := []sourceChoice{}
	for _, c := range snapshot.SourceCandidates {
		choices = append(choices, sourceChoice{c.Source.TaskID, c.Source.AssetID, c.Fingerprint.Variant, c.Model, c.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), c.Preferred})
	}
	// The functions a direction card option may switch to (#374). They are only
	// offered while the Agent can open that card at all.
	type switchableOperation struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	var switchable []switchableOperation
	if slices.Contains(tools, "ask_choice") {
		for _, operation := range SwitchableHarnessOperations(o.schema.Harness.Operations, snapshot) {
			switchable = append(switchable, switchableOperation{operation.ID, operation.Label, operation.Description})
		}
	}
	data := struct {
		Selection  harness.Selection     `json:"selection"`
		Status     string                `json:"status"`
		Missing    []string              `json:"missing_parameters"`
		Images     []string              `json:"conversation_parameters,omitempty"`
		Tools      []string              `json:"executable_tools"`
		Paths      [][]string            `json:"prerequisite_proposals,omitempty"`
		Sources    []sourceChoice        `json:"source_candidates,omitempty"`
		Truncated  bool                  `json:"source_scan_truncated,omitempty"`
		Switchable []switchableOperation `json:"switchable_operations,omitempty"`
	}{snapshot.Selection, snapshot.Status, snapshot.MissingParameters, snapshot.ConversationParameters, tools, snapshot.PrerequisitePaths, choices, snapshot.SourceScanTruncated, switchable}
	encoded, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(o.schema.Harness.ContextTemplate, "{snapshot}", string(encoded))
}

package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

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
	data := struct {
		Selection harness.Selection `json:"selection"`
		Status    string            `json:"status"`
		Missing   []string          `json:"missing_parameters"`
		Images    []string          `json:"conversation_parameters,omitempty"`
		Tools     []string          `json:"executable_tools"`
		Paths     [][]string        `json:"prerequisite_proposals,omitempty"`
		Sources   []sourceChoice    `json:"source_candidates,omitempty"`
		Truncated bool              `json:"source_scan_truncated,omitempty"`
	}{snapshot.Selection, snapshot.Status, snapshot.MissingParameters, snapshot.ConversationParameters, tools, snapshot.PrerequisitePaths, choices, snapshot.SourceScanTruncated}
	encoded, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(o.schema.Harness.ContextTemplate, "{snapshot}", string(encoded))
}

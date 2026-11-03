package chat

import "strings"

// multiviewDirective is the system-context block of a turn that carries the
// person's structured multiview request (#65). It tells the Agent which tool
// to call and with which group and model; the pictures and their views are
// bound by the tool from the request itself, never from what the Agent
// writes. The block is added only while the tool is offered this turn.
func (orchestrator *Orchestrator) multiviewDirective(registry *toolRegistry, options TurnOptions) string {
	request := options.Multiview
	multiview := orchestrator.schema.ThreeView.Multiview
	if request == nil || multiview.Tool == "" {
		return ""
	}
	offered := false
	for _, tool := range registry.describe(orchestrator.agentScope.Normalize(options.AgentScope)) {
		offered = offered || tool.Name == multiview.Tool
	}
	if !offered {
		return ""
	}
	views := make([]string, 0, len(request.Views))
	for _, view := range request.Views {
		views = append(views, view.View)
	}
	template := multiview.UploadDirective
	if request.Group != "" {
		template = multiview.GroupDirective
	}
	return strings.NewReplacer(
		"{tool}", multiview.Tool,
		"{group}", request.Group,
		"{model}", request.Model,
		"{views}", strings.Join(views, multiview.ViewSeparator),
	).Replace(template)
}

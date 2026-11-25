package chat

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// ThreeViewGroupFact is one three-view group of a session as the Agent sees it
// (#630): its run ID, the value create_model_3d_from_views takes as the group,
// and where each view stands.
type ThreeViewGroupFact struct {
	RunID string
	Views []ThreeViewViewFact
}

// ThreeViewViewFact is one view of a group. TaskID and AssetID name its current
// attempt and picture; they are not shown to the Agent and only let a refusal
// tell which group a value the Agent passed by mistake belongs to.
type ThreeViewViewFact struct {
	View    string
	Status  string
	TaskID  string
	AssetID string
}

// ThreeViewGroupsText renders the Schema's groups block (#630): one line per
// group, its run ID and each view as key=status. No groups, or a Schema
// without the block, renders nothing.
func ThreeViewGroupsText(schema catalog.ChatThreeViewSchema, groups []ThreeViewGroupFact) string {
	messages := schema.Messages
	if len(groups) == 0 || strings.TrimSpace(messages.GroupsContext) == "" {
		return ""
	}
	separator := schema.Multiview.ViewSeparator
	if separator == "" {
		separator = ", "
	}
	lines := make([]string, 0, len(groups))
	for _, group := range groups {
		views := make([]string, 0, len(group.Views))
		for _, view := range group.Views {
			views = append(views, view.View+"="+view.Status)
		}
		lines = append(lines, strings.NewReplacer(
			"{run}", group.RunID, "{views}", strings.Join(views, separator),
		).Replace(messages.GroupsContextLine))
	}
	return strings.ReplaceAll(messages.GroupsContext, "{groups}", strings.Join(lines, "\n"))
}

// ThreeViewGroupOf finds the listed group one of whose views has value as its
// Task or picture: the value an Agent passes as the group by mistake (#630).
func ThreeViewGroupOf(groups []ThreeViewGroupFact, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	for _, group := range groups {
		for _, view := range group.Views {
			if view.TaskID == value || view.AssetID == value {
				return group.RunID, true
			}
		}
	}
	return "", false
}

// threeViewGroupsPrompt is the per-turn block of the session's three-view
// groups (#630). A view the person starts on the card reaches no tool result,
// and the create_three_view result that named the run ID can be compacted
// away, so the run ID and the view states are read from the groups each turn.
// The block appears only while the multiview tool is offered this turn.
func (orchestrator *Orchestrator) threeViewGroupsPrompt(ctx context.Context, registry *toolRegistry,
	options TurnOptions, ownerID, sessionID string) string {
	if orchestrator.threeViewGroups == nil || sessionID == "" || !orchestrator.multiviewToolOffered(registry, options) {
		return ""
	}
	groups, err := orchestrator.threeViewGroups(ctx, ownerID, sessionID)
	if err != nil {
		if orchestrator.logger != nil {
			orchestrator.logger.Warn("三视图组上下文读取失败，本轮不列出三视图组",
				"owner_id", ownerID, "session_id", sessionID, "error", err)
		}
		return ""
	}
	return ThreeViewGroupsText(orchestrator.schema.ThreeView, groups)
}

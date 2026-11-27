package chat

// Session scope of the Agent's read-only tools (#665). get_task and the
// picture compare_model_reference names by ID read only what belongs to the
// session: its own Tasks, the Tasks a branch inherited from its ancestor
// sessions (#186), the references of the production-list item a sub-session
// runs (#328) and what the session's project registers (#240). Another
// session of the same owner is refused in the same Schema words as an ID that
// does not exist, so the answer never says whether it exists. Pictures the
// session's own history or verified selection carries stay readable: they are
// the session's record (a branch copies its ancestor's messages, an item
// sub-session starts from its item's references).

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// AgentReadScope answers whether a Task or an input asset is readable by a
// read-only Agent tool of a session. A Task or asset the session's history or
// verified selection carries is always readable. Owner is always required; a nil scope
// function relaxes nothing.
type AgentReadScope struct {
	// TaskInSession is the generation scope: the session's own Task or one a
	// branch ref names (#186).
	TaskInSession func(ctx context.Context, owner generation.OwnerID, session string, task generation.TaskID) (bool, error)
	// ProjectReference reports whether the owner's project stores the
	// reference (#240). A reference with a TaskID and no AssetID matches any
	// stored picture or model of that Task.
	ProjectReference func(ctx context.Context, owner, projectID string, reference project.Reference) (bool, error)
	// Refused is presentation.messages.agent_read_out_of_scope ({id}); Texts
	// translates it into the turn's locale.
	Refused string
	Texts   *catalog.TextLocalizer
}

// ReadScopeRefusal is the refusal of an ID outside the session scope, or of
// one that does not exist: Message is the localized Schema text.
type ReadScopeRefusal struct{ Message string }

func (refusal *ReadScopeRefusal) Error() string { return refusal.Message }

// Refusal returns the Schema refusal for id in the command's locale; fallback
// is used when the Schema declares none.
func (scope *AgentReadScope) Refusal(command ToolCommand, id, fallback string) *ReadScopeRefusal {
	if scope == nil || strings.TrimSpace(scope.Refused) == "" {
		return &ReadScopeRefusal{Message: fallback}
	}
	text := scope.Refused
	if scope.Texts != nil {
		text = scope.Texts.Text(command.Options.Locale, text)
	}
	return &ReadScopeRefusal{Message: strings.ReplaceAll(text, "{id}", id)}
}

// TaskReadable reports whether the command's session may read the Task.
func (scope *AgentReadScope) TaskReadable(ctx context.Context, command ToolCommand, task generation.Task) (bool, error) {
	if task.ID == "" || command.OwnerID == "" || task.OwnerID != generation.OwnerID(command.OwnerID) {
		return false, nil
	}
	if command.SessionID != "" && task.SessionID == command.SessionID {
		return true, nil
	}
	// A picture of the Task the session carries (a studio Task the person
	// attached, an item reference) is the session's own record.
	if commandCarriesTask(command, string(task.ID)) {
		return true, nil
	}
	return scope.taskIDReadable(ctx, command, task.ID)
}

// InputAssetReadable reports whether the command's session may read an owned
// input asset: one its history or selection carries, a Task output of a Task
// the session may read, an item reference or a project registration. An asset
// with a Task is readable when either its Task is in scope or the asset itself
// is (a list item or the project names the asset): a planning session reads
// the asset its item's sub-session made when the item or project names it,
// whichever session made its Task (#328, #665).
func (scope *AgentReadScope) InputAssetReadable(ctx context.Context, command ToolCommand, asset generation.InputAsset) (bool, error) {
	if asset.ID == "" || command.OwnerID == "" || asset.OwnerID != generation.OwnerID(command.OwnerID) {
		return false, nil
	}
	if commandCarriesAsset(command, string(asset.ID)) {
		return true, nil
	}
	if asset.TaskID != "" {
		if command.SessionID != "" && asset.SessionID == command.SessionID {
			return true, nil
		}
		if commandCarriesTask(command, string(asset.TaskID)) {
			return true, nil
		}
		allowed, err := scope.taskIDReadable(ctx, command, asset.TaskID)
		if err != nil || allowed {
			return allowed, err
		}
	}
	for _, reference := range command.Options.AssetListReferences {
		if reference.InputAssetID == string(asset.ID) {
			return true, nil
		}
	}
	return scope.projectStores(ctx, command, project.Reference{InputAssetID: string(asset.ID)})
}

func (scope *AgentReadScope) taskIDReadable(ctx context.Context, command ToolCommand, id generation.TaskID) (bool, error) {
	if scope == nil {
		return false, nil
	}
	if scope.TaskInSession != nil && command.SessionID != "" {
		allowed, err := scope.TaskInSession(ctx, generation.OwnerID(command.OwnerID), command.SessionID, id)
		if err != nil || allowed {
			return allowed, err
		}
	}
	for _, reference := range command.Options.AssetListReferences {
		if reference.InputAssetID == "" && reference.TaskID == string(id) {
			return true, nil
		}
	}
	return scope.projectStores(ctx, command, project.Reference{TaskID: string(id)})
}

func (scope *AgentReadScope) projectStores(ctx context.Context, command ToolCommand, reference project.Reference) (bool, error) {
	projectID := strings.TrimSpace(command.Options.ProjectID)
	if scope == nil || scope.ProjectReference == nil || projectID == "" {
		return false, nil
	}
	return scope.ProjectReference(ctx, command.OwnerID, projectID, reference)
}

// commandCarriesTask reports whether the session's history or the turn's
// verified selection carries an attachment of the Task.
func commandCarriesTask(command ToolCommand, taskID string) bool {
	for _, message := range command.ReferenceHistory() {
		for _, attachment := range message.Attachments {
			if attachment.TaskID == taskID {
				return true
			}
		}
	}
	for _, attachment := range command.Options.Selection {
		if attachment.TaskID == taskID {
			return true
		}
	}
	return false
}

// commandCarriesAsset reports whether the session's history or the turn's
// verified selection carries the asset.
func commandCarriesAsset(command ToolCommand, assetID string) bool {
	for _, message := range command.ReferenceHistory() {
		for _, attachment := range message.Attachments {
			if attachment.AssetID == assetID {
				return true
			}
		}
	}
	for _, attachment := range command.Options.Selection {
		if attachment.AssetID == assetID {
			return true
		}
	}
	return false
}

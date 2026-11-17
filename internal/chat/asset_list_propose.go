package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// Production lists (#327): the Agent proposes one draft list of assets for the
// person's current project, the person confirms or rejects it, and later steps
// run each item. This file owns the proposal tool's validation and reference
// resolution; the list workflow, its bounds and its persistence stay in
// assetlist. Proposing creates no Task and no session, counts no step and
// charges nothing.

// AssetListPolicy wires the proposal tool: the workflow service every
// proposal goes through and the Schema section that declares its items and
// words its replies. The tool only proposes a draft; confirmation stays with
// the person.
type AssetListPolicy struct {
	Service *assetlist.Service
	Schema  catalog.AssetListSchema
}

// SetAssetLists installs the production-list proposal writer. Application
// assembly calls it after the asset-list service exists (the service reuses
// this service's offering snapshot, so it is built after it), like
// SetProductionPlans.
func (service *Service) SetAssetLists(policy AssetListPolicy) {
	if policy.Service == nil {
		service.assetLists = nil
		return
	}
	service.assetLists = &policy
}

// proposedAssetListArguments is the tool's declared object. The project,
// owner and session never come from the arguments: the proposal tool has no
// project_id parameter, and the server holds the command's scope.
type proposedAssetListArguments struct {
	Title string                  `json:"title"`
	Items []proposedAssetListItem `json:"items"`
}

// proposedAssetListItem is one declared asset. ReferenceHandles are the
// @imgN/@projectN tokens the turn's frozen history and project snapshot
// resolve; the Schema's kind and route ids are validated by assetlist.
type proposedAssetListItem struct {
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	RouteID          string   `json:"route_id"`
	ReferenceHandles []string `json:"reference_handles"`
}

// ProposeAssetList validates and stores one draft list for the Agent's own
// session. The command's owner, session, message and call come from the
// server-held tool call; the arguments only describe the list. References are
// resolved against this turn's frozen history and project snapshot, never
// against another session's or project's objects. The service response maps
// to the Schema's refusal copy, so an internal error text never reaches the
// Agent.
func (service *Service) ProposeAssetList(ctx context.Context, command ToolCommand) (ToolResult, error) {
	policy := service.assetLists
	if policy == nil {
		return assetListRefusal("asset lists are unavailable"), nil
	}
	messages := policy.Schema.Messages
	invalid := assetListMessage(messages.ProposeInvalidItems,
		"the asset list arguments are invalid")
	var arguments proposedAssetListArguments
	decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
	decoder.DisallowUnknownFields()
	if len(command.Call.Arguments) == 0 || decoder.Decode(&arguments) != nil ||
		decoder.Decode(new(any)) != io.EOF {
		return assetListRefusal(invalid), nil
	}
	if strings.TrimSpace(arguments.Title) == "" || len(arguments.Items) == 0 {
		return assetListRefusal(invalid), nil
	}
	projectID := strings.TrimSpace(command.Options.ProjectID)
	if projectID == "" {
		return assetListRefusal(assetListMessage(messages.ProposeSessionNotInProject,
			"the session is not in a project")), nil
	}
	items := make([]assetlist.ItemInput, 0, len(arguments.Items))
	for _, item := range arguments.Items {
		// Kind values and routes are checked by assetlist against the Schema;
		// only the tool's required fields are checked here.
		if strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Name) == "" ||
			strings.TrimSpace(item.Description) == "" {
			return assetListRefusal(invalid), nil
		}
		references, resolved := service.resolveAssetListReferences(command,
			item.ReferenceHandles, policy.Schema.Limits.MaxReferencesPerItem)
		if !resolved {
			return assetListRefusal(assetListMessage(messages.ProposeReferenceUnresolved,
				"a reference handle could not be resolved")), nil
		}
		items = append(items, assetlist.ItemInput{
			Kind:        item.Kind,
			Name:        item.Name,
			Description: item.Description,
			RouteID:     item.RouteID,
			References:  references,
		})
	}
	stored, err := policy.Service.Propose(ctx, assetlist.ProposeInput{
		Owner:           command.OwnerID,
		ProjectID:       projectID,
		SessionID:       command.SessionID,
		OriginMessageID: command.AssistantMessageID,
		RequestKey:      assetListRequestKey(command),
		Title:           arguments.Title,
		Items:           items,
	})
	if err != nil {
		switch {
		case errors.Is(err, assetlist.ErrSessionNotInProject):
			return assetListRefusal(assetListMessage(messages.ProposeSessionNotInProject,
				"the session is not in the project")), nil
		case errors.Is(err, assetlist.ErrInvalid), errors.Is(err, assetlist.ErrLimit):
			return assetListRefusal(invalid), nil
		default:
			// The store's own text is never shown to the Agent.
			return assetListRefusal(assetListMessage(messages.ProposeFailed,
				"the asset list could not be proposed")), nil
		}
	}
	content, err := json.Marshal(buildAssetListContent(stored, messages))
	if err != nil {
		return ToolResult{NoSubmission: true}, err
	}
	summary, _ := json.Marshal(map[string]string{"asset_list_id": stored.ID})
	return ToolResult{AgentContent: string(content), Summary: summary, NoSubmission: true}, nil
}

// assetListRequestKey is the proposal's stable identity. It includes the turn
// so a repeated model call ID in a later turn cannot replay an old list; a
// replay inside the same turn returns the stored list untouched.
func assetListRequestKey(command ToolCommand) string {
	identity, _ := json.Marshal([]string{
		command.SessionID, command.AssistantMessageID, command.Call.ID, command.IdempotencyKey(),
	})
	digest := sha256.Sum256(identity)
	return "asset-list:" + hex.EncodeToString(digest[:])
}

// resolveAssetListReferences turns one item's handles into stored project
// references. @imgN handles resolve through the session's frozen history and
// selection; @projectN handles through the turn's frozen project snapshot. A
// picture-only project asset cannot become a reference (it stores URLs, not an
// asset id), so it is refused rather than guessed. The per-item Schema bound
// is applied after duplicate handles collapse.
func (service *Service) resolveAssetListReferences(command ToolCommand, handles []string,
	maximum int) ([]project.Reference, bool) {
	references := make([]project.Reference, 0, len(handles))
	seen := make(map[project.Reference]bool, len(handles))
	add := func(reference project.Reference) bool {
		if !reference.Valid() {
			return false
		}
		if seen[reference] {
			return true
		}
		seen[reference] = true
		references = append(references, reference)
		return maximum <= 0 || len(references) <= maximum
	}
	for _, raw := range handles {
		handle := strings.TrimSpace(raw)
		if handle == "" {
			return nil, false
		}
		imageHandle := false
		if service.handles.Enabled() {
			if _, _, ok := service.handles.Parse(handle); ok || service.handles.IsSelectionAlias(handle) {
				imageHandle = true
			}
		}
		if imageHandle {
			resolved, err := service.handles.ResolveHandles(command.ReferenceHistory(),
				command.Options.Selection, []string{handle}, AttachmentKindImage)
			if err != nil || len(resolved) == 0 {
				return nil, false
			}
			for _, item := range resolved {
				reference, ok := assetListAttachmentReference(item.Attachment)
				if !ok || !add(reference) {
					return nil, false
				}
			}
			continue
		}
		resolved, ok := assetListProjectReferences(command.Options.ProjectContext, handle)
		if !ok {
			return nil, false
		}
		for _, reference := range resolved {
			if !add(reference) {
				return nil, false
			}
		}
	}
	return references, true
}

// assetListAttachmentReference stores one verified picture as a project
// reference. A generated picture names its Task asset; an upload, render or
// capture screenshot names its owned input asset. A picture without a stable
// id (a pasted data URI, a legacy generated file without an asset) cannot be
// referenced.
func assetListAttachmentReference(attachment Attachment) (project.Reference, bool) {
	if attachment.Kind != AttachmentKindImage {
		return project.Reference{}, false
	}
	switch {
	case attachment.AssetID == "":
		return project.Reference{}, false
	case attachment.CaptureID != "":
		return project.Reference{InputAssetID: attachment.AssetID}, true
	case attachment.TaskID != "":
		return project.Reference{TaskID: attachment.TaskID, AssetID: attachment.AssetID}, true
	default:
		return project.Reference{InputAssetID: attachment.AssetID}, true
	}
}

// assetListProjectReferences finds the frozen project asset a @projectN token
// names. Only its saved 3D models carry ids; pictures are URLs in the turn's
// snapshot, so a picture-only asset is unresolved. A handle absent from the
// frozen snapshot belongs to another project or none of this owner's.
func assetListProjectReferences(context *ProjectContext, handle string) ([]project.Reference, bool) {
	asset := context.ProjectAssetByHandle(handle)
	if asset == nil || len(asset.Models) == 0 {
		return nil, false
	}
	references := make([]project.Reference, 0, len(asset.Models))
	for _, model := range asset.Models {
		if model.TaskID == "" || model.AssetID == "" {
			return nil, false
		}
		references = append(references, project.Reference{TaskID: model.TaskID, AssetID: model.AssetID})
	}
	return references, true
}

// assetListUnavailableItem is one planned item whose route cannot run now.
type assetListUnavailableItem struct {
	Position int    `json:"position"`
	Name     string `json:"name"`
	Reason   string `json:"reason"`
}

// assetListProposedContent is the Agent-facing result of the proposal tool.
// The state copy is the Schema's proposed message with the server's counts, so
// the Agent repeats what the person will see on the card.
type assetListProposedContent struct {
	ListID                  string                     `json:"list_id"`
	Version                 int64                      `json:"version"`
	Status                  string                     `json:"status"`
	ItemCount               int                        `json:"item_count"`
	PlannedStepCount        int                        `json:"planned_step_count"`
	PlannedModel3DStepCount int                        `json:"planned_model3d_step_count"`
	UnavailableItems        []assetListUnavailableItem `json:"unavailable_items"`
	Message                 string                     `json:"message"`
}

func buildAssetListContent(list assetlist.List, messages catalog.AssetListMessagesSchema) assetListProposedContent {
	itemCount := list.ItemCount
	if itemCount == 0 && len(list.Items) > 0 {
		itemCount = len(list.Items)
	}
	unavailable := make([]assetListUnavailableItem, 0)
	for index, item := range list.Items {
		reasons := make([]string, 0)
		for _, step := range item.Route {
			if step.Available {
				continue
			}
			reason := strings.TrimSpace(step.Reason)
			if reason == "" {
				reason = step.Capability
			}
			if reason != "" {
				reasons = append(reasons, reason)
			}
		}
		if len(reasons) == 0 {
			continue
		}
		unavailable = append(unavailable, assetListUnavailableItem{
			Position: index + 1,
			Name:     item.Name,
			Reason:   strings.Join(reasons, "; "),
		})
	}
	status := list.Status
	if list.EffectiveStatus != "" {
		status = list.EffectiveStatus
	}
	message := strings.NewReplacer(
		"{count}", strconv.Itoa(itemCount),
		"{steps}", strconv.Itoa(list.PlannedStepCount),
	).Replace(assetListMessage(messages.Proposed,
		"Asset list proposed; wait for the person to confirm it."))
	return assetListProposedContent{
		ListID:                  list.ID,
		Version:                 list.Version,
		Status:                  status,
		ItemCount:               itemCount,
		PlannedStepCount:        list.PlannedStepCount,
		PlannedModel3DStepCount: list.PlannedModel3DStepCount,
		UnavailableItems:        unavailable,
		Message:                 message,
	}
}

// assetListMessage keeps a historical Catalog's empty copy readable instead of
// handing the Agent an empty refusal.
func assetListMessage(declared, fallback string) string {
	if strings.TrimSpace(declared) == "" {
		return fallback
	}
	return declared
}

func assetListRefusal(reason string) ToolResult {
	content, _ := json.Marshal(map[string]any{"success": false, "error": reason})
	return ToolResult{AgentContent: string(content), Error: reason, NoSubmission: true}
}

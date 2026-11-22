package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// The read-only production-list tool (#329f): the Agent reads the current
// state of the list its session works on — every item's state, the artifacts it
// produced and the self-check conclusion the review recorded. It reads: it
// creates no Task, charges nothing, decides nothing and never accepts or
// returns an item.

// assetListReadArguments is the tool's declared object. The owner, session and
// project never come from the arguments: the server holds them in the tool
// command. An empty list_id reads the list the session belongs to.
type assetListReadArguments struct {
	ListID string `json:"list_id"`
}

// assetListReadCheck is one recorded acceptance check. Status is the snapshot's
// own word (pass, fail or unknown); unknown is a fact, never a pass.
type assetListReadCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
}

// assetListReadSelfCheck is the Agent's persisted self-check conclusion of the
// reported Task. Status is the Schema's conclusion role; Summary and Remaining
// are what the Agent wrote.
type assetListReadSelfCheck struct {
	Status    string   `json:"status"`
	Summary   string   `json:"summary,omitempty"`
	Remaining []string `json:"remaining,omitempty"`
}

// assetListReadArtifact is one stored result file of an item.
type assetListReadArtifact struct {
	AssetID   string `json:"asset_id"`
	Role      string `json:"role,omitempty"`
	Variant   string `json:"variant,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// assetListReadItem is one item's read projection: identity, state, what the
// item produced and what the review judged. The plan values and the acceptance
// board stay where they are: this tool reports facts about the item's work.
type assetListReadItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	StateReason string `json:"state_reason,omitempty"`
	// ReturnCount is how many times the person sent the item back; Round is
	// the item's Agent turns.
	ReturnCount int `json:"return_count"`
	Round       int `json:"round"`
	// TaskID is the reported successful Task, when the item reported one.
	TaskID    string                  `json:"task_id,omitempty"`
	Artifacts []assetListReadArtifact `json:"artifacts"`
	Checks    []assetListReadCheck    `json:"checks"`
	SelfCheck *assetListReadSelfCheck `json:"self_check,omitempty"`
}

// assetListReadContent is the tool's answer: the list's identity and state plus
// every item, in the list's order.
type assetListReadContent struct {
	ListID string `json:"list_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// RunStatus is the list's execution state when it has a run; empty means
	// the list is not running automatically (or the deployment has no runner).
	RunStatus string              `json:"run_status,omitempty"`
	Items     []assetListReadItem `json:"items"`
}

// ReadAssetList answers the read-only get_asset_list tool (#329f). It resolves
// the list from the arguments or from the session's own project, then projects
// the stored items and their review snapshots. A missing or foreign list
// answers the Schema's list-not-found copy: an internal error text never
// reaches the Agent.
func (service *Service) ReadAssetList(ctx context.Context, command ToolCommand) (ToolResult, error) {
	policy := service.assetLists
	if policy == nil || policy.Service == nil {
		return assetListRefusal("asset lists are unavailable"), nil
	}
	messages := policy.Schema.Messages
	notFound := assetListMessage(messages.ListNotFound,
		"that list does not exist or does not belong to you")
	var arguments assetListReadArguments
	// No arguments at all is the normal call; only a malformed object is
	// refused, so a stray key cannot silently change what is read.
	if len(command.Call.Arguments) > 0 {
		decoder := json.NewDecoder(strings.NewReader(string(command.Call.Arguments)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&arguments) != nil || decoder.Decode(new(any)) != io.EOF {
			return assetListRefusal(notFound), nil
		}
	}
	projectID := strings.TrimSpace(command.Options.ProjectID)
	if projectID == "" {
		return assetListRefusal(notFound), nil
	}
	list, err := assetListForRead(ctx, policy.Service, command.OwnerID, command.SessionID, projectID,
		strings.TrimSpace(arguments.ListID))
	if err != nil {
		return assetListRefusal(notFound), nil
	}
	content, err := json.Marshal(assetListRead(list))
	if err != nil {
		return assetListRefusal(notFound), nil
	}
	summary, _ := json.Marshal(map[string]string{
		"asset_list_id": list.ID,
		"status":        list.Status,
	})
	return ToolResult{
		AgentContent: string(content),
		Summary:      summary,
		NoSubmission: true,
	}, nil
}

// assetListForRead resolves which list the session reads: the named one, the
// item's own list when the session is a list item's sub-session, otherwise the
// project's current list. Every missing or foreign read is ErrNotFound, so the
// caller answers one refusal for all of them.
func assetListForRead(
	ctx context.Context,
	lists *assetlist.Service,
	ownerID, sessionID, projectID, listID string,
) (assetlist.List, error) {
	if listID == "" {
		// An item's sub-session knows its own list without a second guess.
		item, found, err := lists.ItemBySession(ctx, ownerID, sessionID)
		if err != nil && !errors.Is(err, assetlist.ErrNotFound) {
			return assetlist.List{}, err
		}
		if err == nil && found {
			listID = item.ListID
		}
	}
	if listID == "" {
		proposed, err := lists.ListSession(ctx, ownerID, sessionID)
		if err != nil {
			return assetlist.List{}, err
		}
		if len(proposed) > 0 {
			listID = proposed[0].ID
		}
	}
	if listID == "" {
		project, err := lists.List(ctx, ownerID, projectID)
		if err != nil {
			return assetlist.List{}, err
		}
		for _, candidate := range project {
			if candidate.Status == assetlist.StatusConfirmed {
				listID = candidate.ID
				break
			}
		}
		if listID == "" && len(project) > 0 {
			listID = project[0].ID
		}
	}
	if listID == "" {
		return assetlist.List{}, assetlist.ErrNotFound
	}
	return lists.Get(ctx, ownerID, projectID, listID)
}

// assetListRead projects one stored list into the Agent's read view. The review
// snapshot is authoritative for the artifacts, the checks and the self-check
// conclusion; an item reported before snapshots existed keeps an empty view
// rather than a guessed one.
func assetListRead(list assetlist.List) assetListReadContent {
	content := assetListReadContent{
		ListID: list.ID,
		Title:  list.Title,
		Status: list.Status,
		Items:  make([]assetListReadItem, 0, len(list.Items)),
	}
	if list.EffectiveStatus != "" {
		content.Status = list.EffectiveStatus
	}
	if list.Run != nil {
		content.RunStatus = list.Run.Status
	}
	for _, item := range list.Items {
		projected := assetListReadItem{
			ID:          item.ID,
			Name:        item.Name,
			Kind:        item.Kind,
			State:       item.State,
			StateReason: item.StateReason,
			ReturnCount: item.ReturnCount,
			Round:       item.Round,
			Artifacts:   []assetListReadArtifact{},
			Checks:      []assetListReadCheck{},
		}
		snapshot := assetlist.ReviewSnapshot{}
		if len(item.Review) > 0 && json.Unmarshal(item.Review, &snapshot) == nil {
			projected.TaskID = snapshot.TaskID
			for _, artifact := range snapshot.Artifacts {
				if artifact.AssetID == "" {
					continue
				}
				projected.Artifacts = append(projected.Artifacts, assetListReadArtifact{
					AssetID:   artifact.AssetID,
					Role:      artifact.Role,
					Variant:   artifact.Variant,
					MediaType: artifact.MediaType,
				})
			}
			if snapshot.Model3D != nil {
				for _, check := range snapshot.Model3D.Checks {
					projected.Checks = append(projected.Checks, assetListReadCheck{
						Check: check.Check, Status: check.Status,
					})
				}
			}
			if snapshot.Image2D != nil {
				for _, check := range snapshot.Image2D.Checks {
					projected.Checks = append(projected.Checks, assetListReadCheck{
						Check: check.Check, Status: check.Status,
					})
				}
			}
			if snapshot.SelfCheck != nil {
				projected.SelfCheck = &assetListReadSelfCheck{
					Status:    snapshot.SelfCheck.Status,
					Summary:   snapshot.SelfCheck.Summary,
					Remaining: snapshot.SelfCheck.Remaining,
				}
			}
		}
		content.Items = append(content.Items, projected)
	}
	return content
}

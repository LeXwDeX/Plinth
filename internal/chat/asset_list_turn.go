package chat

import (
	"context"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/project"
)

var (
	// ErrAssetListTurnUnavailable refuses an item turn while the deployment
	// runs no asset-list workflow.
	ErrAssetListTurnUnavailable = errors.New("chat: asset lists are unavailable")
	// ErrAssetListItemNotFound reports an item the owner does not have in the
	// named list.
	ErrAssetListItemNotFound = errors.New("chat: asset-list item not found")
	// ErrAssetListItemSessionMismatch reports an item whose sub-session is not
	// the session of this turn.
	ErrAssetListItemSessionMismatch = errors.New("chat: asset-list item belongs to another session")
	// ErrAssetListItemNotWorking reports an item that is not running its turn
	// (todo, review, failed or skipped).
	ErrAssetListItemNotWorking = errors.New("chat: asset-list item is not working")
	// ErrAssetListItemProjectMismatch reports a turn whose session is not in
	// the named project.
	ErrAssetListItemProjectMismatch = errors.New("chat: asset-list item session is not in the project")
)

// ServerTurnOptions carries the fields only the service may set on a turn
// (#328). StartTurn (the request entry) never passes it; a server-started
// item turn does. Continuation is the reply text the runner composed, stored
// as the user message; the asset-list marks are frozen with the turn.
type ServerTurnOptions struct {
	Continuation        string
	AssetListItemID     string
	AssetListReferences []project.Reference
}

// StartItemTurnInput is one server-started turn of a production-list item.
// The runner names the item and its session; Continuation is the already
// composed text of this round (first turn or continuation).
type StartItemTurnInput struct {
	OwnerID      string
	SessionID    string
	ProjectID    string
	ListID       string
	ItemID       string
	Continuation string
}

// StartItemTurn starts the turn of one working production-list item (#328).
// It verifies the item against the owner and list, the item's sub-session and
// the session's project before reusing startTurn's freeze path; a running
// turn of the session refuses it with ErrTurnActive. It returns the assistant
// message and the frozen options the caller schedules the turn with.
func (service *Service) StartItemTurn(
	ctx context.Context,
	input StartItemTurnInput,
) (Message, TurnOptions, error) {
	policy := service.assetLists
	if policy == nil || policy.Service == nil {
		return Message{}, TurnOptions{}, ErrAssetListTurnUnavailable
	}
	item, err := policy.Service.ItemByID(ctx, input.OwnerID, input.ListID, input.ItemID)
	if err != nil {
		if errors.Is(err, assetlist.ErrNotFound) {
			return Message{}, TurnOptions{}, ErrAssetListItemNotFound
		}
		return Message{}, TurnOptions{}, err
	}
	if item.SessionID != input.SessionID {
		return Message{}, TurnOptions{}, ErrAssetListItemSessionMismatch
	}
	if item.State != assetlist.StateWorking {
		return Message{}, TurnOptions{}, ErrAssetListItemNotWorking
	}
	// The session must be the item's and still in the named project; the
	// stored session is the authority, never the runner's claim.
	projectID, found, err := service.repository.SessionProject(ctx, input.OwnerID, input.SessionID)
	if err != nil {
		return Message{}, TurnOptions{}, err
	}
	if !found {
		return Message{}, TurnOptions{}, ErrSessionNotFound
	}
	if projectID != input.ProjectID {
		return Message{}, TurnOptions{}, ErrAssetListItemProjectMismatch
	}
	assistant, err := service.startTurn(ctx, StartTurn{
		OwnerID:   input.OwnerID,
		SessionID: input.SessionID,
	}, "", &ServerTurnOptions{
		Continuation:        input.Continuation,
		AssetListItemID:     item.ID,
		AssetListReferences: item.References,
	})
	if err != nil {
		return Message{}, TurnOptions{}, err
	}
	turn, err := service.GetTurn(ctx, input.OwnerID, input.SessionID, assistant.ID)
	if err != nil {
		return Message{}, TurnOptions{}, err
	}
	return assistant, turn.Options, nil
}

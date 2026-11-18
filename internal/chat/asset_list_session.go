package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/assetlist"
)

// ErrAssetListItemNotStartable reports an item that is past its turn work
// (review, accepted, returned, failed or skipped). Only todo and working items
// may get a sub-session (#328).
var ErrAssetListItemNotStartable = errors.New("chat: asset-list item is not todo or working")

// CreateItemSessionInput names one production-list item and the sub-session it
// needs (#328). Title is optional; empty composes "list title · item name".
// ProjectID and ListID must be the item's own.
type CreateItemSessionInput struct {
	OwnerID   string
	ProjectID string
	ListID    string
	ItemID    string
	Title     string
}

// CreateItemSession returns the item's sub-session, creating it the first
// time (#328). The session belongs to the list's project, stores approval mode
// manual (the effective steps mode is derived from the item, never stored),
// copies the list's brief, and copies the proposal session's explicit 3D/2D
// model picks. The item is bound to the session with a version CAS; a lost
// race deletes the session this call created, so an unbound duplicate never
// survives. todo and working items are accepted; any other state is refused.
func (service *Service) CreateItemSession(
	ctx context.Context,
	input CreateItemSessionInput,
) (Session, error) {
	policy := service.assetLists
	if policy == nil || policy.Service == nil {
		return Session{}, ErrAssetListTurnUnavailable
	}
	ownerID := strings.TrimSpace(input.OwnerID)
	projectID := strings.TrimSpace(input.ProjectID)
	listID := strings.TrimSpace(input.ListID)
	itemID := strings.TrimSpace(input.ItemID)
	if ownerID == "" || projectID == "" || listID == "" || itemID == "" {
		return Session{}, ErrAssetListItemNotFound
	}
	item, err := policy.Service.ItemByID(ctx, ownerID, listID, itemID)
	if err != nil {
		if errors.Is(err, assetlist.ErrNotFound) {
			return Session{}, ErrAssetListItemNotFound
		}
		return Session{}, err
	}
	if item.State != assetlist.StateTodo && item.State != assetlist.StateWorking {
		return Session{}, ErrAssetListItemNotStartable
	}
	list, err := policy.Service.Get(ctx, ownerID, projectID, listID)
	if err != nil {
		if errors.Is(err, assetlist.ErrNotFound) {
			return Session{}, ErrAssetListItemNotFound
		}
		return Session{}, err
	}
	// The stored binding wins while its session still exists. A session that
	// was deleted is replaced below, so token-based retries stay idempotent.
	if item.SessionID != "" {
		existing, found, err := service.repository.Session(ctx, ownerID, item.SessionID)
		if err != nil {
			return Session{}, err
		}
		if found {
			if existing.ProjectID != projectID {
				return Session{}, ErrAssetListItemProjectMismatch
			}
			return existing, nil
		}
	}
	model3D, modelImage, err := service.itemSessionModels(ctx, ownerID, list.SourceSessionID)
	if err != nil {
		return Session{}, err
	}
	session, err := service.NewSessionInProject(ctx, ownerID, "",
		itemSessionTitle(input.Title, list.Title, item.Name), projectID)
	if err != nil {
		return Session{}, err
	}
	// The stored column keeps a legal value. Steps are derived from the item
	// binding; writing "steps" here would violate the column's CHECK.
	session.ApprovalMode = approvalpolicy.ModeManual
	if !briefValueEmpty(list.Brief) {
		var brief ProductionBrief
		if err := json.Unmarshal(list.Brief, &brief); err != nil {
			return Session{}, err
		}
		// An empty snapshot keeps the project conventions seeded by
		// NewSessionInProject, like a session created without a brief.
		if !brief.Empty() {
			session.Brief = cloneProductionBrief(brief)
		}
	}
	created, err := service.repository.CreateSession(ctx, session)
	if err != nil {
		return Session{}, err
	}
	if model3D != "" {
		if found, err := service.repository.SetSessionExplicitModel3D(ctx, ownerID, created.ID, model3D); err != nil || !found {
			service.cleanupItemSession(ctx, ownerID, created.ID)
			if err != nil {
				return Session{}, err
			}
			return Session{}, ErrSessionNotFound
		}
		created.ExplicitModel3D = model3D
	}
	if modelImage != "" {
		if found, err := service.repository.SetSessionExplicitModelImage(ctx, ownerID, created.ID, modelImage); err != nil || !found {
			service.cleanupItemSession(ctx, ownerID, created.ID)
			if err != nil {
				return Session{}, err
			}
			return Session{}, ErrSessionNotFound
		}
		created.ExplicitModelImage = modelImage
	}
	if _, err := policy.Service.SetItemSession(ctx, ownerID, listID, itemID,
		created.ID, item.Version); err != nil {
		// A lost CAS means another writer owns the item now; remove the
		// session this call created rather than leave it unbound.
		service.cleanupItemSession(ctx, ownerID, created.ID)
		return Session{}, err
	}
	return created, nil
}

// cleanupItemSession removes a session that could not be bound to its item.
// The delete is best-effort: the caller is already reporting the real error.
func (service *Service) cleanupItemSession(ctx context.Context, ownerID, sessionID string) {
	_ = service.DeleteSession(ctx, ownerID, sessionID)
}

// itemSessionModels copies the proposal session's frozen explicit model picks.
// The newest turn's options carry what the proposal turn actually used; the
// session columns cover a proposal session that never ran a turn. Domain model
// picks have no session-level storage, so only the two explicit picks travel.
// A missing source session copies nothing instead of failing the item.
func (service *Service) itemSessionModels(
	ctx context.Context,
	ownerID, sourceSessionID string,
) (string, string, error) {
	sourceSessionID = strings.TrimSpace(sourceSessionID)
	if sourceSessionID == "" {
		return "", "", nil
	}
	var model3D, modelImage string
	recent, err := service.repository.RecentSessionTurnOptions(ctx, ownerID, sourceSessionID, 1)
	if err != nil {
		return "", "", err
	}
	if len(recent) > 0 {
		model3D = recent[0].ExplicitModel3D
		modelImage = recent[0].ExplicitModelImage
	}
	if model3D == "" {
		model, found, err := service.repository.SessionExplicitModel3D(ctx, ownerID, sourceSessionID)
		if err != nil {
			return "", "", err
		}
		if found {
			model3D = model
		}
	}
	if modelImage == "" {
		model, found, err := service.repository.SessionExplicitModelImage(ctx, ownerID, sourceSessionID)
		if err != nil {
			return "", "", err
		}
		if found {
			modelImage = model
		}
	}
	return model3D, modelImage, nil
}

// itemSessionTitle uses the caller's title, else composes "list · item". The
// new-session path keeps its own default when both names are empty.
func itemSessionTitle(requested, listTitle, itemName string) string {
	if title := strings.TrimSpace(requested); title != "" {
		return title
	}
	parts := make([]string, 0, 2)
	if title := strings.TrimSpace(listTitle); title != "" {
		parts = append(parts, title)
	}
	if name := strings.TrimSpace(itemName); name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, " · ")
}

// LatestTurn reads the session's newest turn in any state (#328). found is
// false for a session with no turns, an unknown session and a foreign owner;
// reads are always scoped to the owner and session.
func (service *Service) LatestTurn(
	ctx context.Context,
	ownerID, sessionID string,
) (Turn, bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	sessionID = strings.TrimSpace(sessionID)
	if ownerID == "" || sessionID == "" {
		return Turn{}, false, nil
	}
	turns, err := service.repository.ListSessionTurns(ctx, ownerID, sessionID, []string{
		service.turnQueued, service.turnRunning, service.turnWaitingTask,
		service.turnDone, service.turnError,
	})
	if err != nil || len(turns) == 0 {
		return Turn{}, false, err
	}
	latest := turns[0]
	for _, turn := range turns[1:] {
		if turn.CreatedAt.After(latest.CreatedAt) ||
			(turn.CreatedAt.Equal(latest.CreatedAt) && turn.AssistantMessageID > latest.AssistantMessageID) {
			latest = turn
		}
	}
	return latest, true, nil
}

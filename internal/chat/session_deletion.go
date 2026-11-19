package chat

import (
	"context"
	"sync"
	"time"
)

// SessionQuiescer is the turn runner's side of deleting a session (#384). The
// Service asks every quiescer to take the session's background work off the
// rows before they are deleted, so that nothing writes to a session that is
// going away and nothing reports an error for it afterwards. The orchestrator
// is the only implementation; it registers itself when it is built.
type SessionQuiescer interface {
	// QuiesceSession interrupts the session's running turn and its tool
	// continuations and returns once they have left, or after a bounded wait
	// (the work then ends by itself as soon as it notices the session is gone).
	// It never cancels a public Task: Tasks belong to the generation kernel and
	// keep running without their session.
	QuiesceSession(ctx context.Context, ownerID, sessionID string)
	// ResumeSession is called when the deletion did not remove the session
	// (a storage failure): the turns QuiesceSession interrupted continue from
	// their persisted state, as after a restart.
	ResumeSession(ctx context.Context, ownerID, sessionID string)
}

// sessionDeletionPreflight is implemented by a repository that can tell, before
// anything is stopped or deleted, that a session cannot be deleted. It answers
// ErrSessionDeletionBlocked for such a session and nil otherwise.
type sessionDeletionPreflight interface {
	PreflightSessionDeletion(ctx context.Context, ownerID, sessionID string) error
}

// sessionDeleteTimeout bounds one deletion once the turn is stopped. The
// deletion no longer follows the request that asked for it: a client that
// disconnects must not leave a half-deleted session behind.
const sessionDeleteTimeout = 2 * time.Minute

// sessionDeletions marks the sessions whose deletion is in progress. The mark
// is in memory on purpose: it only has to cover the live background work of
// this process, and a restart finds either the whole session (deletion not
// finished) or none of it.
type sessionDeletions struct {
	mu     sync.Mutex
	active map[string]int
}

func deletionKey(ownerID, sessionID string) string {
	return ownerID + "\x00" + sessionID
}

// begin marks the session and returns the function that clears the mark; the
// function may be called more than once.
func (deletions *sessionDeletions) begin(ownerID, sessionID string) func() {
	key := deletionKey(ownerID, sessionID)
	deletions.mu.Lock()
	if deletions.active == nil {
		deletions.active = map[string]int{}
	}
	deletions.active[key]++
	deletions.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			deletions.mu.Lock()
			defer deletions.mu.Unlock()
			if deletions.active[key] <= 1 {
				delete(deletions.active, key)
				return
			}
			deletions.active[key]--
		})
	}
}

func (deletions *sessionDeletions) deleting(ownerID, sessionID string) bool {
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	return deletions.active[deletionKey(ownerID, sessionID)] > 0
}

// sessionDeleting reports whether the owner's session is being deleted now.
// Background work reads it to leave quietly instead of writing rows the
// deletion is about to remove; StartTurn reads it to refuse a new turn.
func (service *Service) sessionDeleting(ownerID, sessionID string) bool {
	return service.deletions.deleting(ownerID, sessionID)
}

// addSessionQuiescer registers the turn runner; NewOrchestrator calls it.
func (service *Service) addSessionQuiescer(quiescer SessionQuiescer) {
	service.quiescerMu.Lock()
	defer service.quiescerMu.Unlock()
	service.quiescers = append(service.quiescers, quiescer)
}

func (service *Service) sessionQuiescers() []SessionQuiescer {
	service.quiescerMu.RLock()
	defer service.quiescerMu.RUnlock()
	return append([]SessionQuiescer(nil), service.quiescers...)
}

// DeleteSession deletes the owner's session. The session is marked first, so
// no new turn starts on it and the turn runner's leftovers end quietly; then
// its running turn is interrupted and awaited; only then are the rows deleted,
// in short transactions (see the repository), so the deletion neither fights a
// live turn for its rows nor holds the database against everyone else.
func (service *Service) DeleteSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) error {
	end := service.deletions.begin(ownerID, sessionID)
	defer end()
	owned, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	// A session that is not the owner's (or is gone) has no turn to stop; the
	// repository is still asked, as before, so that it clears what the owner
	// left under that ID and reports not found.
	var quiescers []SessionQuiescer
	if owned {
		// A deletion that is bound to be refused must not stop the running turn.
		if preflight, ok := service.repository.(sessionDeletionPreflight); ok {
			if err := preflight.PreflightSessionDeletion(ctx, ownerID, sessionID); err != nil {
				return err
			}
		}
		quiescers = service.sessionQuiescers()
		for _, quiescer := range quiescers {
			quiescer.QuiesceSession(ctx, ownerID, sessionID)
		}
	}
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionDeleteTimeout)
	defer cancel()
	deleted, err := service.repository.DeleteSession(deleteCtx, ownerID, sessionID)
	if err != nil || !deleted {
		// The session stays: clear the mark first (work enqueued while it is
		// set is skipped), then let the interrupted turns go on.
		end()
		for _, quiescer := range quiescers {
			quiescer.ResumeSession(deleteCtx, ownerID, sessionID)
		}
		if err != nil {
			return err
		}
		return ErrSessionNotFound
	}
	return nil
}

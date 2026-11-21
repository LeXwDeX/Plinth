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

// RunSessionDeletion runs deleteRows, which removes the rows of the owner's
// session, under the protocol every deletion of a session follows, so that the
// session's turn is never left running on rows that are going away (#384).
// A deletion that is bound to be refused (ErrSessionDeletionBlocked) is refused
// before the session is marked or its turn is touched; the preflight runs
// first on purpose, because a turn that ends during the mark would skip its
// finishing writes and stay running on the surviving session (#430). Once the
// preflight passed, the session is marked, so no new turn starts on it and the
// turn runner's leftovers end quietly; then its running turn is interrupted and
// awaited; only then do the rows go, in short transactions where the repository
// can split them, so the deletion neither fights a live turn for its rows nor
// holds the database against everyone else. deleteRows reports whether it
// deleted anything; when it did not, or failed, the session stays, the mark is
// cleared and the interrupted turns go on.
//
// DeleteSession is the chat session's own deletion; the canvas project that
// owns a session deletes the project and the session in one transaction and
// runs that through here too.
func (service *Service) RunSessionDeletion(
	ctx context.Context,
	ownerID string,
	sessionID string,
	deleteRows func(context.Context) (bool, error),
) (bool, error) {
	owned, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return false, err
	}
	// A session that is not the owner's (or is gone) has no turn to stop;
	// deleteRows is still run, as before, so that it clears what the owner left
	// under that ID and reports that nothing was deleted.
	var quiescers []SessionQuiescer
	if owned {
		// The refusal must not suppress the turn's own finishing writes: the
		// deletion mark is set only after it (#430).
		if preflight, ok := service.repository.(sessionDeletionPreflight); ok {
			if err := preflight.PreflightSessionDeletion(ctx, ownerID, sessionID); err != nil {
				return false, err
			}
		}
		quiescers = service.sessionQuiescers()
	}
	end := service.deletions.begin(ownerID, sessionID)
	defer end()
	for _, quiescer := range quiescers {
		quiescer.QuiesceSession(ctx, ownerID, sessionID)
	}
	deleteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionDeleteTimeout)
	defer cancel()
	deleted, err := deleteRows(deleteCtx)
	if err != nil || !deleted {
		// The session stays: clear the mark first (work enqueued while it is
		// set is skipped), then let the interrupted turns go on.
		end()
		for _, quiescer := range quiescers {
			quiescer.ResumeSession(deleteCtx, ownerID, sessionID)
		}
		return false, err
	}
	return true, nil
}

// DeleteSession deletes the owner's session: ErrSessionNotFound when there was
// none, ErrSessionDeletionBlocked when a SkillRun still has work in flight.
func (service *Service) DeleteSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) error {
	deleted, err := service.RunSessionDeletion(ctx, ownerID, sessionID,
		func(ctx context.Context) (bool, error) {
			return service.repository.DeleteSession(ctx, ownerID, sessionID)
		})
	if err != nil {
		return err
	}
	if !deleted {
		return ErrSessionNotFound
	}
	return nil
}

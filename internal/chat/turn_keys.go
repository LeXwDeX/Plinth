package chat

import (
	"context"
	"errors"
	"strings"
	"time"
)

// A person's send is keyed by the browser's Idempotency-Key (#665): when the
// request that starts a turn reached the server but its answer was lost, the
// browser sends the same request again with the same key. The key's record,
// written in the same transaction as the turn, makes that repeat answer the
// turn it started instead of starting a second, paid one.

// TurnKey is the record of one keyed send: the key the owner sent to the
// session, the digest of the request it came with, the turn it started and
// until when it names that turn. An expired record names nothing; the next
// turn start with any key removes it.
type TurnKey struct {
	OwnerID            string
	SessionID          string
	Key                string
	Fingerprint        string
	UserMessageID      string
	AssistantMessageID string
	CreatedAt          time.Time
	ExpiresAt          time.Time
}

// TurnKeyTakenError is what Repository.StartTurn returns when the turn's key
// already names a live record of the owner's session. Nothing was written;
// Existing is that record.
type TurnKeyTakenError struct {
	Existing TurnKey
}

func (err *TurnKeyTakenError) Error() string {
	return "chat: turn idempotency key already started a turn"
}

// ErrIdempotencyConflict refuses a send whose key already started a turn of
// the session from a different request. Nothing is started.
var ErrIdempotencyConflict = errors.New("chat: idempotency key was used with a different request")

// turnKeyRecord is the record the command's key would write for the turn of
// user and assistant, or nil when the command carries no key or the service
// keeps none.
func (service *Service) turnKeyRecord(command StartTurn, user, assistant Message, now time.Time) *TurnKey {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" || service.turnKeyRetention <= 0 {
		return nil
	}
	return &TurnKey{
		OwnerID:            command.OwnerID,
		SessionID:          command.SessionID,
		Key:                key,
		Fingerprint:        command.RequestFingerprint,
		UserMessageID:      user.ID,
		AssistantMessageID: assistant.ID,
		CreatedAt:          now,
		ExpiresAt:          now.Add(service.turnKeyRetention),
	}
}

// replayTurnKey answers a send whose key already started a turn: found is
// true when the key names a live record of the session; the returned message
// is that turn's assistant message (ID and session only), or the error is
// ErrIdempotencyConflict when the record came from a different request.
func (service *Service) replayTurnKey(ctx context.Context, command StartTurn) (Message, bool, error) {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" || service.turnKeyRetention <= 0 {
		return Message{}, false, nil
	}
	record, found, err := service.repository.TurnKey(ctx, command.OwnerID, command.SessionID, key, service.clock())
	if err != nil || !found {
		return Message{}, false, err
	}
	return replayedTurn(record, command)
}

func replayedTurn(record TurnKey, command StartTurn) (Message, bool, error) {
	if record.Fingerprint != command.RequestFingerprint {
		return Message{}, true, ErrIdempotencyConflict
	}
	return Message{ID: record.AssistantMessageID, SessionID: record.SessionID}, true, nil
}

// ReleaseTurnKey forgets the key of a turn that was started but never ran
// (its scheduling was refused and the turn failed at once), so the browser's
// repeat starts a fresh turn instead of answering the failed one. Only the
// record that names assistantMessageID is removed.
func (service *Service) ReleaseTurnKey(ctx context.Context, ownerID, sessionID, key, assistantMessageID string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return service.repository.ReleaseTurnKey(ctx, ownerID, sessionID, key, assistantMessageID)
}

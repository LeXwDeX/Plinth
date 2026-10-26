package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrSessionNotesTooLong reports a settings summary above the Schema bound.
// The bound is never silently applied by cutting the text: the writer (the
// Agent or the user) has to shorten it.
var ErrSessionNotesTooLong = errors.New("chat: session notes too long")

// SessionNotesTooLongError carries the length and the bound for the message
// that tells the writer what to fix.
type SessionNotesTooLongError struct {
	Length  int
	Maximum int
}

func (err *SessionNotesTooLongError) Error() string {
	return fmt.Sprintf("chat: session notes are %d characters, the maximum is %d", err.Length, err.Maximum)
}

func (err *SessionNotesTooLongError) Is(target error) bool {
	return target == ErrSessionNotesTooLong
}

// MaximumSessionNotes is the Schema bound on a session's settings summary.
func (service *Service) MaximumSessionNotes() int {
	return service.maximumNotes
}

// SessionNotes reads the settings summary of the owner's own session. A
// missing or foreign session is the same ErrSessionNotFound.
func (service *Service) SessionNotes(ctx context.Context, ownerID, sessionID string) (string, error) {
	notes, found, err := service.repository.SessionNotes(ctx, ownerID, sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return notes, nil
}

// SetSessionNotes replaces the settings summary of the owner's own session and
// returns the stored text. It is the one write path for the Agent tool and for
// the user's edit, so both obey the same bound.
func (service *Service) SetSessionNotes(ctx context.Context, ownerID, sessionID, notes string) (string, error) {
	notes = NormalizeSessionNotes(notes)
	if length := utf8.RuneCountInString(notes); service.maximumNotes <= 0 || length > service.maximumNotes {
		return "", &SessionNotesTooLongError{Length: length, Maximum: service.maximumNotes}
	}
	found, err := service.repository.SetSessionNotes(ctx, ownerID, sessionID, notes)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return notes, nil
}

// NormalizeSessionNotes makes the stored text independent of the client:
// line endings are unified, control characters other than newline and tab are
// dropped and the ends are trimmed.
func NormalizeSessionNotes(notes string) string {
	notes = strings.ReplaceAll(notes, "\r\n", "\n")
	notes = strings.ReplaceAll(notes, "\r", "\n")
	notes = strings.Map(func(value rune) rune {
		if value == '\n' || value == '\t' {
			return value
		}
		if value < 0x20 || value == 0x7f {
			return -1
		}
		return value
	}, notes)
	return strings.TrimSpace(notes)
}

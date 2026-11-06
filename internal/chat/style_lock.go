package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var (
	ErrStyleLockTextTooLong      = errors.New("chat: style lock text too long")
	ErrStyleLockReferenceLimit   = errors.New("chat: too many style lock references")
	ErrStyleLockReferenceInvalid = errors.New("chat: invalid style lock reference")
)

type StyleLock struct {
	Enabled    bool
	Text       string
	References []string
}

type StyleLockTextTooLongError struct {
	Length  int
	Maximum int
}

func (err *StyleLockTextTooLongError) Error() string {
	return fmt.Sprintf("chat: style lock text is %d characters, the maximum is %d", err.Length, err.Maximum)
}

func (err *StyleLockTextTooLongError) Is(target error) bool {
	return target == ErrStyleLockTextTooLong
}

type StyleLockReferenceLimitError struct {
	Count   int
	Maximum int
}

func (err *StyleLockReferenceLimitError) Error() string {
	return fmt.Sprintf("chat: style lock has %d references, the maximum is %d", err.Count, err.Maximum)
}

func (err *StyleLockReferenceLimitError) Is(target error) bool {
	return target == ErrStyleLockReferenceLimit
}

// StyleLock reads the owner's style lock. A missing or foreign session is
// ErrSessionNotFound.
func (service *Service) StyleLock(ctx context.Context, ownerID, sessionID string) (StyleLock, error) {
	lock, found, err := service.repository.StyleLock(ctx, ownerID, sessionID)
	if err != nil {
		return StyleLock{}, err
	}
	if !found {
		return StyleLock{}, ErrSessionNotFound
	}
	lock.References = append([]string(nil), lock.References...)
	return lock, nil
}

// SetStyleLock validates and replaces the owner's whole style lock. Reference
// IDs are checked through the same owner/session attachment verifier used by
// chat input claims; failed checks never reveal which ID was rejected.
func (service *Service) SetStyleLock(
	ctx context.Context,
	ownerID, sessionID string,
	enabled bool,
	text string,
	references []string,
) (StyleLock, error) {
	text = strings.TrimSpace(text)
	length := utf8.RuneCountInString(text)
	if maximum := service.maximumStyleLockText; maximum <= 0 || length > maximum {
		return StyleLock{}, &StyleLockTextTooLongError{Length: length, Maximum: maximum}
	}
	if maximum := service.maximumStyleLockReferences; maximum <= 0 || len(references) > maximum {
		return StyleLock{}, &StyleLockReferenceLimitError{Count: len(references), Maximum: maximum}
	}
	if len(references) > 0 && service.attachments == nil {
		return StyleLock{}, ErrStyleLockReferenceInvalid
	}
	for _, reference := range references {
		if strings.TrimSpace(reference) != reference || reference == "" {
			return StyleLock{}, ErrStyleLockReferenceInvalid
		}
		if _, err := service.attachments.VerifyAttachment(ctx, ownerID, sessionID, AttachmentRef{
			Kind: AttachmentKindImage, AssetID: reference,
		}); err != nil {
			return StyleLock{}, ErrStyleLockReferenceInvalid
		}
	}
	stored := StyleLock{Enabled: enabled, Text: text, References: append([]string(nil), references...)}
	found, err := service.repository.SetStyleLock(ctx, ownerID, sessionID, stored)
	if err != nil {
		return StyleLock{}, err
	}
	if !found {
		return StyleLock{}, ErrSessionNotFound
	}
	return stored, nil
}

func EncodeStyleLockReferences(references []string) (string, error) {
	if references == nil {
		references = []string{}
	}
	encoded, err := json.Marshal(references)
	if err != nil {
		return "", fmt.Errorf("encode style lock references: %w", err)
	}
	return string(encoded), nil
}

func DecodeStyleLockReferences(raw string) []string {
	var references []string
	if json.Unmarshal([]byte(raw), &references) != nil || references == nil {
		return []string{}
	}
	return references
}

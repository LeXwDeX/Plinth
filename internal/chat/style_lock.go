package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var (
	ErrStyleLockTextTooLong      = errors.New("chat: style lock text too long")
	ErrStyleLockReferenceLimit   = errors.New("chat: too many style lock references")
	ErrStyleLockReferenceInvalid = errors.New("chat: invalid style lock reference")
	// ErrStyleLockReferenceUnavailable reports that a direct prepare input
	// cannot carry the lock's reference pictures: it has no reference
	// parameter, or its single-picture parameter already holds the turn's own
	// picture. The HTTP boundary turns it into the Schema refusal text.
	ErrStyleLockReferenceUnavailable = errors.New("chat: style lock reference cannot be applied")
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

// StyleLockAppliesToDirect reports whether the Schema declares this capability
// for direct prepare injection. Background removal and 3D are deliberately
// absent: their reference parameters are source pictures, not style hints, and
// 3D has the production brief's style field.
func StyleLockAppliesToDirect(style catalog.ChatStyleLockSchema, capability generation.Capability) bool {
	for _, declared := range style.DirectTaskCapabilities {
		if declared == capability {
			return true
		}
	}
	return false
}

// ApplyStyleLockToDirectInput merges a session's style lock into the input of
// a direct prepare request. The HTTP prepare boundary calls it exactly once,
// before the input is normalized and frozen; retries and recovery reuse the
// frozen NormalizedInput and must never call it again.
//
// The lock text is appended with the same prompt and edit-scope templates the
// generation tool uses. Lock references follow the turn's own pictures, in the
// lock's order; a reference already present is not repeated. An input that
// cannot carry the references is refused rather than sent without them.
func ApplyStyleLockToDirectInput(
	lock StyleLock,
	skipped bool,
	capability generation.Capability,
	offering generation.Offering,
	style catalog.ChatStyleLockSchema,
	input json.RawMessage,
) (json.RawMessage, error) {
	text := strings.TrimSpace(lock.Text)
	if skipped || !lock.Enabled || (text == "" && len(lock.References) == 0) ||
		!StyleLockAppliesToDirect(style, capability) || !StyleLockAppliesToDirect(style, offering.Capability) {
		return input, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(input, &values); err != nil || values == nil {
		// The shape is not an input object; normalization reports it as before.
		return input, nil
	}
	if text != "" {
		parameter, exists := directStyleLockParameter(offering, generation.SemanticPrompt)
		if exists {
			prompt := ""
			if raw, present := values[parameter.Name]; present {
				if json.Unmarshal(raw, &prompt) != nil {
					// Normalization reports a prompt that is not text.
					return input, nil
				}
			}
			template := style.PromptTemplate
			if template == "" {
				template = "\n\nStyle lock: {style}"
			}
			prompt += strings.NewReplacer("{style}", text, "{text}", text).Replace(template)
			if offering.Capability == generation.ImageToImage && style.EditScopeTemplate != "" {
				prompt += style.EditScopeTemplate
			}
			if parameter.MaxLength > 0 {
				if runes := []rune(prompt); len(runes) > parameter.MaxLength {
					prompt = string(runes[:parameter.MaxLength])
				}
			}
			encoded, err := json.Marshal(prompt)
			if err != nil {
				return input, err
			}
			values[parameter.Name] = encoded
		}
	}
	if len(lock.References) > 0 {
		if err := mergeDirectStyleLockReferences(offering, values, lock.References); err != nil {
			return input, err
		}
	}
	merged, err := json.Marshal(values)
	if err != nil {
		return input, err
	}
	return merged, nil
}

func directStyleLockParameter(offering generation.Offering, semantic generation.ParameterSemantic) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == semantic {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

func uniqueStyleLockReferences(references []string) []string {
	unique := make([]string, 0, len(references))
	seen := make(map[string]bool, len(references))
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if reference == "" || seen[reference] {
			continue
		}
		seen[reference] = true
		unique = append(unique, reference)
	}
	return unique
}

func mergeDirectStyleLockReferences(
	offering generation.Offering,
	values map[string]json.RawMessage,
	references []string,
) error {
	parameter, exists := directStyleLockParameter(offering, generation.SemanticReferenceImage)
	if !exists {
		return ErrStyleLockReferenceUnavailable
	}
	references = uniqueStyleLockReferences(references)
	switch parameter.Type {
	case "asset-array":
		var pictures []string
		if raw, present := values[parameter.Name]; present && len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &pictures) != nil {
				// Normalization reports a reference list that is not text asset IDs.
				return nil
			}
		}
		seen := make(map[string]bool, len(pictures)+len(references))
		for _, picture := range pictures {
			seen[picture] = true
		}
		for _, reference := range references {
			if seen[reference] {
				continue
			}
			seen[reference] = true
			pictures = append(pictures, reference)
		}
		encoded, err := json.Marshal(pictures)
		if err != nil {
			return err
		}
		values[parameter.Name] = encoded
		return nil
	case "asset":
		current := ""
		if raw, present := values[parameter.Name]; present && len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &current) != nil {
				return nil
			}
		}
		// A single-picture parameter already used by the turn's own picture
		// cannot also be the style reference. With no picture and exactly one
		// style reference there is no ambiguity.
		if strings.TrimSpace(current) != "" || len(references) != 1 {
			return ErrStyleLockReferenceUnavailable
		}
		encoded, err := json.Marshal(references[0])
		if err != nil {
			return err
		}
		values[parameter.Name] = encoded
		return nil
	default:
		return ErrStyleLockReferenceUnavailable
	}
}

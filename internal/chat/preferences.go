package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Session preferences (#241) are the server's record of what the person chose
// and refused in a conversation: the picked candidate, the candidate they said
// "not this one" about with a reason, and the weak signals the workspace sends
// when the person regenerates, repaints or rewrites a prompt. The records are
// session-scoped facts, not a model of the person: nothing here is learned
// across sessions or users.
const (
	// PreferenceKindSelected marks the direction the person chose.
	PreferenceKindSelected = "selected"
	// PreferenceKindRejected marks a direction the person refused.
	PreferenceKindRejected = "rejected"
	// PreferenceKindSignal marks a weak, unconfirmed signal: the person asked
	// to redo something but did not say what was wrong.
	PreferenceKindSignal = "signal"
)

const (
	// PreferenceSourcePick is a selection made by picking a candidate.
	PreferenceSourcePick = "pick"
	// PreferenceSourceReject is an explicit rejection with a reason.
	PreferenceSourceReject = "reject"
	// PreferenceSourceRegenerate is the client's weak signal for "generate
	// again this step".
	PreferenceSourceRegenerate = "regenerate"
	// PreferenceSourceInpaint is the weak signal of a canvas repaint.
	PreferenceSourceInpaint = "inpaint"
	// PreferenceSourcePromptEdit is the weak signal of editing the prompt and
	// sending it again.
	PreferenceSourcePromptEdit = "prompt_edit"
)

var (
	// ErrPreferenceRejected reports a preference write the server refuses: a
	// claim that is not a generated 2D candidate of the owner's session, an
	// undeclared reason preset, or a weak signal source the Schema does not
	// declare.
	ErrPreferenceRejected = errors.New("chat: preference rejected")
	// ErrPreferenceNotFound reports a preference ID that is not an active
	// record of the owner's session. A missing and a foreign preference are the
	// same answer, so the route never tells whether another owner's row exists.
	ErrPreferenceNotFound = errors.New("chat: preference not found")
	// ErrPreferenceTooLong reports text above the Schema bound. It is never cut
	// silently: the writer shortens it.
	ErrPreferenceTooLong = errors.New("chat: preference text too long")
)

// PreferenceTooLongError carries the offending field and its bound.
type PreferenceTooLongError struct {
	Field   string
	Length  int
	Maximum int
}

func (err *PreferenceTooLongError) Error() string {
	return fmt.Sprintf("chat: preference %s is %d characters, the maximum is %d", err.Field, err.Length, err.Maximum)
}

func (err *PreferenceTooLongError) Is(target error) bool {
	return target == ErrPreferenceTooLong
}

// Preference is one stored choice fact of a session. Direction is the prompt
// the Agent wrote for the direction (for a pick, the prompt of the tool call
// that produced the candidate); it is empty when the history no longer carries
// the call, in which case Handle names the object.
type Preference struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Source       string    `json:"source"`
	Direction    string    `json:"direction,omitempty"`
	ReasonPreset string    `json:"reason_preset,omitempty"`
	ReasonText   string    `json:"reason_text,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	AssetID      string    `json:"asset_id,omitempty"`
	Handle       string    `json:"handle,omitempty"`
	Alternatives []string  `json:"alternatives,omitempty"`
	Explicit     bool      `json:"explicit"`
	Active       bool      `json:"active"`
	UserEdited   bool      `json:"user_edited,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PreferenceEdit is the user's edit of one record. A nil field is left as it
// is; active=false hides the record from later prompts and lists and is the
// same effect as a delete.
type PreferenceEdit struct {
	Direction    *string
	ReasonPreset *string
	ReasonText   *string
	Active       *bool
}

// EncodePreferenceAlternatives stores the unselected candidate handles of the
// same result card. They are handles the session already gave, never raw IDs.
func EncodePreferenceAlternatives(handles []string) (string, error) {
	if len(handles) == 0 {
		return "[]", nil
	}
	encoded, err := json.Marshal(handles)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// DecodePreferenceAlternatives reads the stored handles; a corrupt value reads
// as no alternatives rather than failing the whole list.
func DecodePreferenceAlternatives(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var handles []string
	if err := json.Unmarshal([]byte(raw), &handles); err != nil {
		return nil
	}
	return handles
}

// ReasonLabel resolves a declared preset id to its label.
func (service *Service) ReasonLabel(id string) (string, bool) {
	for _, preset := range service.preferenceSchema.ReasonPresets {
		if preset.ID == id {
			return preset.Label, true
		}
	}
	return "", false
}

// PreferencesEnabled reports whether the Schema declares preference storage.
func (service *Service) PreferencesEnabled() bool {
	return service.preferenceSchema.MaximumEntries > 0
}

// ListPreferences returns the active preference records of the owner's own
// session, newest first. A session without preferences returns an empty list;
// a missing or foreign session is ErrSessionNotFound.
func (service *Service) ListPreferences(ctx context.Context, ownerID, sessionID string) ([]Preference, error) {
	preferences, found, err := service.repository.ListPreferences(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	if !service.PreferencesEnabled() {
		return nil, nil
	}
	return preferences, nil
}

// RecordRejection records the person's explicit "not this one" about a
// generated 2D candidate. The candidate is verified exactly like a pick. An
// unknown reason preset or text above the bound refuses the write; a rejection
// of the current pick clears the pick, because the refused picture cannot stay
// the session's current object.
func (service *Service) RecordRejection(
	ctx context.Context,
	ownerID, sessionID string,
	ref AttachmentRef,
	reasonPreset, reasonText string,
) (Preference, error) {
	if !service.PreferencesEnabled() {
		return Preference{}, ErrPreferenceRejected
	}
	verified, err := service.verifyCandidate(ctx, ownerID, sessionID, ref)
	if err != nil {
		return Preference{}, preferenceError(err)
	}
	verified = service.withKnownHandle(ctx, ownerID, sessionID, verified)
	reasonPreset = strings.TrimSpace(reasonPreset)
	if reasonPreset != "" {
		if _, declared := service.ReasonLabel(reasonPreset); !declared {
			return Preference{}, ErrPreferenceRejected
		}
	}
	reasonText = NormalizeSessionNotes(reasonText)
	if err := service.checkPreferenceText("reason", reasonText, service.preferenceSchema.MaximumReasonCharacters); err != nil {
		return Preference{}, err
	}
	history, _ := service.repository.ListMessages(ctx, ownerID, sessionID)
	direction, alternatives := preferenceDirectionAndAlternatives(history, verified, service.placeholderHealing.PromptArgument)
	preference := Preference{
		ID:           service.newID("preference"),
		Kind:         PreferenceKindRejected,
		Source:       PreferenceSourceReject,
		Direction:    service.boundPreferenceDirection(direction),
		ReasonPreset: reasonPreset,
		ReasonText:   reasonText,
		TaskID:       verified.TaskID,
		AssetID:      verified.AssetID,
		Handle:       verified.Handle,
		Alternatives: alternatives,
		Explicit:     true,
		Active:       true,
	}
	stored, err := service.storePreference(ctx, ownerID, sessionID, preference)
	if err != nil {
		return Preference{}, err
	}
	// A rejected current pick must not stay the selection of the next turn.
	if pick, found, pickErr := service.repository.SessionPick(ctx, ownerID, sessionID); pickErr == nil && found &&
		SameObject(pick, verified) {
		if _, clearErr := service.repository.SetSessionPick(ctx, ownerID, sessionID, Attachment{}); clearErr != nil {
			return stored, clearErr
		}
	}
	return stored, nil
}

// RecordSignal stores the client's weak, unconfirmed signal about objects the
// person asked to redo. Every claimed object is verified for the owner and the
// session; a claim that does not verify is skipped, because a weak signal must
// never make the server trust an unowned ID. The returned records are the ones
// stored; an undeclared source or no verified object refuses the write.
func (service *Service) RecordSignal(
	ctx context.Context,
	ownerID, sessionID, signal string,
	refs []AttachmentRef,
) ([]Preference, error) {
	if !service.PreferencesEnabled() {
		return nil, ErrPreferenceRejected
	}
	switch signal {
	case PreferenceSourceRegenerate, PreferenceSourceInpaint, PreferenceSourcePromptEdit:
	default:
		return nil, ErrPreferenceRejected
	}
	owns, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if !owns {
		return nil, ErrSessionNotFound
	}
	history, _ := service.repository.ListMessages(ctx, ownerID, sessionID)
	// An object the person explicitly rejected stays rejected: a later weak
	// signal must not add a contradictory "unconfirmed redo" line beside it.
	existing, _, _ := service.repository.ListPreferences(ctx, ownerID, sessionID)
	explicitObjects := map[string]bool{}
	for _, preference := range existing {
		if preference.Explicit && preference.TaskID != "" && preference.AssetID != "" {
			explicitObjects[preference.TaskID+"\x00"+preference.AssetID] = true
		}
	}
	seen := map[string]bool{}
	verifiedAny := false
	stored := make([]Preference, 0, len(refs))
	for _, ref := range refs {
		verified, verifyErr := service.verifyCandidate(ctx, ownerID, sessionID, ref)
		if verifyErr != nil {
			continue
		}
		verified = service.withKnownHandle(ctx, ownerID, sessionID, verified)
		key := verified.TaskID + "\x00" + verified.AssetID
		if verified.TaskID == "" && verified.AssetID == "" {
			continue
		}
		verifiedAny = true
		if seen[key] || explicitObjects[key] {
			continue
		}
		seen[key] = true
		direction, _ := preferenceDirectionAndAlternatives(history, verified, service.placeholderHealing.PromptArgument)
		preference := Preference{
			ID:        service.newID("preference"),
			Kind:      PreferenceKindSignal,
			Source:    signal,
			Direction: service.boundPreferenceDirection(direction),
			TaskID:    verified.TaskID,
			AssetID:   verified.AssetID,
			Handle:    verified.Handle,
			Explicit:  false,
			Active:    true,
		}
		saved, saveErr := service.storePreference(ctx, ownerID, sessionID, preference)
		if saveErr != nil {
			return stored, saveErr
		}
		stored = append(stored, saved)
	}
	if len(stored) == 0 && !verifiedAny {
		return nil, ErrPreferenceRejected
	}
	return stored, nil
}

// UpdatePreference edits one active record of the owner's own session. The
// edit marks the record user_edited, so the automatic cap never evicts it.
func (service *Service) UpdatePreference(
	ctx context.Context,
	ownerID, sessionID, preferenceID string,
	edit PreferenceEdit,
) (Preference, error) {
	if !service.PreferencesEnabled() {
		return Preference{}, ErrPreferenceNotFound
	}
	if edit.Direction != nil {
		direction := NormalizeSessionNotes(*edit.Direction)
		if err := service.checkPreferenceText("direction", direction, service.preferenceSchema.MaximumDirectionCharacters); err != nil {
			return Preference{}, err
		}
		edit.Direction = &direction
	}
	if edit.ReasonPreset != nil {
		preset := strings.TrimSpace(*edit.ReasonPreset)
		if preset != "" {
			if _, declared := service.ReasonLabel(preset); !declared {
				return Preference{}, ErrPreferenceRejected
			}
		}
		edit.ReasonPreset = &preset
	}
	if edit.ReasonText != nil {
		text := NormalizeSessionNotes(*edit.ReasonText)
		if err := service.checkPreferenceText("reason", text, service.preferenceSchema.MaximumReasonCharacters); err != nil {
			return Preference{}, err
		}
		edit.ReasonText = &text
	}
	stored, found, err := service.repository.UpdatePreference(ctx, ownerID, sessionID, preferenceID, edit)
	if err != nil {
		return Preference{}, err
	}
	if !found {
		// A missing session and a foreign preference are the same 404.
		if owns, ownsErr := service.repository.OwnsSession(ctx, ownerID, sessionID); ownsErr == nil && !owns {
			return Preference{}, ErrSessionNotFound
		}
		return Preference{}, ErrPreferenceNotFound
	}
	return stored, nil
}

// DeletePreference hides one record: it is no longer listed or injected, but
// the history fact it records stays in the database.
func (service *Service) DeletePreference(ctx context.Context, ownerID, sessionID, preferenceID string) error {
	owns, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	if !owns {
		return ErrSessionNotFound
	}
	found, err := service.repository.DeletePreference(ctx, ownerID, sessionID, preferenceID)
	if err != nil {
		return err
	}
	if !found {
		return ErrPreferenceNotFound
	}
	return nil
}

// recordSelection records the pick as a preference. It is best effort: a
// preference write must never make the pick itself fail, exactly like the
// handle lookup of the pick.
func (service *Service) recordSelection(ctx context.Context, ownerID, sessionID string, picked Attachment) {
	if !service.PreferencesEnabled() {
		return
	}
	history, err := service.repository.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return
	}
	direction, alternatives := preferenceDirectionAndAlternatives(history, picked, service.placeholderHealing.PromptArgument)
	preference := Preference{
		ID:           service.newID("preference"),
		Kind:         PreferenceKindSelected,
		Source:       PreferenceSourcePick,
		Direction:    service.boundPreferenceDirection(direction),
		TaskID:       picked.TaskID,
		AssetID:      picked.AssetID,
		Handle:       picked.Handle,
		Alternatives: alternatives,
		Explicit:     true,
		Active:       true,
	}
	_, _ = service.storePreference(ctx, ownerID, sessionID, preference)
}

// storePreference replaces the object's previous active record (latest action
// wins), keeps a user-edited record's edits, enforces the entry cap and
// returns the stored record.
func (service *Service) storePreference(ctx context.Context, ownerID, sessionID string, preference Preference) (Preference, error) {
	now := service.clock()
	preference.CreatedAt = now
	preference.UpdatedAt = now
	existing, found, err := service.repository.ListPreferences(ctx, ownerID, sessionID)
	if err != nil {
		return Preference{}, err
	}
	if !found {
		return Preference{}, ErrSessionNotFound
	}
	for _, current := range existing {
		if !preferenceReplaces(current, preference) {
			continue
		}
		preference.ID = current.ID
		if current.UserEdited {
			preference.Direction = current.Direction
			preference.ReasonPreset = current.ReasonPreset
			preference.ReasonText = current.ReasonText
			preference.UserEdited = true
		}
		preference.CreatedAt = current.CreatedAt
		break
	}
	stored, found, err := service.repository.PutPreference(ctx, ownerID, sessionID, preference)
	if err != nil {
		return Preference{}, err
	}
	if !found {
		return Preference{}, ErrSessionNotFound
	}
	if err := service.prunePreferences(ctx, ownerID, sessionID); err != nil {
		return stored, err
	}
	return stored, nil
}

// preferenceReplaces reports whether the new record supersedes the stored one.
// The same object's latest explicit action replaces the old kind; weak signals
// only replace a weak signal of the same source, so one regeneration does not
// erase an explicit rejection of the same object.
func preferenceReplaces(current, next Preference) bool {
	if current.ID == "" || next.TaskID == "" || next.AssetID == "" {
		return false
	}
	if current.TaskID != next.TaskID || current.AssetID != next.AssetID {
		return false
	}
	if next.Explicit {
		return true
	}
	return !current.Explicit && current.Source == next.Source && current.Kind == PreferenceKindSignal
}

// prunePreferences applies the Schema cap: the oldest weak signals go first,
// then the oldest explicit records; a record the user edited is never evicted.
func (service *Service) prunePreferences(ctx context.Context, ownerID, sessionID string) error {
	maximum := service.preferenceSchema.MaximumEntries
	if maximum <= 0 {
		return nil
	}
	preferences, found, err := service.repository.ListPreferences(ctx, ownerID, sessionID)
	if err != nil || !found {
		return err
	}
	if len(preferences) <= maximum {
		return nil
	}
	// Newest first: evict from the end (oldest). Within the weak group, oldest
	// first; explicit records are only evicted when no weak record is left.
	evictable := make([]Preference, 0, len(preferences))
	for index := len(preferences) - 1; index >= 0; index-- {
		if preferences[index].UserEdited {
			continue
		}
		evictable = append(evictable, preferences[index])
	}
	sort.SliceStable(evictable, func(left, right int) bool {
		leftWeak := !evictable[left].Explicit
		rightWeak := !evictable[right].Explicit
		if leftWeak != rightWeak {
			return leftWeak
		}
		return evictable[left].CreatedAt.Before(evictable[right].CreatedAt)
	})
	remove := len(preferences) - maximum
	for _, victim := range evictable {
		if remove <= 0 {
			break
		}
		if _, err := service.repository.DeletePreference(ctx, ownerID, sessionID, victim.ID); err != nil {
			return err
		}
		remove--
	}
	return nil
}

func (service *Service) checkPreferenceText(field, value string, maximum int) error {
	length := utf8.RuneCountInString(value)
	if maximum <= 0 || length > maximum {
		return &PreferenceTooLongError{Field: field, Length: length, Maximum: maximum}
	}
	return nil
}

// boundPreferenceDirection bounds an Agent-written direction taken from the
// history. Selection recording is best effort and must not fail on a long
// prompt, so the value is cut to the Schema bound here (unlike user writes,
// which are refused).
func (service *Service) boundPreferenceDirection(direction string) string {
	direction = NormalizeSessionNotes(direction)
	maximum := service.preferenceSchema.MaximumDirectionCharacters
	if maximum > 0 && utf8.RuneCountInString(direction) > maximum {
		return string([]rune(direction)[:maximum])
	}
	if maximum <= 0 {
		return ""
	}
	return direction
}

// preferenceDirectionAndAlternatives reads the prompt of the tool call that
// produced the candidate and the handles of the other generated pictures of
// the same result card. Both are facts already stored in the session history;
// an absent call or a message without siblings yields empty values.
func preferenceDirectionAndAlternatives(history []Message, candidate Attachment, promptArgument string) (string, []string) {
	for index := len(history) - 1; index >= 0; index-- {
		message := history[index]
		matched := false
		for _, attachment := range message.Attachments {
			if SameObject(attachment, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		direction := ""
		for _, call := range message.ToolCalls {
			if call.TaskID != "" && candidate.TaskID != "" && call.TaskID != candidate.TaskID {
				continue
			}
			var arguments map[string]json.RawMessage
			if json.Unmarshal(call.Arguments, &arguments) != nil {
				continue
			}
			var prompt string
			if raw, present := arguments[promptArgument]; present {
				_ = json.Unmarshal(raw, &prompt)
			}
			if strings.TrimSpace(prompt) == "" {
				continue
			}
			direction = prompt
			break
		}
		var alternatives []string
		for _, attachment := range message.Attachments {
			// The card is the message: every other generated picture of the
			// same reply is an unselected candidate, whether it came from the
			// same Task or from a sibling call of the same result card.
			if attachment.Handle == "" || attachment.Kind != AttachmentKindImage ||
				attachment.Role != AttachmentRoleGenerated || SameObject(attachment, candidate) {
				continue
			}
			alternatives = append(alternatives, attachment.Handle)
		}
		return direction, alternatives
	}
	return "", nil
}

// verifyCandidate proves a claimed generated 2D candidate of the owner's
// session: a generated picture with a Task and an asset whose Task is not a 3D
// model. ErrSessionNotFound and ErrCandidateRejected are distinct so callers
// can keep the 404 for a missing or foreign session.
func (service *Service) verifyCandidate(ctx context.Context, ownerID, sessionID string, ref AttachmentRef) (Attachment, error) {
	owns, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return Attachment{}, err
	}
	if !owns {
		return Attachment{}, ErrSessionNotFound
	}
	if service.attachments == nil {
		return Attachment{}, ErrCandidateRejected
	}
	ref.Kind, ref.ImageSlot = AttachmentKindImage, 0
	verified, err := service.attachments.VerifyAttachment(ctx, ownerID, sessionID, ref)
	if err != nil || verified.Role != AttachmentRoleGenerated || verified.TaskID == "" || verified.AssetID == "" ||
		service.isModelPreview(ctx, ownerID, sessionID, verified) {
		return Attachment{}, ErrCandidateRejected
	}
	return verified, nil
}

// ErrCandidateRejected is the internal refusal of a claim that is not a
// generated 2D candidate; pick and preference callers map it to their own
// public error.
var ErrCandidateRejected = errors.New("chat: candidate rejected")

func preferenceError(err error) error {
	if errors.Is(err, ErrCandidateRejected) {
		return ErrPreferenceRejected
	}
	return err
}

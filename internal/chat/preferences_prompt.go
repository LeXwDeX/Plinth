package chat

import (
	"context"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// preferencesPrompt renders the session's stored choices for the system
// prompt (#241). The records are read from storage on every request, so a
// preference the person edits or deletes applies to the next turn and context
// compaction never loses the block: it is not built from the message window.
//
// Directions come from the Agent's own tool prompts and reasons are free text
// the person wrote. Both are therefore framed like the settings summary: the
// Schema wraps them between two markers that say the block is background, not
// instructions, and every occurrence of either marker (or of the settings
// summary's markers) is removed from the text first, so a record cannot end
// the frame early and continue as prompt text.
func (orchestrator *Orchestrator) preferencesPrompt(ctx context.Context, ownerID, sessionID string) string {
	preferencesSchema := orchestrator.schema.Preferences
	if preferencesSchema.ContextTemplate == "" || sessionID == "" || orchestrator.service == nil ||
		!orchestrator.service.PreferencesEnabled() {
		return ""
	}
	preferences, err := orchestrator.service.ListPreferences(ctx, ownerID, sessionID)
	if err != nil || len(preferences) == 0 {
		return ""
	}
	items := renderPreferenceItems(preferences, preferencesSchema, orchestrator.schema.Context)
	if len(items) == 0 {
		return ""
	}
	// One pass: text substituted for a placeholder is never scanned again, so a
	// direction that spells "{close}" stays literal.
	return strings.NewReplacer(
		"{open}", preferencesSchema.OpenMarker,
		"{items}", strings.Join(items, "\n"),
		"{close}", preferencesSchema.CloseMarker,
	).Replace(preferencesSchema.ContextTemplate)
}

// renderPreferenceItems orders and bounds the records the prompt shows: the
// newest explicit selection first (it is the direction to continue), then the
// explicit rejections newest first, then the weak unconfirmed signals. The
// Schema's maximum_prompt_items bounds the block.
func renderPreferenceItems(
	preferences []Preference,
	preferencesSchema catalog.ChatPreferencesSchema,
	contextSchema catalog.ChatContextSchema,
) []string {
	maximum := preferencesSchema.MaximumPromptItems
	if maximum <= 0 {
		return nil
	}
	// ListPreferences returns newest first. Keep the newest explicit selection
	// and hide older selections: only one direction is the current one.
	var selected, rejected, signals []Preference
	for _, preference := range preferences {
		switch {
		case preference.Explicit && preference.Kind == PreferenceKindSelected:
			if len(selected) == 0 {
				selected = append(selected, preference)
			}
		case preference.Explicit && preference.Kind == PreferenceKindRejected:
			rejected = append(rejected, preference)
		case !preference.Explicit && preference.Kind == PreferenceKindSignal:
			signals = append(signals, preference)
		}
	}
	sort.SliceStable(rejected, func(left, right int) bool {
		return rejected[left].CreatedAt.After(rejected[right].CreatedAt)
	})
	sort.SliceStable(signals, func(left, right int) bool {
		return signals[left].CreatedAt.After(signals[right].CreatedAt)
	})
	ordered := append(selected, rejected...)
	ordered = append(ordered, signals...)
	items := make([]string, 0, len(ordered))
	for _, preference := range ordered {
		if len(items) >= maximum {
			break
		}
		item := preferenceItem(preference, preferencesSchema, contextSchema)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func preferenceItem(
	preference Preference,
	preferencesSchema catalog.ChatPreferencesSchema,
	contextSchema catalog.ChatContextSchema,
) string {
	markers := []string{
		preferencesSchema.OpenMarker,
		preferencesSchema.CloseMarker,
		contextSchema.SessionNotesOpenMarker,
		contextSchema.SessionNotesCloseMarker,
	}
	direction := withoutMarkers(strings.TrimSpace(preference.Direction), markers...)
	handle := withoutMarkers(strings.TrimSpace(preference.Handle), markers...)
	if direction == "" {
		direction = handle
	}
	if direction == "" {
		return ""
	}
	values := map[string]string{"{direction}": direction, "{handle}": handle}
	template := preferencesSchema.SignalItemTemplate
	switch {
	case preference.Kind == PreferenceKindSelected:
		template = preferencesSchema.SelectedItemTemplate
	case preference.Kind == PreferenceKindRejected:
		template = preferencesSchema.RejectedItemTemplate
		label := preference.ReasonPreset
		for _, preset := range preferencesSchema.ReasonPresets {
			if preset.ID == preference.ReasonPreset {
				label = preset.Label
				break
			}
		}
		reason := preferenceReason(label, withoutMarkers(preference.ReasonText, markers...), preferencesSchema.ReasonJoinTemplate)
		values["{reason}"] = reason
	}
	replacer := strings.NewReplacer(
		"{direction}", values["{direction}"],
		"{handle}", values["{handle}"],
		"{reason}", values["{reason}"],
	)
	return replacer.Replace(template)
}

// preferenceReason joins the preset label and the free text with the Schema's
// template. Whichever part is missing is left out; both missing is empty.
func preferenceReason(label, text, joinTemplate string) string {
	label = strings.TrimSpace(label)
	text = strings.TrimSpace(text)
	switch {
	case label != "" && text != "":
		return strings.NewReplacer("{preset}", label, "{text}", text).Replace(joinTemplate)
	case label != "":
		return label
	default:
		return text
	}
}

package chat

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// handlePolicy is the attachment handle syntax declared by chat.context.
func (orchestrator *Orchestrator) handlePolicy() HandlePolicy {
	return NewHandlePolicy(orchestrator.schema.Context)
}

// attachAssistantResults records the generated pictures and models of the
// assistant message as attachments before the final write. The tools whose
// Schema result is a 3D model decide which Tasks yield model attachments.
func (orchestrator *Orchestrator) attachAssistantResults(
	ctx context.Context,
	ownerID string,
	assistant *Message,
) {
	if !orchestrator.handlePolicy().Enabled() {
		return
	}
	kinds := map[string]string{}
	for _, tool := range orchestrator.schema.Tools {
		kinds[tool.Name] = tool.ResultAttachmentKind
	}
	modelTasks := map[string]bool{}
	for _, call := range assistant.ToolCalls {
		if call.TaskID != "" && kinds[call.Name] == AttachmentKindModel {
			modelTasks[call.TaskID] = true
		}
	}
	orchestrator.service.AttachResults(ctx, ownerID, assistant, modelTasks)
}

// canvasLayoutText names an artboard snapshot's layout with the Schema's
// templates: its pixel size and, when known, its visible layer count (#272).
func canvasLayoutText(context catalog.ChatContextSchema, layout *CanvasLayout) string {
	if layout == nil {
		return ""
	}
	layers := ""
	if layout.Layers > 0 {
		layers = strings.ReplaceAll(context.CanvasLayersTemplate, "{layers}", itoa(layout.Layers))
	}
	return strings.NewReplacer(
		"{width}", itoa(layout.Width),
		"{height}", itoa(layout.Height),
		"{layers}", layers,
	).Replace(context.CanvasLayoutTemplate)
}

// selectionPrompt renders the "current selection" block that tells the Agent
// which objects the user has selected on the workspace. On a canvas turn the
// artboard snapshot is named as the layout the person arranged, and the
// Schema's canvas note follows the block (#272).
func (orchestrator *Orchestrator) selectionPrompt(options TurnOptions) string {
	context := orchestrator.schema.Context
	policy := orchestrator.handlePolicy()
	selection, picked := options.Selection, options.SelectionPicked
	if !policy.Enabled() || len(selection) == 0 || context.SelectionTemplate == "" {
		return ""
	}
	canvasTurn := context.CanvasInputSource != "" && options.InputSource == context.CanvasInputSource
	canvasShown := false
	var items strings.Builder
	for _, attachment := range selection {
		if attachment.Handle == "" {
			continue
		}
		var details strings.Builder
		if canvasTurn && attachment.Role == AttachmentRoleCanvas && attachment.Canvas != nil {
			details.WriteString(strings.ReplaceAll(context.SelectionCanvasTemplate, "{layout}", canvasLayoutText(context, attachment.Canvas)))
			canvasShown = true
		}
		if attachment.TaskID != "" {
			details.WriteString(strings.ReplaceAll(context.SelectionTaskTemplate, "{task_id}", attachment.TaskID))
		}
		if attachment.CaptureID != "" {
			details.WriteString(strings.ReplaceAll(context.SelectionCaptureTemplate, "{capture_id}", attachment.CaptureID))
		}
		items.WriteString(strings.NewReplacer(
			"{handle}", attachment.Handle,
			"{kind}", context.KindLabels[attachment.Kind],
			"{details}", details.String(),
		).Replace(context.SelectionItemTemplate))
	}
	if items.Len() == 0 {
		return ""
	}
	prompt := strings.NewReplacer(
		"{alias}", policy.SelectionAlias,
		"{items}", items.String(),
	).Replace(context.SelectionTemplate)
	if picked {
		prompt += context.SelectionPickedTemplate
	}
	if canvasShown {
		prompt += context.SelectionCanvasNote
	}
	return prompt
}

// attachmentLabel names a message picture by its handle and lineage. It
// reports false for a picture without a structured attachment, which keeps
// the legacy index-only label.
func (orchestrator *Orchestrator) attachmentLabel(message Message, slot, index int) (string, bool) {
	if !orchestrator.handlePolicy().Enabled() {
		return "", false
	}
	attachment, found := AttachmentForSlot(message, slot)
	if !found || attachment.Handle == "" {
		return "", false
	}
	template := orchestrator.schema.Context.AttachmentLabelTemplates[attachment.Kind+"."+attachment.Role]
	if template == "" {
		return "", false
	}
	return strings.NewReplacer(
		"{index}", itoa(index),
		"{handle}", attachment.Handle,
		"{task_id}", attachment.TaskID,
		"{capture_id}", attachment.CaptureID,
	).Replace(template), true
}

// currentUploadHandles renders the handles of the pictures the latest user
// message carries, so the upload hint can say which pictures are this turn's
// (#293). It is empty when handles are off, the Schema declares no template or
// none of the pictures has a handle.
func (orchestrator *Orchestrator) currentUploadHandles(message Message, images []indexedChatImage) string {
	template := orchestrator.schema.Context.CurrentUploadHandlesTemplate
	if template == "" || !orchestrator.handlePolicy().Enabled() {
		return ""
	}
	handles := make([]string, 0, len(images))
	for _, image := range images {
		if attachment, found := AttachmentForSlot(message, image.slot); found && attachment.Handle != "" {
			handles = append(handles, attachment.Handle)
		}
	}
	if len(handles) == 0 {
		return ""
	}
	return strings.ReplaceAll(template, "{handles}", strings.Join(handles, " "))
}

// unpicturedLabels names the attachments of a message that show no picture.
func (orchestrator *Orchestrator) unpicturedLabels(message Message) string {
	return orchestrator.unpicturedLabelsExcept(message, nil)
}

// unpicturedLabelsExcept is unpicturedLabels without the attachments (by
// position in message.Attachments) that already ride as a picture part with
// their own label.
func (orchestrator *Orchestrator) unpicturedLabelsExcept(message Message, shown map[int]bool) string {
	if !orchestrator.handlePolicy().Enabled() {
		return ""
	}
	var labels strings.Builder
	for position, attachment := range message.Attachments {
		if shown[position] {
			continue
		}
		labels.WriteString(orchestrator.unpicturedLabel(attachment))
	}
	return labels.String()
}

// unpicturedLabel is the Schema label of one attachment that has no picture
// slot, or an empty string when it has none.
func (orchestrator *Orchestrator) unpicturedLabel(attachment Attachment) string {
	if attachment.ImageSlot != 0 || attachment.Handle == "" {
		return ""
	}
	templates := orchestrator.schema.Context.UnpicturedAttachmentTemplates
	template := templates[attachment.Kind]
	// A role's own label (the artboard snapshot, #272) comes first.
	if roleTemplate := templates[attachment.Kind+"."+attachment.Role]; attachment.Role != "" && roleTemplate != "" &&
		(attachment.Role != AttachmentRoleCanvas || attachment.Canvas != nil) {
		template = roleTemplate
	}
	if template == "" {
		return ""
	}
	return strings.NewReplacer(
		"{handle}", attachment.Handle,
		"{task_id}", attachment.TaskID,
		"{capture_id}", attachment.CaptureID,
		"{layout}", canvasLayoutText(orchestrator.schema.Context, attachment.Canvas),
	).Replace(template)
}

// canvasSnapshotParts renders the artboard snapshot of the current turn as a
// real picture followed by its canvas label (#16). The snapshot travels as a
// selection attachment, not in message.image_urls, so the history image index
// never sees it; without this the model only read its text label. Each
// picture is inlined like a history picture (so a snapshot over
// vision_inline_max_bytes takes the #292 derivative path) and shown at the
// Schema's canvas_image_detail. A snapshot that cannot be inlined keeps its
// text label, which unpicturedLabelsExcept still emits; the turn does not
// fail. The result is bounded by limit, and shown holds the positions in
// message.Attachments that now have a picture.
func (orchestrator *Orchestrator) canvasSnapshotParts(
	ctx context.Context,
	ownerID string,
	message Message,
	limit int,
) (parts []agents.ContentPart, shown map[int]bool) {
	if !orchestrator.schema.Agent.Vision || !orchestrator.handlePolicy().Enabled() {
		return nil, nil
	}
	for position, attachment := range message.Attachments {
		if len(shown) >= limit {
			break
		}
		if attachment.Kind != AttachmentKindImage || attachment.Role != AttachmentRoleCanvas ||
			attachment.ImageSlot != 0 {
			continue
		}
		pictureURL := AttachmentURL(message, attachment)
		label := orchestrator.unpicturedLabel(attachment)
		if pictureURL == "" || label == "" || !orchestrator.visionCandidate(pictureURL) {
			continue
		}
		if !visionSafeImage(pictureURL) {
			inlined, ok := orchestrator.visionInline(ctx, generation.OwnerID(ownerID), pictureURL)
			if !ok {
				continue
			}
			pictureURL = inlined
		}
		parts = append(parts,
			agents.ContentPart{
				Kind:   orchestrator.schema.Context.ImagePartKind,
				URL:    pictureURL,
				Detail: orchestrator.schema.Context.CanvasImageDetail,
			},
			agents.ContentPart{Kind: orchestrator.schema.Context.TextPartKind, Text: label},
		)
		if shown == nil {
			shown = map[int]bool{}
		}
		shown[position] = true
	}
	return parts, shown
}

// notesPrompt renders the session's settings summary for the system prompt.
// It is read for the owner's own session on every request, so an edit by the
// user or by the update_session_notes tool applies to the next turn; a session
// without notes (or a read failure) adds nothing.
//
// The notes are free text written by the user or the Agent, and they are the
// last thing in the prompt. They are therefore framed: the Schema's template
// puts them between an opening and a closing marker and says they are context,
// not instructions. Any occurrence of either marker inside the notes is removed
// first, so the notes cannot end the frame early and continue as prompt text,
// and they cannot be mistaken for the real selection or handle labels above.
func (orchestrator *Orchestrator) notesPrompt(ctx context.Context, ownerID, sessionID string) string {
	notesContext := orchestrator.schema.Context
	if notesContext.SessionNotesTemplate == "" || sessionID == "" {
		return ""
	}
	notes, err := orchestrator.service.SessionNotes(ctx, ownerID, sessionID)
	if err != nil {
		return ""
	}
	notes = withoutMarkers(notes, notesContext.SessionNotesOpenMarker, notesContext.SessionNotesCloseMarker)
	if strings.TrimSpace(notes) == "" {
		return ""
	}
	// One pass: text substituted for a placeholder is never scanned again, so a
	// note that spells "{close}" stays literal.
	return strings.NewReplacer(
		"{open}", notesContext.SessionNotesOpenMarker,
		"{notes}", notes,
		"{close}", notesContext.SessionNotesCloseMarker,
	).Replace(notesContext.SessionNotesTemplate)
}

// withoutMarkers removes every marker from text until none is left, so that
// removing one cannot join the pieces around it into another. A marker is
// matched on a folded form of the text: letter case, full-width ASCII, and
// whitespace or invisible format characters (zero-width joiners and the like)
// inside it do not hide it, because the model reading the prompt would still
// take such a spelling for the frame marker. The text itself is returned as
// written apart from the removed spans.
func withoutMarkers(text string, markers ...string) string {
	folded := make([]string, len(markers))
	for index, marker := range markers {
		folded[index], _ = foldMarkerText(marker)
	}
	for {
		foldedText, offsets := foldMarkerText(text)
		removed := false
		for _, marker := range folded {
			if marker == "" {
				continue
			}
			if start := strings.Index(foldedText, marker); start >= 0 {
				text = text[:offsets[start]] + text[offsets[start+len(marker)]:]
				removed = true
				break
			}
		}
		if !removed {
			return text
		}
	}
}

// foldMarkerText returns the marker-matching form of text and, for each byte
// offset of that form (plus one past its end), the byte offset in text it came
// from. Folding lowercases, maps full-width ASCII to ASCII, and drops
// whitespace and Unicode format characters.
func foldMarkerText(text string) (string, []int) {
	var builder strings.Builder
	offsets := make([]int, 0, len(text)+1)
	for offset, r := range text {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0 // full-width ASCII to ASCII
		}
		r = unicode.ToLower(r)
		for range utf8.RuneLen(r) {
			offsets = append(offsets, offset)
		}
		builder.WriteRune(r)
	}
	offsets = append(offsets, len(text))
	return builder.String(), offsets
}

// generatedVisionParts picks the newest generated pictures of the session, up
// to the Schema's recent_generated_image_limit, and renders each as a picture
// part followed by its label. A picture that cannot be inlined is skipped and
// does not use up a place. The result is ordered oldest first.
func (orchestrator *Orchestrator) generatedVisionParts(
	ctx context.Context,
	ownerID string,
	history []Message,
	indexed [][]indexedChatImage,
) []agents.ContentPart {
	limit := orchestrator.schema.Context.RecentGeneratedImageLimit
	if !orchestrator.schema.Agent.Vision || limit <= 0 {
		return nil
	}
	type picked struct {
		pictureURL string
		label      string
	}
	var newestFirst []picked
	for messageIndex := len(history) - 1; messageIndex >= 0 && len(newestFirst) < limit; messageIndex-- {
		message := history[messageIndex]
		if message.Role != orchestrator.schema.Roles.Assistant {
			continue
		}
		images := indexed[messageIndex]
		for imageIndex := len(images) - 1; imageIndex >= 0 && len(newestFirst) < limit; imageIndex-- {
			image := images[imageIndex]
			if !orchestrator.visionCandidate(image.url) {
				continue
			}
			pictureURL := image.url
			if !visionSafeImage(pictureURL) {
				inlined, ok := orchestrator.visionInline(ctx, generation.OwnerID(ownerID), pictureURL)
				if !ok {
					continue
				}
				pictureURL = inlined
			}
			label, structured := orchestrator.attachmentLabel(message, image.slot, image.index)
			if !structured {
				label = strings.ReplaceAll(orchestrator.schema.Context.AIImageLabelTemplate, "{index}", itoa(image.index))
			}
			newestFirst = append(newestFirst, picked{pictureURL: pictureURL, label: label})
		}
	}
	var parts []agents.ContentPart
	if intro := orchestrator.schema.Context.GeneratedVisionIntro; intro != "" && len(newestFirst) > 0 {
		parts = append(parts, agents.ContentPart{Kind: orchestrator.schema.Context.TextPartKind, Text: intro})
	}
	for index := len(newestFirst) - 1; index >= 0; index-- {
		parts = append(parts,
			agents.ContentPart{
				Kind:   orchestrator.schema.Context.ImagePartKind,
				URL:    newestFirst[index].pictureURL,
				Detail: orchestrator.schema.Context.ImageDetail,
			},
			agents.ContentPart{
				Kind: orchestrator.schema.Context.TextPartKind,
				Text: newestFirst[index].label,
			},
		)
	}
	return parts
}

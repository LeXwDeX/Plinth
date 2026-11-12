package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

// turnResults is what the reply produced before this round: its pictures,
// its calls with the Tasks bound so far and the attachments already given
// handles. Later steps of the reply look references up in it (#25).
func turnResults(assistant Message, boundTasks map[string]string) Message {
	produced := copyMessage(assistant)
	for index := range produced.ToolCalls {
		if produced.ToolCalls[index].TaskID == "" {
			produced.ToolCalls[index].TaskID = boundTasks[produced.ToolCalls[index].ID]
		}
	}
	return produced
}

// copyMessage copies the slices a tool may read, so a tool never sees the
// reply change under it.
func copyMessage(message Message) Message {
	message.ImageURLs = append([]string(nil), message.ImageURLs...)
	message.ToolCalls = append([]ToolCall(nil), message.ToolCalls...)
	message.Attachments = append([]Attachment(nil), message.Attachments...)
	return message
}

// recordTurnResult adds one call's result to the reply's results and gives
// handles to what it produced at once, with the numbering the final write
// keeps (AttachResults numbers after the highest handle and skips what has
// one). It returns the new handles.
func (orchestrator *Orchestrator) recordTurnResult(
	ctx context.Context,
	ownerID string,
	produced *Message,
	callID, taskID string,
	imageURLs []string,
) []string {
	if produced == nil {
		return nil
	}
	produced.ImageURLs = append(produced.ImageURLs, imageURLs...)
	if taskID != "" {
		for index := range produced.ToolCalls {
			if produced.ToolCalls[index].ID == callID && produced.ToolCalls[index].TaskID == "" {
				produced.ToolCalls[index].TaskID = taskID
			}
		}
	}
	if taskID == "" && len(imageURLs) == 0 {
		return nil
	}
	before := len(produced.Attachments)
	orchestrator.attachAssistantResults(context.WithoutCancel(ctx), ownerID, produced)
	var handles []string
	for _, attachment := range produced.Attachments[before:] {
		if attachment.Handle != "" {
			handles = append(handles, attachment.Handle)
		}
	}
	return handles
}

// withResultHandles writes the handles a call produced into its JSON result
// under the Schema's key. When the result also carries the Schema's views
// array and it has one entry per handle, each entry gets its own handle in
// render order. A result that is not a JSON object is left as is.
func (orchestrator *Orchestrator) withResultHandles(content string, handles []string) string {
	key := orchestrator.schema.Context.ResultHandlesField
	if key == "" || len(handles) == 0 {
		return content
	}
	var result map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &result) != nil || result == nil {
		return content
	}
	encoded, err := json.Marshal(handles)
	if err != nil {
		return content
	}
	result[key] = encoded
	if viewsField := orchestrator.schema.ModelReview.ResultViewsField; viewsField != "" && len(result[viewsField]) > 0 {
		result[viewsField] = bindViewHandles(result[viewsField], handles)
	}
	updated, err := json.Marshal(result)
	if err != nil {
		return content
	}
	return string(updated)
}

// withSummaryHandles writes the handles a call produced into its compact
// summary under the Schema's key, so the conversation can bind each picture to
// its view by handle (#103 review F9). A summary that is absent or not a JSON
// object is left as is.
func (orchestrator *Orchestrator) withSummaryHandles(summary json.RawMessage, handles []string) json.RawMessage {
	key := orchestrator.schema.Context.ResultHandlesField
	if key == "" || len(handles) == 0 || len(summary) == 0 {
		return summary
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(summary, &result) != nil || result == nil {
		return summary
	}
	encoded, err := json.Marshal(handles)
	if err != nil {
		return summary
	}
	result[key] = encoded
	updated, err := json.Marshal(result)
	if err != nil {
		return summary
	}
	return updated
}

// bindViewHandles writes one handle into each entry of a tool result's views
// array. It returns the array unchanged unless its length matches the handles
// and every entry is a JSON object; a partial pairing could name the wrong
// picture.
func bindViewHandles(raw json.RawMessage, handles []string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var views []map[string]json.RawMessage
	if json.Unmarshal(raw, &views) != nil || len(views) != len(handles) {
		return raw
	}
	for index, view := range views {
		if view == nil {
			return raw
		}
		encoded, err := json.Marshal(handles[index])
		if err != nil {
			return raw
		}
		view["handle"] = encoded
	}
	updated, err := json.Marshal(views)
	if err != nil {
		return raw
	}
	return updated
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
	prompt += selectionMaskText(context, options)
	return prompt
}

// selectionMaskText names the frozen selection mask of a canvas turn (#244):
// the picture it belongs to, the pixel box and coverage of the selected region
// and the transparency meaning. It is appended after the selection block, so a
// canvas turn's Agent reads the exact region the person painted instead of
// guessing it from the snapshot.
func selectionMaskText(context catalog.ChatContextSchema, options TurnOptions) string {
	mask := options.SelectionMask
	if mask == nil || context.SelectionMaskItemTemplate == "" {
		return ""
	}
	handle := ""
	if snapshot, found := canvasSnapshotAttachment(options); found {
		handle = snapshot.Handle
	}
	if handle == "" {
		return ""
	}
	item := strings.NewReplacer(
		"{handle}", handle,
		"{bbox}", fmt.Sprintf("%d,%d-%d,%d", mask.BBox[0], mask.BBox[1], mask.BBox[2], mask.BBox[3]),
		"{coverage}", strconv.FormatFloat(mask.Coverage*100, 'f', 1, 64)+"%",
	).Replace(context.SelectionMaskItemTemplate)
	return item + context.SelectionMaskNote
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

// briefPrompt renders the production brief frozen with the turn (#103). The
// brief is read from the stored session when the turn starts, so a brief the
// user or the Agent writes applies to the next turn. A session without a brief
// gets the Schema's empty text, which tells the Agent to ask for the purpose
// before starting 3D work. Field labels come from the brief Schema; a profile
// adds its budget and requirements.
func (orchestrator *Orchestrator) briefPrompt(options TurnOptions) string {
	briefSchema := orchestrator.schema.Model3DUsage.Brief
	if strings.TrimSpace(briefSchema.ContextTemplate) == "" {
		return ""
	}
	body := orchestrator.productionBriefText(options.ProductionBrief, briefSchema)
	if body == "" {
		body = briefSchema.EmptyText
	}
	return strings.ReplaceAll(briefSchema.ContextTemplate, "{brief}", body)
}

func (orchestrator *Orchestrator) productionBriefText(
	brief ProductionBrief,
	briefSchema catalog.ChatProductionBriefSchema,
) string {
	if brief.Empty() {
		return ""
	}
	// Locate the purpose and the triangle override by their declared types, so
	// renaming either field in the Schema needs no code change (#103 review
	// C3).
	purposeField := productionBriefPurposeField(briefSchema)
	integerField := productionBriefIntegerField(briefSchema)
	purposeID := brief.Text(purposeField)
	override, hasOverride := brief.Integer(integerField)
	// The purpose's budget carries a session triangle override, so it is
	// rendered with the purpose and not again as a separate field.
	usage := orchestrator.schema.Model3DUsage
	var parts []string
	for _, field := range briefSchema.Fields {
		label := field.Label
		if label == "" {
			label = field.Name
		}
		if integerField != "" && field.Name == integerField && hasOverride && purposeID != "" {
			continue
		}
		switch field.Type {
		case "profile":
			if field.Name != purposeField || purposeID == "" {
				continue
			}
			profile, found := usageProfile(usage, purposeID)
			value := purposeID
			budget := ""
			if found {
				// The budget the Agent plans against is the one for the brief's
				// target platform when the purpose has a variant for it (#275);
				// the platform itself is rendered as its own field.
				profile, _ = usage.ForPlatform(profile, brief.Text(usage.PlatformField))
				value = profile.Label
				budget = productionBriefBudgetText(profile.Budget, override, hasOverride, briefSchema)
			}
			if budget != "" {
				value = strings.NewReplacer("{label}", value, "{budget}", budget).Replace(briefSchema.PurposeTemplate)
			}
			parts = append(parts, strings.NewReplacer("{label}", label, "{value}", value).Replace(briefSchema.FieldTemplate))
		case "image_profile":
			imageUseField := productionBriefImageUseField(briefSchema)
			if field.Name != imageUseField {
				continue
			}
			imageUse := brief.Text(field.Name)
			if imageUse == "" {
				continue
			}
			value := imageUse
			if profile, found := image2DUsageProfile(orchestrator.schema.Image2DUsage, imageUse); found {
				value = profile.Label
				if requirements := image2DRequirementsText(profile, orchestrator.schema.Image2DUsage.RequirementsNote, briefSchema.ImageRequirementSeparator); requirements != "" {
					if briefSchema.ImageRequirementsTemplate != "" {
						requirements = strings.ReplaceAll(briefSchema.ImageRequirementsTemplate, "{items}", requirements)
					}
					value = strings.NewReplacer(
						"{label}", value, "{requirements}", requirements,
					).Replace(briefSchema.ImagePurposeTemplate)
				}
			}
			parts = append(parts, strings.NewReplacer("{label}", label, "{value}", value).Replace(briefSchema.FieldTemplate))
		case "select":
			// A stored option id reads as its label; a value outside the
			// options (free text written before the field became a select)
			// is kept and shown as it was written (#286).
			value := brief.Text(field.Name)
			if value == "" {
				continue
			}
			if option, found := field.OptionByID(value); found {
				value = option.Label
			}
			parts = append(parts, strings.NewReplacer("{label}", label, "{value}", value).Replace(briefSchema.FieldTemplate))
		case "text":
			value := brief.Text(field.Name)
			if value == "" {
				continue
			}
			parts = append(parts, strings.NewReplacer("{label}", label, "{value}", value).Replace(briefSchema.FieldTemplate))
		case "boolean":
			if !brief.Flag(field.Name) {
				continue
			}
			parts = append(parts, strings.ReplaceAll(briefSchema.FlagTemplate, "{label}", label))
		case "integer":
			value, exists := brief.Integer(field.Name)
			if !exists {
				continue
			}
			parts = append(parts, strings.NewReplacer(
				"{label}", label,
				"{value}", strconv.Itoa(value),
			).Replace(briefSchema.FieldTemplate))
		}
	}
	return strings.Join(parts, briefSchema.Separator)
}

// image2DUsageProfile finds a declared 2D purpose by id.
func image2DUsageProfile(usage catalog.ChatImage2DUsageSchema, id string) (catalog.ChatImage2DUsageProfile, bool) {
	for _, profile := range usage.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return catalog.ChatImage2DUsageProfile{}, false
}

// image2DRequirementsText renders one 2D purpose's delivery requirements with
// the Schema's own words, so no enum literal reaches the Agent (#239). A
// tileable purpose adds its tiling requirement and a purpose with prompt
// guidance adds how to write the prompt (#287).
func image2DRequirementsText(profile catalog.ChatImage2DUsageProfile, note catalog.ChatImage2DRequirementsNote, separator string) string {
	if strings.TrimSpace(note.Template) == "" {
		return ""
	}
	if separator == "" {
		separator = note.Separator
	}
	requirements := profile.Requirements
	items := []string{
		strings.ReplaceAll(note.AspectRatioTemplate, "{value}", requirements.AspectRatio),
		strings.ReplaceAll(note.MinimumShortEdgeTemplate, "{value}", strconv.Itoa(requirements.MinimumShortEdge)),
		strings.ReplaceAll(note.BackgroundTemplate, "{value}", note.BackgroundTexts[requirements.Background]),
		strings.ReplaceAll(note.SubjectsTemplate, "{value}", note.SubjectsTexts[requirements.Subjects]),
	}
	if requirements.Pose != "" {
		if text := note.PoseTexts[requirements.Pose]; text != "" {
			items = append(items, strings.ReplaceAll(note.PoseTemplate, "{value}", text))
		}
	}
	if requirements.Tileable {
		items = append(items, note.TileableText)
	}
	if guidance := strings.TrimSpace(profile.PromptGuidance); guidance != "" {
		items = append(items, strings.ReplaceAll(note.GuidanceTemplate, "{value}", guidance))
	}
	kept := items[:0]
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.ReplaceAll(note.Template, "{items}", strings.Join(kept, separator))
}

// productionBriefBudgetText renders the triangle budget of a purpose. A
// session override replaces the maximum; the requirements still come from the
// purpose, because the purpose is what asks for UV, a watertight mesh or skin.
func productionBriefBudgetText(
	budget catalog.ChatModel3DUsageBudget,
	override int,
	hasOverride bool,
	briefSchema catalog.ChatProductionBriefSchema,
) string {
	requirements := productionBriefRequirements(budget, briefSchema)
	switch {
	case hasOverride:
		return strings.NewReplacer(
			"{value}", strconv.Itoa(override),
			"{requirements}", requirements,
		).Replace(briefSchema.OverrideBudgetTemplate)
	case budget.MinTriangles != nil && *budget.MinTriangles > 0:
		return strings.NewReplacer(
			"{minimum}", strconv.Itoa(*budget.MinTriangles),
			"{maximum}", strconv.Itoa(budget.MaxTriangles),
			"{requirements}", requirements,
		).Replace(briefSchema.BudgetTemplate)
	case budget.MaxTriangles > 0:
		return strings.NewReplacer(
			"{maximum}", strconv.Itoa(budget.MaxTriangles),
			"{requirements}", requirements,
		).Replace(briefSchema.OpenBudgetTemplate)
	default:
		return ""
	}
}

func productionBriefRequirements(budget catalog.ChatModel3DUsageBudget, briefSchema catalog.ChatProductionBriefSchema) string {
	var items []string
	if budget.RequireUV && briefSchema.UVText != "" {
		items = append(items, briefSchema.UVText)
	}
	if budget.RequireWatertight && briefSchema.WatertightText != "" {
		items = append(items, briefSchema.WatertightText)
	}
	if budget.RequireSkin && briefSchema.SkinText != "" {
		items = append(items, briefSchema.SkinText)
	}
	if len(items) == 0 {
		return ""
	}
	return strings.ReplaceAll(briefSchema.RequirementsTemplate, "{items}", strings.Join(items, briefSchema.RequirementSeparator))
}

func usageProfile(usage catalog.ChatModel3DUsageSchema, id string) (catalog.ChatModel3DUsageProfile, bool) {
	for _, profile := range usage.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return catalog.ChatModel3DUsageProfile{}, false
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

package chat

import (
	"context"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// verifiedAttachments is the outcome of checking a turn's claims against the
// owner and session, before handles are given out.
type verifiedAttachments struct {
	// images has one entry per message image, in order.
	images []Attachment
	// selection holds the verified selected objects, without handles.
	selection []Attachment
	// mentions has one entry per mention that names a verified object.
	mentions []verifiedMention
	// droppedMentions are the tokens left as plain text.
	droppedMentions []string
	// picked is true when selection is the session's picked candidate and the
	// client sent no selection of its own.
	picked bool
}

// verifyTurnAttachments verifies every claim of the turn. It never fails the
// turn: a claim that does not hold is demoted to a plain upload (message
// images) or dropped (selection), so an unverified ID can never reach the
// Agent or a tool. The IO happens before the session stream lock is taken.
func (service *Service) verifyTurnAttachments(ctx context.Context, command StartTurn) verifiedAttachments {
	policy := service.handles
	if !policy.Enabled() || service.attachments == nil {
		return verifiedAttachments{}
	}
	claims := map[int]AttachmentRef{}
	accepted := 0
	for _, ref := range command.Attachments {
		if policy.MaximumAttachments > 0 && accepted >= policy.MaximumAttachments {
			break
		}
		accepted++
		if ref.ImageSlot < 1 || ref.ImageSlot > len(command.ImageURLs) {
			continue
		}
		if _, claimed := claims[ref.ImageSlot]; !claimed {
			claims[ref.ImageSlot] = ref
		}
	}
	var result verifiedAttachments
	for index, imageURL := range command.ImageURLs {
		slot := index + 1
		ref, claimed := claims[slot]
		ref.Kind, ref.URL, ref.ImageSlot = AttachmentKindImage, imageURL, slot
		if !claimed {
			// No claim: an image that already is a platform URL still gets its
			// lineage when it verifies, but a failure is not a rejection.
			ref.AssetID, ref.TaskID, ref.CaptureID = "", "", ""
		}
		attachment, err := service.attachments.VerifyAttachment(ctx, command.OwnerID, command.SessionID, ref)
		if err != nil {
			attachment = Attachment{Kind: AttachmentKindImage, Role: AttachmentRoleUpload}
		}
		attachment.Kind, attachment.ImageSlot, attachment.URL = AttachmentKindImage, slot, ""
		result.images = append(result.images, attachment)
	}
	canvasMarked := false
	for _, ref := range command.Selection {
		if policy.MaximumSelection > 0 && len(result.selection) >= policy.MaximumSelection {
			break
		}
		ref.ImageSlot = 0
		attachment, err := service.attachments.VerifyAttachment(ctx, command.OwnerID, command.SessionID, ref)
		if err != nil {
			continue
		}
		if attachment.AssetID == "" && attachment.TaskID == "" && attachment.CaptureID == "" && attachment.URL == "" {
			// A selected object without a platform identity or a picture (a
			// pasted data URI) would only get a handle that resolves to nothing.
			continue
		}
		if command.Options.InputSource != "" && !canvasMarked {
			canvasMarked = service.markCanvasSnapshot(ctx, command.OwnerID, command.SessionID, ref, &attachment)
		}
		duplicate := false
		for _, other := range result.selection {
			duplicate = duplicate || SameObject(other, attachment)
		}
		if !duplicate {
			result.selection = append(result.selection, attachment)
		}
	}
	if len(result.selection) == 0 && len(command.Selection) == 0 {
		// Nothing is selected on the workspace: the candidate the user picked
		// ("选用") is the current object. It is verified like any selection, so a
		// pick whose Task is gone is simply not used.
		if pick, ok, err := service.SessionPick(ctx, command.OwnerID, command.SessionID); err == nil && ok {
			pick.Handle = ""
			result.selection = append(result.selection, pick)
			result.picked = true
		}
	}
	service.verifyMentions(ctx, command, &result)
	return result
}

// markCanvasSnapshot gives the artboard snapshot of a canvas turn its own role
// and layout (#272), so the Agent reads it as the layout the person arranged
// rather than as one more upload. Only a verified owned input asset qualifies
// (the snapshot is uploaded at send time; a generated picture or a capture is
// never relabelled). Its pixel size is read from the asset itself; the
// client's layer count is kept only within the Schema bound. A picture whose
// size cannot be read keeps the upload role. It reports whether it marked.
func (service *Service) markCanvasSnapshot(ctx context.Context, ownerID, sessionID string, ref AttachmentRef, attachment *Attachment) bool {
	if attachment.Kind != AttachmentKindImage || attachment.Role != AttachmentRoleUpload || attachment.AssetID == "" ||
		attachment.TaskID != "" || attachment.CaptureID != "" {
		return false
	}
	reader, ok := service.attachments.(ImageDimensionReader)
	if !ok {
		return false
	}
	width, height, err := reader.ImageDimensions(ctx, ownerID, attachment.AssetID)
	if err != nil || width <= 0 || height <= 0 {
		return false
	}
	layers := ref.CanvasLayers
	if layers < 1 || (service.handles.MaximumCanvasLayers > 0 && layers > service.handles.MaximumCanvasLayers) {
		layers = 0
	}
	attachment.Role = AttachmentRoleCanvas
	attachment.Canvas = &CanvasLayout{Width: width, Height: height, Layers: layers,
		Sources: service.canvasSources(ctx, ownerID, sessionID, ref.CanvasSources)}
	return true
}

// canvasSources verifies the picture layers an artboard snapshot claims to
// flatten and keeps the Tasks they come from (#189), so a tool that takes a
// source Task can resolve the snapshot back to the preview it shows. Each URL
// is verified like any image claim (owner, and the session for a Task); a
// layer without a Task (an upload, a drawing) or a claim that fails is left
// out, and the list never exceeds the Schema's layer bound.
func (service *Service) canvasSources(ctx context.Context, ownerID, sessionID string, urls []string) []string {
	var sources []string
	for index, url := range urls {
		if service.handles.MaximumCanvasLayers > 0 && index >= service.handles.MaximumCanvasLayers {
			break
		}
		layer, err := service.attachments.VerifyAttachment(ctx, ownerID, sessionID, AttachmentRef{Kind: AttachmentKindImage, URL: url})
		if err != nil || layer.TaskID == "" || layer.CaptureID != "" || slices.Contains(sources, layer.TaskID) {
			continue
		}
		sources = append(sources, layer.TaskID)
	}
	return sources
}

// applyTurnAttachments gives the verified attachments their handles and
// records them on the user message and, for the selection, on the turn
// options. It runs under the session stream lock so two turns of one session
// cannot hand out the same handle number.
func (service *Service) applyTurnAttachments(
	ctx context.Context,
	command StartTurn,
	verified verifiedAttachments,
	user *Message,
	options *TurnOptions,
) error {
	policy := service.handles
	if !policy.Enabled() || (len(verified.images) == 0 && len(verified.selection) == 0 && len(verified.mentions) == 0) {
		return nil
	}
	history, err := service.repository.ListMessages(ctx, command.OwnerID, command.SessionID)
	if err != nil {
		return err
	}
	// A selected object that the session already shows keeps its handle.
	known := func(attachment Attachment) (Attachment, bool) {
		for index := len(history) - 1; index >= 0; index-- {
			for _, candidate := range history[index].Attachments {
				if SameObject(candidate, attachment) {
					candidate.URL = AttachmentURL(history[index], candidate)
					candidate.ImageSlot = 0
					return candidate, true
				}
			}
		}
		return Attachment{}, false
	}
	pending := make([]Attachment, 0, len(verified.images)+len(verified.selection))
	pending = append(pending, verified.images...)
	type selectionSlot struct {
		existing Attachment
		found    bool
		imageIdx int
		newIdx   int
	}
	slots := make([]selectionSlot, len(verified.selection))
	for index, attachment := range verified.selection {
		slot := selectionSlot{imageIdx: -1, newIdx: -1}
		if existing, found := known(attachment); found {
			slot.existing, slot.found = existing, true
		} else {
			for imageIndex, candidate := range verified.images {
				if SameObject(candidate, attachment) {
					slot.imageIdx = imageIndex
				}
			}
			if slot.imageIdx < 0 {
				slot.newIdx = len(pending)
				pending = append(pending, attachment)
			}
		}
		slots[index] = slot
	}
	mentionSlots := make([]int, len(verified.mentions))
	for index, mention := range verified.mentions {
		mentionSlots[index] = -1
		if mention.imageIndex >= 0 {
			continue
		}
		if _, found := known(mention.attachment); found {
			continue
		}
		duplicate := false
		for pendingIndex, other := range pending {
			if SameObject(other, mention.attachment) {
				mentionSlots[index], duplicate = pendingIndex, true
				break
			}
		}
		if !duplicate {
			mentionSlots[index] = len(pending)
			pending = append(pending, mention.attachment)
		}
	}
	assigned := policy.Assign(history, pending)
	user.Attachments = assigned
	options.Selection = nil
	options.SelectionPicked = verified.picked
	for index, slot := range slots {
		switch {
		case slot.found:
			existing := slot.existing
			if verified.selection[index].Role == AttachmentRoleCanvas {
				// The same picture sent again as the artboard (an unchanged
				// board uploads the same asset) keeps its handle; this turn
				// reads it as the layout measured now.
				existing.Role, existing.Canvas = AttachmentRoleCanvas, verified.selection[index].Canvas
			}
			options.Selection = append(options.Selection, existing)
		case slot.imageIdx >= 0:
			picked := assigned[slot.imageIdx]
			picked.URL = command.ImageURLs[picked.ImageSlot-1]
			picked.ImageSlot = 0
			picked.Role = verified.selection[index].Role
			picked.Canvas = verified.selection[index].Canvas
			options.Selection = append(options.Selection, picked)
		default:
			options.Selection = append(options.Selection, assigned[slot.newIdx])
		}
	}
	handles := map[string]string{}
	for index, mention := range verified.mentions {
		switch {
		case mention.imageIndex >= 0:
			handles[mention.token] = assigned[mention.imageIndex].Handle
		case mentionSlots[index] >= 0:
			handles[mention.token] = assigned[mentionSlots[index]].Handle
		default:
			if existing, found := known(mention.attachment); found {
				handles[mention.token] = existing.Handle
			}
		}
	}
	for token, handle := range handles {
		if handle == "" {
			delete(handles, token)
		}
	}
	if len(handles) > 0 {
		user.Content = rewriteMentions(user.Content, handles)
	}
	return nil
}

// AttachResults records the generated pictures of an assistant message as
// structured attachments with handles. modelTasks names the Tasks whose
// result is a 3D model: the pictures of such a Task are the model's preview
// (the render of the built model and any picture the provider drew before
// modelling), never 2D candidates. The first of them becomes the model
// attachment and shows in its ImageSlot; every further picture of the same
// Task, and a picture whose model does not verify, gets no attachment and so
// no image handle. Only a Task outside modelTasks yields image attachments.
// Anything that does not verify for the owner and session is left without an
// attachment; the picture itself is unaffected. Existing attachments keep
// their handles, so the call is repeatable.
func (service *Service) AttachResults(
	ctx context.Context,
	ownerID string,
	message *Message,
	modelTasks map[string]bool,
) {
	policy := service.handles
	if !policy.Enabled() || service.attachments == nil {
		return
	}
	var pending []Attachment
	covered := map[string]bool{}
	for _, existing := range message.Attachments {
		if existing.Kind == AttachmentKindModel {
			covered[existing.TaskID] = true
		}
	}
	for index, imageURL := range message.ImageURLs {
		slot := index + 1
		if _, done := AttachmentForSlot(*message, slot); done || imageURL == "" {
			continue
		}
		picture, err := service.attachments.VerifyAttachment(ctx, ownerID, message.SessionID,
			AttachmentRef{Kind: AttachmentKindImage, URL: imageURL, ImageSlot: slot})
		if err != nil {
			continue
		}
		if picture.TaskID != "" && modelTasks[picture.TaskID] {
			// A picture of a model Task previews the model; it must not look
			// like a 2D candidate that can be picked or edited.
			if covered[picture.TaskID] {
				continue
			}
			model, err := service.attachments.VerifyAttachment(ctx, ownerID, message.SessionID,
				AttachmentRef{Kind: AttachmentKindModel, TaskID: picture.TaskID, URL: imageURL, ImageSlot: slot})
			if err != nil {
				continue
			}
			model.URL = ""
			covered[model.TaskID] = true
			pending = append(pending, model)
			continue
		}
		picture.URL = ""
		pending = append(pending, picture)
	}
	taskIDs := make([]string, 0, len(modelTasks))
	for taskID, isModel := range modelTasks {
		if isModel {
			taskIDs = append(taskIDs, taskID)
		}
	}
	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		if covered[taskID] {
			continue
		}
		model, err := service.attachments.VerifyAttachment(ctx, ownerID, message.SessionID,
			AttachmentRef{Kind: AttachmentKindModel, TaskID: taskID})
		if err == nil {
			covered[taskID] = true
			pending = append(pending, model)
		}
	}
	if len(pending) == 0 {
		return
	}
	history, err := service.repository.ListMessages(ctx, ownerID, message.SessionID)
	if err != nil {
		return
	}
	// The message's own attachments count for numbering even when the stored
	// copy of the message does not carry them yet.
	history = append(history, Message{Attachments: message.Attachments})
	message.Attachments = append(append([]Attachment(nil), message.Attachments...), policy.Assign(history, pending)...)
}

// MentionRef is one `@` reference of a message: the token written in the text
// and the client's claim about the picture it names.
type MentionRef struct {
	Token string
	Ref   AttachmentRef
}

// verifiedMention is a mention whose claim holds. imageIndex is the message
// image it names (ImageSlot) or -1.
type verifiedMention struct {
	token      string
	attachment Attachment
	imageIndex int
}

// maximumMentionTokenRunes bounds a token; a handle or a picture's name is far shorter.
const maximumMentionTokenRunes = 64

// verifyMentions checks every mention against the owner and session. A token
// that is not written in the text as a whole word, is repeated, or whose claim
// does not hold is dropped, never an error: the text stays as the person wrote it.
func (service *Service) verifyMentions(ctx context.Context, command StartTurn, result *verifiedAttachments) {
	policy := service.handles
	seen := map[string]bool{}
	for _, mention := range command.Mentions {
		token := strings.TrimSpace(mention.Token)
		if token == "" || utf8.RuneCountInString(token) > maximumMentionTokenRunes {
			continue
		}
		if seen[token] {
			continue
		}
		seen[token] = true
		if !policy.Enabled() || service.attachments == nil || !mentionsToken(command.Content, token) {
			result.droppedMentions = append(result.droppedMentions, token)
			continue
		}
		if _, _, isHandle := policy.Parse(token); isHandle && mention.Ref.empty() {
			// A handle of the session needs no claim: it is resolved from the history when used.
			continue
		}
		ref := mention.Ref
		if ref.Kind == "" {
			ref.Kind = AttachmentKindImage
		}
		if ref.ImageSlot >= 1 && ref.ImageSlot <= len(result.images) {
			result.mentions = append(result.mentions, verifiedMention{token: token, imageIndex: ref.ImageSlot - 1})
			continue
		}
		ref.ImageSlot = 0
		attachment, err := service.attachments.VerifyAttachment(ctx, command.OwnerID, command.SessionID, ref)
		if err != nil || (attachment.AssetID == "" && attachment.TaskID == "" && attachment.CaptureID == "" && attachment.URL == "") {
			result.droppedMentions = append(result.droppedMentions, token)
			continue
		}
		result.mentions = append(result.mentions, verifiedMention{token: token, attachment: attachment, imageIndex: -1})
	}
}

func isMentionBoundary(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",，。.、(（", r)
}

// isMentionWordRune reports whether r continues a token. Only ASCII letters, digits and underscore do, so
// a CJK character or punctuation may follow a mention ("@图层2改成红色"). Keep in step with mentionsToken in
// src/lib/chat/mentions.ts.
func isMentionWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// mentionSpans finds the whole-word occurrences of token in text: it starts at
// the beginning or after a space or punctuation mark, and is not followed by an
// ASCII letter, digit or underscore.
func mentionSpans(text, token string) [][2]int {
	var spans [][2]int
	for offset := 0; offset <= len(text)-len(token); {
		at := strings.Index(text[offset:], token)
		if at < 0 {
			break
		}
		start := offset + at
		end := start + len(token)
		offset = end
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			if !isMentionBoundary(previous) {
				continue
			}
		}
		if end < len(text) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			if isMentionWordRune(next) {
				continue
			}
		}
		spans = append(spans, [2]int{start, end})
	}
	return spans
}

func mentionsToken(text, token string) bool {
	return len(mentionSpans(text, token)) > 0
}

// rewriteMentions replaces each whole-word occurrence of a token with its handle.
func rewriteMentions(text string, handles map[string]string) string {
	type edit struct {
		start, end int
		handle     string
	}
	var edits []edit
	for token, handle := range handles {
		for _, span := range mentionSpans(text, token) {
			edits = append(edits, edit{span[0], span[1], handle})
		}
	}
	sort.Slice(edits, func(left, right int) bool { return edits[left].start < edits[right].start })
	var builder strings.Builder
	last := 0
	for _, item := range edits {
		if item.start < last {
			continue
		}
		builder.WriteString(text[last:item.start])
		builder.WriteString(item.handle)
		last = item.end
	}
	builder.WriteString(text[last:])
	return builder.String()
}

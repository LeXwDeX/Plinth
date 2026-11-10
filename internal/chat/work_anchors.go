package chat

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The style anchor of the work ledger (#248). The session's existing style
// lock is the style anchor: its text and reference pictures are read as the
// frozen session setting and injected verbatim into the system prompt every
// turn, outside context compaction. The style lock is not copied into another
// store, and a generation turn's one-turn exemption (StyleLockSkipped) changes
// only what the generator sends, never this recorded fact.
//
// Identity anchors (a separately stored identity description and its
// references) are not persisted here: the current Schema has no session
// setting that could carry that text without a new column or migration, and
// #248's delivery reserves the migration for #247. #250 owns identity-anchor
// persistence and its confirmation flow; the Model3D/2D craft prompts keep
// asking the Agent to reuse the identity wording verbatim in the meantime.

// workAnchorsSummary renders the anchor block for the owner's session. It
// returns "" when the style lock is disabled or empty, or when the Schema
// declares no anchor block.
func (service *Service) workAnchorsSummary(ctx context.Context, ownerID, sessionID string) string {
	anchors := service.workLedger.Anchors
	if sessionID == "" || strings.TrimSpace(anchors.ContextTemplate) == "" {
		return ""
	}
	lock, found, err := service.repository.StyleLock(ctx, ownerID, sessionID)
	if err != nil || !found {
		return ""
	}
	text := strings.TrimSpace(lock.Text)
	if !lock.Enabled || (text == "" && len(lock.References) == 0) {
		return ""
	}
	if anchors.MaximumTextCharacters > 0 && utf8.RuneCountInString(text) > anchors.MaximumTextCharacters {
		text = truncateRunes(text, anchors.MaximumTextCharacters)
	}
	references := service.workAnchorReferences(ctx, ownerID, sessionID, lock.References, anchors.ReferenceTemplate)
	entry := strings.NewReplacer(
		"{label}", anchors.StyleLabel,
		"{text}", withoutMarkers(text, anchors.OpenMarker, anchors.CloseMarker),
		"{references}", references,
	).Replace(anchors.EntryTemplate)
	return strings.NewReplacer(
		"{open}", anchors.OpenMarker,
		"{anchors}", entry,
		"{close}", anchors.CloseMarker,
	).Replace(anchors.ContextTemplate)
}

// workAnchorReferences renders the style lock's reference pictures by the
// handle the session already gave each one. An asset ID is never projected; a
// reference the history has not shown stays a count.
func (service *Service) workAnchorReferences(
	ctx context.Context,
	ownerID, sessionID string,
	references []string,
	template string,
) string {
	if len(references) == 0 || strings.TrimSpace(template) == "" {
		return ""
	}
	handles := make([]string, 0, len(references))
	if len(references) > 0 {
		if history, err := service.repository.ListMessages(ctx, ownerID, sessionID); err == nil {
			byAsset := make(map[string]string)
			for _, message := range history {
				for _, attachment := range message.Attachments {
					if attachment.Handle != "" && attachment.AssetID != "" {
						byAsset[attachment.AssetID] = attachment.Handle
					}
				}
			}
			seen := make(map[string]bool, len(references))
			for _, reference := range references {
				handle := byAsset[reference]
				if handle == "" || seen[handle] {
					continue
				}
				seen[handle] = true
				handles = append(handles, handle)
			}
		}
	}
	rendered := strings.Join(handles, "、")
	if rendered == "" {
		rendered = fmt.Sprintf("%d 张（会话中尚未出现句柄）", len(references))
	}
	return strings.ReplaceAll(template, "{handles}", rendered)
}

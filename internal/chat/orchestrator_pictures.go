package chat

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/visionview"
)

// earlierCalls are the calls of the reply that come before the call with the
// given ID, in order. A call that is not found has every stored call before it.
func earlierCalls(calls []ToolCall, id string) []ToolCall {
	for index, call := range calls {
		if call.ID == id {
			return append([]ToolCall(nil), calls[:index]...)
		}
	}
	return append([]ToolCall(nil), calls...)
}

// picturesWithHandles names each tool picture by the handle its attachment
// received. imagesBefore is how many pictures the reply had before the call
// appended this result's images; the call's pictures occupy the following
// slots in order. A picture without an attachment keeps an empty handle.
func picturesWithHandles(message Message, imagesBefore int, pictures []ToolPicture) []ToolPicture {
	if len(pictures) == 0 {
		return nil
	}
	named := append([]ToolPicture(nil), pictures...)
	for index := range named {
		if named[index].Handle != "" {
			continue
		}
		if attachment, found := AttachmentForSlot(message, imagesBefore+index+1); found {
			named[index].Handle = attachment.Handle
		}
	}
	return named
}

// attachToolPictures adds the pictures a tool asked to show (#279) to its
// reply: the tool text first, then each picture followed by its label. Every
// picture is read for the turn's owner; one that cannot be read is named as
// unreadable in the text, and none is shown when the Agent does not read
// images. followUpRequest moves the pictures out of the tool reply.
//
// Each picture's Purpose selects its vision role (#247): a role's places,
// detail and declared token cost bound the group. When a group is larger than
// its places, the pictures are composed into one labelled contact sheet; if
// that fails they fall back to evenly spaced pictures. The shared spend bounds
// the whole request (base pictures and every tool round) and dropped handles
// are named in the overflow note.
func (orchestrator *Orchestrator) attachToolPictures(
	ctx context.Context,
	ownerID string,
	reply *agents.Message,
	pictures []ToolPicture,
	budget visionBudget,
	spend *visionSpend,
) {
	if len(pictures) == 0 {
		return
	}
	texts := orchestrator.schema.Context
	if !budget.Enabled {
		reply.Content += texts.HistoryImageVisionOffMessage
		return
	}
	var parts []agents.ContentPart
	var dropped []string
	for _, group := range groupToolPictures(pictures) {
		detail, tokenCost, quota := orchestrator.toolPicturePolicy(budget, group.purpose)
		groupPictures := group.pictures
		if quota >= 0 && len(groupPictures) > quota {
			if budget.Vision != nil && quota > 0 && budget.Contact.Enabled {
				if sheet, ok := orchestrator.contactSheet(ctx, ownerID, group.pictures, budget, spend); ok {
					parts = append(parts, sheet.parts...)
					reply.FreshPictures = reply.FreshPictures || freshPurpose(group.purpose)
					continue
				}
			}
			if quota <= 0 {
				for _, picture := range groupPictures {
					dropped = append(dropped, orchestrator.toolPictureHandle(picture))
				}
				continue
			}
			// Composition failed (or is off): keep the evenly spaced pictures
			// the place count allows and name the rest.
			groupPictures = evenlySpacedPictures(groupPictures, quota)
			dropped = append(dropped, orchestrator.droppedToolPictures(group.pictures, groupPictures)...)
		}
		for _, picture := range groupPictures {
			if picture.Fresh {
				reply.FreshPictures = true
			}
			pictureDetail := detail
			if picture.Detail != "" {
				pictureDetail = picture.Detail
			}
			pictureLabel, pictureUnreadable := orchestrator.toolPictureTexts(group.purpose)
			if picture.Label != "" {
				pictureLabel = picture.Label
			}
			pictureURL := picture.URL
			if pictureURL != "" && !visionSafeImage(pictureURL) {
				inlined, ok := "", false
				if orchestrator.visionInline != nil {
					inlined, ok = orchestrator.visionInline(ctx, generation.OwnerID(ownerID), pictureURL)
				}
				pictureURL = ""
				if ok {
					pictureURL = inlined
				}
			}
			if pictureURL == "" {
				reply.Content += strings.ReplaceAll(pictureUnreadable, "{handle}", orchestrator.toolPictureHandle(picture))
				continue
			}
			if budget.Vision != nil && spend != nil && !budget.canSpend(*spend, tokenCost, int64(len(pictureURL))) {
				dropped = append(dropped, orchestrator.toolPictureHandle(picture))
				continue
			}
			if spend != nil {
				spend.Images++
				spend.Tokens += tokenCost
				spend.Bytes += int64(len(pictureURL))
			}
			parts = append(parts,
				agents.ContentPart{Kind: texts.ImagePartKind, URL: pictureURL, Detail: pictureDetail},
				agents.ContentPart{Kind: texts.TextPartKind, Text: strings.ReplaceAll(pictureLabel, "{handle}", orchestrator.toolPictureHandle(picture))},
			)
		}
	}
	if note := budget.overflowText(dropped); note != "" {
		reply.Content += note
	}
	if len(parts) > 0 {
		reply.Parts = append([]agents.ContentPart{{Kind: texts.TextPartKind, Text: reply.Content}}, parts...)
	}
}

// droppedToolPictures names the pictures of all that picked does not keep.
func (orchestrator *Orchestrator) droppedToolPictures(all, picked []ToolPicture) []string {
	remaining := map[string]int{}
	for _, picture := range picked {
		remaining[toolPictureKey(picture)]++
	}
	var dropped []string
	for _, picture := range all {
		key := toolPictureKey(picture)
		if remaining[key] > 0 {
			remaining[key]--
			continue
		}
		dropped = append(dropped, orchestrator.toolPictureHandle(picture))
	}
	return dropped
}

func toolPictureKey(picture ToolPicture) string {
	return picture.Handle + "\x00" + picture.URL
}

type toolPictureGroup struct {
	purpose  string
	pictures []ToolPicture
}

// groupToolPictures keeps the call's picture order while grouping by purpose.
func groupToolPictures(pictures []ToolPicture) []toolPictureGroup {
	var groups []toolPictureGroup
	byPurpose := map[string]int{}
	for _, picture := range pictures {
		purpose := picture.Purpose
		if purpose == "" {
			// A Fresh picture without a declared purpose is this turn's result
			// (the historical behavior); anything else is a history lookup.
			if picture.Fresh {
				purpose = ToolPictureResult
			} else {
				purpose = ToolPictureHistory
			}
		}
		index, found := byPurpose[purpose]
		if !found {
			index = len(groups)
			byPurpose[purpose] = index
			groups = append(groups, toolPictureGroup{purpose: purpose})
		}
		groups[index].pictures = append(groups[index].pictures, picture)
	}
	return groups
}

func freshPurpose(purpose string) bool {
	return purpose == ToolPictureResult || purpose == ToolPictureRender
}

// toolPicturePolicy returns the detail, declared token cost and place quota of
// one purpose. A negative quota means the place count does not bound the group
// (the legacy Schema, or no role declared for the purpose).
func (orchestrator *Orchestrator) toolPicturePolicy(budget visionBudget, purpose string) (string, int, int) {
	if budget.Vision == nil {
		// The legacy fallback keeps context.max_result_pictures as the hard cap
		// of one result's candidates (#245): the caller still selects evenly
		// spaced pictures when there are more, exactly like the pre-#247
		// generation executor did.
		if purpose == ToolPictureResult {
			quota := orchestrator.schema.Context.MaxResultPictures
			if quota <= 0 {
				quota = 4
			}
			return orchestrator.schema.Context.ImageDetail, 0, quota
		}
		return orchestrator.schema.Context.ImageDetail, 0, -1
	}
	switch purpose {
	case ToolPictureResult:
		role, found := budget.roleFor("generated")
		quota, detail, cost := role.MaximumImages, role.Detail, role.TokenCost
		if !found {
			quota, detail, cost = 0, budget.DefaultDetail, 0
		}
		if maximum := orchestrator.schema.Context.MaxResultPictures; maximum > 0 {
			quota = min(quota, maximum)
		}
		return detail, cost, quota
	case ToolPictureRender:
		role, found := budget.roleFor("render")
		if !found {
			return budget.DefaultDetail, 0, 0
		}
		return role.Detail, role.TokenCost, role.MaximumImages
	case ToolPictureCrop:
		return budget.Crop.Detail, budget.Crop.TokenCost, 2
	default:
		role, found := budget.roleFor("history")
		if !found {
			return budget.DefaultDetail, 0, 0
		}
		return role.Detail, role.TokenCost, role.MaximumImages
	}
}

// toolPictureTexts is the label and unreadable text of a purpose. Results and
// renders of this turn say so; a history lookup uses the history copies.
func (orchestrator *Orchestrator) toolPictureTexts(purpose string) (string, string) {
	texts := orchestrator.schema.Context
	label, unreadable := texts.HistoryImageViewLabelTemplate, texts.HistoryImageUnreadableTemplate
	if freshPurpose(purpose) {
		if texts.ResultImageViewLabelTemplate != "" {
			label = texts.ResultImageViewLabelTemplate
		}
		if texts.ResultImageUnreadableTemplate != "" {
			unreadable = texts.ResultImageUnreadableTemplate
		}
	}
	return label, unreadable
}

// toolPictureHandle names a picture the Agent reads: its handle, or the
// Schema's unnamed-picture label for old data without a structured attachment.
// The label is Schema text, never a hardcoded word; the legacy fallback keeps
// the empty name that the pre-#247 request produced.
func (orchestrator *Orchestrator) toolPictureHandle(picture ToolPicture) string {
	if picture.Handle != "" {
		return picture.Handle
	}
	if vision := orchestrator.schema.Context.Vision; vision != nil {
		return vision.UnnamedPictureLabel
	}
	return ""
}

// toolPictureSheet is the composed contact sheet's parts.
type toolPictureSheet struct {
	parts []agents.ContentPart
}

// contactSheet composes a group's pictures into one labelled JPEG. ok is false
// when the sheet cannot be made (no Schema, no decodable picture, composition
// over the byte bound); the caller then falls back to evenly spaced pictures.
// On success the whole group's spend is charged as one picture at the contact
// sheet's declared cost.
func (orchestrator *Orchestrator) contactSheet(
	ctx context.Context,
	ownerID string,
	pictures []ToolPicture,
	budget visionBudget,
	spend *visionSpend,
) (toolPictureSheet, bool) {
	sheet := budget.Contact
	if !sheet.Enabled {
		return toolPictureSheet{}, false
	}
	if sheet.MaximumTiles > 0 && len(pictures) > sheet.MaximumTiles {
		pictures = evenlySpacedPictures(pictures, sheet.MaximumTiles)
	}
	var tiles []visionview.Tile
	var handles []string
	for index, picture := range pictures {
		url := picture.URL
		if !visionSafeImage(url) {
			if orchestrator.visionInline == nil {
				continue
			}
			inlined, ok := orchestrator.visionInline(ctx, generation.OwnerID(ownerID), url)
			if !ok {
				continue
			}
			url = inlined
		}
		decoded, err := visionview.DecodeDataURI(url)
		if err != nil {
			continue
		}
		label := sheet.LabelTemplate
		label = strings.ReplaceAll(label, "{handle}", orchestrator.toolPictureHandle(picture))
		label = strings.ReplaceAll(label, "{index}", itoa(index+1))
		tiles = append(tiles, visionview.Tile{Picture: decoded, Label: label})
		if picture.Handle != "" {
			handles = append(handles, picture.Handle)
		}
	}
	if len(tiles) < 2 {
		return toolPictureSheet{}, false
	}
	data, err := visionview.ComposeSheet(tiles, visionview.SheetOptions{
		Columns: sheet.Columns, TilePixels: sheet.TilePixels, LabelHeight: sheet.LabelHeight,
		MaximumTiles: sheet.MaximumTiles, MaximumBytes: sheet.MaximumBytes,
		JPEGQualities: sheet.JPEGQualities,
	})
	if err != nil {
		orchestrator.logger.Warn("联系表合成失败，改为均匀抽样", "error", err)
		return toolPictureSheet{}, false
	}
	dataURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
	if spend != nil && !budget.canSpend(*spend, sheet.TokenCost, int64(len(dataURI))) {
		return toolPictureSheet{}, false
	}
	if spend != nil {
		spend.Images++
		spend.Tokens += sheet.TokenCost
		spend.Bytes += int64(len(dataURI))
	}
	var parts []agents.ContentPart
	if strings.TrimSpace(sheet.Intro) != "" {
		parts = append(parts, agents.ContentPart{
			Kind: orchestrator.schema.Context.TextPartKind, Text: sheet.Intro,
		})
	}
	parts = append(parts,
		agents.ContentPart{
			Kind: orchestrator.schema.Context.ImagePartKind, URL: dataURI, Detail: sheet.Detail,
		},
		agents.ContentPart{
			Kind: orchestrator.schema.Context.TextPartKind, Text: strings.Join(handles, " "),
		},
	)
	return toolPictureSheet{parts: parts}, true
}

// evenlySpacedPictures picks limit pictures keeping the first and the last.
func evenlySpacedPictures(pictures []ToolPicture, limit int) []ToolPicture {
	if limit <= 0 || len(pictures) <= limit {
		return pictures
	}
	if limit == 1 {
		return []ToolPicture{pictures[len(pictures)/2]}
	}
	picked := make([]ToolPicture, 0, limit)
	for index := 0; index < limit; index++ {
		at := index * (len(pictures) - 1) / (limit - 1)
		picked = append(picked, pictures[at])
	}
	return picked
}

// withToolPictures returns the tool replies as plain text and, when a reply
// carried pictures, one message after them that shows all of them. Picture
// parts are not valid in tool replies of OpenAI-compatible protocols, so they
// ride in a user-role message that says it only carries the looked-up images.
func (orchestrator *Orchestrator) withToolPictures(toolResults []agents.Message) []agents.Message {
	result := make([]agents.Message, 0, len(toolResults)+1)
	var pictures []agents.ContentPart
	fresh := false
	for _, reply := range toolResults {
		if reply.Role == orchestrator.schema.Roles.Tool && len(reply.Parts) > 1 {
			pictures = append(pictures, reply.Parts[1:]...)
			fresh = fresh || reply.FreshPictures
			reply.Parts = nil
		}
		result = append(result, reply)
	}
	if len(pictures) > 0 {
		texts := orchestrator.schema.Context
		intro := texts.HistoryImageViewIntro
		if fresh && texts.ResultImageViewIntro != "" {
			intro = texts.ResultImageViewIntro
		}
		result = append(result, agents.Message{
			Role:         orchestrator.schema.Roles.User,
			Parts:        append([]agents.ContentPart{{Kind: texts.TextPartKind, Text: intro}}, pictures...),
			PicturesOnly: true,
		})
	}
	return result
}

package chat

import (
	"context"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/generation"
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
func (orchestrator *Orchestrator) attachToolPictures(
	ctx context.Context,
	ownerID string,
	reply *agents.Message,
	pictures []ToolPicture,
) {
	if len(pictures) == 0 {
		return
	}
	texts := orchestrator.schema.Context
	if !orchestrator.schema.Agent.Vision {
		reply.Content += texts.HistoryImageVisionOffMessage
		return
	}
	label := texts.HistoryImageViewLabelTemplate
	unreadable := texts.HistoryImageUnreadableTemplate
	var parts []agents.ContentPart
	for _, picture := range pictures {
		if picture.Fresh {
			reply.FreshPictures = true
		}
		pictureLabel, pictureUnreadable := label, unreadable
		if picture.Fresh {
			if texts.ResultImageViewLabelTemplate != "" {
				pictureLabel = texts.ResultImageViewLabelTemplate
			}
			if texts.ResultImageUnreadableTemplate != "" {
				pictureUnreadable = texts.ResultImageUnreadableTemplate
			}
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
			reply.Content += strings.ReplaceAll(pictureUnreadable, "{handle}", picture.Handle)
			continue
		}
		parts = append(parts,
			agents.ContentPart{Kind: texts.ImagePartKind, URL: pictureURL, Detail: texts.ImageDetail},
			agents.ContentPart{Kind: texts.TextPartKind, Text: strings.ReplaceAll(pictureLabel, "{handle}", picture.Handle)},
		)
	}
	if len(parts) > 0 {
		reply.Parts = append([]agents.ContentPart{{Kind: texts.TextPartKind, Text: reply.Content}}, parts...)
	}
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

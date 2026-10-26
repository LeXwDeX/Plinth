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
	var parts []agents.ContentPart
	for _, picture := range pictures {
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
			reply.Content += strings.ReplaceAll(texts.HistoryImageUnreadableTemplate, "{handle}", picture.Handle)
			continue
		}
		parts = append(parts,
			agents.ContentPart{Kind: texts.ImagePartKind, URL: pictureURL, Detail: texts.ImageDetail},
			agents.ContentPart{Kind: texts.TextPartKind, Text: strings.ReplaceAll(texts.HistoryImageViewLabelTemplate, "{handle}", picture.Handle)},
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
	for _, reply := range toolResults {
		if reply.Role == orchestrator.schema.Roles.Tool && len(reply.Parts) > 1 {
			pictures = append(pictures, reply.Parts[1:]...)
			reply.Parts = nil
		}
		result = append(result, reply)
	}
	if len(pictures) > 0 {
		texts := orchestrator.schema.Context
		result = append(result, agents.Message{
			Role:  orchestrator.schema.Roles.User,
			Parts: append([]agents.ContentPart{{Kind: texts.TextPartKind, Text: texts.HistoryImageViewIntro}}, pictures...),
		})
	}
	return result
}

package chat

// ProjectMessage is the stable semantic projection of one message. A message is
// pending while it is still the placeholder, and also while its turn has not
// ended (Message.TurnActive, #403): the Orchestrator stores the real text and
// tool calls after the first round, so an active turn's reply keeps its text
// and is still pending. Only the placeholder itself is replaced by empty text.
func ProjectMessage(
	policy ProjectionPolicy,
	message Message,
) FrontendMessage {
	placeholder := message.Content == policy.PendingContent
	pending := placeholder || message.TurnActive
	content := message.Content
	if placeholder {
		content = ""
	}
	images := make([]Image, 0, len(message.ImageURLs))
	for _, url := range message.ImageURLs {
		images = append(images, Image{URL: url})
	}
	pendingTypes := make([]string, 0, len(message.ToolCalls))
	if pending {
		for _, call := range message.ToolCalls {
			if capability := policy.ToolCapabilities[call.Name]; capability != "" {
				pendingTypes = append(pendingTypes, capability)
			}
		}
	}
	return FrontendMessage{
		ID:                    message.ID,
		Role:                  message.Role,
		Content:               content,
		Images:                images,
		IsPending:             pending,
		PendingTaskTypes:      pendingTypes,
		CreatedAtMilliseconds: message.CreatedAt.UnixMilli(),
	}
}

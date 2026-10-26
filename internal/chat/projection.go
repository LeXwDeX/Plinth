package chat

func ProjectMessage(
	policy ProjectionPolicy,
	message Message,
) FrontendMessage {
	pending := message.Content == policy.PendingContent
	content := message.Content
	if pending {
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

package resolve

import (
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// conversationParameters lists an Offering's conversation inputs in
// declaration order: the reference images the Agent takes from the chat
// (schema.ConversationReferenceImage, #187 D) and passes by handle.
func conversationParameters(offering generation.Offering) []string {
	names := []string{}
	for _, parameter := range offering.Parameters {
		if schema.ConversationReferenceImage(parameter) {
			names = append(names, parameter.Name)
		}
	}
	return names
}

package resolve

import (
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// ConversationParameter reports whether an Offering parameter takes its value
// from the conversation in a Harness turn (#187 D): a reference image the
// person attaches to a message or names by its handle. Such a picture belongs
// to one request, not to the saved settings, so it is never fixed or made
// fillable there; the Agent passes the handle and the executor resolves it to
// an owned input asset before PrepareInput. Sources, fixed and hidden
// parameters are never conversation inputs.
func ConversationParameter(parameter generation.Parameter) bool {
	if _, source := schema.SourceParameterRole(parameter); source || parameter.Fixed {
		return false
	}
	if parameter.Presentation != nil && parameter.Presentation.Surface == schema.SurfaceHidden {
		return false
	}
	return parameter.Semantic == schema.SemanticReferenceImage && (parameter.Type == "asset" || parameter.Type == "asset-array")
}

// conversationParameters lists an Offering's conversation inputs in
// declaration order.
func conversationParameters(offering generation.Offering) []string {
	names := []string{}
	for _, parameter := range offering.Parameters {
		if ConversationParameter(parameter) {
			names = append(names, parameter.Name)
		}
	}
	return names
}

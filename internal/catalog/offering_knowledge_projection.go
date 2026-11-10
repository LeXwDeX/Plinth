package catalog

// Rendering an Offering's knowledge for the Agent (#249).
import (
	"fmt"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// OfferingKnowledgeModelLabel is the name an Offering's model is shown by.
func OfferingKnowledgeModelLabel(offering schemacontract.Offering) string {
	if offering.ModelName != "" {
		return offering.ModelName
	}
	return offering.ModelID
}

// RenderPromptGuidance turns an Offering's Prompt declaration into the
// Agent-facing guidance sentence. Undeclared facts use UnknownText; a nil
// knowledge or prompt never invents a value. An empty template or knowledge
// yields "".
func (schema ChatOfferingKnowledgeSchema) RenderPromptGuidance(
	model string,
	knowledge *schemacontract.OfferingKnowledge,
) string {
	if strings.TrimSpace(schema.GuidanceTemplate) == "" || knowledge == nil || knowledge.Prompt == nil {
		return ""
	}
	prompt := knowledge.Prompt
	unknown := strings.TrimSpace(schema.UnknownText)
	if unknown == "" {
		unknown = "unknown"
	}
	text := func(values map[string]string, key, fallback string) string {
		if key == "" {
			key = "unknown"
		}
		if value, exists := values[key]; exists {
			return value
		}
		return fallback
	}
	language := text(schema.LanguageTexts, prompt.Language, unknown)
	form := text(schema.FormTexts, prompt.Form, unknown)
	length := unknown
	if prompt.WordsMax > 0 {
		unit := prompt.Unit
		if unit == "" {
			unit = "words"
		}
		if prompt.WordsMin > 0 {
			length = fmt.Sprintf("%d-%d %s", prompt.WordsMin, prompt.WordsMax, unit)
		} else {
			length = fmt.Sprintf("up to %d %s", prompt.WordsMax, unit)
		}
	}
	booleanKey := func(value *bool) string {
		if value == nil {
			return "unknown"
		}
		if *value {
			return "true"
		}
		return "false"
	}
	negative := text(schema.NegativePromptTexts, booleanKey(prompt.NegativePrompt), unknown)
	reads := text(schema.ReadsTextTexts, booleanKey(prompt.ReadsText), unknown)
	referenceUse := unknown
	if len(knowledge.ReferenceUse) > 0 {
		parts := make([]string, 0, len(knowledge.ReferenceUse))
		for _, use := range knowledge.ReferenceUse {
			parts = append(parts, text(schema.ReferenceUseTexts, use, unknown))
		}
		separator := schema.LayerSeparator
		if separator == "" {
			separator = ", "
		}
		referenceUse = strings.Join(parts, separator)
	}
	return strings.NewReplacer(
		"{model}", model,
		"{language}", language,
		"{form}", form,
		"{length}", length,
		"{negative_prompt}", negative,
		"{reads_text}", reads,
		"{reference_use}", referenceUse,
	).Replace(schema.GuidanceTemplate)
}

// RenderNoTextGuidance is the Schema's statement that an Offering does not
// read a text prompt; "" when the template or knowledge is absent.
func (schema ChatOfferingKnowledgeSchema) RenderNoTextGuidance(
	model string,
	knowledge *schemacontract.OfferingKnowledge,
) string {
	if strings.TrimSpace(schema.NoTextTemplate) == "" || knowledge == nil ||
		knowledge.Prompt == nil || knowledge.Prompt.ReadsText == nil || *knowledge.Prompt.ReadsText {
		return ""
	}
	return strings.ReplaceAll(schema.NoTextTemplate, "{model}", model)
}

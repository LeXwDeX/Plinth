package generationtool

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// processingStyleReference writes the session's style picture into a
// retexture step (#250). Only a reference-capability-declared retexture may
// carry it, and only when the step's own reference parameter is free: a
// single-picture parameter already holding the person's picture is never
// replaced. The style text is not written into 3D prompts; the production
// brief keeps that role. A skip returns the Schema's note for the result.
func (executor *Executor) processingStyleReference(
	ctx context.Context,
	command chat.ToolCommand,
	capability generation.Capability,
	offering generation.Offering,
	input map[string]any,
) (string, error) {
	if capability != generation.Retexture || !executor.styleExtendsTo(capability) {
		return "", nil
	}
	urls, _, err := executor.styleReferenceURLs(command)
	if err != nil {
		return "", err
	}
	if len(urls) == 0 {
		return "", nil
	}
	parameter, declared := semanticParameter(offering, generation.SemanticReferenceImage)
	if !declared || parameter.Type != "asset" {
		return executor.styleNote(command, executor.chat.StyleLock.UnsupportedNote), nil
	}
	if current, exists := input[parameter.Name]; exists && current != nil && current != "" {
		return executor.styleNote(command, executor.chat.StyleLock.SkipNote), nil
	}
	// Some services take either a prompt or a reference picture for one style
	// input (Meshy retexture). The Schema's input rule already groups the
	// alternatives; when the step filled another member, the style picture
	// would make the frozen input invalid, so it is skipped and reported.
	if executor.processingStyleInputOccupied(offering, parameter.Name, input) {
		return executor.styleNote(command, executor.chat.StyleLock.SkipNote), nil
	}
	assets, resolveErr := executor.references.Resolve(ctx, commandReferenceScope(command), urls[:1])
	if resolveErr != nil {
		return "", resolveErr
	}
	if len(assets) == 0 {
		return executor.styleNote(command, executor.chat.StyleLock.UnsupportedNote), nil
	}
	input[parameter.Name] = assets[0]
	return "", nil
}

// processingStyleInputOccupied reports whether another parameter of the same
// declared input rule already fills the style input. The rule is the Offering's
// own contract (for example "prompt or style_asset, exactly one"), so no
// service name is hard-coded here.
func (executor *Executor) processingStyleInputOccupied(offering generation.Offering, referenceParameter string, input map[string]any) bool {
	for _, rule := range offering.InputRules {
		named := false
		for _, name := range rule.Parameters {
			if name == referenceParameter {
				named = true
				break
			}
		}
		if !named {
			continue
		}
		for _, name := range rule.Parameters {
			if name == referenceParameter {
				continue
			}
			if value, exists := input[name]; exists && processingInputPresent(value) {
				return true
			}
		}
	}
	return false
}

func processingInputPresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []string:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}

func (executor *Executor) styleExtendsTo(capability generation.Capability) bool {
	declared := executor.chat.StyleLock.ReferenceCapabilities
	if len(declared) == 0 {
		return capability == generation.TextToImage || capability == generation.ImageToImage
	}
	for _, item := range declared {
		if item == capability {
			return true
		}
	}
	return false
}

func (executor *Executor) styleNote(command chat.ToolCommand, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return executor.localized(command, text)
}

// attachProcessingReferenceNote adds the style-reference skip note to the
// step's result content. The note uses the tool's declared warning field, so
// the Agent reads the same status report as every other warning.
func (executor *Executor) attachProcessingReferenceNote(tool catalog.ChatToolSchema, note string, result chat.ToolResult) chat.ToolResult {
	if note == "" {
		return result
	}
	field := tool.ResultFields.Warning
	if field == "" || result.AgentContent == "" {
		return result
	}
	var content map[string]any
	if json.Unmarshal([]byte(result.AgentContent), &content) != nil || content == nil {
		return result
	}
	if existing, _ := content[field].(string); strings.TrimSpace(existing) != "" {
		content[field] = existing + " " + note
	} else {
		content[field] = note
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return result
	}
	result.AgentContent = string(encoded)
	return result
}

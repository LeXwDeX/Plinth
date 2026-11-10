package promptenhance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type OfferingCatalog interface {
	List(generation.Capability) []generation.Offering
}

type Config struct {
	Offerings OfferingCatalog
	Schema    catalog.ChatSchema
	Model     string
	Agent     agents.Adapter
}

type Request struct {
	Prompt     string
	Capability generation.Capability
	ModelID    string
}

type Result struct {
	Prompt         string
	NegativePrompt string
}

type Service struct {
	offerings OfferingCatalog
	schema    catalog.ChatSchema
	model     string
	agent     agents.Adapter
}

func New(config Config) (*Service, error) {
	if config.Offerings == nil ||
		config.Agent == nil ||
		strings.TrimSpace(config.Model) == "" && !agents.IsNotConfigured(config.Agent) {
		return nil, errors.New("prompt enhancer: incomplete dependencies")
	}
	promptEnhance := config.Schema.API.PromptEnhance
	if _, exists := promptEnhance.Profiles[promptEnhance.DefaultProfile]; !exists {
		return nil, errors.New("prompt enhancer: invalid default profile")
	}
	return &Service{
		offerings: config.Offerings,
		schema:    config.Schema,
		model:     config.Model,
		agent:     config.Agent,
	}, nil
}

func (service *Service) Enhance(
	ctx context.Context,
	request Request,
) (Result, error) {
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		return Result{}, errors.New("prompt enhancer: prompt is empty")
	}
	profile, err := service.profile(request.Capability)
	if err != nil {
		return Result{}, err
	}
	maxLength := service.promptMaxLength(
		request.Capability,
		request.ModelID,
	)
	// #249: the enhancer and the Agent share the same Offering self-description.
	// An Offering that declares its prompt craft yields a different system
	// prompt; a historical Catalog or an unknown model keeps the generic rules.
	offering, hasOffering := service.offering(request.Capability, request.ModelID)
	profile, systemPrompt := buildSystemPrompt(profile, maxLength, service.schema.Agent.OfferingKnowledge, offering, hasOffering)
	completion, err := service.agent.Complete(ctx, agents.Request{
		Model:       service.model,
		Temperature: service.schema.API.PromptEnhance.Temperature,
		Messages: []agents.Message{
			{
				Role:    service.schema.Roles.System,
				Content: systemPrompt,
			},
			{
				Role:    service.schema.Roles.User,
				Content: request.Prompt,
			},
		},
	})
	if err != nil {
		return Result{}, fmt.Errorf("prompt enhancer completion: %w", err)
	}
	content := strings.TrimSpace(completion.Content)
	if content == "" {
		return Result{}, errors.New("prompt enhancer: completion is empty")
	}
	// B16：上游 LLM 常把 JSON 包进 Markdown 代码围栏，剥掉后再解析，
	// 围栏原样进入 prompt 会直接污染生成输入。
	content = stripCodeFence(content)
	result := parseCompletion(content, profile)
	result.Prompt = truncateRunes(result.Prompt, maxLength)
	return result, nil
}

func (service *Service) profile(
	capability generation.Capability,
) (catalog.ChatPromptEnhanceProfileSchema, error) {
	promptEnhance := service.schema.API.PromptEnhance
	profileID := promptEnhance.DefaultProfile
	if configured, exists := promptEnhance.CapabilityProfiles[capability]; exists {
		profileID = configured
	}
	profile, exists := promptEnhance.Profiles[profileID]
	if !exists {
		return catalog.ChatPromptEnhanceProfileSchema{}, fmt.Errorf(
			"prompt enhancer: unknown profile %q",
			profileID,
		)
	}
	return profile, nil
}

func (service *Service) promptMaxLength(
	capability generation.Capability,
	modelID string,
) int {
	if capability == "" || strings.TrimSpace(modelID) == "" {
		return 0
	}
	for _, offering := range service.offerings.List(capability) {
		if offering.ModelID != modelID {
			continue
		}
		for _, parameter := range offering.Parameters {
			if parameter.Semantic == generation.SemanticPrompt {
				return parameter.MaxLength
			}
		}
	}
	return 0
}

func buildSystemPrompt(
	profile catalog.ChatPromptEnhanceProfileSchema,
	maxLength int,
	knowledge catalog.ChatOfferingKnowledgeSchema,
	offering generation.Offering,
	hasOffering bool,
) (catalog.ChatPromptEnhanceProfileSchema, string) {
	lengthRule := profile.UnboundedLengthRule
	if maxLength > 0 {
		lengthRule = strings.ReplaceAll(
			profile.BoundedLengthRule,
			profile.MaxLengthPlaceholder,
			fmt.Sprint(maxLength),
		)
	}
	prompt := strings.ReplaceAll(
		profile.SystemPrompt,
		profile.LengthRulePlaceholder,
		lengthRule,
	)
	if !hasOffering || offering.Knowledge == nil || offering.Knowledge.Prompt == nil {
		return profile, prompt
	}
	declared := offering.Knowledge.Prompt
	if text := knowledge.RenderPromptGuidance(catalog.OfferingKnowledgeModelLabel(offering), offering.Knowledge); text != "" {
		prompt += "\n\n" + text
	}
	if declared.ReadsText != nil && !*declared.ReadsText {
		prompt += "\nThe selected service does not read a text prompt; say so in the result and do not promise that the text affects the result."
	}
	if declared.NegativePrompt != nil && !*declared.NegativePrompt {
		// The service has no negative-prompt input: do not return one.
		profile.NegativePromptField = ""
		profile.DefaultNegativePrompt = ""
		prompt += "\nThe selected service accepts no negative prompt; output only the prompt field."
	}
	return profile, prompt
}

// offering finds the Offering an enhancement targets. A request without a
// model id keeps the generic rules.
func (service *Service) offering(capability generation.Capability, modelID string) (generation.Offering, bool) {
	if capability == "" || strings.TrimSpace(modelID) == "" {
		return generation.Offering{}, false
	}
	for _, offering := range service.offerings.List(capability) {
		if offering.ModelID == modelID {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

func parseCompletion(
	content string,
	profile catalog.ChatPromptEnhanceProfileSchema,
) Result {
	result := Result{
		Prompt:         content,
		NegativePrompt: profile.DefaultNegativePrompt,
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &parsed) != nil {
		return result
	}
	var prompt string
	if json.Unmarshal(parsed[profile.PromptField], &prompt) == nil &&
		strings.TrimSpace(prompt) != "" {
		result.Prompt = prompt
	}
	if profile.NegativePromptField == "" {
		return result
	}
	var negativePrompt string
	if json.Unmarshal(
		parsed[profile.NegativePromptField],
		&negativePrompt,
	) == nil && strings.TrimSpace(negativePrompt) != "" {
		result.NegativePrompt = negativePrompt
	}
	return result
}

func truncateRunes(value string, maximum int) string {
	if maximum <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

// stripCodeFence 剥掉整段 Markdown 代码围栏（```json ... ``` 等），
// 只处理首尾成对的围栏；内部文本原样保留。
func stripCodeFence(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	body := content[3:]
	if newline := strings.IndexByte(body, '\n'); newline >= 0 {
		body = body[newline+1:]
	} else {
		return content
	}
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)
	if body == "" {
		return content
	}
	return body
}

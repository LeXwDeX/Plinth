// Package skilldraft drafts a personal Skill from the person's name and
// requirement with one chat model call (#548). The draft is returned, never
// saved: the person reviews it and saves it through the personal Skill API. It
// opens no session, no public Task and no Grant.
package skilldraft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// ErrInvalidRequest is a name or requirement that is empty or too long.
var ErrInvalidRequest = errors.New("skill draft: invalid name or requirement")

// ErrUnusableDraft is a completion that holds no usable draft.
var ErrUnusableDraft = errors.New("skill draft: the model returned no usable draft")

// ModelSource names the chat model a draft runs on: its connection (empty for
// the deployment's own) and the upstream model.
type ModelSource func(context.Context) (connection, model string, err error)

type Config struct {
	Chat  catalog.ChatSchema
	Agent agents.Adapter
	Model ModelSource
	// MaxPromptCharacters bounds the drafted body: the personal Skill limit.
	MaxPromptCharacters int
}

type Service struct {
	chat      catalog.ChatSchema
	agent     agents.Adapter
	model     ModelSource
	maxPrompt int
}

// Request is one draft. Authoring is the prompt in the reader's locale; an
// empty one uses the Schema's. Taken lists the slugs the owner can no longer
// use (system Skills and their own).
type Request struct {
	Name        string
	Requirement string
	Authoring   catalog.ChatSkillAuthoringSchema
	Taken       map[string]bool
}

// Draft is the proposed standard shape. An empty Slug means the model's slug
// was invalid or taken; the Skill then takes one derived from its ID when it
// is saved.
type Draft struct {
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Apply       string `json:"apply"`
}

func New(config Config) (*Service, error) {
	if config.Agent == nil || config.Model == nil {
		return nil, errors.New("skill draft: incomplete dependencies")
	}
	return &Service{chat: config.Chat, agent: config.Agent, model: config.Model, maxPrompt: config.MaxPromptCharacters}, nil
}

// Draft runs one completion and reads the draft from it. Nothing is retried:
// a failure is reported and the person retries or writes the Skill themselves.
func (service *Service) Draft(ctx context.Context, request Request) (Draft, error) {
	creation := service.chat.Skills.Creation
	name := strings.TrimSpace(request.Name)
	requirement := strings.TrimSpace(request.Requirement)
	if name == "" || requirement == "" ||
		utf8.RuneCountInString(name) > creation.MaxNameCharacters ||
		utf8.RuneCountInString(requirement) > creation.MaxRequirementCharacters {
		return Draft{}, ErrInvalidRequest
	}
	authoring := request.Authoring
	if authoring.SystemPrompt == "" || authoring.UserTemplate == "" {
		authoring = service.chat.Skills.Authoring
	}
	connection, model, err := service.model(ctx)
	if err != nil {
		return Draft{}, fmt.Errorf("skill draft model: %w", err)
	}
	profile, _ := service.chat.Agent.Thinking.ProfileFor(model)
	completion, err := service.agent.Complete(ctx, agents.Request{
		Model:      model,
		Connection: connection,
		// A one-shot helper request like prompt enhancement: it takes that
		// kind's level rather than a session's choice.
		ReasoningEffort: profile.EffortForTurn(catalog.ChatTurnEffortPromptEnhance),
		Purpose:         agents.PurposeSkillDraft,
		Messages: []agents.Message{
			{Role: service.chat.Roles.System, Content: authoring.SystemPrompt},
			// The requirement is the person's text: it stays in the user
			// message, inside the template's delimiters, which it cannot close.
			{Role: service.chat.Roles.User, Content: strings.NewReplacer(
				"{name}", undelimited(name), "{requirement}", undelimited(requirement),
			).Replace(authoring.UserTemplate)},
		},
	})
	if err != nil {
		return Draft{}, fmt.Errorf("skill draft completion: %w", err)
	}
	return service.read(completion.Content, request.Taken)
}

// undelimited removes the user template's own delimiters from person text.
func undelimited(text string) string {
	return strings.NewReplacer("<<<", "", ">>>", "").Replace(text)
}

// read takes the first JSON object of the completion and keeps what the
// standard allows: a slug that is valid and free, a one-line description
// without angle brackets, a body within the personal Skill limit and a known
// apply (instruction otherwise).
func (service *Service) read(content string, taken map[string]bool) (Draft, error) {
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return Draft{}, ErrUnusableDraft
	}
	var raw struct {
		Slug        string `json:"slug"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Apply       string `json:"apply"`
	}
	if json.Unmarshal([]byte(content[start:end+1]), &raw) != nil {
		return Draft{}, ErrUnusableDraft
	}
	draft := Draft{
		Slug:   strings.ToLower(strings.TrimSpace(raw.Slug)),
		Prompt: strings.TrimSpace(raw.Prompt),
		Apply:  strings.TrimSpace(raw.Apply),
	}
	if !catalog.ValidSkillSlug(draft.Slug) || taken[draft.Slug] {
		draft.Slug = ""
	}
	description := strings.Join(strings.Fields(strings.NewReplacer("<", "", ">", "").Replace(raw.Description)), " ")
	if utf8.RuneCountInString(description) > catalog.SkillDescriptionMaxCharacters {
		description = string([]rune(description)[:catalog.SkillDescriptionMaxCharacters])
	}
	draft.Description = description
	if service.maxPrompt > 0 && utf8.RuneCountInString(draft.Prompt) > service.maxPrompt {
		draft.Prompt = string([]rune(draft.Prompt)[:service.maxPrompt])
	}
	if draft.Apply != catalog.SkillApplyGenerationPrompt && draft.Apply != catalog.SkillApplyInstruction {
		draft.Apply = catalog.SkillApplyInstruction
	}
	if draft.Description == "" || draft.Prompt == "" {
		return Draft{}, ErrUnusableDraft
	}
	return draft, nil
}

package chat

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The session production brief (#103): the user's purpose and locked choices,
// written once through the brief API or the Agent tool before 3D work starts.
// The Schema declares the fields; the service validates a whole replacement,
// owns the profile table, and closes a purpose card when the purpose is set.

var (
	// ErrProductionBriefUnavailable refuses a brief write when the Schema
	// declares no brief fields (a historical Catalog).
	ErrProductionBriefUnavailable = errors.New("chat: production brief is not declared")
	// ErrInvalidExplicitModel3D refuses a model id outside the stored form: a
	// bounded token naming a model or Offering, never free text.
	ErrInvalidExplicitModel3D = errors.New("chat: invalid explicit 3d model")
)

var explicitModel3DName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

// ProductionBriefEnabled reports whether the Schema declares the brief.
func (service *Service) ProductionBriefEnabled() bool {
	return len(service.model3DUsage.Brief.Fields) > 0
}

// ProductionBriefFields returns the declared brief fields for the session
// read, so a client can edit the brief without a second Schema endpoint.
func (service *Service) ProductionBriefFields() []catalog.ChatProductionBriefField {
	return append([]catalog.ChatProductionBriefField(nil), service.model3DUsage.Brief.Fields...)
}

// SessionBrief reads the owner's stored brief. A missing or foreign session is
// ErrSessionNotFound.
func (service *Service) SessionBrief(ctx context.Context, ownerID, sessionID string) (ProductionBrief, error) {
	brief, found, err := service.repository.SessionBrief(ctx, ownerID, sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	return brief, nil
}

// SetProductionBrief validates and replaces the whole brief of the owner's
// session. The Agent tool and the session API share this one write path, so
// both obey the same declared fields and bounds. Returns the stored brief.
func (service *Service) SetProductionBrief(
	ctx context.Context,
	ownerID string,
	sessionID string,
	raw map[string]json.RawMessage,
) (ProductionBrief, error) {
	if !service.ProductionBriefEnabled() {
		return nil, ErrProductionBriefUnavailable
	}
	brief, err := ValidateProductionBrief(service.model3DUsage.Brief, service.model3DUsage.Profiles, raw)
	if err != nil {
		return nil, err
	}
	found, err := service.repository.SetSessionBrief(ctx, ownerID, sessionID, brief)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	// A purpose card the Agent opened before this write is now answered by the
	// value the brief carries. Answering twice is harmless: a closed card is
	// left as it is.
	service.settleUsageProfileChoice(ctx, ownerID, sessionID, brief.Text("purpose"))
	return brief, nil
}

// SessionExplicitModel3D reads the 3D model the person explicitly picked in
// the session. An empty string means no explicit pick.
func (service *Service) SessionExplicitModel3D(ctx context.Context, ownerID, sessionID string) (string, error) {
	model, found, err := service.repository.SessionExplicitModel3D(ctx, ownerID, sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return model, nil
}

// SetExplicitModel3D records the person's explicit 3D model pick for the
// session; an empty model clears it. The pick is only a lock the generation
// tools honor; it submits nothing.
func (service *Service) SetExplicitModel3D(ctx context.Context, ownerID, sessionID, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model != "" && !explicitModel3DName.MatchString(model) {
		return "", ErrInvalidExplicitModel3D
	}
	found, err := service.repository.SetSessionExplicitModel3D(ctx, ownerID, sessionID, model)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return model, nil
}

// AskChoiceArguments is one generic choice card the Agent asks for (#103).
type AskChoiceArguments struct {
	Kind        string
	Question    string
	Recommended string
}

// AskChoice opens a generic card of a kind the Schema declares. The usage
// profile kind builds its options from model3d_usage.profiles; each option's
// arguments are the target tool's declared argument filled with the profile
// id, so the answer continuation calls exactly that tool. A recommended id
// that names no option is ignored.
func (service *Service) AskChoice(
	ctx context.Context,
	command ToolCommand,
	kind catalog.ChatChoiceKindSchema,
	arguments AskChoiceArguments,
) (ToolResult, error) {
	if service.choices == nil {
		return ToolResult{Error: "choice cards unavailable", NoSubmission: true}, nil
	}
	if kind.OptionSource != "usage_profiles" {
		return ToolResult{Error: "choice kind unavailable", NoSubmission: true}, nil
	}
	profiles := service.model3DUsage.Profiles
	if len(profiles) == 0 {
		return ToolResult{Error: "choice kind unavailable", NoSubmission: true}, nil
	}
	recommended := strings.TrimSpace(arguments.Recommended)
	options := make([]ChoiceOption, 0, len(profiles))
	seenRecommended := false
	for _, profile := range profiles {
		optionArguments, err := json.Marshal(map[string]string{kind.Argument: profile.ID})
		if err != nil {
			return ToolResult{Error: "choice card unavailable", NoSubmission: true}, nil
		}
		option := ChoiceOption{
			ID:          profile.ID,
			Label:       profile.Label,
			Description: profile.Description,
			Arguments:   optionArguments,
		}
		if recommended != "" && profile.ID == recommended {
			option.Recommended = true
			seenRecommended = true
		}
		options = append(options, option)
	}
	if !seenRecommended {
		recommended = ""
	}
	question := strings.TrimSpace(arguments.Question)
	if question == "" {
		question = kind.Question
	}
	choice, err := service.choices.CreateChoice(ctx, Choice{
		OwnerID:            command.OwnerID,
		SessionID:          command.SessionID,
		AssistantMessageID: command.AssistantMessageID,
		CallID:             command.Call.ID,
		ToolName:           kind.ToolName,
		Kind:               kind.ID,
		Capability:         kind.ID,
		StepIndex:          -1,
		Options:            options,
		State:              ChoicePending,
		CreatedAt:          service.clock(),
	})
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ToolResult{Error: "session not found", NoSubmission: true}, nil
		}
		return ToolResult{}, err
	}
	wire := make([]map[string]any, 0, len(choice.Options))
	for _, option := range choice.Options {
		wire = append(wire, map[string]any{
			"id":          option.ID,
			"label":       option.Label,
			"description": option.Description,
			"recommended": option.Recommended,
		})
	}
	content, err := json.Marshal(map[string]any{
		"success":         false,
		"choice_required": true,
		"choice_id":       choice.ID,
		"kind":            kind.ID,
		"question":        question,
		"recommended":     recommended,
		"options":         wire,
		"message":         kind.Message,
	})
	if err != nil {
		return ToolResult{Error: "choice card unavailable", NoSubmission: true}, nil
	}
	return ToolResult{AgentContent: string(content), NoSubmission: true}, nil
}

// usageProfileKindOf finds the declared choice kind that a brief write settles:
// the one whose options come from the purpose profiles. Its id and the argument
// carrying the purpose are Schema values, so renaming either needs no code
// change.
func usageProfileKindOf(kinds []catalog.ChatChoiceKindSchema) *catalog.ChatChoiceKindSchema {
	for _, kind := range kinds {
		if kind.OptionSource == "usage_profiles" {
			declared := kind
			return &declared
		}
	}
	return nil
}

// ChangedProductionBriefFields lists the field names whose stored value differs,
// including fields one side does not have. It never returns field text, so an
// audit event can record the shape of a change without copying the brief.
func ChangedProductionBriefFields(previous, next ProductionBrief) []string {
	names := map[string]struct{}{}
	for name := range previous {
		names[name] = struct{}{}
	}
	for name := range next {
		names[name] = struct{}{}
	}
	changed := make([]string, 0, len(names))
	for name := range names {
		before, hadBefore := previous[name]
		after, hadAfter := next[name]
		if hadBefore != hadAfter || string(before) != string(after) {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

// settleUsageProfileChoice answers the session's open usage-profile card when
// the brief now carries the purpose it offered. The answer is recorded with
// the Agent source: the value came from the brief write, not from a click.
func (service *Service) settleUsageProfileChoice(ctx context.Context, ownerID, sessionID, purpose string) {
	kind := service.usageProfileKind
	if service.choices == nil || purpose == "" || kind == nil || kind.Argument == "" {
		return
	}
	choices, err := service.choices.ListChoices(ctx, ownerID, sessionID, service.choiceLimit)
	if err != nil {
		return
	}
	now := service.clock()
	for _, choice := range choices {
		if choice.State != ChoicePending || choice.Kind != kind.ID {
			continue
		}
		for _, option := range choice.Options {
			var arguments map[string]json.RawMessage
			var value string
			if json.Unmarshal(option.Arguments, &arguments) != nil ||
				json.Unmarshal(arguments[kind.Argument], &value) != nil || value != purpose {
				continue
			}
			_, _ = service.choices.AnswerChoice(ctx, ownerID, sessionID, choice.ID, ChoiceRecord{
				OptionID:   option.ID,
				Source:     ChoiceAnsweredByAgent,
				AnsweredAt: now,
			})
			break
		}
	}
}

// ProductionBriefValidationErrorText renders a refused brief through the
// Schema's invalid_template, so the Agent's tool result and the API message
// carry the same wording.
func ProductionBriefValidationErrorText(template string, err error) string {
	var brief *BriefError
	if !errors.As(err, &brief) {
		return ""
	}
	return strings.ReplaceAll(template, "{reason}", brief.Reason)
}

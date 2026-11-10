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
	// ErrInvalidExplicitModelImage refuses a 2D model id outside the stored
	// form, the same bounded token rule as the 3D lock (#239).
	ErrInvalidExplicitModelImage = errors.New("chat: invalid explicit image model")
	// ErrProductionBriefConflict means another writer changed the brief during
	// every bounded retry of a read-merge-write (#103 review C5).
	ErrProductionBriefConflict = errors.New("chat: production brief changed")
)

// productionBriefWriteAttempts bounds the compare-and-swap retries of a brief
// write. A conflict means another writer committed between the read and the
// conditional update, so the merged value is recomputed from the new state.
const productionBriefWriteAttempts = 3

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
// session. The session API uses it; the Agent tool patches through
// UpdateProductionBrief. Both share one compare-and-swap write path, so a
// concurrent writer cannot drop the other's change and the returned previous
// value is the one the audit compares against (#103 review C5). Returns the
// brief before and after the write.
func (service *Service) SetProductionBrief(
	ctx context.Context,
	ownerID string,
	sessionID string,
	raw map[string]json.RawMessage,
) (previous, stored ProductionBrief, err error) {
	return service.writeProductionBrief(ctx, ownerID, sessionID, func(ProductionBrief) (ProductionBrief, error) {
		return ValidateProductionBrief(service.model3DUsage.Brief, service.model3DUsage.Profiles, service.image2DUsage.Profiles, raw)
	})
}

// UpdateProductionBrief applies the named fields to the session's brief and
// leaves every other field as it was. The choice-card continuation replays
// update_production_brief with only the purpose argument, so treating that
// replay as a whole replacement would clear the style, notes and triangle
// budget the person had already set (#103 review C2). A present empty value
// clears that field.
func (service *Service) UpdateProductionBrief(
	ctx context.Context,
	ownerID string,
	sessionID string,
	patch map[string]json.RawMessage,
) (previous, stored ProductionBrief, err error) {
	return service.writeProductionBrief(ctx, ownerID, sessionID, func(previous ProductionBrief) (ProductionBrief, error) {
		normalized, err := ValidateProductionBrief(service.model3DUsage.Brief, service.model3DUsage.Profiles, service.image2DUsage.Profiles, patch)
		if err != nil {
			return nil, err
		}
		merged := make(ProductionBrief, len(previous))
		for name, value := range previous {
			merged[name] = value
		}
		for name, value := range patch {
			if briefValueEmpty(value) {
				delete(merged, name)
				continue
			}
			merged[name] = normalized[name]
		}
		return merged, nil
	})
}

// writeProductionBrief is the one read-merge-validate-write loop of the brief.
// The conditional update makes the previous value truthful for the audit; a
// conflict re-reads and recomputes instead of overwriting the other writer.
func (service *Service) writeProductionBrief(
	ctx context.Context,
	ownerID, sessionID string,
	transform func(ProductionBrief) (ProductionBrief, error),
) (previous, stored ProductionBrief, err error) {
	if !service.ProductionBriefEnabled() {
		return nil, nil, ErrProductionBriefUnavailable
	}
	for attempt := 0; attempt < productionBriefWriteAttempts; attempt++ {
		before, found, err := service.repository.SessionBrief(ctx, ownerID, sessionID)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, ErrSessionNotFound
		}
		next, err := transform(before)
		if err != nil {
			return nil, nil, err
		}
		found, conflict, err := service.repository.ReplaceSessionBrief(ctx, ownerID, sessionID, before, next)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, ErrSessionNotFound
		}
		if conflict {
			continue
		}
		// A purpose card the Agent opened before this write is now answered by
		// the value the brief carries. Answering twice is harmless: a closed
		// card is left as it is. The 3D and 2D purposes settle their own card
		// by the argument each kind writes (#239).
		if purpose := next.Text(productionBriefPurposeField(service.model3DUsage.Brief)); purpose != "" {
			service.settleUsageProfileChoice(ctx, ownerID, sessionID, service.usageProfileKind, purpose)
		}
		if imageUse := next.Text(productionBriefImageUseField(service.model3DUsage.Brief)); imageUse != "" {
			service.settleUsageProfileChoice(ctx, ownerID, sessionID, service.imageUsageProfileKind, imageUse)
		}
		return before, next, nil
	}
	return nil, nil, ErrProductionBriefConflict
}

// productionBriefPurposeField is the declared profile field's name. Locating
// it by type keeps a renamed Schema field working without a code change (#103
// review C3).
func productionBriefPurposeField(brief catalog.ChatProductionBriefSchema) string {
	return brief.FieldNameOfType("profile")
}

// productionBriefIntegerField is the declared integer field's name, the one
// carrying the session triangle override. Locating it by type keeps a renamed
// Schema field working without a code change (#103 review C3).
func productionBriefIntegerField(brief catalog.ChatProductionBriefSchema) string {
	return brief.FieldNameOfType("integer")
}

// productionBriefImageUseField is the declared image_profile field's name, the
// one carrying the session's 2D purpose (#239).
func productionBriefImageUseField(brief catalog.ChatProductionBriefSchema) string {
	return brief.FieldNameOfType("image_profile")
}

func briefValueEmpty(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed == "" || trimmed == "null"
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
	// The lock is honored by every 3D tool, so a token that names no enabled
	// 3D Offering would refuse the whole session instead of one step (#103
	// review C4).
	if model != "" && service.explicitModel3DValid != nil && !service.explicitModel3DValid(model) {
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

// SessionExplicitModelImage reads the 2D model the person explicitly picked in
// the session (#239). An empty string means no explicit pick.
func (service *Service) SessionExplicitModelImage(ctx context.Context, ownerID, sessionID string) (string, error) {
	model, found, err := service.repository.SessionExplicitModelImage(ctx, ownerID, sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return model, nil
}

// SetExplicitModelImage records the person's explicit 2D model pick for the
// session; an empty model clears it. The pick is only a lock the image tools
// honor; it submits nothing.
func (service *Service) SetExplicitModelImage(ctx context.Context, ownerID, sessionID, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model != "" && !explicitModel3DName.MatchString(model) {
		return "", ErrInvalidExplicitModelImage
	}
	if model != "" && service.explicitModelImageValid != nil && !service.explicitModelImageValid(model) {
		return "", ErrInvalidExplicitModelImage
	}
	found, err := service.repository.SetSessionExplicitModelImage(ctx, ownerID, sessionID, model)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return model, nil
}

// AskChoiceArguments is one generic choice card the Agent asks for (#103,
// #239). Options is only read for a kind whose option_source is agent (the
// direction card); the server builds the options of a purpose kind.
type AskChoiceArguments struct {
	Kind        string
	Question    string
	Recommended string
	Options     []ChoiceOptionInput
}

// ChoiceOptionInput is one Agent-supplied option of a direction card. ID is
// the bounded token the answer writes into the target tool's argument; Label
// and Description are what the person reads.
type ChoiceOptionInput struct {
	ID          string
	Label       string
	Description string
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
	type optionSpec struct {
		id, label, description string
	}
	var specs []optionSpec
	switch kind.OptionSource {
	case "usage_profiles":
		for _, profile := range service.model3DUsage.Profiles {
			specs = append(specs, optionSpec{profile.ID, profile.Label, profile.Description})
		}
	case "image_usage_profiles":
		for _, profile := range service.image2DUsage.Profiles {
			specs = append(specs, optionSpec{profile.ID, profile.Label, profile.Description})
		}
	case "agent":
		if len(arguments.Options) < 2 || (service.choiceMaxOptions > 0 && len(arguments.Options) > service.choiceMaxOptions) {
			return ToolResult{Error: "choice options invalid", NoSubmission: true}, nil
		}
		for _, option := range arguments.Options {
			id := strings.TrimSpace(option.ID)
			label := strings.TrimSpace(option.Label)
			if id == "" || label == "" || !explicitModel3DName.MatchString(id) {
				return ToolResult{Error: "choice options invalid", NoSubmission: true}, nil
			}
			specs = append(specs, optionSpec{id, label, strings.TrimSpace(option.Description)})
		}
	default:
		return ToolResult{Error: "choice kind unavailable", NoSubmission: true}, nil
	}
	if len(specs) == 0 {
		return ToolResult{Error: "choice kind unavailable", NoSubmission: true}, nil
	}
	recommended := strings.TrimSpace(arguments.Recommended)
	options := make([]ChoiceOption, 0, len(specs))
	seenRecommended := false
	for _, spec := range specs {
		optionArguments, err := json.Marshal(map[string]string{kind.Argument: spec.id})
		if err != nil {
			return ToolResult{Error: "choice card unavailable", NoSubmission: true}, nil
		}
		option := ChoiceOption{
			ID:          spec.id,
			Label:       spec.label,
			Description: spec.description,
			Arguments:   optionArguments,
		}
		if recommended != "" && spec.id == recommended {
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

// choiceKindOf finds the declared choice kind whose options come from one
// option source. Its id and the argument carrying the answer are Schema
// values, so renaming either needs no code change (#103, #239).
func choiceKindOf(kinds []catalog.ChatChoiceKindSchema, source string) *catalog.ChatChoiceKindSchema {
	for _, kind := range kinds {
		if kind.OptionSource == source {
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
func (service *Service) settleUsageProfileChoice(ctx context.Context, ownerID, sessionID string,
	kind *catalog.ChatChoiceKindSchema, value string) {
	if service.choices == nil || value == "" || kind == nil || kind.Argument == "" {
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
			var chosen string
			if json.Unmarshal(option.Arguments, &arguments) != nil ||
				json.Unmarshal(arguments[kind.Argument], &chosen) != nil || chosen != value {
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

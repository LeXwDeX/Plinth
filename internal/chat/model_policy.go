package chat

import (
	"context"
	"errors"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	// ErrChatModelUnavailable refuses a model choice while the deployment
	// keeps none (no model source or no context store); ErrInvalidChatModel
	// one naming a model that is not available now (#322 Scope 5).
	ErrChatModelUnavailable = errors.New("chat: the deployment keeps no per-session chat model")
	ErrInvalidChatModel     = errors.New("chat: invalid chat model")
)

// ModelPolicy names a session's chat model and reads its Product Schema
// profile: the thinking levels it offers and its context window (#322).
// Without a Source every session uses the deployment's model (llm_model);
// with one, the administrator's settings list the models people may use.
type ModelPolicy struct {
	// Model is the deployment's chat model.
	Model string
	// Thinking is chat.agent.thinking; nil or off means no profiles.
	Thinking *catalog.ChatThinkingSchema
	// WindowOverride replaces the profile's context window for the
	// deployment's model (runtime setting chat.agent.context_window_setting);
	// 0 keeps the profile's value.
	WindowOverride int
	// Source lists the models sessions may use now (#322 Scope 5): the
	// configured connections the administrator offers, with the levels they
	// opened. Nil keeps the single deployment model.
	Source ChatModelSource
}

// ChatModelOption is one chat model a session may use. ID names its
// connection (the request goes there), Model is the upstream model name,
// EffortLevels the levels people may choose (empty: none) and DefaultEffort
// the level used when the session chose none of them. Window is the context
// window in tokens (0: not declared).
type ChatModelOption struct {
	ID            string
	Model         string
	EffortLevels  []string
	DefaultEffort string
	Window        int
}

// ChatModelSet is the models sessions may use now, in display order, and the
// default one.
type ChatModelSet struct {
	Options   []ChatModelOption
	DefaultID string
}

// ChatModelSource reads the current model set.
type ChatModelSource func(context.Context) (ChatModelSet, error)

// SessionModel is the deployment's model (the default without a Source).
func (policy ModelPolicy) SessionModel() string { return policy.Model }

// Profile is the model's thinking profile.
func (policy ModelPolicy) Profile(model string) (catalog.ChatThinkingModelProfile, bool) {
	return policy.Thinking.ProfileFor(model)
}

// Window is the model's context window in tokens; 0 when none is declared.
func (policy ModelPolicy) Window(model string) int {
	if policy.WindowOverride > 0 && model == policy.Model {
		return policy.WindowOverride
	}
	profile, _ := policy.Profile(model)
	return profile.ContextWindowTokens
}

// Models is the current model set: the Source's, or the deployment's single
// model with all of its profile's levels.
func (policy ModelPolicy) Models(ctx context.Context) (ChatModelSet, error) {
	if policy.Source != nil {
		return policy.Source(ctx)
	}
	option := ChatModelOption{Model: policy.Model, EffortLevels: []string{}, Window: policy.Window(policy.Model)}
	if profile, _ := policy.Profile(policy.Model); profile.OffersEffort() {
		option.EffortLevels = slices.Clone(profile.EffortLevels)
		option.DefaultEffort = profile.DefaultEffort
	}
	return ChatModelSet{Options: []ChatModelOption{option}, DefaultID: option.ID}, nil
}

// resolve picks the option for a session's choice: the chosen one while it
// is available, else the default (fellBack reports a choice that is gone).
// With no option at all the deployment's model stands in.
func (policy ModelPolicy) resolve(set ChatModelSet, chosen string) (option ChatModelOption, fellBack bool) {
	if chosen != "" {
		for _, candidate := range set.Options {
			if candidate.ID == chosen {
				return candidate, false
			}
		}
		fellBack = true
	}
	for _, candidate := range set.Options {
		if candidate.ID == set.DefaultID {
			return candidate, fellBack
		}
	}
	if len(set.Options) > 0 {
		return set.Options[0], fellBack
	}
	return ChatModelOption{Model: policy.Model, EffortLevels: []string{}}, fellBack
}

// EffortFor is the level a turn on this model sends for a stored choice: the
// choice while the model offers it, else the model's default; "" when the
// model offers no level.
func (option ChatModelOption) EffortFor(chosen string) string {
	switch {
	case len(option.EffortLevels) == 0:
		return ""
	case slices.Contains(option.EffortLevels, chosen):
		return chosen
	case slices.Contains(option.EffortLevels, option.DefaultEffort):
		return option.DefaultEffort
	default:
		return option.EffortLevels[0]
	}
}

// ChatModelView is a session's chat model as the composer shows it: Selected
// is the model the next turn uses, Chosen the session's own choice ("" when
// it follows the default), Fallback whether that choice is no longer
// available so the default is used.
type ChatModelView struct {
	Selected string
	Chosen   string
	Default  string
	Fallback bool
	Options  []ChatModelOption
}

// sessionModel resolves the model of the owner's session from its stored
// choice.
func (service *Service) sessionModel(ctx context.Context, chosen string) (ChatModelOption, ChatModelView, error) {
	set, err := service.models.Models(ctx)
	if err != nil {
		return ChatModelOption{}, ChatModelView{}, err
	}
	option, fellBack := service.models.resolve(set, chosen)
	options := slices.Clone(set.Options)
	if options == nil {
		options = []ChatModelOption{}
	}
	return option, ChatModelView{Selected: option.ID, Chosen: chosen, Default: set.DefaultID, Fallback: fellBack, Options: options}, nil
}

// SessionChatModel reads the session's chat model and the models it may use.
func (service *Service) SessionChatModel(ctx context.Context, ownerID, sessionID string) (ChatModelView, error) {
	if err := service.requireSession(ctx, ownerID, sessionID); err != nil {
		return ChatModelView{}, err
	}
	state, err := service.ContextState(ctx, ownerID, sessionID)
	if err != nil {
		return ChatModelView{}, err
	}
	_, view, err := service.sessionModel(ctx, state.ChatModel)
	return view, err
}

// SetSessionChatModel stores the owner's model choice for the session: one
// of the models available now, or "" to follow the default. It applies to
// turns that start afterwards.
func (service *Service) SetSessionChatModel(ctx context.Context, ownerID, sessionID, model string) (ChatModelView, error) {
	if err := service.requireSession(ctx, ownerID, sessionID); err != nil {
		return ChatModelView{}, err
	}
	if service.contextStore == nil || service.models.Source == nil {
		return ChatModelView{}, ErrChatModelUnavailable
	}
	if model != "" {
		set, err := service.models.Models(ctx)
		if err != nil {
			return ChatModelView{}, err
		}
		if !slices.ContainsFunc(set.Options, func(option ChatModelOption) bool { return option.ID == model }) {
			return ChatModelView{}, ErrInvalidChatModel
		}
	}
	found, err := service.contextStore.SetSessionChatModel(ctx, ownerID, sessionID, model)
	if err != nil {
		return ChatModelView{}, err
	}
	if !found {
		return ChatModelView{}, ErrSessionNotFound
	}
	_, view, err := service.sessionModel(ctx, model)
	return view, err
}

// TurnModel is the model, connection and thinking level a turn of the
// owner's session would use if it started now.
func (service *Service) TurnModel(ctx context.Context, ownerID, sessionID string) (ChatModelOption, string, error) {
	state, err := service.ContextState(ctx, ownerID, sessionID)
	if err != nil {
		return ChatModelOption{}, "", err
	}
	option, _, err := service.sessionModel(ctx, state.ChatModel)
	if err != nil {
		return ChatModelOption{}, "", err
	}
	return option, option.EffortFor(state.ReasoningEffort), nil
}

// ReasoningView is a session's thinking level as the composer shows it:
// Effort is the level the next turn sends ("" when the model offers none),
// Levels the model's own level values and Default its default.
type ReasoningView struct {
	Effort  string
	Levels  []string
	Default string
}

// ContextView is a session's context as the composer shows it: Tokens is the
// context the newest model request used (nil while unknown), Window the
// model's window (0 when not declared), Compactions the session's records.
type ContextView struct {
	Tokens      *int
	Window      int
	Compactions []Compaction
}

// SetContextStore installs the store of the sessions' thinking level, usage
// and compactions (#322); listLimit bounds one compaction read. Without it
// sessions have no level, usage or compaction.
func (service *Service) SetContextStore(store ContextStore, listLimit int) {
	service.contextStore, service.compactionListLimit = store, listLimit
}

// SetModelSource installs the administrator's model set (#322 Scope 5);
// without it every session uses the deployment's model.
func (service *Service) SetModelSource(source ChatModelSource) {
	service.models.Source = source
}

// ModelPolicy exposes the sessions' model policy to the orchestrator.
func (service *Service) ModelPolicy() ModelPolicy { return service.models }

// ContextState reads the stored thinking level, usage and newest compaction
// of the owner's session. Without a store it is empty.
func (service *Service) ContextState(ctx context.Context, ownerID, sessionID string) (SessionContext, error) {
	if service.contextStore == nil {
		return SessionContext{}, nil
	}
	state, found, err := service.contextStore.SessionContext(ctx, ownerID, sessionID)
	if err != nil {
		return SessionContext{}, err
	}
	if !found {
		return SessionContext{}, ErrSessionNotFound
	}
	return state, nil
}

func reasoningView(option ChatModelOption, stored string) ReasoningView {
	if len(option.EffortLevels) == 0 {
		return ReasoningView{Levels: []string{}}
	}
	return ReasoningView{
		Effort:  option.EffortFor(stored),
		Levels:  slices.Clone(option.EffortLevels),
		Default: option.EffortFor(option.DefaultEffort),
	}
}

// SessionReasoning reads the session's thinking level for its model: the
// stored choice when the model offers it, else the model's default.
func (service *Service) SessionReasoning(ctx context.Context, ownerID, sessionID string) (ReasoningView, error) {
	if err := service.requireSession(ctx, ownerID, sessionID); err != nil {
		return ReasoningView{}, err
	}
	state, err := service.ContextState(ctx, ownerID, sessionID)
	if err != nil {
		return ReasoningView{}, err
	}
	option, _, err := service.sessionModel(ctx, state.ChatModel)
	if err != nil {
		return ReasoningView{}, err
	}
	return reasoningView(option, state.ReasoningEffort), nil
}

// SetSessionReasoning stores the owner's level choice for the session. It
// must be one of the levels the session's model offers (as the administrator
// opened them); it applies to turns that start afterwards.
func (service *Service) SetSessionReasoning(ctx context.Context, ownerID, sessionID, level string) (ReasoningView, error) {
	if err := service.requireSession(ctx, ownerID, sessionID); err != nil {
		return ReasoningView{}, err
	}
	if service.contextStore == nil {
		return ReasoningView{}, ErrReasoningUnavailable
	}
	option, _, err := service.TurnModel(ctx, ownerID, sessionID)
	if err != nil {
		return ReasoningView{}, err
	}
	if len(option.EffortLevels) == 0 {
		return ReasoningView{}, ErrReasoningUnavailable
	}
	if !slices.Contains(option.EffortLevels, level) {
		return ReasoningView{}, ErrInvalidReasoningEffort
	}
	found, err := service.contextStore.SetSessionReasoningEffort(ctx, ownerID, sessionID, level)
	if err != nil {
		return ReasoningView{}, err
	}
	if !found {
		return ReasoningView{}, ErrSessionNotFound
	}
	return reasoningView(option, level), nil
}

// SessionContextView reads the session's context usage, its model's window
// and its compaction records.
func (service *Service) SessionContextView(ctx context.Context, ownerID, sessionID string) (ContextView, error) {
	if err := service.requireSession(ctx, ownerID, sessionID); err != nil {
		return ContextView{}, err
	}
	state, err := service.ContextState(ctx, ownerID, sessionID)
	if err != nil {
		return ContextView{}, err
	}
	option, _, err := service.sessionModel(ctx, state.ChatModel)
	if err != nil {
		return ContextView{}, err
	}
	view := ContextView{Window: option.Window, Compactions: []Compaction{}}
	if service.contextStore == nil {
		return view, nil
	}
	if state.Usage != nil {
		tokens := state.Usage.Tokens()
		view.Tokens = &tokens
	}
	compactions, err := service.contextStore.ListCompactions(ctx, ownerID, sessionID, service.compactionListLimit)
	if err != nil {
		return ContextView{}, err
	}
	if compactions != nil {
		view.Compactions = compactions
	}
	return view, nil
}

// RecordContextUsage stores the usage of the session's newest model request;
// nil records that the upstream reported none.
func (service *Service) RecordContextUsage(ctx context.Context, ownerID, sessionID string, usage *ContextUsage) error {
	if service.contextStore == nil {
		return nil
	}
	if usage != nil && usage.UpdatedAt.IsZero() {
		usage.UpdatedAt = service.clock()
	}
	return service.contextStore.RecordContextUsage(ctx, ownerID, sessionID, usage)
}

// SaveCompaction stores a compaction of the session (see ContextStore).
func (service *Service) SaveCompaction(ctx context.Context, compaction Compaction, previousID string) (Compaction, error) {
	if service.contextStore == nil {
		return Compaction{}, ErrCompactionUnavailable
	}
	if compaction.CreatedAt.IsZero() {
		compaction.CreatedAt = service.clock()
	}
	return service.contextStore.SaveCompaction(ctx, compaction, previousID)
}

// HasActiveTurn reports whether a turn of the session has not finished
// (queued, running, waiting for a task or for an approval).
func (service *Service) HasActiveTurn(ctx context.Context, ownerID, sessionID string) (bool, error) {
	turns, err := service.repository.ListSessionTurns(ctx, ownerID, sessionID,
		[]string{service.turnQueued, service.turnRunning, service.turnWaitingTask})
	if err != nil {
		return false, err
	}
	return len(turns) > 0, nil
}

func (service *Service) requireSession(ctx context.Context, ownerID, sessionID string) error {
	owned, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	if !owned {
		return ErrSessionNotFound
	}
	return nil
}

// dsmlSchema is the DSML reading of a turn: on only for a model whose
// profile writes DSML tool calls when the deployment says so per model.
func (orchestrator *Orchestrator) dsmlSchema(options TurnOptions) catalog.ChatDSMLSchema {
	schema := orchestrator.schema.DSML
	if orchestrator.dsmlFor != nil {
		schema.Enabled = orchestrator.dsmlFor(orchestrator.turnModel(options))
	}
	return schema
}

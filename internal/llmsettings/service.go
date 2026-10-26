// Package llmsettings manages the administrator's LLM settings (#322 Scope
// 2): which chat model connections people may use, the default one, and the
// thinking levels each model offers. Connections themselves (addresses, keys,
// model names) are declared by the runtime environment and never edited or
// shown here: a connection only reports whether each variable has a value.
// Published settings are versioned like the Agent configuration, and every
// write names the revision it was based on.
package llmsettings

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	ErrConflict          = errors.New("llm settings revision conflict")
	ErrNotFound          = errors.New("llm settings revision not found")
	ErrInvalid           = errors.New("invalid llm settings")
	ErrUnknownConnection = errors.New("unknown llm connection")
)

// Version sources.
const (
	SourcePublish = "publish"
	SourceReset   = "reset"
	sourceRestore = "restore:"
)

// Image-input findings of a connection test.
const (
	VisionSupported   = "supported"
	VisionUnsupported = "unsupported"
	VisionUnknown     = "unknown"
)

// Connection test outcomes. They are classifications only: an upstream
// error body never reaches the settings, the page or the audit log.
const (
	OutcomeOK            = "ok"
	OutcomeNotConfigured = "not_configured"
	OutcomeUnauthorized  = "unauthorized"
	OutcomeNotFound      = "not_found"
	OutcomeRateLimited   = "rate_limited"
	OutcomeTimeout       = "timeout"
	OutcomeUnreachable   = "unreachable"
	OutcomeUpstreamError = "upstream_error"
)

// Connection is one chat model connection as the environment declares it.
// It carries whether each variable is set, never a value.
type Connection struct {
	ID    string
	Model string
	// BaseURLSet and APIKeySet report whether the variables have values;
	// CredentialsRequired is false when the Agent runs in mock mode.
	BaseURLSet          bool
	APIKeySet           bool
	CredentialsRequired bool
	// WindowOverride is <ID>_CONTEXT_WINDOW_TOKENS; 0 keeps the profile's.
	WindowOverride int
	// Environment names the variables read: base URL, key, model, window.
	Environment [4]string
}

// Configured reports whether the connection can serve requests.
func (connection Connection) Configured() bool {
	return connection.Model != "" && (!connection.CredentialsRequired || connection.BaseURLSet && connection.APIKeySet)
}

// ModelSettings are the administrator's choices for one connection.
type ModelSettings struct {
	Enabled       bool     `json:"enabled"`
	EffortLevels  []string `json:"effort_levels"`
	DefaultEffort string   `json:"default_effort"`
}

// Settings is one published settings document. Models is keyed by
// connection ID and lists every declared connection.
type Settings struct {
	DefaultModel string                   `json:"default_model"`
	Models       map[string]ModelSettings `json:"models"`
}

type Version struct {
	Revision  string    `json:"revision"`
	Settings  Settings  `json:"settings"`
	CreatedAt time.Time `json:"created_at"`
	ActorID   string    `json:"actor_id"`
	Source    string    `json:"source"`
}

// Check is the latest connection test of a connection.
type Check struct {
	ConnectionID string    `json:"connection_id"`
	Model        string    `json:"model"`
	CheckedAt    time.Time `json:"checked_at"`
	Reachable    bool      `json:"reachable"`
	Vision       string    `json:"vision"`
	Outcome      string    `json:"outcome"`
	ActorID      string    `json:"actor_id"`
}

type Store interface {
	LatestLLMSettings(context.Context) (Version, error)
	LLMSettingsVersion(context.Context, string) (Version, error)
	LLMSettingsHistory(context.Context, int) ([]Version, error)
	// AppendLLMSettings stores the next version if expected is still the
	// newest revision ("0" for none); otherwise ErrConflict.
	AppendLLMSettings(context.Context, string, Version) (Version, error)
	SaveLLMConnectionCheck(context.Context, Check) error
	LLMConnectionChecks(context.Context) ([]Check, error)
}

// ProbeResult is one connection test, already classified.
type ProbeResult struct {
	Reachable bool
	Vision    string
	Outcome   string
}

// Prober sends the minimal test request to a configured connection.
type Prober func(context.Context, Connection) ProbeResult

type Config struct {
	Store        Store
	Connections  []Connection
	Thinking     *catalog.ChatThinkingSchema
	Prober       Prober
	HistoryLimit int
	Clock        func() time.Time
}

type Service struct {
	store        Store
	connections  []Connection
	thinking     *catalog.ChatThinkingSchema
	prober       Prober
	historyLimit int
	clock        func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Store == nil || len(config.Connections) == 0 {
		return nil, errors.New("llm settings: missing store or connections")
	}
	seen := map[string]bool{}
	for _, connection := range config.Connections {
		if connection.ID == "" || seen[connection.ID] {
			return nil, fmt.Errorf("llm settings: empty or duplicate connection %q", connection.ID)
		}
		seen[connection.ID] = true
	}
	if config.HistoryLimit <= 0 {
		config.HistoryLimit = 100
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Service{store: config.Store, connections: slices.Clone(config.Connections), thinking: config.Thinking,
		prober: config.Prober, historyLimit: config.HistoryLimit, clock: config.Clock}, nil
}

// Connections are the declared connections, the deployment's own first.
func (service *Service) Connections() []Connection { return slices.Clone(service.connections) }

func (service *Service) connection(id string) (Connection, bool) {
	for _, connection := range service.connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return Connection{}, false
}

func (service *Service) profile(model string) catalog.ChatThinkingModelProfile {
	profile, _ := service.thinking.ProfileFor(model)
	if !profile.OffersEffort() {
		return catalog.ChatThinkingModelProfile{}
	}
	return profile
}

// Defaults is the preset: every connection offered with its profile's levels
// and default, the deployment's own connection as the default model.
func (service *Service) Defaults() Settings {
	settings := Settings{DefaultModel: service.connections[0].ID, Models: map[string]ModelSettings{}}
	for _, connection := range service.connections {
		profile := service.profile(connection.Model)
		settings.Models[connection.ID] = ModelSettings{Enabled: true, EffortLevels: nonNil(slices.Clone(profile.EffortLevels)), DefaultEffort: profile.DefaultEffort}
	}
	return settings
}

func nonNil(levels []string) []string {
	if levels == nil {
		return []string{}
	}
	return levels
}

// current is the newest settings, read against today's connections: entries
// for connections no longer declared are dropped, missing ones get defaults.
func (service *Service) current(ctx context.Context) (string, Settings, error) {
	version, err := service.store.LatestLLMSettings(ctx)
	if errors.Is(err, ErrNotFound) {
		return "0", service.Defaults(), nil
	}
	if err != nil {
		return "", Settings{}, err
	}
	return version.Revision, service.normalize(version.Settings), nil
}

func (service *Service) normalize(settings Settings) Settings {
	defaults := service.Defaults()
	result := Settings{DefaultModel: settings.DefaultModel, Models: map[string]ModelSettings{}}
	for _, connection := range service.connections {
		model, found := settings.Models[connection.ID]
		if !found {
			model = defaults.Models[connection.ID]
		}
		model.EffortLevels = nonNil(slices.Clone(model.EffortLevels))
		result.Models[connection.ID] = model
	}
	if _, declared := service.connection(result.DefaultModel); !declared {
		result.DefaultModel = defaults.DefaultModel
	}
	return result
}

// Model is one chat model people may use now.
type Model struct {
	ID            string
	Model         string
	EffortLevels  []string
	DefaultEffort string
	Window        int
}

// Effective is what sessions may use now: the configured, offered models in
// connection order and the default one.
type Effective struct {
	Revision  string
	Models    []Model
	DefaultID string
}

func (service *Service) Effective(ctx context.Context) (Effective, error) {
	revision, settings, err := service.current(ctx)
	if err != nil {
		return Effective{}, err
	}
	effective := Effective{Revision: revision, Models: []Model{}}
	for _, connection := range service.connections {
		chosen := settings.Models[connection.ID]
		if !connection.Configured() || !chosen.Enabled {
			continue
		}
		profile := service.profile(connection.Model)
		levels := []string{}
		for _, level := range profile.EffortLevels {
			if slices.Contains(chosen.EffortLevels, level) {
				levels = append(levels, level)
			}
		}
		if len(levels) == 0 {
			levels = nonNil(slices.Clone(profile.EffortLevels))
		}
		fallback := ""
		switch {
		case len(levels) == 0:
		case slices.Contains(levels, chosen.DefaultEffort):
			fallback = chosen.DefaultEffort
		case slices.Contains(levels, profile.DefaultEffort):
			fallback = profile.DefaultEffort
		default:
			fallback = levels[0]
		}
		declared, _ := service.thinking.ProfileFor(connection.Model)
		window := declared.ContextWindowTokens
		if connection.WindowOverride > 0 {
			window = connection.WindowOverride
		}
		effective.Models = append(effective.Models, Model{ID: connection.ID, Model: connection.Model,
			EffortLevels: levels, DefaultEffort: fallback, Window: window})
	}
	for _, model := range effective.Models {
		if model.ID == settings.DefaultModel {
			effective.DefaultID = model.ID
		}
	}
	if effective.DefaultID == "" && len(effective.Models) > 0 {
		effective.DefaultID = effective.Models[0].ID
	}
	return effective, nil
}

// Validate checks a settings document against today's connections and
// their profiles.
func (service *Service) Validate(settings Settings) error {
	invalid := func(format string, arguments ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, arguments...))
	}
	if settings.Models == nil {
		return invalid("models missing")
	}
	for id := range settings.Models {
		if _, declared := service.connection(id); !declared {
			return invalid("unknown connection %q", id)
		}
	}
	for _, connection := range service.connections {
		chosen, found := settings.Models[connection.ID]
		if !found {
			return invalid("connection %q missing", connection.ID)
		}
		profile := service.profile(connection.Model)
		if len(profile.EffortLevels) == 0 {
			if len(chosen.EffortLevels) > 0 || chosen.DefaultEffort != "" {
				return invalid("connection %q has no thinking levels", connection.ID)
			}
			continue
		}
		if len(chosen.EffortLevels) == 0 {
			return invalid("connection %q offers no thinking level", connection.ID)
		}
		seen := map[string]bool{}
		for _, level := range chosen.EffortLevels {
			if seen[level] || !slices.Contains(profile.EffortLevels, level) {
				return invalid("connection %q: level %q", connection.ID, level)
			}
			seen[level] = true
		}
		if !seen[chosen.DefaultEffort] {
			return invalid("connection %q: default level %q", connection.ID, chosen.DefaultEffort)
		}
	}
	if chosen, found := settings.Models[settings.DefaultModel]; !found || !chosen.Enabled {
		return invalid("default model %q is not an offered connection", settings.DefaultModel)
	}
	return nil
}

// Publish stores the settings as the version after expected.
func (service *Service) Publish(ctx context.Context, expected, actor, source string, settings Settings) (Version, error) {
	if err := service.Validate(settings); err != nil {
		return Version{}, err
	}
	if _, err := strconv.ParseUint(expected, 10, 64); err != nil {
		return Version{}, ErrConflict
	}
	stored := service.normalize(settings)
	// Levels are kept in the profile's order.
	for id, model := range stored.Models {
		connection, _ := service.connection(id)
		ordered := []string{}
		for _, level := range service.profile(connection.Model).EffortLevels {
			if slices.Contains(model.EffortLevels, level) {
				ordered = append(ordered, level)
			}
		}
		model.EffortLevels = ordered
		stored.Models[id] = model
	}
	return service.store.AppendLLMSettings(ctx, expected, Version{Settings: stored, CreatedAt: service.clock().UTC(), ActorID: actor, Source: source})
}

// Restore republishes an earlier version ("0" is the preset) as the next one.
func (service *Service) Restore(ctx context.Context, expected, target, actor string) (Version, error) {
	if target == "0" {
		return service.Publish(ctx, expected, actor, SourceReset, service.Defaults())
	}
	version, err := service.store.LLMSettingsVersion(ctx, target)
	if err != nil {
		return Version{}, err
	}
	return service.Publish(ctx, expected, actor, sourceRestore+target, service.normalize(version.Settings))
}

// Test sends the minimal request to a connection and stores the outcome. A
// connection without its settings is not called.
func (service *Service) Test(ctx context.Context, id, actor string) (Check, error) {
	connection, declared := service.connection(id)
	if !declared {
		return Check{}, ErrUnknownConnection
	}
	result := ProbeResult{Vision: VisionUnknown, Outcome: OutcomeNotConfigured}
	if connection.Configured() && service.prober != nil {
		result = service.prober(ctx, connection)
	}
	if result.Vision == "" {
		result.Vision = VisionUnknown
	}
	check := Check{ConnectionID: id, Model: connection.Model, CheckedAt: service.clock().UTC(),
		Reachable: result.Reachable, Vision: result.Vision, Outcome: result.Outcome, ActorID: actor}
	if err := service.store.SaveLLMConnectionCheck(ctx, check); err != nil {
		return Check{}, err
	}
	return check, nil
}

// ConnectionView is a connection as the administration page shows it.
type ConnectionView struct {
	ID                  string   `json:"id"`
	Model               string   `json:"model"`
	Default             bool     `json:"deployment_default"`
	Environment         []string `json:"environment"`
	BaseURLSet          bool     `json:"base_url_set"`
	APIKeySet           bool     `json:"api_key_set"`
	ModelSet            bool     `json:"model_set"`
	CredentialsRequired bool     `json:"credentials_required"`
	Configured          bool     `json:"configured"`
	ProfileLevels       []string `json:"profile_levels"`
	ProfileDefault      string   `json:"profile_default"`
	ContextWindow       int      `json:"context_window"`
	Check               *Check   `json:"check"`
}

type History struct {
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	ActorID   string    `json:"actor_id"`
	Source    string    `json:"source"`
}

// Management is the administration page's read.
type Management struct {
	Revision    string           `json:"revision"`
	Settings    Settings         `json:"settings"`
	Defaults    Settings         `json:"defaults"`
	Connections []ConnectionView `json:"connections"`
	History     []History        `json:"history"`
	// DefaultAvailable reports whether the chosen default model can serve
	// people now (configured and offered).
	DefaultAvailable bool `json:"default_available"`
	// EffectiveDefault is the model sessions default to now.
	EffectiveDefault string `json:"effective_default"`
}

func (service *Service) Management(ctx context.Context) (Management, error) {
	revision, settings, err := service.current(ctx)
	if err != nil {
		return Management{}, err
	}
	effective, err := service.Effective(ctx)
	if err != nil {
		return Management{}, err
	}
	history, err := service.store.LLMSettingsHistory(ctx, service.historyLimit)
	if err != nil {
		return Management{}, err
	}
	checks, err := service.store.LLMConnectionChecks(ctx)
	if err != nil {
		return Management{}, err
	}
	result := Management{Revision: revision, Settings: settings, Defaults: service.Defaults(),
		Connections: []ConnectionView{}, History: []History{}, EffectiveDefault: effective.DefaultID}
	result.DefaultAvailable = effective.DefaultID == settings.DefaultModel
	for index, connection := range service.connections {
		profile := service.profile(connection.Model)
		declared, _ := service.thinking.ProfileFor(connection.Model)
		window := declared.ContextWindowTokens
		if connection.WindowOverride > 0 {
			window = connection.WindowOverride
		}
		view := ConnectionView{ID: connection.ID, Model: connection.Model, Default: index == 0,
			Environment: append([]string(nil), connection.Environment[:3]...), BaseURLSet: connection.BaseURLSet, APIKeySet: connection.APIKeySet,
			ModelSet: connection.Model != "", CredentialsRequired: connection.CredentialsRequired, Configured: connection.Configured(),
			ProfileLevels: nonNil(slices.Clone(profile.EffortLevels)), ProfileDefault: profile.DefaultEffort, ContextWindow: window}
		for _, check := range checks {
			// A check of another model name (the variable changed since) is stale.
			if check.ConnectionID == connection.ID && check.Model == connection.Model {
				recorded := check
				view.Check = &recorded
			}
		}
		result.Connections = append(result.Connections, view)
	}
	for _, version := range history {
		result.History = append(result.History, History{version.Revision, version.CreatedAt, version.ActorID, version.Source})
	}
	return result, nil
}

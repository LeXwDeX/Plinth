package llmsettings

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type memoryStore struct {
	mu       sync.Mutex
	versions []Version
	checks   map[string]Check
}

func (store *memoryStore) LatestLLMSettings(context.Context) (Version, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.versions) == 0 {
		return Version{}, ErrNotFound
	}
	return store.versions[len(store.versions)-1], nil
}

func (store *memoryStore) LLMSettingsVersion(_ context.Context, revision string) (Version, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, version := range store.versions {
		if version.Revision == revision {
			return version, nil
		}
	}
	return Version{}, ErrNotFound
}

func (store *memoryStore) LLMSettingsHistory(_ context.Context, limit int) ([]Version, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := []Version{}
	for index := len(store.versions) - 1; index >= 0 && len(result) < limit; index-- {
		result = append(result, store.versions[index])
	}
	return result, nil
}

func (store *memoryStore) AppendLLMSettings(_ context.Context, expected string, version Version) (Version, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if expected != strconv.Itoa(len(store.versions)) {
		return Version{}, ErrConflict
	}
	version.Revision = strconv.Itoa(len(store.versions) + 1)
	store.versions = append(store.versions, version)
	return version, nil
}

func (store *memoryStore) SaveLLMConnectionCheck(_ context.Context, check Check) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.checks == nil {
		store.checks = map[string]Check{}
	}
	store.checks[check.ConnectionID] = check
	return nil
}

func (store *memoryStore) LLMConnectionChecks(context.Context) ([]Check, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := []Check{}
	for _, check := range store.checks {
		result = append(result, check)
	}
	return result, nil
}

func testThinking() *catalog.ChatThinkingSchema {
	return &catalog.ChatThinkingSchema{Enabled: true, ModelProfiles: []catalog.ChatThinkingModelProfile{
		{ModelPrefixes: []string{"deepseek"}, EffortField: "reasoning_effort", EffortLevels: []string{"low", "high", "max"}, DefaultEffort: "low", ContextWindowTokens: 1000000},
		{ModelPrefixes: []string{"glm"}, EffortField: "reasoning_effort", EffortLevels: []string{"low", "high", "max"}, DefaultEffort: "max"},
	}}
}

func connection(id, model string, configured bool) Connection {
	return Connection{ID: id, Model: model, BaseURLSet: configured, APIKeySet: configured, CredentialsRequired: true,
		Environment: [4]string{id + "_BASE_URL", id + "_API_KEY", id + "_MODEL", id + "_CONTEXT_WINDOW_TOKENS"}}
}

func newTestService(t *testing.T, connections []Connection, prober Prober) (*Service, *memoryStore) {
	t.Helper()
	store := &memoryStore{}
	service, err := New(Config{Store: store, Connections: connections, Thinking: testThinking(), Prober: prober, HistoryLimit: 10,
		Clock: func() time.Time { return time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

// With nothing published, every configured connection is offered with all
// of its profile's levels and the profile default, and the deployment's own
// connection (listed first) is the default: the behaviour before #322.
func TestEffectiveWithoutPublishedSettingsKeepsTheDeploymentDefaults(t *testing.T) {
	glm := connection("GLM", "glm-5.3", true)
	glm.WindowOverride = 200000
	service, _ := newTestService(t, []Connection{connection("LLM", "deepseek", true), glm, connection("QWEN", "qwen3.8-plus", false)}, nil)
	effective, err := service.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if effective.Revision != "0" || effective.DefaultID != "LLM" || len(effective.Models) != 2 {
		t.Fatalf("effective = %+v", effective)
	}
	deepseek, glmModel := effective.Models[0], effective.Models[1]
	if deepseek.ID != "LLM" || deepseek.Model != "deepseek" || !slices.Equal(deepseek.EffortLevels, []string{"low", "high", "max"}) ||
		deepseek.DefaultEffort != "low" || deepseek.Window != 1000000 {
		t.Fatalf("deepseek = %+v", deepseek)
	}
	if glmModel.ID != "GLM" || glmModel.DefaultEffort != "max" || glmModel.Window != 200000 {
		t.Fatalf("glm = %+v", glmModel)
	}
}

// The administrator narrows the levels, picks the default level and the
// default model, and takes a model offline; each publish needs the revision it
// was based on.
func TestPublishAppliesLevelsDefaultsAndOfflineModels(t *testing.T) {
	service, _ := newTestService(t, []Connection{connection("LLM", "deepseek", true), connection("GLM", "glm-5.3", true)}, nil)
	ctx := context.Background()
	settings := Settings{DefaultModel: "GLM", Models: map[string]ModelSettings{
		"LLM": {Enabled: true, EffortLevels: []string{"low", "high"}, DefaultEffort: "high"},
		"GLM": {Enabled: true, EffortLevels: []string{"max"}, DefaultEffort: "max"},
	}}
	published, err := service.Publish(ctx, "0", "admin-1", SourcePublish, settings)
	if err != nil || published.Revision != "1" {
		t.Fatalf("publish: %+v %v", published, err)
	}
	if _, err := service.Publish(ctx, "0", "admin-2", SourcePublish, settings); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish: %v", err)
	}
	effective, err := service.Effective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if effective.DefaultID != "GLM" || !slices.Equal(effective.Models[0].EffortLevels, []string{"low", "high"}) ||
		effective.Models[0].DefaultEffort != "high" || !slices.Equal(effective.Models[1].EffortLevels, []string{"max"}) {
		t.Fatalf("effective = %+v", effective)
	}
	settings.Models["GLM"] = ModelSettings{Enabled: false, EffortLevels: []string{"max"}, DefaultEffort: "max"}
	settings.DefaultModel = "LLM"
	if _, err := service.Publish(ctx, "1", "admin-1", SourcePublish, settings); err != nil {
		t.Fatal(err)
	}
	effective, _ = service.Effective(ctx)
	if len(effective.Models) != 1 || effective.Models[0].ID != "LLM" || effective.DefaultID != "LLM" {
		t.Fatalf("offline model still offered: %+v", effective)
	}
	// Restoring version 1 brings the first settings back as version 3.
	restored, err := service.Restore(ctx, "2", "1", "admin-1")
	if err != nil || restored.Revision != "3" || restored.Source != "restore:1" {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	reset, err := service.Restore(ctx, "3", "0", "admin-1")
	if err != nil || reset.Revision != "4" || reset.Source != SourceReset {
		t.Fatalf("reset: %+v %v", reset, err)
	}
	effective, _ = service.Effective(ctx)
	if effective.DefaultID != "LLM" || len(effective.Models) != 2 || len(effective.Models[1].EffortLevels) != 3 {
		t.Fatalf("after reset: %+v", effective)
	}
}

func TestValidateRejectsLevelsOutsideTheProfileAndUnknownModels(t *testing.T) {
	service, _ := newTestService(t, []Connection{connection("LLM", "deepseek", true), connection("PLAIN", "gpt-like", true)}, nil)
	valid := func() Settings {
		return Settings{DefaultModel: "LLM", Models: map[string]ModelSettings{
			"LLM":   {Enabled: true, EffortLevels: []string{"low"}, DefaultEffort: "low"},
			"PLAIN": {Enabled: true, EffortLevels: []string{}},
		}}
	}
	if err := service.Validate(valid()); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	for name, mutate := range map[string]func(*Settings){
		"level the model does not offer": func(s *Settings) {
			s.Models["LLM"] = ModelSettings{Enabled: true, EffortLevels: []string{"xhigh"}, DefaultEffort: "xhigh"}
		},
		"no level for a model with levels": func(s *Settings) {
			s.Models["LLM"] = ModelSettings{Enabled: true, EffortLevels: []string{}}
		},
		"default level outside the offered ones": func(s *Settings) {
			s.Models["LLM"] = ModelSettings{Enabled: true, EffortLevels: []string{"low"}, DefaultEffort: "max"}
		},
		"duplicate level": func(s *Settings) {
			s.Models["LLM"] = ModelSettings{Enabled: true, EffortLevels: []string{"low", "low"}, DefaultEffort: "low"}
		},
		"levels for a model without a profile": func(s *Settings) {
			s.Models["PLAIN"] = ModelSettings{Enabled: true, EffortLevels: []string{"low"}, DefaultEffort: "low"}
		},
		"unknown connection": func(s *Settings) { s.Models["NOPE"] = ModelSettings{Enabled: true} },
		"unknown default":    func(s *Settings) { s.DefaultModel = "NOPE" },
		"offline default": func(s *Settings) {
			s.Models["LLM"] = ModelSettings{EffortLevels: []string{"low"}, DefaultEffort: "low"}
		},
		"missing default":     func(s *Settings) { s.DefaultModel = "" },
		"missing model entry": func(s *Settings) { delete(s.Models, "PLAIN") },
		"nil models":          func(s *Settings) { s.Models = nil },
	} {
		settings := valid()
		mutate(&settings)
		if err := service.Validate(settings); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// A connection that leaves the environment drops out of the effective set and
// the management view; the stored version is read without it.
func TestEffectiveIgnoresConnectionsNoLongerDeclared(t *testing.T) {
	ctx := context.Background()
	service, store := newTestService(t, []Connection{connection("LLM", "deepseek", true), connection("GLM", "glm-5.3", true)}, nil)
	if _, err := service.Publish(ctx, "0", "admin", SourcePublish, Settings{DefaultModel: "GLM", Models: map[string]ModelSettings{
		"LLM": {Enabled: true, EffortLevels: []string{"low"}, DefaultEffort: "low"},
		"GLM": {Enabled: true, EffortLevels: []string{"high"}, DefaultEffort: "high"},
	}}); err != nil {
		t.Fatal(err)
	}
	shrunk, err := New(Config{Store: store, Connections: []Connection{connection("LLM", "deepseek", true)}, Thinking: testThinking(), HistoryLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := shrunk.Effective(ctx)
	if err != nil || len(effective.Models) != 1 || effective.DefaultID != "LLM" {
		t.Fatalf("effective = %+v %v", effective, err)
	}
	management, err := shrunk.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The default that left falls back to the deployment's own connection.
	if _, kept := management.Settings.Models["GLM"]; kept || management.Settings.DefaultModel != "LLM" || !management.DefaultAvailable {
		t.Fatalf("management = %+v", management)
	}
}

// The connection test stores the classified outcome only, for the connection
// the environment declares; an unknown connection is not tested.
func TestConnectionTestStoresTheClassifiedOutcome(t *testing.T) {
	var probed []string
	prober := func(_ context.Context, target Connection) ProbeResult {
		probed = append(probed, target.ID+"/"+target.Model)
		if target.ID == "GLM" {
			return ProbeResult{Reachable: true, Vision: VisionUnsupported, Outcome: OutcomeOK}
		}
		return ProbeResult{Vision: VisionUnknown, Outcome: OutcomeUnauthorized}
	}
	service, _ := newTestService(t, []Connection{connection("LLM", "deepseek", true), connection("GLM", "glm-5.3", true)}, prober)
	ctx := context.Background()
	check, err := service.Test(ctx, "GLM", "admin-1")
	if err != nil || !check.Reachable || check.Vision != VisionUnsupported || check.Outcome != OutcomeOK || check.Model != "glm-5.3" || check.ActorID != "admin-1" {
		t.Fatalf("check = %+v %v", check, err)
	}
	if _, err := service.Test(ctx, "LLM", "admin-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(ctx, "NOPE", "admin-1"); !errors.Is(err, ErrUnknownConnection) {
		t.Fatalf("unknown connection: %v", err)
	}
	if !slices.Equal(probed, []string{"GLM/glm-5.3", "LLM/deepseek"}) {
		t.Fatalf("probed = %v", probed)
	}
	management, err := service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range management.Connections {
		if view.Check == nil {
			t.Fatalf("connection %s lost its check", view.ID)
		}
	}
	if management.Connections[0].Check.Outcome != OutcomeUnauthorized || management.Connections[0].Check.Reachable {
		t.Fatalf("LLM check = %+v", management.Connections[0].Check)
	}
}

// A connection without its settings is not tested upstream: the result says
// it is not configured.
func TestConnectionTestOfAnUnconfiguredConnectionDoesNotCallUpstream(t *testing.T) {
	called := false
	service, _ := newTestService(t, []Connection{connection("LLM", "deepseek", false)}, func(context.Context, Connection) ProbeResult {
		called = true
		return ProbeResult{}
	})
	check, err := service.Test(context.Background(), "LLM", "admin")
	if err != nil || called || check.Outcome != OutcomeNotConfigured || check.Reachable {
		t.Fatalf("check = %+v %v called=%v", check, err, called)
	}
}

// #401: every Agent turn carries function tools. A connection whose model
// cannot run them on chat completions is never offered to sessions, even when
// it is configured and enabled and the administrator made it the default; the
// administration read says so and the connection test still reaches it.
func TestEffectiveDoesNotOfferAModelThatCannotRunAgentTools(t *testing.T) {
	runs := false
	thinking := testThinking()
	thinking.ModelProfiles = append(thinking.ModelProfiles, catalog.ChatThinkingModelProfile{
		ModelPrefixes: []string{"gpt-astra"}, EffortField: "reasoning_effort", EffortLevels: []string{"low", "high"}, DefaultEffort: "high",
		OmitTemperature: true, ChatCompletionsTools: &runs})
	store := &memoryStore{}
	var tested []string
	service, err := New(Config{Store: store, Thinking: thinking, HistoryLimit: 10,
		Connections: []Connection{connection("GPT", "gpt-astra", true), connection("LLM", "deepseek", true), connection("GLM", "glm-5.3", true)},
		Prober: func(_ context.Context, connection Connection) ProbeResult {
			tested = append(tested, connection.ID)
			return ProbeResult{Reachable: true, Vision: VisionUnknown, Outcome: OutcomeOK}
		},
		Clock: func() time.Time { return time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The preset enables every connection and names the first one the default;
	// that one cannot serve, so the first model that can stands in.
	effective, err := service.Effective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range effective.Models {
		ids = append(ids, model.ID)
	}
	if !slices.Equal(ids, []string{"LLM", "GLM"}) || effective.DefaultID != "LLM" {
		t.Fatalf("offered = %v, default %q", ids, effective.DefaultID)
	}
	management, err := service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if management.DefaultAvailable || management.EffectiveDefault != "LLM" || management.Settings.DefaultModel != "GPT" {
		t.Fatalf("management default = %q available=%t effective=%q", management.Settings.DefaultModel, management.DefaultAvailable, management.EffectiveDefault)
	}
	agentTools := map[string]bool{}
	for _, view := range management.Connections {
		agentTools[view.ID] = view.AgentTools
	}
	if agentTools["GPT"] || !agentTools["LLM"] || !agentTools["GLM"] {
		t.Fatalf("agent tools = %v", agentTools)
	}
	// An administrator who makes the unusable model the default publishes
	// fine (the connection is enabled), and sessions still get a usable one.
	settings := management.Settings
	settings.DefaultModel = "GLM"
	if _, err := service.Publish(ctx, management.Revision, "admin", SourcePublish, settings); err != nil {
		t.Fatal(err)
	}
	if effective, err = service.Effective(ctx); err != nil || effective.DefaultID != "GLM" || len(effective.Models) != 2 {
		t.Fatalf("after publish: %+v %v", effective, err)
	}
	// The test of that connection still sends its request.
	check, err := service.Test(ctx, "GPT", "admin")
	if err != nil || !check.Reachable || !slices.Equal(tested, []string{"GPT"}) {
		t.Fatalf("check = %+v tested = %v err = %v", check, tested, err)
	}
}

// #401: a model served on the Responses wire runs the function tools of an
// Agent turn there, so it is offered to sessions even when the profile also
// says chat completions cannot run them; the administration read says so too.
func TestEffectiveOffersAModelServedOnTheResponsesWire(t *testing.T) {
	cannot := false
	thinking := testThinking()
	thinking.ModelProfiles = append(thinking.ModelProfiles, catalog.ChatThinkingModelProfile{
		ModelPrefixes: []string{"gpt-astra"}, EffortField: "reasoning_effort", EffortLevels: []string{"low", "high"}, DefaultEffort: "high",
		OmitTemperature: true, WireAPI: catalog.ChatWireResponses, ChatCompletionsTools: &cannot})
	thinking.ModelProfiles = append(thinking.ModelProfiles, catalog.ChatThinkingModelProfile{
		ModelPrefixes: []string{"gpt-sol"}, EffortField: "reasoning_effort", EffortLevels: []string{"low"}, DefaultEffort: "low",
		ChatCompletionsTools: &cannot})
	service, err := New(Config{Store: &memoryStore{}, Thinking: thinking, HistoryLimit: 10,
		Connections: []Connection{connection("GPT", "gpt-astra", true), connection("SOL", "gpt-sol", true), connection("LLM", "deepseek", true)},
		Clock:       func() time.Time { return time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	effective, err := service.Effective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range effective.Models {
		ids = append(ids, model.ID)
	}
	// SOL is on chat completions and cannot run tools there; GPT is on the
	// Responses wire and can, and is the default the administrator's preset
	// names first.
	if !slices.Equal(ids, []string{"GPT", "LLM"}) || effective.DefaultID != "GPT" {
		t.Fatalf("offered = %v, default %q", ids, effective.DefaultID)
	}
	management, err := service.Management(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agentTools := map[string]bool{}
	for _, view := range management.Connections {
		agentTools[view.ID] = view.AgentTools
	}
	if !agentTools["GPT"] || agentTools["SOL"] || !agentTools["LLM"] || !management.DefaultAvailable {
		t.Fatalf("agent tools = %v, default available = %t", agentTools, management.DefaultAvailable)
	}
}

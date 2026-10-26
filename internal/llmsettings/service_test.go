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

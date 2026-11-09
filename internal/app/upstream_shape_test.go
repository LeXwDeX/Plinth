package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// fakeOverlayEnvironment reads the art-ai-go environment block of the fake
// upstream overlay (#219) as KEY -> value.
func fakeOverlayEnvironment(t *testing.T) map[string]string {
	t.Helper()
	compose := readRepositoryFile(t, "docker-compose.go-fake-upstream.yml")
	start := strings.Index(compose, "\n  art-ai-go:\n")
	if start < 0 {
		t.Fatal("overlay has no art-ai-go service")
	}
	section := compose[start:]
	at := strings.Index(section, "\n    environment:\n")
	if at < 0 {
		t.Fatal("overlay art-ai-go has no environment block")
	}
	line := regexp.MustCompile(`^      ([A-Z][A-Z0-9_]*): ?(.*)$`)
	values := map[string]string{}
	for _, raw := range strings.Split(section[at+len("\n    environment:\n"):], "\n") {
		match := line.FindStringSubmatch(raw)
		if match == nil {
			break
		}
		values[match[1]] = strings.Trim(strings.TrimSpace(match[2]), `"`)
	}
	if len(values) == 0 {
		t.Fatal("overlay environment block is empty")
	}
	return values
}

// liveRuntimeEnvironment stands for a dev-test runtime.env that still holds
// real Provider addresses and keys; the overlay must replace every one.
func liveRuntimeEnvironment(schema catalog.ApplicationSchema) map[string]string {
	values := map[string]string{
		"APP_ENV": "production", "AUTH_MODE": "session", "PROVIDER_MODE": "live", "AGENT_MODE": "live",
		"SESSION_SECRET": strings.Repeat("s", 40), "LLM_MODEL": "gpt-astra",
	}
	for _, connection := range schema.Runtime.ProviderConnections {
		for _, name := range []string{connection.BaseURLSetting, connection.APIKeySetting} {
			for _, source := range schema.Runtime.Configuration.Settings[name].Environment {
				if strings.HasSuffix(name, "_key") {
					values[source.Name] = "sk-real-" + strings.ToLower(source.Name)
				} else {
					values[source.Name] = "https://real-" + strings.ToLower(strings.ReplaceAll(source.Name, "_", "-")) + ".example"
				}
			}
		}
	}
	values["LLM_CONNECTIONS"] = "EXTRA"
	values["EXTRA_BASE_URL"] = "https://real-extra.example/v1"
	values["EXTRA_API_KEY"] = "sk-real-extra"
	values["EXTRA_MODEL"] = "extra-model"
	return values
}

func shapeConfig(t *testing.T, schema catalog.ApplicationSchema, environment map[string]string) Config {
	t.Helper()
	config, err := loadConfigFromSchema(schema, func(name string) string { return environment[name] })
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func defaultSchema(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog.Application()
}

// The overlay replaces every Provider and chat variable (canonical names and
// deprecated aliases) of a runtime.env that holds real values, and the result
// passes the fail-closed wiring check.
func TestFakeUpstreamOverlayReplacesEveryUpstreamVariable(t *testing.T) {
	schema := defaultSchema(t)
	overlay := fakeOverlayEnvironment(t)
	for _, connection := range schema.Runtime.ProviderConnections {
		for _, name := range []string{connection.BaseURLSetting, connection.APIKeySetting} {
			for _, source := range schema.Runtime.Configuration.Settings[name].Environment {
				if _, ok := overlay[source.Name]; !ok {
					t.Errorf("overlay does not override %s (%s)", source.Name, connection.ID)
				}
			}
		}
	}
	if value, ok := overlay["LLM_CONNECTIONS"]; !ok || value != "" {
		t.Error("overlay must clear LLM_CONNECTIONS")
	}
	environment := liveRuntimeEnvironment(schema)
	for key, value := range overlay { // Compose environment wins over env_file.
		environment[key] = value
	}
	config := shapeConfig(t, schema, environment)
	shape, err := resolveUpstreamShape(config)
	if err != nil || !shape.fake() {
		t.Fatalf("overlay shape = %v, %v", shape.fake(), err)
	}
	if err := validateFakeUpstreamWiring(shape, config, schema); err != nil {
		t.Fatalf("overlay wiring refused: %v", err)
	}
}

func TestUpstreamShapeDefaultsToLiveAndRefusesHalfConfiguration(t *testing.T) {
	schema := defaultSchema(t)
	base := map[string]string{"PROVIDER_MODE": "live", "AGENT_MODE": "live"}
	shape, err := resolveUpstreamShape(shapeConfig(t, schema, base))
	if err != nil || shape.fake() {
		t.Fatalf("default shape fake=%v err=%v, want live", shape.fake(), err)
	}
	with := func(extra map[string]string) map[string]string {
		values := map[string]string{}
		for key, value := range base {
			values[key] = value
		}
		for key, value := range extra {
			values[key] = value
		}
		return values
	}
	for name, environment := range map[string]map[string]string{
		"origin without fake shape": with(map[string]string{"FAKE_UPSTREAM_ORIGIN": "http://fake-upstream:19000"}),
		"fake without origin":       with(map[string]string{"UPSTREAM_SHAPE": "fake"}),
		"public origin":             with(map[string]string{"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": "http://fake.example.com"}),
		"public address":            with(map[string]string{"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": "http://8.8.8.8:19000"}),
		"https origin":              with(map[string]string{"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": "https://fake-upstream"}),
		"origin with path":          with(map[string]string{"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": "http://fake-upstream:19000/tripo"}),
		"origin with credentials":   with(map[string]string{"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": "http://u:p@fake-upstream:19000"}),
		"mock providers": {"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": "http://fake-upstream:19000",
			"PROVIDER_MODE": "mock", "AGENT_MODE": "live"},
	} {
		if _, err := resolveUpstreamShape(shapeConfig(t, schema, environment)); err == nil {
			t.Errorf("%s: shape accepted", name)
		}
	}
	for _, origin := range []string{"http://fake-upstream:19000", "http://127.0.0.1:19000", "http://10.1.2.3", "http://localhost:19000/"} {
		shape, err := resolveUpstreamShape(shapeConfig(t, schema, with(map[string]string{"UPSTREAM_SHAPE": "fake", "FAKE_UPSTREAM_ORIGIN": origin})))
		if err != nil || !shape.fake() || shape.origin.Path != "" {
			t.Errorf("%s: fake=%v err=%v", origin, shape.fake(), err)
		}
	}
}

// Any connection left on a real address, a real key or an extra chat
// connection keeps the fake-shaped stack from starting.
func TestFakeUpstreamWiringFailsClosed(t *testing.T) {
	schema := defaultSchema(t)
	overlay := fakeOverlayEnvironment(t)
	for name, change := range map[string]map[string]string{
		"tripo on its real address": {"TRIPO_BASE_URL": "https://openapi.tripo3d.ai/v3"},
		"openai left on default":    {"OPENAI_BASE_URL": ""},
		"matting left on default":   {"MATTING_BASE_URL": ""},
		"real meshy key":            {"MESHY_API_KEY": "msy-real"},
		"real chat key":             {"LLM_API_KEY": "sk-real"},
		"chat on another host":      {"LLM_BASE_URL": "http://gateway:19000/v1"},
		"extra chat connection": {"LLM_CONNECTIONS": "EXTRA", "EXTRA_BASE_URL": "http://fake-upstream:19000/openai/v1",
			"EXTRA_API_KEY": "fake-test-key", "EXTRA_MODEL": "m"},
	} {
		environment := map[string]string{"PROVIDER_MODE": "live", "AGENT_MODE": "live"}
		for key, value := range overlay {
			environment[key] = value
		}
		for key, value := range change {
			environment[key] = value
		}
		config := shapeConfig(t, schema, environment)
		shape, err := resolveUpstreamShape(config)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := validateFakeUpstreamWiring(shape, config, schema); err == nil {
			t.Errorf("%s: wiring accepted", name)
		}
	}
	// The live shape checks nothing: formal-test keeps its own wiring.
	config := shapeConfig(t, schema, map[string]string{"PROVIDER_MODE": "live", "AGENT_MODE": "live", "TRIPO_API_KEY": "real"})
	if err := validateFakeUpstreamWiring(upstreamShape{}, config, schema); err != nil {
		t.Fatalf("live shape refused: %v", err)
	}
}

// newWithDependencies refuses a misconfigured fake shape before it opens
// anything.
func TestApplicationRefusesFakeShapeWithRealKey(t *testing.T) {
	schema := defaultSchema(t)
	environment := map[string]string{"PROVIDER_MODE": "live", "AGENT_MODE": "live"}
	for key, value := range fakeOverlayEnvironment(t) {
		environment[key] = value
	}
	environment["TRIPO_API_KEY"] = "tsk-real"
	config := shapeConfig(t, schema, environment)
	config.DatabasePath = t.TempDir() + "/never.db"
	if _, err := newWithDependencies(config, nil, assemblyOverrides{}); err == nil ||
		!strings.Contains(err.Error(), "tripo_api_key") {
		t.Fatalf("err = %v, want the tripo_api_key refusal", err)
	}
}

func TestFakeShapeCatalogAddsFakeAssetHostOnlyToArtifactProviders(t *testing.T) {
	live, err := loadCatalogForShape("", upstreamShape{})
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse("http://fake-upstream:19000")
	fake, err := loadCatalogForShape("", upstreamShape{origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, offering := range fake.List("") {
		before, _ := live.Get(offering.ID)
		has := slices.Contains(offering.AssetHosts, fakeAssetHost)
		if slices.Contains(fakeAssetProviders, string(offering.ProviderID)) {
			checked++
			if !has {
				t.Errorf("%s lacks the fake asset host", offering.ID)
			}
			for _, host := range before.AssetHosts {
				if !slices.Contains(offering.AssetHosts, host) {
					t.Errorf("%s lost asset host %s", offering.ID, host)
				}
			}
		} else if has || !slices.Equal(offering.AssetHosts, before.AssetHosts) {
			t.Errorf("%s (%s) asset hosts changed", offering.ID, offering.ProviderID)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d artifact Offerings checked", checked)
	}
	for _, offering := range live.List("") {
		if slices.Contains(offering.AssetHosts, fakeAssetHost) {
			t.Fatalf("live Catalog %s accepts the fake asset host", offering.ID)
		}
	}
}

type recordedRoundTrip struct{ hosts *[]string }

func (r recordedRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	*r.hosts = append(*r.hosts, request.URL.Host)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
}

func TestFakeShapeTransportRoutesOnlyTheFakeAssetHost(t *testing.T) {
	fake := &url.URL{Scheme: "http", Host: "127.0.0.1:1"}
	decorate := fakeShapeHTTPTransport(fake)
	var hosts []string
	base := recordedRoundTrip{hosts: &hosts}
	if got := decorate("openai-images", base); got != http.RoundTripper(base) {
		t.Fatal("a connection without fake artifacts must keep its transport")
	}
	transport := decorate("tripo", base)
	request, _ := http.NewRequest(http.MethodGet, "https://real-assets.example/model.glb", nil)
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0] != "real-assets.example" {
		t.Fatalf("other hosts must keep the base transport: %v", hosts)
	}
	request, _ = http.NewRequest(http.MethodGet, "https://"+fakeAssetHost+"/model.glb", nil)
	// The fake origin refuses the connection; the point is that the base
	// transport never sees the fake asset host.
	_, _ = transport.RoundTrip(request)
	if len(hosts) != 1 {
		t.Fatalf("the fake asset host reached the base transport: %v", hosts)
	}
}

// A Tripo text-to-3d Task runs to SUCCEEDED against the fake upstream with the
// fake shape's Catalog and transport, the deployed equivalent of the #96 test
// stack. With the live Catalog the same artifact URL is refused: the asset
// host relaxation exists only in the fake shape.
func TestFakeShapeRunsTripoTextTo3DAgainstFakeUpstream(t *testing.T) {
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fakeURL, _ := url.Parse(server.URL)
	settings := map[string]string{
		"tripo_base_url": server.URL + "/tripo/v3",
		"tripo_api_key":  fakeUpstreamKeyPrefix + "test-key",
	}
	run := func(shape upstreamShape) generation.Task {
		taskCatalog, err := loadCatalogForShape("", shape)
		if err != nil {
			t.Fatal(err)
		}
		harness := newAcmeHarnessWithTransport(t, taskCatalog, settings, fakeShapeHTTPTransport(fakeURL))
		accepted := harness.submitApproved(t, generation.TextTo3D, "tripo/tripo-v3.1-full/text-to-3d",
			json.RawMessage(`{"prompt":"a plain cube","authorize_extra_credits":true}`), "fake-shape-tripo")
		return harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	}
	task := run(upstreamShape{origin: fakeURL})
	if task.State != generation.TaskSucceeded || len(task.Assets) == 0 {
		t.Fatalf("fake shape task state=%s assets=%d failure=%#v", task.State, len(task.Assets), task.Failure)
	}
	if live := run(upstreamShape{}); live.State == generation.TaskSucceeded {
		t.Fatal("the live Catalog accepted the fake asset host")
	}
}

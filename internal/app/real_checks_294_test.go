package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #294: two of the things the real checks of the game purposes showed (2026-11-12),
// run through the whole stack against the fake upstream (the real Catalog, the
// kernel resolving the source Task, the real adapters). The other two, the P2
// quad output that is an FBX and no GLB and the quad ratio of Meshy 7.1, are
// properties of the fake's delivery and of the Catalog's numbers, tested there.
//
//   - Meshy Rigging refuses finger bones for a model made with quad topology (HTTP
//     400 upstream); the Schema's source rule refuses the combination in Prepare,
//     before a grant, a Task or any request exists.
//   - A Hyper3D submission refused for the account's missing API subscription
//     fails the Task as an account failure with no HTTP status, in words that
//     name the subscription and not the key.

// fakeStackHarness starts the fake upstream (every provider), points the
// connections the settings name at it and returns the harness with a recorder of
// the bodies POSTed to the paths ending in one of suffixes.
func fakeStackHarness(t *testing.T, settings func(origin string) map[string]string, suffixes ...string) (*acmeHarness, *catalog.Catalog, *pathLog) {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost})
	if err != nil {
		t.Fatal(err)
	}
	log := &pathLog{posts: map[string][]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			log.mu.Lock()
			for _, suffix := range suffixes {
				if strings.HasSuffix(r.URL.Path, suffix) {
					log.posts[suffix] = append(log.posts[suffix], body)
				}
			}
			log.mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	fakeURL, _ := url.Parse(server.URL)
	taskCatalog, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	return newInputAwareHarness(t, taskCatalog, settings(server.URL), fakeShapeHTTPTransport(fakeURL)), taskCatalog, log
}

// newInputAwareHarness is newAcmeHarnessWithTransport with the input asset
// service wired: a step with an upload parameter next to its source task (the
// rigging step has both) cannot be prepared without one, and the Meshy adapter
// reads the source model through it.
func newInputAwareHarness(
	t *testing.T,
	taskCatalog *catalog.Catalog,
	settings map[string]string,
	transport func(string, http.RoundTripper) http.RoundTripper,
) *acmeHarness {
	t.Helper()
	root := t.TempDir()
	repository, err := sqlitetest.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	assetStore, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = assetStore.Close() })
	inputs, err := inputassets.NewWithConfig(repository, assetStore, inputassets.Config{ProviderAssetRoute: taskCatalog.Application().Runtime.HTTP.Routes.ProviderAsset})
	if err != nil {
		t.Fatal(err)
	}
	providerRegistry := providers.NewRegistry()
	if err := registerProvidersWithTransport(Config{ProviderMode: "live", RuntimeSettings: settings}, taskCatalog, providerRegistry,
		inputs, assetStore, discardLogger(), transport); err != nil {
		t.Fatal(err)
	}
	clock := &fakeAcmeClock{now: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	repository.SetClock(clock.Now)
	if _, err := repository.CreateSession(context.Background(), chat.Session{
		ID: "acme-session", OwnerID: "owner-acme", OwnerName: "test", Title: "acme", CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{
		Repository: repository, Catalog: taskCatalog, Providers: providerRegistry, Assets: assetStore, Inputs: inputs, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return &acmeHarness{module: module, repository: repository, clock: clock, settings: settings}
}

func TestMeshyRiggingRefusesFingersOnAQuadModelBeforePayment(t *testing.T) {
	harness, product, log := fakeStackHarness(t, func(origin string) map[string]string {
		return map[string]string{"meshy_base_url": origin + "/meshy", "meshy_api_key": fakeUpstreamKeyPrefix + "test-key"}
	}, "/rigging")
	// A Meshy 7.1 model the way the game character purpose asks for it, in the
	// given topology.
	generate := func(topology string) generation.Task {
		t.Helper()
		reason := "the game character purpose asks for it"
		accepted := submitStep(t, harness, generation.TextTo3D, "meshy/meshy-7.1/text-to-3d",
			`{"prompt":"a game character in an A pose","should_remesh":true,"topology":"`+topology+`","target_polycount":8000,"texture_resolution":"2k"}`,
			"rigging-source-"+topology, map[string]string{
				"should_remesh": reason, "topology": reason, "target_polycount": reason, "texture_resolution": reason,
			})
		task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
		if task.State != generation.TaskSucceeded {
			t.Fatalf("%s source state = %s, failure = %#v", topology, task.State, task.Failure)
		}
		return task
	}
	prepare := func(source generation.Task, input string) (generation.PreparedInput, error) {
		return harness.module.PrepareInput(context.Background(), generation.PrepareInputCommand{
			OwnerID: "owner-acme", SessionID: "acme-session", Capability: generation.Rigging,
			OfferingID: "meshy/processing/rigging", Input: json.RawMessage(`{"source_task":"` + string(source.ID) + `"` + input + `}`),
		})
	}

	quad := generate("quad")
	_, err := prepare(quad, `,"include_fingers":true`)
	refusal := generation.UserFacingError(err)
	if refusal == nil || refusal.Code != generation.ErrInvalidInput {
		t.Fatalf("finger bones on a quad model were not refused as invalid input: %v", err)
	}
	for _, want := range []string{"四边面拓扑", "包含手指骨骼", "这一步没有提交，也没有扣积分"} {
		if !strings.Contains(refusal.Detail, want) {
			t.Errorf("the refusal %q does not say %q", refusal.Detail, want)
		}
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
	for _, want := range []string{"quad topology", "Include finger bones", "no credits were spent"} {
		if !strings.Contains(english, want) {
			t.Errorf("the English refusal %q does not say %q", english, want)
		}
	}
	if strings.ContainsAny(english, "{}") || strings.IndexFunc(english, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 {
		t.Errorf("the English refusal is not English text: %q", english)
	}
	if requests := log.bodies("/rigging"); len(requests) != 0 {
		t.Fatalf("a refused combination reached the upstream: %v", requests)
	}

	// The same quad model rigs without fingers, and a triangle model keeps its
	// fingers: only the combination is refused.
	rigged := submitStep(t, harness, generation.Rigging, "meshy/processing/rigging",
		`{"source_task":"`+string(quad.ID)+`","include_fingers":false}`, "rigging-quad-plain", nil)
	if task := harness.runUntilTerminal(t, "owner-acme", rigged.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("quad rigging without fingers: state = %s, failure = %#v", task.State, task.Failure)
	}
	triangle := generate("triangle")
	fingers := submitStep(t, harness, generation.Rigging, "meshy/processing/rigging",
		`{"source_task":"`+string(triangle.ID)+`","include_fingers":true}`, "rigging-triangle-fingers", nil)
	if task := harness.runUntilTerminal(t, "owner-acme", fingers.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("triangle rigging with fingers: state = %s, failure = %#v", task.State, task.Failure)
	}
	requests := log.bodies("/rigging")
	if len(requests) != 2 || requests[0]["include_fingers"] != nil || requests[1]["include_fingers"] != true {
		t.Fatalf("rigging requests = %v, want one without fingers then one with", requests)
	}
}

func TestHyper3DSubscriptionRefusalFailsTheTaskAsAnAccountFailure(t *testing.T) {
	harness, product, _ := fakeStackHarness(t, func(origin string) map[string]string {
		return map[string]string{
			"hyper3d_base_url": origin + "/hyper3d/api/v2",
			"hyper3d_api_key":  fakeUpstreamKeyPrefix + string(fakeupstream.ScenarioNoActiveSubscription),
		}
	})
	accepted := submitStep(t, harness, generation.TextTo3D, "hyper3d/rodin-gen-2.5/text-to-3d",
		`{"prompt":"a carved oak chair","authorize_extra_credits":true}`, "hyper3d-no-subscription", nil)
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil {
		t.Fatalf("state = %s, failure = %#v, want a failed Task", task.State, task.Failure)
	}
	failure := task.Failure
	// An account failure that waiting does not mend, recorded with no HTTP status
	// (the refusal arrives in an HTTP 201 body), and not an authentication failure.
	if failure.Class != generation.FailureInsufficientBalance || failure.Retryable || failure.Ambiguous ||
		failure.UpstreamCode != "API_NO_ACTIVE_SUBSCRIPTION" || failure.UpstreamStatus != 0 {
		t.Fatalf("failure = %#v, want a definite insufficient_balance of API_NO_ACTIVE_SUBSCRIPTION with upstream status 0", failure)
	}

	// What the person and the Agent read names the subscription, not the key.
	application := product.Application()
	person := application.Content.Messages["failure."+string(failure.Class)]
	agent := generationtool.FailureMessage(application.Chat, task.Offering.CreditsMetered, failure)
	for name, text := range map[string]string{"the task's failure text": person, "the Agent's report": agent} {
		if !strings.Contains(text, "API 订阅") || strings.Contains(text, "身份验证") {
			t.Errorf("%s = %q: it should name the API subscription and not authentication", name, text)
		}
	}
	if agent != application.Chat.Presentation.Messages.GenerationFailedByClass[string(failure.Class)] {
		t.Errorf("the Agent's report = %q, want the Schema copy of the class", agent)
	}
}

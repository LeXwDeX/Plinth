package meshy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
)

// refineRefusingUpstream is the Meshy fake with the documented 429 (#504)
// selected for the first refusals refine creates. The fake answers the 429; the
// wrapper only chooses which requests ask for it, through the fake's own
// X-Fake-Scenario header.
type refineRefusingUpstream struct {
	mu           sync.Mutex
	server       *httptest.Server
	scenario     fakeupstream.Scenario
	refusals     int
	previewPosts int
	refinePosts  int
	// bodies are the create bodies the Adapter sent, in order (#542).
	bodies []map[string]any
}

func newRefineRefusingUpstream(t *testing.T, scenario fakeupstream.Scenario, refusals int) *refineRefusingUpstream {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{RetryAfter: 7 * time.Second}, meshyopenapiv1.Version(), meshyopenapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	upstream := &refineRefusingUpstream{scenario: scenario, refusals: refusals}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/openapi/v2/text-to-3d") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			upstream.mu.Lock()
			upstream.bodies = append(upstream.bodies, body)
			if body["mode"] == "refine" {
				upstream.refinePosts++
				if upstream.refusals > 0 {
					upstream.refusals--
					r.Header.Set(fakeupstream.ScenarioHeader, string(upstream.scenario))
				}
			} else {
				upstream.previewPosts++
			}
			upstream.mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *refineRefusingUpstream) createBodies() []map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]map[string]any(nil), u.bodies...)
}

func (u *refineRefusingUpstream) posts() (int, int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.previewPosts, u.refinePosts
}

func (u *refineRefusingUpstream) adapter(t *testing.T) *meshy.Adapter {
	t.Helper()
	return newMeshyAdapter(t, meshy.Config{
		BaseURL: u.server.URL + "/meshy", APIKey: "test", HTTPClient: u.server.Client(), AllowHTTPAssets: true,
	})
}

// observeUntilRefineSubmit polls the preview until the Adapter tries the refine
// create, and returns that observation.
func observeUntilRefineSubmit(
	t *testing.T,
	ctx context.Context,
	adapter *meshy.Adapter,
	upstream *refineRefusingUpstream,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	t.Helper()
	_, before := upstream.posts()
	for range 8 {
		observation, err := adapter.Observe(ctx, request)
		if _, after := upstream.posts(); after > before {
			return observation, err
		}
		if err != nil {
			t.Fatal(err)
		}
		request.Opaque = observation.Opaque
	}
	t.Fatal("the refine was never submitted")
	return generation.Observation{}, nil
}

func startRefineTask(t *testing.T, upstream *refineRefusingUpstream) (*meshy.Adapter, generation.ObserveRequest) {
	t.Helper()
	taskCatalog, offering := upgradeOffering(t, "meshy/meshy-7.1/text-to-3d", upstream.server)
	input := normalizedOfferingInput(t, taskCatalog, offering, `{"prompt":"a small robot"}`)
	adapter := upstream.adapter(t)
	started, err := adapter.Start(context.Background(), generation.StartRequest{
		TaskID: "task-1", AttemptID: "attempt-1", SubmissionKey: "submission-1", Offering: offering, Input: input,
	})
	if err != nil || started.Job == nil {
		t.Fatalf("start = %+v, %v", started, err)
	}
	return adapter, generation.ObserveRequest{
		TaskID: "task-1", AttemptID: "attempt-1", Offering: offering, Job: *started.Job, Input: input, Opaque: started.Opaque,
	}
}

func refineCheckpointFields(t *testing.T, opaque json.RawMessage) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(opaque, &fields); err != nil {
		t.Fatalf("opaque %s: %v", opaque, err)
	}
	return fields
}

// #504: an explicit 429 on the paid refine create is a refusal Meshy did not
// accept. The permit is settled not_dispatched, the intent is cleared and the
// next observation sends the same refine again, which then succeeds.
func TestTextTo3DRefineRefusedBy429IsResentAndSucceeds504(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario fakeupstream.Scenario
		wait     time.Duration
	}{
		// Retry-After (7 s here) wins for the requests-per-second limit.
		{"requests-per-second", fakeupstream.ScenarioRateLimit, 7 * time.Second},
		// The concurrency 429 carries no Retry-After: the Offering's retry
		// backoff (initial 30 s) applies.
		{"concurrency", fakeupstream.ScenarioConcurrencyLimit, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newRefineRefusingUpstream(t, tc.scenario, 1)
			adapter, request := startRefineTask(t, upstream)
			checkpoints := &meshyRecordingCheckpointer{}
			ctx := generation.WithDispatchCheckpointer(context.Background(), checkpoints)

			refused, err := observeUntilRefineSubmit(t, ctx, adapter, upstream, request)
			if err != nil {
				t.Fatalf("a refused refine must not fail the observation: %v", err)
			}
			if refused.Phase != generation.UpstreamRunning || refused.Failure != nil {
				t.Fatalf("refused = phase %s failure %+v", refused.Phase, refused.Failure)
			}
			if refused.Dispatch == nil || refused.Dispatch.Result != generation.DispatchResultNotDispatched {
				t.Fatalf("permit must be settled not_dispatched: %+v", refused.Dispatch)
			}
			fields := refineCheckpointFields(t, refused.Opaque)
			if fields["stage"] != nil || fields["refine_task_id"] != nil {
				t.Fatalf("the refine intent must be cleared: %s", refused.Opaque)
			}
			if refused.PollAfter != tc.wait {
				t.Fatalf("PollAfter = %v, want %v", refused.PollAfter, tc.wait)
			}
			if refused.ObservedAt.IsZero() || refused.DedupeKey == "" || refused.Job == nil || *refused.Job != request.Job {
				t.Fatalf("refused observation must be a fresh observation of the preview job: %+v", refused)
			}

			request.Opaque = refused.Opaque
			submitted, err := adapter.Observe(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if previews, refines := upstream.posts(); previews != 1 || refines != 2 {
				t.Fatalf("posts = preview %d refine %d, want the same refine sent again once", previews, refines)
			}
			if submitted.Dispatch == nil || submitted.Dispatch.Result != generation.DispatchResultDispatched ||
				submitted.DedupeKey == refused.DedupeKey {
				t.Fatalf("resend = %+v", submitted)
			}
			if id, _ := refineCheckpointFields(t, submitted.Opaque)["refine_task_id"].(string); id == "" {
				t.Fatalf("resend must record the refine task: %s", submitted.Opaque)
			}
			if checkpoints.count() != 2 {
				t.Fatalf("permits = %d, want one per refine create", checkpoints.count())
			}

			request.Opaque = submitted.Opaque
			for range 8 {
				observed, err := adapter.Observe(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				if observed.Phase == generation.UpstreamCompleted {
					if len(observed.Artifacts) == 0 {
						t.Fatal("completed refine must deliver artifacts")
					}
					if _, refines := upstream.posts(); refines != 2 {
						t.Fatalf("refine posts = %d after completion", refines)
					}
					return
				}
				if observed.Phase != generation.UpstreamRunning {
					t.Fatalf("refine observation = %+v", observed)
				}
				request.Opaque = observed.Opaque
			}
			t.Fatal("refine never completed")
		})
	}
}

// #504: the refusals are bounded by the Schema's refine_after_preview
// rate_limit_retries. When they are used up the task fails as rate limited,
// not as an unknown submission, and the permit is still settled.
func TestTextTo3DRefineRateLimitBudgetEndsRateLimited504(t *testing.T) {
	upstream := newRefineRefusingUpstream(t, fakeupstream.ScenarioRateLimit, 100)
	adapter, request := startRefineTask(t, upstream)
	retries := declaredRefineRateLimitRetries(t, request.Offering)
	if retries <= 0 {
		t.Fatalf("meshy-7.1 text-to-3d must declare refine_after_preview.rate_limit_retries, got %d", retries)
	}
	ctx := generation.WithDispatchCheckpointer(context.Background(), &meshyRecordingCheckpointer{})
	observation, err := observeUntilRefineSubmit(t, ctx, adapter, upstream, request)
	for range retries {
		if err != nil || observation.Phase != generation.UpstreamRunning {
			t.Fatalf("refusal within the budget = %+v, %v", observation, err)
		}
		request.Opaque = observation.Opaque
		observation, err = adapter.Observe(ctx, request)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, refines := upstream.posts(); refines != retries+1 {
		t.Fatalf("refine posts = %d, want %d", refines, retries+1)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil ||
		observation.Failure.Class != generation.FailureRateLimited || observation.Failure.Retryable ||
		!strings.Contains(observation.Failure.SafeDetail, "限流") || strings.Contains(observation.Failure.SafeDetail, "不明") {
		t.Fatalf("exhausted budget = %+v failure %+v", observation, observation.Failure)
	}
	if observation.Dispatch == nil || observation.Dispatch.Result != generation.DispatchResultNotDispatched {
		t.Fatalf("the last refused permit must be settled: %+v", observation.Dispatch)
	}
	if _, err := adapter.Observe(ctx, generation.ObserveRequest{
		TaskID: request.TaskID, AttemptID: request.AttemptID, Offering: request.Offering,
		Job: request.Job, Input: request.Input, Opaque: observation.Opaque,
	}); err != nil {
		t.Fatal(err)
	}
	if _, refines := upstream.posts(); refines != retries+1 {
		t.Fatalf("a spent budget must not post again: %d", refines)
	}
}

// #504: a 5xx on the refine create stays an unknown outcome: the error is
// ambiguous, the intent stays, and the next observation fails without posting.
func TestTextTo3DRefineServerErrorStaysUnknownAndIsNeverResent504(t *testing.T) {
	upstream := newRefineRefusingUpstream(t, fakeupstream.ScenarioServerError, 1)
	adapter, request := startRefineTask(t, upstream)
	checkpoints := &meshyRecordingCheckpointer{}
	ctx := generation.WithDispatchCheckpointer(context.Background(), checkpoints)
	_, err := observeUntilRefineSubmit(t, ctx, adapter, upstream, request)
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || !provider.Failure.Ambiguous ||
		provider.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("error = %v, want an ambiguous unknown submission", err)
	}
	// The kernel keeps the persisted intent as the opaque checkpoint.
	request.Opaque = json.RawMessage(`{"stage":"text-to-3d.refine","submission_key":"attempt-1:refine"}`)
	observation, err := adapter.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Phase != generation.UpstreamFailed || observation.Failure == nil ||
		!strings.Contains(observation.Failure.SafeDetail, "不明") {
		t.Fatalf("observation = %+v", observation)
	}
	if _, refines := upstream.posts(); refines != 1 {
		t.Fatalf("an unknown refine must not be posted again: %d", refines)
	}
}

func declaredRefineRateLimitRetries(t *testing.T, offering generation.Offering) int {
	t.Helper()
	var options struct {
		RefineAfterPreview struct {
			RateLimitRetries int `json:"rate_limit_retries"`
		} `json:"refine_after_preview"`
	}
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		t.Fatal(err)
	}
	return options.RefineAfterPreview.RateLimitRetries
}

// assetHostCatalog is the default Catalog with the fake's host allowed for the
// result downloads.
type assetHostCatalog struct {
	*catalog.Catalog
	host string
}

func (c assetHostCatalog) patch(offering generation.Offering) generation.Offering {
	offering.AssetHosts = []string{c.host}
	return offering
}

func (c assetHostCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	offering, ok := c.Catalog.Get(id)
	return c.patch(offering), ok
}

func (c assetHostCatalog) List(capability generation.Capability) []generation.Offering {
	offerings := c.Catalog.List(capability)
	for index := range offerings {
		offerings[index] = c.patch(offerings[index])
	}
	return offerings
}

func (c assetHostCatalog) Normalize(
	capability generation.Capability,
	id generation.OfferingID,
	raw json.RawMessage,
) (generation.Offering, json.RawMessage, error) {
	offering, normalized, err := c.Catalog.Normalize(capability, id, raw)
	return c.patch(offering), normalized, err
}

type refineTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *refineTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *refineTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// #504 through the real kernel and SQLite: the refused refine leaves the
// private checkpoint idle, the cleared intent is what the Attempt keeps, and
// the Task ends SUCCEEDED with the textured refine after two refusals.
func TestTextTo3DRefineRefusedTwiceSucceedsThroughTheKernel504(t *testing.T) {
	ctx := context.Background()
	upstream := newRefineRefusingUpstream(t, fakeupstream.ScenarioRateLimit, 2)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(upstream.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	clock := &refineTestClock{now: time.Now().UTC()}
	repository, err := sqlitetest.Open(filepath.Join(root, "refine.db"))
	if err != nil {
		t.Fatal(err)
	}
	repository.SetClock(clock.Now)
	store, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", upstream.adapter(t)); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{
		Conversions: generation.NoConversions, Repository: repository,
		Catalog: assetHostCatalog{Catalog: product, host: parsed.Hostname()}, Providers: registry,
		Assets: store, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", Title: "refine", CreatedAt: clock.Now(), UpdatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner", SessionID: "session", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"a small robot"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: repository, Prepared: repository}
	human := executiongrant.Principal{OwnerID: "owner", ActorID: "test-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: human, SessionID: "session", OfferingID: string(prepared.OfferingID),
		OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
		PreparedInputHash: prepared.InputHash, IdempotencyKey: "refine-504", OperationID: "refine-504",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Decide(ctx, human, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: "owner", SessionID: "session", PreparedInputID: prepared.ID,
		AuthorizationRef: grant.RequestID, OperationID: "refine-504", IdempotencyKey: "refine-504",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 40 {
		if _, err := module.ReconcileOnce(ctx, generation.CoordinatorConfig{WorkerID: "refine-504", LeaseFor: time.Minute, BatchSize: 1}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Minute)
		task, err := module.Get(ctx, "owner", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			if task.State != generation.TaskSucceeded {
				t.Fatalf("task = %s failure %+v", task.State, task.Failure)
			}
			if previews, refines := upstream.posts(); previews != 1 || refines != 3 {
				t.Fatalf("posts = preview %d refine %d, want 1 and 3", previews, refines)
			}
			return
		}
	}
	t.Fatal("task did not finish")
}

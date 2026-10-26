package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// switchableStore rejects every write while failing is set: the local store
// is read-only after the upstream already finished (#265 note 97279).
type switchableStore struct {
	generation.AssetStore
	mu      sync.Mutex
	failing bool
}

func (s *switchableStore) setFailing(value bool) {
	s.mu.Lock()
	s.failing = value
	s.mu.Unlock()
}

func (s *switchableStore) Put(ctx context.Context, write generation.AssetWrite) (generation.Asset, error) {
	s.mu.Lock()
	failing := s.failing
	s.mu.Unlock()
	if failing {
		return generation.Asset{}, errors.New("read-only storage")
	}
	return s.AssetStore.Put(ctx, write)
}

// countingPort counts upstream creates (Start) and status reads (Observe).
type countingPort struct {
	generation.ProviderPort
	mu       sync.Mutex
	starts   int
	observes int
	failNext bool
	// mediaType, when set, replaces the media type every download answers
	// with: a concrete type outside the contract fails the result for good.
	mediaType string
}

func (p *countingPort) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	body, metadata, err := p.ProviderPort.OpenAsset(ctx, request)
	p.mu.Lock()
	if err == nil && p.mediaType != "" {
		metadata.MediaType = p.mediaType
	}
	p.mu.Unlock()
	return body, metadata, err
}

func (p *countingPort) answerMediaType(value string) {
	p.mu.Lock()
	p.mediaType = value
	p.mu.Unlock()
}

func (p *countingPort) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	p.mu.Lock()
	p.starts++
	p.mu.Unlock()
	return p.ProviderPort.Start(ctx, request)
}

func (p *countingPort) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	p.mu.Lock()
	p.observes++
	fail := p.failNext
	p.mu.Unlock()
	if fail {
		return generation.Observation{Phase: generation.UpstreamFailed, Failure: &generation.Failure{Class: generation.FailureRejected, SafeDetail: "expired upstream"}}, nil
	}
	return p.ProviderPort.Observe(ctx, request)
}

func (p *countingPort) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts, p.observes
}

func newRefetchHarness(t *testing.T) (*harness, *switchableStore, *countingPort) {
	t.Helper()
	port := &countingPort{ProviderPort: mock.New()}
	var store *switchableStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		store = &switchableStore{AssetStore: deps.Assets}
		deps.Assets = store
	})
	return h, store, port
}

// failAfterUpstream submits a credits-metered Meshy text-to-3d Task that the
// mock upstream completes while every local write fails.
func failAfterUpstream(t *testing.T, h *harness, store *switchableStore) generation.Task {
	t.Helper()
	store.setFailing(true)
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"stool"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureIntegrity {
		t.Fatalf("source = %s %#v", task.State, task.Failure)
	}
	store.setFailing(false)
	return task
}

func TestUpstreamRefetchRecoversWithoutNewUpstreamCreate(t *testing.T) {
	h, store, port := newRefetchHarness(t)
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)
	startsBefore, _ := port.counts()
	if startsBefore != 1 {
		t.Fatalf("source starts = %d", startsBefore)
	}

	view, err := h.module.RefetchEligibility(ctx, "owner-a", source.ID)
	if err != nil || !view.Eligible || view.Reason != "" || view.AvailableUntil == nil || view.RefetchTaskID != "" {
		t.Fatalf("eligibility = %#v, %v", view, err)
	}
	// #330: the provider finished and billed the run; the view says how much
	// (the frozen estimate the session total counts) and no refetch failed yet.
	if want := source.Offering.EstimateTotalCredits(source.Input); !source.Offering.CreditsMetered || want <= 0 || view.ChargedCredits != want || view.FailedRefetches != 0 {
		t.Fatalf("charged = %d (want %d), failed = %d", view.ChargedCredits, want, view.FailedRefetches)
	}
	if _, err := h.module.RefetchEligibility(ctx, "owner-b", source.ID); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("other owner eligibility = %v", err)
	}
	if _, err := h.module.RefetchUpstreamResult(ctx, "owner-b", source.ID, generation.RefetchByOwner); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("other owner refetch = %v", err)
	}

	result, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil || result.TaskID == "" || result.Replayed {
		t.Fatalf("refetch = %#v, %v", result, err)
	}
	// Asking again while it runs is a no-op that names the same Task.
	again, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByAgent)
	if err != nil || again.TaskID != result.TaskID || !again.Replayed || !again.Refetch.Eligible || again.Refetch.Reason != "" {
		t.Fatalf("second refetch = %#v, %v", again, err)
	}

	recovered := h.runUntilTerminal(t, "owner-a", result.TaskID)
	if recovered.State != generation.TaskSucceeded || len(recovered.Assets) == 0 {
		t.Fatalf("refetch task = %s %#v assets=%d", recovered.State, recovered.Failure, len(recovered.Assets))
	}
	if recovered.Capability != source.Capability || recovered.SessionID != source.SessionID || recovered.Offering.ID != source.Offering.ID {
		t.Fatalf("refetch identity = %#v", recovered)
	}
	if recovered.Offering.CreditsMetered || recovered.Offering.EstimatedCredits != 0 || recovered.Offering.Cost != nil || recovered.Offering.PostSuccess != nil {
		t.Fatalf("refetch Offering keeps billing: %#v", recovered.Offering)
	}
	if starts, observes := port.counts(); starts != startsBefore || observes == 0 {
		t.Fatalf("starts=%d observes=%d: a refetch must only read the accepted job", starts, observes)
	}

	// The source keeps its failure, Attempt and job; nothing is revived.
	after, err := h.module.Get(ctx, "owner-a", source.ID)
	if err != nil || after.State != generation.TaskFailed || after.Version != source.Version || len(after.Assets) != 0 {
		t.Fatalf("source changed: %#v, %v", after, err)
	}
	provenance, err := h.module.TaskRecoveryProvenance(ctx, recovered)
	if err != nil || provenance == nil || provenance.Kind != generation.RecoveryKindUpstreamRefetch || provenance.SourceTaskID != source.ID {
		t.Fatalf("provenance = %#v, %v", provenance, err)
	}
	sources, err := h.module.SessionRefetchSources(ctx, "owner-a", source.SessionID)
	if err != nil || sources[recovered.ID] != source.ID {
		t.Fatalf("session refetches = %#v, %v", sources, err)
	}

	// #330 second follow-ups, observation 4: once a refetch delivered there
	// is nothing left to fetch. Eligibility says so with one judgment that
	// every client reads: not eligible, reason delivered, naming the Task.
	delivered, err := h.module.RefetchEligibility(ctx, "owner-a", source.ID)
	if err != nil || delivered.Eligible || delivered.Reason != generation.RefetchDelivered ||
		delivered.RefetchTaskID != recovered.ID || delivered.RefetchState != generation.TaskSucceeded {
		t.Fatalf("eligibility after success = %#v, %v", delivered, err)
	}
	// After success, another request returns the recovered Task; no new one.
	done, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil || done.TaskID != recovered.ID || !done.Replayed || done.Refetch.RefetchState != generation.TaskSucceeded || done.Refetch.Eligible {
		t.Fatalf("refetch after success = %#v, %v", done, err)
	}
	events, err := h.module.Events(ctx, "owner-a", recovered.ID, 0, 100)
	if err != nil || countEvents(events, "provider.refetch") != 1 || countEvents(events, "provider.start") != 0 {
		t.Fatalf("refetch events = %v, %v", eventTypes(events), err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(events[0].Data, &submitted); err != nil || submitted["refetch_of"] != string(source.ID) {
		t.Fatalf("submitted event = %s", events[0].Data)
	}
}

func TestUpstreamRefetchFailureAllowsAnotherRefetch(t *testing.T) {
	h, store, port := newRefetchHarness(t)
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)
	port.mu.Lock()
	port.failNext = true
	port.mu.Unlock()
	first, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByAgent)
	if err != nil {
		t.Fatal(err)
	}
	failed := h.runUntilTerminal(t, "owner-a", first.TaskID)
	if failed.State != generation.TaskFailed || failed.Failure == nil || failed.Failure.SafeDetail == "" {
		t.Fatalf("failed refetch = %s %#v", failed.State, failed.Failure)
	}
	if failed.Offering.CreditsMetered {
		t.Fatal("a failed refetch must not look like another charged failure")
	}
	// #330: the failed refetch is counted, so clients can offer the paid retry.
	if view, err := h.module.RefetchEligibility(ctx, "owner-a", source.ID); err != nil || !view.Eligible || view.FailedRefetches != 1 || view.ChargedCredits <= 0 {
		t.Fatalf("eligibility after a failed refetch = %#v, %v", view, err)
	}
	port.mu.Lock()
	port.failNext = false
	port.mu.Unlock()
	second, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil || second.TaskID == first.TaskID || second.Replayed {
		t.Fatalf("second refetch = %#v, %v", second, err)
	}
	if task := h.runUntilTerminal(t, "owner-a", second.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("second refetch = %s %#v", task.State, task.Failure)
	}
	if starts, _ := port.counts(); starts != 1 {
		t.Fatalf("starts = %d", starts)
	}
}

// A recovered result can feed processing that references its upstream job
// (Meshy remesh by task id): the job is the failed source's, which delivered it.
func TestUpstreamRefetchResultFeedsJobBasedProcessing(t *testing.T) {
	h, store, _ := newRefetchHarness(t)
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)
	result, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	if task := h.runUntilTerminal(t, "owner-a", result.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("refetch = %s %#v", task.State, task.Failure)
	}
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", SessionID: source.SessionID, Capability: generation.Remesh,
		OfferingID: "meshy/processing/remesh", Input: []byte(`{"source_task":"` + string(result.TaskID) + `","target_formats":["glb"]}`),
	})
	if err != nil {
		t.Fatalf("remesh of the recovered result = %v", err)
	}
	if task := h.runUntilTerminal(t, "owner-a", accepted.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("remesh = %s %#v", task.State, task.Failure)
	}
}

// A refetch whose own download fails again is not a new source: only the
// original failed Task can be fetched again, and that stays possible.
func TestUpstreamRefetchThatFailsToStoreIsNotItselfRefetchable(t *testing.T) {
	h, store, _ := newRefetchHarness(t)
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)
	store.setFailing(true)
	first, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	failed := h.runUntilTerminal(t, "owner-a", first.TaskID)
	store.setFailing(false)
	if failed.State != generation.TaskFailed || failed.Failure == nil || failed.Failure.Class != generation.FailureIntegrity {
		t.Fatalf("refetch = %s %#v", failed.State, failed.Failure)
	}
	view, err := h.module.RefetchEligibility(ctx, "owner-a", failed.ID)
	if err != nil || view.Eligible || view.Reason != generation.RefetchNotApplicable {
		t.Fatalf("refetch task eligibility = %#v, %v", view, err)
	}
	view, err = h.module.RefetchEligibility(ctx, "owner-a", source.ID)
	if err != nil || !view.Eligible || view.RefetchTaskID != "" {
		t.Fatalf("source eligibility after a failed refetch = %#v, %v", view, err)
	}
}

func TestUpstreamRefetchEligibilityReasons(t *testing.T) {
	h, store, _ := newRefetchHarness(t)
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)

	// Meshy keeps API results for 72 hours (Schema result_refetch).
	h.clock.Advance(73 * time.Hour)
	view, err := h.module.RefetchEligibility(ctx, "owner-a", source.ID)
	if err != nil || view.Eligible || view.Reason != generation.RefetchExpired {
		t.Fatalf("expired eligibility = %#v, %v", view, err)
	}
	if _, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("expired refetch = %v", err)
	}

	// A succeeded Task has nothing to fetch again.
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"chair"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	view, err = h.module.RefetchEligibility(ctx, "owner-a", accepted.TaskID)
	if err != nil || view.Eligible || view.Reason != generation.RefetchNotApplicable {
		t.Fatalf("succeeded eligibility = %#v, %v", view, err)
	}

	// A synchronous image result was one-shot: there is no job to read again.
	store.setFailing(true)
	image, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"lamp"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := h.runUntilTerminal(t, "owner-a", image.TaskID)
	store.setFailing(false)
	if failed.State != generation.TaskFailed {
		t.Fatalf("image task = %s", failed.State)
	}
	view, err = h.module.RefetchEligibility(ctx, "owner-a", image.TaskID)
	if err != nil || view.Eligible || view.Reason != generation.RefetchNotSupported {
		t.Fatalf("image eligibility = %#v, %v", view, err)
	}
}

// failDeliveredResult submits a job-based Meshy Task (an image Offering whose
// contract names image/*) whose upstream completes but whose downloads answer
// a media type outside the frozen contract: a non-retryable integrity
// failure, as for task_70b839e5 (#295 note 97369), whose frozen snapshot
// predates the per-format MIME declarations.
func failDeliveredResult(t *testing.T, h *harness, port *countingPort) generation.Task {
	t.Helper()
	port.answerMediaType("text/html")
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "meshy/nano-banana-pro/text-to-image", Input: []byte(`{"prompt":"stool"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureIntegrity || task.Failure.Retryable {
		t.Fatalf("source = %s %#v", task.State, task.Failure)
	}
	return task
}

// A refetch that fails with exactly the source's deterministic failure closes
// the source: neither the task detail nor the Agent offers another free
// refetch that could only fail again. A refetch that failed differently (a
// retryable store error) leaves it open.
func TestUpstreamRefetchRepeatingTheSourceFailureClosesIt(t *testing.T) {
	h, store, port := newRefetchHarness(t)
	ctx := context.Background()
	source := failDeliveredResult(t, h, port)

	// A different, retryable failure (the store rejects the write) does not
	// prove the result can never be stored: another refetch stays offered.
	port.answerMediaType("")
	store.setFailing(true)
	first, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	if task := h.runUntilTerminal(t, "owner-a", first.TaskID); task.State != generation.TaskFailed || task.Failure == nil || !task.Failure.Retryable {
		t.Fatalf("first refetch = %s %#v", task.State, task.Failure)
	}
	store.setFailing(false)
	view, err := h.module.RefetchEligibility(ctx, "owner-a", source.ID)
	if err != nil || !view.Eligible || view.Reason != "" || view.FailureClass != "" {
		t.Fatalf("eligibility after a different failure = %#v, %v", view, err)
	}

	// The same deterministic failure again: the source is no longer offered.
	port.answerMediaType("text/html")
	second, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByAgent)
	if err != nil || second.TaskID == first.TaskID {
		t.Fatalf("second refetch = %#v, %v", second, err)
	}
	repeated := h.runUntilTerminal(t, "owner-a", second.TaskID)
	if repeated.State != generation.TaskFailed || repeated.Failure == nil || *repeated.Failure != *source.Failure {
		t.Fatalf("second refetch = %s %#v, source %#v", repeated.State, repeated.Failure, source.Failure)
	}
	view, err = h.module.RefetchEligibility(ctx, "owner-a", source.ID)
	if err != nil || view.Eligible || view.Reason != generation.RefetchRepeatedFailure ||
		view.FailureClass != generation.FailureIntegrity || view.RefetchTaskID != "" {
		t.Fatalf("eligibility after the same failure = %#v, %v", view, err)
	}
	// Asking again is refused with that reason and creates no Task.
	refused, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByAgent)
	if !generation.IsCode(err, generation.ErrConflict) || refused.TaskID != "" || refused.Refetch.Reason != generation.RefetchRepeatedFailure {
		t.Fatalf("refetch after the same failure = %#v, %v", refused, err)
	}
	if body, err := json.Marshal(refused.Refetch); err != nil || !strings.Contains(string(body), `"reason":"repeated_failure"`) ||
		!strings.Contains(string(body), `"failure_class":"integrity_failure"`) || strings.Contains(string(body), "text/html") {
		t.Fatalf("public eligibility = %s, %v", body, err)
	}
	// The #294 guard input is unchanged: both stay failed refetches of the
	// source, which answer nothing.
	sources, err := h.module.SessionRefetchSources(ctx, "owner-a", source.SessionID)
	if err != nil || len(sources) != 2 || sources[first.TaskID] != source.ID || sources[second.TaskID] != source.ID {
		t.Fatalf("session refetches = %#v, %v", sources, err)
	}
	if starts, _ := port.counts(); starts != 1 {
		t.Fatalf("starts = %d", starts)
	}
	after, err := h.module.Get(ctx, "owner-a", source.ID)
	if err != nil || after.State != generation.TaskFailed || after.Version != source.Version {
		t.Fatalf("source changed: %#v, %v", after, err)
	}
}

// A non-deterministic refetch failure (the upstream no longer answers the
// result) never closes the source, however often it repeats.
func TestUpstreamRefetchTransientFailuresKeepItOpen(t *testing.T) {
	h, _, port := newRefetchHarness(t)
	ctx := context.Background()
	source := failDeliveredResult(t, h, port)
	port.mu.Lock()
	port.failNext = true
	port.mu.Unlock()
	for range 2 {
		result, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
		if err != nil {
			t.Fatal(err)
		}
		if task := h.runUntilTerminal(t, "owner-a", result.TaskID); task.State != generation.TaskFailed {
			t.Fatalf("refetch = %s", task.State)
		}
		view, err := h.module.RefetchEligibility(ctx, "owner-a", source.ID)
		if err != nil || !view.Eligible {
			t.Fatalf("eligibility after an unavailable result = %#v, %v", view, err)
		}
	}
}

// Every request that names a refetch Task is audited, repeats included.
func TestUpstreamRefetchRepeatsAreLogged(t *testing.T) {
	var logs bytes.Buffer
	port := &countingPort{ProviderPort: mock.New()}
	var store *switchableStore
	h := newConfiguredHarness(t, port, nil, func(deps *generation.Dependencies) {
		store = &switchableStore{AssetStore: deps.Assets}
		deps.Assets = store
		deps.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	})
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)
	first, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByAgent)
	if err != nil || !again.Replayed {
		t.Fatalf("repeat = %#v, %v", again, err)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["event"] == "task.upstream_refetch" {
			lines = append(lines, entry)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("audit lines = %v", lines)
	}
	for index, want := range []struct {
		by       string
		replayed bool
	}{{"owner", false}, {"agent", true}} {
		line := lines[index]
		if line["task_id"] != string(first.TaskID) || line["source_task_id"] != string(source.ID) || line["source_attempt_id"] == "" ||
			line["owner_id"] != "owner-a" || line["requested_by"] != want.by || line["replayed"] != want.replayed {
			t.Fatalf("audit line %d = %v", index, line)
		}
	}
}

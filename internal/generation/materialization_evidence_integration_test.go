package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

const privateMaterializationSentinel = "https://upstream.invalid/private?signature=materialization-test"

func TestMaterializationFailureEvidenceIncludesTerminalPass(t *testing.T) {
	for _, budget := range []int{1, 3} {
		t.Run("budget_"+strconv.Itoa(budget), func(t *testing.T) {
			store := newRejectingMaterializationStore(t, -1)
			h := newConfiguredHarness(t, completeWithBrokenOptional{}, store, materializationBudget(budget))
			accepted := submitMaterializationEvidenceTask(t, h)
			h.reconcile(t)
			before, err := h.repository.Get(context.Background(), "owner-a", accepted.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
			assertMaterializationIdentity(t, before, task)
			if task.State != generation.TaskFailed || task.MaterializeFailures != budget || len(task.Assets) != 0 ||
				task.Failure == nil || task.Failure.Class != generation.FailureIntegrity || !task.Failure.Retryable {
				t.Fatalf("terminal task state=%s failures=%d assets=%d failure=%v", task.State, task.MaterializeFailures, len(task.Assets), task.Failure)
			}
			events := materializationEvidenceEvents(t, h.repository, accepted.TaskID)
			assertMaterializationFailureEvents(t, events, budget, budget, budget-1, 1)
			if strings.Contains(task.Failure.SafeDetail, privateMaterializationSentinel) {
				t.Fatal("raw store error reached Task failure")
			}
			// The stored (non-public) forensic fields name role/format and the
			// allowlisted storage reason, and never echo the raw store error.
			if task.Failure.UpstreamCode != "asset_store_rejected" ||
				task.Failure.UpstreamExcerpt != "role=model format=glb stage=unknown reason=unknown errno=unknown" {
				t.Fatalf("forensics = %q %q", task.Failure.UpstreamCode, task.Failure.UpstreamExcerpt)
			}
			reopened := reopenMaterializationEvidenceRepository(t, h)
			stored, err := reopened.Get(context.Background(), "owner-a", accepted.TaskID)
			if err != nil || stored.State != generation.TaskFailed || stored.MaterializeFailures != budget {
				t.Fatalf("reopened task state=%s failures=%d err=%v", stored.State, stored.MaterializeFailures, err)
			}
			assertReopenedMaterializationIdentity(t, h, reopened, task)
			assertMaterializationFailureEvents(t, materializationEvidenceEvents(t, reopened, accepted.TaskID), budget, budget, budget-1, 1)
		})
	}
}

func TestMaterializationRetrySuccessRetainsSafeFailureEvidence(t *testing.T) {
	store := newRejectingMaterializationStore(t, 1)
	h := newConfiguredHarness(t, completeWithBrokenOptional{}, store, materializationBudget(3))
	accepted := submitMaterializationEvidenceTask(t, h)
	h.reconcile(t)
	before, err := h.repository.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	retrying, err := h.repository.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil || retrying.State != generation.TaskMaterializing || retrying.MaterializeFailures != 1 || len(retrying.Assets) != 0 {
		t.Fatalf("retry state=%s failures=%d assets=%d err=%v", retrying.State, retrying.MaterializeFailures, len(retrying.Assets), err)
	}
	assertMaterializationFailureEvents(t, materializationEvidenceEvents(t, h.repository, accepted.TaskID), 1, 1, 1, 0)
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	assertMaterializationIdentity(t, before, task)
	if task.State != generation.TaskSucceeded || task.MaterializeFailures != 1 || len(task.Assets) != 6 {
		t.Fatalf("successful retry state=%s failures=%d assets=%d", task.State, task.MaterializeFailures, len(task.Assets))
	}
	assertMaterializationFailureEvents(t, materializationEvidenceEvents(t, h.repository, accepted.TaskID), 1, 2, 1, 0)
	reopened := reopenMaterializationEvidenceRepository(t, h)
	assertReopenedMaterializationIdentity(t, h, reopened, task)
	assertMaterializationFailureEvents(t, materializationEvidenceEvents(t, reopened, accepted.TaskID), 1, 2, 1, 0)
}

func TestMaterializationNonRetryableFailureCountsOnePass(t *testing.T) {
	h := newConfiguredHarness(t, unexpectedMaterializationMediaAdapter{ProviderPort: completeWithBrokenOptional{}}, nil, func(deps *generation.Dependencies) {
		// Meshy's default model slots permit any declared media type. Tighten
		// this fixture's GLB slot to exercise the generic nonretryable branch.
		deps.Catalog = materializationBudgetCatalog{Catalog: deps.Catalog, budget: 3, glbMediaType: "model/gltf-binary"}
	})
	accepted := submitMaterializationEvidenceTask(t, h)
	h.reconcile(t)
	before, err := h.repository.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	assertMaterializationIdentity(t, before, task)
	if task.State != generation.TaskFailed || task.MaterializeFailures != 1 || task.Failure == nil || task.Failure.Retryable {
		t.Fatalf("nonretryable state=%s failures=%d failure=%v", task.State, task.MaterializeFailures, task.Failure)
	}
	events := materializationEvidenceEvents(t, h.repository, accepted.TaskID)
	if countEvents(events, "materialization.artifact_skipped") != 2 ||
		countEvents(events, "materialization.failed") != 1 || countEvents(events, "materialization.retry_scheduled") != 0 {
		t.Fatalf("nonretryable events=%v", eventTypes(events))
	}
	reopened := reopenMaterializationEvidenceRepository(t, h)
	assertReopenedMaterializationIdentity(t, h, reopened, task)
	if got := materializationEvidenceEvents(t, reopened, accepted.TaskID); len(got) != len(events) {
		t.Fatalf("nonretryable reopened events=%v", eventTypes(got))
	}
}

func TestMaterializationEvidenceCommitConflictIsAtomic(t *testing.T) {
	for _, budget := range []int{1, 3} {
		for _, conflict := range []string{"version", "lease"} {
			t.Run("budget_"+strconv.Itoa(budget)+"_"+conflict, func(t *testing.T) {
				var repository *conflictingMaterializationRepository
				configure := materializationBudget(budget)
				h := newConfiguredHarness(t, completeWithBrokenOptional{}, newRejectingMaterializationStore(t, -1), func(deps *generation.Dependencies) {
					configure(deps)
					repository = &conflictingMaterializationRepository{Repository: deps.Repository.(*sqlite.Repository), conflict: conflict}
					deps.Repository = repository
				})
				accepted := submitMaterializationEvidenceTask(t, h)
				h.reconcile(t)
				before, err := h.repository.Load(context.Background(), accepted.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				beforeEvents := materializationEvidenceEvents(t, h.repository, accepted.TaskID)
				h.reconcile(t)
				after, err := h.repository.Load(context.Background(), accepted.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if !repository.rejected || after.Task.State != before.Task.State || after.Task.Version != before.Task.Version ||
					after.Task.MaterializeFailures != 0 || after.CurrentAttempt.ID != before.CurrentAttempt.ID ||
					after.CurrentAttempt.State != before.CurrentAttempt.State || after.CurrentAttempt.Failure != nil {
					t.Fatalf("conflicting commit changed state: rejected=%t before=%s after=%s failures=%d", repository.rejected, before.Task.State, after.Task.State, after.Task.MaterializeFailures)
				}
				if len(materializationEvidenceEvents(t, h.repository, accepted.TaskID)) != len(beforeEvents) {
					t.Fatal("failure events committed separately from rejected state/count")
				}
				// The old claim remains leased. Once it expires, one new pass can
				// commit all evidence; the rejected pass must leave no phantom count.
				h.clock.Advance(time.Minute)
				h.reconcile(t)
				committed, err := h.repository.Get(context.Background(), "owner-a", accepted.TaskID)
				if err != nil || committed.MaterializeFailures != 1 {
					t.Fatalf("valid commit failures=%d err=%v", committed.MaterializeFailures, err)
				}
				assertMaterializationIdentity(t, before.Task, committed)
				terminal, retries := 0, 1
				if budget == 1 {
					terminal, retries = 1, 0
				}
				assertMaterializationFailureEvents(t, materializationEvidenceEvents(t, h.repository, accepted.TaskID), 1, 1, retries, terminal)
				reopened := reopenMaterializationEvidenceRepository(t, h)
				assertReopenedMaterializationIdentity(t, h, reopened, committed)
				assertMaterializationFailureEvents(t, materializationEvidenceEvents(t, reopened, accepted.TaskID), 1, 1, retries, terminal)
			})
		}
	}
}

func submitMaterializationEvidenceTask(t *testing.T, h *harness) generation.Accepted {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d",
		IdempotencyKey: "materialization-evidence", Input: json.RawMessage(`{"prompt":"materialization evidence"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func materializationBudget(budget int) func(*generation.Dependencies) {
	return func(deps *generation.Dependencies) {
		deps.Catalog = materializationBudgetCatalog{Catalog: deps.Catalog, budget: budget}
	}
}

type materializationBudgetCatalog struct {
	generation.Catalog
	budget       int
	glbMediaType string
}

func (c materializationBudgetCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	offering, ok := c.Catalog.Get(id)
	return c.configure(offering), ok
}

func (c materializationBudgetCatalog) Normalize(cap generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	offering, normalized, err := c.Catalog.Normalize(cap, id, input)
	return c.configure(offering), normalized, err
}

func (c materializationBudgetCatalog) configure(offering generation.Offering) generation.Offering {
	offering.MaterializeAttempts = c.budget
	if c.glbMediaType != "" {
		offering.Outputs = append([]generation.AssetRequirement(nil), offering.Outputs...)
		for i := range offering.Outputs {
			if offering.Outputs[i].Role == "model" && offering.Outputs[i].Variant == "glb" {
				offering.Outputs[i].MediaTypes = []string{c.glbMediaType}
			}
		}
	}
	return offering
}

type rejectingMaterializationStore struct {
	generation.AssetStore
	mu        sync.Mutex
	remaining int
}

type unexpectedMaterializationMediaAdapter struct{ generation.ProviderPort }

func (a unexpectedMaterializationMediaAdapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	body, metadata, err := a.ProviderPort.OpenAsset(ctx, request)
	if err == nil && request.Claim.Role == "model" && request.Claim.Variant == "glb" {
		metadata.MediaType = "text/html"
	}
	return body, metadata, err
}

func newRejectingMaterializationStore(t *testing.T, failures int) *rejectingMaterializationStore {
	t.Helper()
	store, err := local.New(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	return &rejectingMaterializationStore{AssetStore: store, remaining: failures}
}

func (s *rejectingMaterializationStore) Put(ctx context.Context, write generation.AssetWrite) (generation.Asset, error) {
	s.mu.Lock()
	fail := write.Role == "model" && write.Variant == "glb" && s.remaining != 0
	if fail && s.remaining > 0 {
		s.remaining--
	}
	s.mu.Unlock()
	if fail {
		return generation.Asset{}, errors.New("raw storage error " + privateMaterializationSentinel)
	}
	return s.AssetStore.Put(ctx, write)
}

type conflictingMaterializationRepository struct {
	*sqlite.Repository
	conflict string
	rejected bool
}

func (r *conflictingMaterializationRepository) Commit(ctx context.Context, commit generation.Commit) error {
	if !r.rejected && (hasEvent(commit.Events, "materialization.retry_scheduled") || hasEvent(commit.Events, "materialization.failed")) {
		r.rejected = true
		if r.conflict == "version" {
			commit.ExpectedVersion++
		} else {
			commit.LeaseToken = "stale-lease"
		}
	}
	return r.Repository.Commit(ctx, commit)
}

func materializationEvidenceEvents(t *testing.T, repository *sqlite.Repository, id generation.TaskID) []generation.Event {
	t.Helper()
	events, err := repository.Events(context.Background(), "owner-a", id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func assertMaterializationFailureEvents(t *testing.T, events []generation.Event, modelFailures, thumbnailFailures, retries, terminal int) {
	t.Helper()
	models, thumbnails := 0, 0
	for _, event := range events {
		if event.Type != "materialization.artifact_skipped" {
			continue
		}
		var data map[string]string
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(event.Data), privateMaterializationSentinel) || strings.Contains(string(event.Data), "broken://thumbnail") {
			t.Fatal("private error or locator reached skipped event")
		}
		if data["role"] == "model" && data["variant"] == "glb" && data["reason"] == "store" {
			models++
		} else if data["role"] == "thumbnail" && data["reason"] == "download" {
			thumbnails++
		} else {
			t.Fatalf("unexpected skipped metadata=%v", data)
		}
	}
	if models != modelFailures || thumbnails != thumbnailFailures ||
		countEvents(events, "materialization.retry_scheduled") != retries || countEvents(events, "materialization.failed") != terminal ||
		countEvents(events, "attempt.started") != 1 || countEvents(events, "task.submitted") != 1 {
		t.Fatalf("materialization evidence model=%d thumbnail=%d retries=%d terminal=%d events=%v", models, thumbnails, retries, terminal, eventTypes(events))
	}
	// Internal variant/claim identity helps audit format failures. The unchanged
	// actual public Schema permits only role/reason, never those private fields.
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	allowed := taskCatalog.Application().Runtime.TaskEvents.PublicEventWhitelist()["materialization.artifact_skipped"]
	if len(allowed) != 2 {
		t.Fatalf("public skipped whitelist changed: %v", allowed)
	}
	for _, field := range []string{"role", "reason"} {
		if _, ok := allowed[field]; !ok {
			t.Fatalf("missing safe public skipped field %s", field)
		}
	}
}

func assertMaterializationIdentity(t *testing.T, before, after generation.Task) {
	t.Helper()
	if before.ID != after.ID || before.OwnerID != after.OwnerID || before.Offering.ID != after.Offering.ID ||
		before.SessionID != after.SessionID || before.Capability != after.Capability ||
		before.Offering.Revision != after.Offering.Revision || before.IdempotencyKey != after.IdempotencyKey ||
		before.RequestHash != after.RequestHash || !bytes.Equal(before.Input, after.Input) ||
		before.CurrentAttemptID == "" || before.CurrentAttemptID != after.CurrentAttemptID {
		t.Fatal("materialization changed accepted input, task or attempt identity")
	}
}

func assertReopenedMaterializationIdentity(t *testing.T, h *harness, repository *sqlite.Repository, task generation.Task) {
	t.Helper()
	aggregate, err := repository.Load(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMaterializationIdentity(t, task, aggregate.Task)
	if aggregate.Task.State != task.State || aggregate.Task.MaterializeFailures != task.MaterializeFailures || aggregate.Task.Version != task.Version {
		t.Fatal("reopen changed materialization state, count or version")
	}
	if aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.ID != task.CurrentAttemptID ||
		aggregate.CurrentAttempt.TaskID != task.ID || aggregate.CurrentAttempt.Ordinal != 1 {
		t.Fatal("reopen changed the original public Task or paid Attempt")
	}
	page, err := repository.List(context.Background(), generation.OwnTasks(task.OwnerID), generation.ListQuery{Limit: 10})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != task.ID {
		t.Fatalf("reopen public Task identity count=%d err=%v", len(page.Tasks), err)
	}
	h.approvalMu.Lock()
	defer h.approvalMu.Unlock()
	if len(h.approvals) != 1 {
		t.Fatal("fixture must have exactly one independently approved input")
	}
	for _, approval := range h.approvals {
		grant, err := repository.GetExecutionGrant(context.Background(), string(task.OwnerID), approval.grant.RequestID)
		if err != nil || grant.Status != executiongrant.Bound || grant.TaskID != string(task.ID) ||
			grant.RequestID != approval.grant.RequestID || grant.Identity != approval.grant.Identity {
			t.Fatal("reopen changed the original human grant or Task binding")
		}
	}
}

func reopenMaterializationEvidenceRepository(t *testing.T, h *harness) *sqlite.Repository {
	t.Helper()
	if err := h.module.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repository.SetClock(h.clock.Now)
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

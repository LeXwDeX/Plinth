package sqlite_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func testConsentRequestID(t *testing.T, store *agentparams.Store, scope string) string {
	t.Helper()
	var requestID string
	if err := json.Unmarshal(store.Get(scope)[agentparams.ConsentRequestIDKey], &requestID); err != nil || requestID == "" {
		t.Fatalf("missing consent request ID: %q, %v", requestID, err)
	}
	return requestID
}

func TestConsentDecisionBindsExactPendingRequest(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "consent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	store := agentparams.NewPersistentStore(repository, nil)
	scope := agentparams.ScopeKey("owner-a", "session-a")
	snapshotA := json.RawMessage(`{"ai_model":"model-a"}`)
	snapshotB := json.RawMessage(`{"ai_model":"model-b"}`)
	if err := store.BeginConsent(scope, json.RawMessage(`{"reason":"first"}`), snapshotA); err != nil {
		t.Fatal(err)
	}
	requestA := testConsentRequestID(t, store, scope)
	if err := store.BeginConsent(scope, json.RawMessage(`{"reason":"second"}`), snapshotB); err != nil {
		t.Fatal(err)
	}
	requestB := testConsentRequestID(t, store, scope)
	if requestA == requestB {
		t.Fatal("replaced consent request reused identity")
	}
	for _, stale := range []string{"", requestA} {
		if decided, err := store.DecideConsent(scope, stale, true); err != nil || decided {
			t.Fatalf("stale decision %q changed active request: decided=%v err=%v", stale, decided, err)
		}
	}
	if _, allowed, err := store.ClaimConsent(scope, "call-b", "", snapshotB); err != nil || allowed {
		t.Fatalf("stale approval made new request claimable: allowed=%v err=%v", allowed, err)
	}
	if decided, err := store.DecideConsent(scope, requestB, true); err != nil || !decided {
		t.Fatalf("current request approval: decided=%v err=%v", decided, err)
	}
	if decided, err := store.DecideConsent(scope, requestB, false); err != nil || decided {
		t.Fatalf("decision was overwritten: decided=%v err=%v", decided, err)
	}
	if _, allowed, err := store.ClaimConsent(scope, "call-b", "", snapshotB); err != nil || !allowed {
		t.Fatalf("current request not claimable: allowed=%v err=%v", allowed, err)
	}
}

func TestConsentClaimPersistsAndConsumesExactlyOneCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consent.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	scope := agentparams.ScopeKey("owner-a", "session-a")
	store := agentparams.NewPersistentStore(repository, nil)
	snapshot := json.RawMessage(`{"ai_model":"model-a","params_hash":"abc"}`)
	request := json.RawMessage(`{"reason":"generate"}`)
	if err := store.BeginConsent(scope, request, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, allowed, err := store.ClaimConsent(scope, "call-a", "", snapshot); err != nil || allowed {
		t.Fatalf("unapproved consent claim: allowed=%v err=%v", allowed, err)
	}
	if decided, err := store.DecideConsent(scope, testConsentRequestID(t, store, scope), true); err != nil || !decided {
		t.Fatalf("human approval: decided=%v err=%v", decided, err)
	}
	if _, allowed, err := store.ClaimConsent(scope, "call-a", "", json.RawMessage(`{"ai_model":"model-b","params_hash":"abc"}`)); err != nil || allowed {
		t.Fatalf("changed target claim: allowed=%v err=%v", allowed, err)
	}
	if _, allowed, err := store.ClaimConsent(scope, "call-a", "", snapshot); err != nil || !allowed {
		t.Fatalf("first claim: allowed=%v err=%v", allowed, err)
	}
	if _, allowed, err := store.ClaimConsent(scope, "call-b", "", snapshot); err != nil || allowed {
		t.Fatalf("second paid call reused approval: allowed=%v err=%v", allowed, err)
	}
	if err := store.BeginConsent(scope, request, snapshot); err == nil {
		t.Fatal("new consent request erased an unresolved claim")
	}
	if decided, err := store.DecideConsent(scope, testConsentRequestID(t, store, scope), true); err != nil || decided {
		t.Fatalf("already claimed consent was decided again: decided=%v err=%v", decided, err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := agentparams.NewPersistentStore(reopened, nil)
	if _, allowed, err := restarted.ClaimConsent(scope, "call-b", "", snapshot); err != nil || allowed {
		t.Fatalf("approval reused after restart: allowed=%v err=%v", allowed, err)
	}
	if err := restarted.AcceptConsent(scope, "call-a", "task-a"); err != nil {
		t.Fatal(err)
	}
	if state := restarted.Get(scope); state[agentparams.ConsentPendingKey] != nil || state[agentparams.ConsentDecisionKey] != nil {
		t.Fatalf("accepted task left visible approval: %#v", state)
	}
	if taskID, allowed, err := restarted.ClaimConsent(scope, "call-a", "", snapshot); err != nil || !allowed || taskID != "task-a" {
		t.Fatalf("same call did not recover task: task=%q allowed=%v err=%v", taskID, allowed, err)
	}
	if _, allowed, err := restarted.ClaimConsent(scope, "call-b", "", snapshot); err != nil || allowed {
		t.Fatalf("accepted task reused approval: allowed=%v err=%v", allowed, err)
	}
	if err := restarted.BeginConsent(scope, request, snapshot); err != nil {
		t.Fatal(err)
	}
	if decided, err := restarted.DecideConsent(scope, testConsentRequestID(t, restarted, scope), true); err != nil || !decided {
		t.Fatalf("new human approval: decided=%v err=%v", decided, err)
	}
	if _, allowed, err := restarted.ClaimConsent(scope, "call-b", "", snapshot); err != nil || !allowed {
		t.Fatalf("new approval did not allow next call: allowed=%v err=%v", allowed, err)
	}
}

func TestConsentRecoversCommittedTaskWhenAcceptanceWriteWasLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consent.db")
	repository, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "owner-a", Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store := agentparams.NewPersistentStore(repository, nil)
	scope := agentparams.ScopeKey("owner-a", "session-a")
	snapshot := json.RawMessage(`{"ai_model":"model-a","params_hash":""}`)
	if err := store.BeginConsent(scope, json.RawMessage(`{"reason":"paid"}`), snapshot); err != nil {
		t.Fatal(err)
	}
	if decided, err := store.DecideConsent(scope, testConsentRequestID(t, store, scope), true); err != nil || !decided {
		t.Fatalf("approve: decided=%v err=%v", decided, err)
	}
	key := "chat:session-a:call-a"
	if _, allowed, err := store.ClaimConsent(scope, "call-a", key, snapshot); err != nil || !allowed {
		t.Fatalf("claim: allowed=%v err=%v", allowed, err)
	}
	task := generation.Task{
		ID: "task-a", OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
		Offering: generation.Offering{ID: "offering-a"}, Input: json.RawMessage(`{}`),
		RequestHash: "request-a", IdempotencyKey: key, State: generation.TaskQueued,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	event := generation.Event{TaskID: task.ID, Type: "task.submitted", Data: json.RawMessage(`{}`), CreatedAt: now}
	task = approveCreateFixture(t, repository, task)
	event.TaskID = task.ID
	if _, _, err := repository.Create(ctx, task, event, 0); err != nil {
		t.Fatal(err)
	}
	// AcceptAgentConsent is intentionally skipped to model a crash after
	// Submit committed but before the chat observation was persisted.
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := agentparams.NewPersistentStore(reopened, nil)
	if taskID, allowed, err := restarted.ClaimConsent(scope, "call-a", key, snapshot); err != nil || !allowed || taskID != "task-a" {
		t.Fatalf("task recovery: task=%q allowed=%v err=%v", taskID, allowed, err)
	}
	if _, allowed, err := restarted.ClaimConsent(scope, "call-b", "chat:session-a:call-b", snapshot); err != nil || allowed {
		t.Fatalf("new call reused lost approval: allowed=%v err=%v", allowed, err)
	}
}

func TestConsentStorageFailureFailsClosed(t *testing.T) {
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "consent.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := agentparams.NewPersistentStore(repository, nil)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, allowed, err := store.ClaimConsent(
		agentparams.ScopeKey("owner-a", "session-a"), "call-a", "",
		json.RawMessage(`{"ai_model":"model-a","params_hash":""}`),
	); err == nil || allowed {
		t.Fatalf("closed database allowed paid call: allowed=%v err=%v", allowed, err)
	}
}

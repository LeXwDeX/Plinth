package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func detachFixture(t *testing.T, r *sqlite.Repository, session, call string) (generationtool.Operation, executiongrant.Grant) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, found, _ := r.Session(ctx, "owner-a", session); !found {
		if _, err := r.CreateSession(ctx, chat.Session{ID: session, OwnerID: "owner-a",
			OwnerName: "owner", Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	turn := "turn-" + call
	if _, _, err := r.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-" + call, SessionID: session, Role: "user", Content: "go", CreatedAt: now},
		chat.Message{ID: turn, SessionID: session, Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: session, Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: session, Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: session, AssistantMessageID: turn,
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	prepared := generation.PreparedInput{ID: generation.PreparedInputID("prepared-" + call), OwnerID: "owner-a",
		SessionID: session, OfferingID: "offering-a", OfferingRevision: "revision-a",
		InputHash: "hash-" + call, NormalizedInput: []byte(`{}`), CreatedAt: now}
	if err := r.CreatePreparedInput(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	op, err := r.InsertAgentOperation(ctx, generationtool.Operation{OwnerID: "owner-a", SessionID: session,
		AssistantMessageID: turn, CallID: call, ToolName: "create_model_3d", CommandHash: "command-" + call,
		IdempotencyKey: "key-" + call, PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
		OfferingID: "offering-a", OfferingRevision: "revision-a"})
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: r, Prepared: r}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: "owner-a", ActorID: "agent", Source: executiongrant.AgentPending},
		SessionID: session, OfferingID: "offering-a", OfferingRevision: "revision-a",
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: op.IdempotencyKey, OperationID: op.OperationID, TriggerTurnID: turn})
	if err != nil {
		t.Fatal(err)
	}
	if op, err = r.AttachAgentGrantRequest(ctx, op, grant.RequestID); err != nil {
		t.Fatal(err)
	}
	return op, grant
}

func TestDetachOnlyLandsWhileTheGrantIsPendingAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "detach.db")
	r, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pending, pendingGrant := detachFixture(t, r, "session-a", "call-pending")
	decided, decidedGrant := detachFixture(t, r, "session-c", "call-decided")
	human := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	if _, err := (executiongrant.Service{Grants: r, Prepared: r}).Decide(ctx, human, decidedGrant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}

	// A decision that committed first wins: the turn must continue in-turn.
	if _, detached, err := r.DetachAgentOperation(ctx, decided); err != nil || detached {
		t.Fatalf("detached an operation whose grant was decided: %v %v", detached, err)
	}
	stored, detached, err := r.DetachAgentOperation(ctx, pending)
	if err != nil || !detached || !stored.Detached {
		t.Fatalf("detach pending: %+v %v %v", stored, detached, err)
	}
	if again, detached, err := r.DetachAgentOperation(ctx, pending); err != nil || !detached || !again.Detached {
		t.Fatalf("detach is not idempotent: %+v %v %v", again, detached, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	listed, err := r.ListPendingDetached(ctx, "owner-a", "session-a", 10, time.Time{})
	if err != nil || len(listed) != 1 || listed[0].GrantRequestID != pendingGrant.RequestID {
		t.Fatalf("detached list after restart = %+v err=%v", listed, err)
	}
	if foreign, err := r.ListPendingDetached(ctx, "owner-b", "session-a", 10, time.Time{}); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign owner lists %+v err=%v", foreign, err)
	}
	if other, err := r.ListPendingDetached(ctx, "owner-a", "session-b", 10, time.Time{}); err != nil || len(other) != 0 {
		t.Fatalf("other session lists %+v err=%v", other, err)
	}
	if _, err := r.LoadAgentOperationByGrant(ctx, "owner-b", pendingGrant.RequestID); !errors.Is(err, generationtool.ErrOperationNotFound) {
		t.Fatalf("foreign owner loaded the operation by grant: %v", err)
	}
	// The policy marker is sticky and idempotent.
	if err := r.MarkAgentPolicyEvaluated(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkAgentPolicyEvaluated(ctx, pending); err != nil {
		t.Fatal(err)
	}
	// Only decided operations are unsettled work for the sweep; once settled
	// they are gone from it.
	if _, err := (executiongrant.Service{Grants: r, Prepared: r}).Decide(ctx, human, pendingGrant.RequestID, executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	loaded, err := r.LoadAgentOperationByGrant(ctx, "owner-a", pendingGrant.RequestID)
	if err != nil || !loaded.PolicyEvaluated || !loaded.Detached || loaded.Settled {
		t.Fatalf("loaded = %+v err=%v", loaded, err)
	}
	unsettled, err := r.ListUnsettledDetached(ctx, 0, 10)
	if err != nil || len(unsettled) != 1 || unsettled[0].GrantRequestID != pendingGrant.RequestID {
		t.Fatalf("unsettled = %+v err=%v", unsettled, err)
	}
	if err := r.SettleAgentOperation(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	if unsettled, err = r.ListUnsettledDetached(ctx, 0, 10); err != nil || len(unsettled) != 0 {
		t.Fatalf("unsettled after settle = %+v err=%v", unsettled, err)
	}
}

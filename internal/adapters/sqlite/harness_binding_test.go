package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

func TestHarnessOperationPersistsBindingAndRejectsRebinding(t *testing.T) {
	r := openGrantTestRepo(t)
	ctx := context.Background()
	binding := harness.DefaultSelection("owner-a", "session-a").Binding
	candidate := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn", CallID: "call", ToolName: "generate", CommandHash: "command", IdempotencyKey: "key", PreparedInputID: generation.PreparedInputID("prepared-a"), PreparedInputHash: "hash-a", OfferingID: "provider/model/action", OfferingRevision: "rev-1", Harness: binding}
	first, err := r.InsertAgentOperation(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := r.LoadAgentOperation(ctx, "owner-a", "session-a", "turn", "call")
	if err != nil || loaded.Harness != binding {
		t.Fatalf("load %+v %v", loaded, err)
	}
	replay, err := r.InsertAgentOperation(ctx, candidate)
	if err != nil || replay.OperationID != first.OperationID {
		t.Fatalf("replay %+v %v", replay, err)
	}
	candidate.Harness = changeHarness(t, r).Binding
	if _, err := r.InsertAgentOperation(ctx, candidate); !errors.Is(err, generationtool.ErrOperationConflict) {
		t.Fatalf("operation rebound %v", err)
	}
	candidate.CallID = "bad"
	candidate.Harness = harness.Binding{Revision: 1}
	if _, err := r.InsertAgentOperation(ctx, candidate); !errors.Is(err, generationtool.ErrOperationConflict) {
		t.Fatalf("malformed binding %v", err)
	}
}

func TestHarnessGrantPersistsChangedPendingBinding(t *testing.T) {
	r := openGrantTestRepo(t)
	ctx := context.Background()
	identity := grantIdentity()
	identity.Harness = harness.DefaultSelection(identity.OwnerID, identity.SessionID).Binding
	first, err := r.PreparePending(ctx, identity, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Harness != identity.Harness {
		t.Fatal("binding not persisted")
	}
	changed := identity
	changed.Harness = changeHarness(t, r).Binding
	if _, err := r.PreparePending(ctx, changed, executiongrant.HumanBrowser, ""); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatal("same key rebound", err)
	}
	changed.IdempotencyKey = "updated-key"
	second, err := r.PreparePending(ctx, changed, executiongrant.HumanBrowser, "")
	if err != nil || second.Harness != changed.Harness || second.RequestID == first.RequestID {
		t.Fatalf("supersede %+v %v", second, err)
	}
	malformed := changed
	malformed.Harness = harness.Binding{Revision: 1}
	if malformed.Valid() {
		t.Fatal("malformed identity valid")
	}
	if _, err := r.PreparePending(ctx, malformed, executiongrant.HumanBrowser, ""); !errors.Is(err, executiongrant.ErrInvalid) {
		t.Fatal("malformed grant", err)
	}
}

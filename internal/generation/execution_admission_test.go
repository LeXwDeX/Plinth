package generation_test

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func TestNewTaskRequiresExactHumanExecutionGrant(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	_, err := h.module.Submit(ctx, generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"paint"}`),
		IdempotencyKey: "bare-new-task",
	})
	if !generation.IsCode(err, generation.ErrForbidden) {
		t.Fatalf("bare Submit accepted new Task: %v", err)
	}
	now := time.Now().UTC()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"paint"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "human-a", Source: executiongrant.HumanBrowser}
	grant, err := service.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: "session-a", OfferingID: string(prepared.OfferingID),
		OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
		PreparedInputHash: prepared.InputHash, IdempotencyKey: "paid-task-key", OperationID: "operation-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := generation.SubmitPreparedCommand{OwnerID: "owner-a", SessionID: "session-a", PreparedInputID: prepared.ID,
		IdempotencyKey: "paid-task-key", OperationID: "operation-a", AuthorizationRef: grant.RequestID}
	if _, err := h.module.SubmitPrepared(ctx, cmd); !generation.IsCode(err, generation.ErrForbidden) {
		t.Fatalf("pending grant accepted: %v", err)
	}
	if _, err := service.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, cmd)
	if err != nil || accepted.Replayed {
		t.Fatalf("approved submit: %+v %v", accepted, err)
	}
	replayed, err := h.module.SubmitPrepared(ctx, cmd)
	if err != nil || !replayed.Replayed || replayed.TaskID != accepted.TaskID {
		t.Fatalf("same operation replay: %+v %v", replayed, err)
	}
	wrong := cmd
	wrong.OperationID = "other-operation"
	if _, err := h.module.SubmitPrepared(ctx, wrong); !generation.IsCode(err, generation.ErrForbidden) {
		t.Fatalf("other operation replayed approved Task: %v", err)
	}
}

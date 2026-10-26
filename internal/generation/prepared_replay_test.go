package generation_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func TestVerifyPreparedTaskReplayNeverSubmitsAndChecksFrozenGrant(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	now := h.clock.Now()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "replay", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	prepare := func(prompt string) generation.PreparedInput {
		t.Helper()
		prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
			OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
			OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"` + prompt + `"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	prepared := prepare("original")
	command := authorizePrepared(t, ctx, h.repository, prepared, "readonly-replay")
	if _, err := h.module.VerifyPreparedTaskReplay(ctx, command); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("unsubmitted replay = %v", err)
	}
	if _, err := h.module.LookupByIdempotency(ctx, command.OwnerID, command.IdempotencyKey); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("verification created a Task: %v", err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || accepted.Replayed {
		t.Fatalf("first actual submit = %#v, %v", accepted, err)
	}
	base, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	drift := preparedModuleWith(t, h.repository, driftCatalog{Catalog: base, drift: true}, h.inputs)
	recovered, err := drift.VerifyPreparedTaskReplay(ctx, command)
	if err != nil || recovered.ID != accepted.TaskID {
		t.Fatalf("read-only recovery after catalog drift = %#v, %v", recovered, err)
	}
	other := prepare("different input")
	for _, change := range []struct {
		name string
		edit func(*generation.SubmitPreparedCommand)
		code generation.ErrorCode
	}{
		{"input", func(c *generation.SubmitPreparedCommand) { c.PreparedInputID = other.ID }, generation.ErrConflict},
		{"session", func(c *generation.SubmitPreparedCommand) { c.SessionID = "different-session" }, generation.ErrConflict},
		{"owner", func(c *generation.SubmitPreparedCommand) { c.OwnerID = "other-owner" }, generation.ErrNotFound},
		{"grant", func(c *generation.SubmitPreparedCommand) { c.AuthorizationRef = "different-grant" }, generation.ErrForbidden},
		{"operation", func(c *generation.SubmitPreparedCommand) { c.OperationID = "different-operation" }, generation.ErrForbidden},
		{"step", func(c *generation.SubmitPreparedCommand) { c.PlanStepID = "different-step" }, generation.ErrForbidden},
		{"missing grant", func(c *generation.SubmitPreparedCommand) { c.AuthorizationRef = "" }, generation.ErrForbidden},
		{"missing task", func(c *generation.SubmitPreparedCommand) { c.IdempotencyKey = "never-submitted" }, generation.ErrNotFound},
	} {
		t.Run(change.name, func(t *testing.T) {
			invalid := command
			change.edit(&invalid)
			if _, err := h.module.VerifyPreparedTaskReplay(ctx, invalid); !generation.IsCode(err, change.code) {
				t.Fatalf("replay mismatch = %v, want %s", err, change.code)
			}
		})
	}
	if _, err := h.module.LookupByIdempotency(ctx, "owner-a", "never-submitted"); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("missing-key verification created a Task: %v", err)
	}
}

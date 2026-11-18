package generation_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// TestSubmissionGateBlocksNewTaskWithoutCreatingOne pins the kernel paid-gate
// fallback (#328 §2.5.5): a refused new admission creates nothing and spends
// no grant, and the same prepared command succeeds once the gate allows again.
func TestSubmissionGateBlocksNewTaskWithoutCreatingOne(t *testing.T) {
	ctx := context.Background()
	var blocked atomic.Bool
	var calls atomic.Int64
	h := newConfiguredHarness(t, mock.New(), nil, func(dependencies *generation.Dependencies) {
		dependencies.SubmissionGate = func(_ context.Context, ownerID, sessionID string) error {
			calls.Add(1)
			if ownerID != "owner-a" || sessionID != "session-a" {
				return &generation.SubmissionBlockedError{Reason: "wrong-identity"}
			}
			if blocked.Load() {
				return &generation.SubmissionBlockedError{Reason: "user"}
			}
			return nil
		}
	})
	h.ensureSession(t, ctx, h.repository, h.clock, "owner-a", "session-a")
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
		OfferingID: "meshy/nano-banana-pro/text-to-image", Input: json.RawMessage(`{"prompt":"paint"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	command := authorizePrepared(t, ctx, h.repository, prepared, "gate-blocked")

	blocked.Store(true)
	_, err = h.module.SubmitPrepared(ctx, command)
	if !errors.Is(err, generation.ErrSubmissionBlocked) {
		t.Fatalf("blocked submit error = %v; want ErrSubmissionBlocked", err)
	}
	var refusal *generation.SubmissionBlockedError
	if !errors.As(err, &refusal) || refusal.Reason != "user" {
		t.Fatalf("blocked refusal = %#v", refusal)
	}
	if generation.IsCode(err, generation.ErrUnavailable) {
		t.Fatal("gate refusal reported as a storage failure")
	}
	page, err := h.repository.List(ctx, generation.OwnTasks("owner-a"), generation.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Tasks) != 0 {
		t.Fatalf("blocked submit created %d Tasks", page.Total)
	}

	blocked.Store(false)
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || accepted.Replayed {
		t.Fatalf("resumed submit = %+v, %v", accepted, err)
	}

	// The committed Task is an idempotent replay: a paused gate must not
	// refuse it, and it is not consulted at all.
	blocked.Store(true)
	before := calls.Load()
	replayed, err := h.module.SubmitPrepared(ctx, command)
	if err != nil || !replayed.Replayed || replayed.TaskID != accepted.TaskID {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	if calls.Load() != before {
		t.Fatal("gate consulted for an idempotent replay")
	}
}

// TestSubmissionGateAllowsExistingTaskReplay pins the cheap Submit path: an
// existing Task for the same key is returned before the gate, so a pause cannot
// strand a reconciliation replay.
func TestSubmissionGateAllowsExistingTaskReplay(t *testing.T) {
	ctx := context.Background()
	var blocked atomic.Bool
	var calls atomic.Int64
	h := newConfiguredHarness(t, mock.New(), nil, func(dependencies *generation.Dependencies) {
		dependencies.SubmissionGate = func(context.Context, string, string) error {
			calls.Add(1)
			if blocked.Load() {
				return &generation.SubmissionBlockedError{Reason: "presence"}
			}
			return nil
		}
	})
	h.ensureSession(t, ctx, h.repository, h.clock, "owner-a", "session-a")
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: "session-a", Capability: generation.TextToImage,
		OfferingID: "meshy/nano-banana-pro/text-to-image", Input: json.RawMessage(`{"prompt":"paint"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	command := authorizePrepared(t, ctx, h.repository, prepared, "gate-replay")
	accepted, err := h.module.SubmitPrepared(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	// The same admission through the legacy Submit entry point.
	submit := generation.SubmitCommand{
		OwnerID: prepared.OwnerID, SessionID: prepared.SessionID, Capability: prepared.Capability,
		OfferingID: prepared.OfferingID, Input: prepared.NormalizedInput,
		IdempotencyKey: command.IdempotencyKey,
		Admission: &generation.ExecutionAdmission{
			RequestID: command.AuthorizationRef, PreparedInputID: prepared.ID,
			PreparedInputHash: prepared.InputHash, OperationID: command.OperationID,
			PlanStepID: command.PlanStepID, Harness: command.Harness,
		},
	}
	blocked.Store(true)
	before := calls.Load()
	replayed, err := h.module.Submit(ctx, submit)
	if err != nil || !replayed.Replayed || replayed.TaskID != accepted.TaskID {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	if calls.Load() != before {
		t.Fatal("gate consulted for an existing Task")
	}
}

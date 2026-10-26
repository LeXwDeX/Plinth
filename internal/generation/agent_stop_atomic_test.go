package generation_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type stoppedSubmissionInputs struct {
	generation.InputAssetResolver
	block   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *stoppedSubmissionInputs) Open(ctx context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	if r.block.Load() {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.InputAssetResolver.Open(ctx, asset)
}

// Both ordinary Agent generation and SkillRun use an AgentPending grant. Stop
// must win over a submission paused after its last turn check but before the
// Task/Grant write transaction, even when the submission context stays live.
func TestAgentStopBeforePaidAdmissionRejectsTaskAfterAssetValidation(t *testing.T) {
	for _, tc := range []struct {
		name, planStep string
	}{
		{name: "generation-tool"},
		{name: "skill-step", planStep: "skill_step_front"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, mock.New(), nil)
			now := h.clock.Now()
			const owner, session, turn = "owner-a", "session-a", "assistant-a"
			if _, err := h.repository.CreateSession(ctx, chat.Session{ID: session, OwnerID: owner,
				OwnerName: "Owner", Title: "stop", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := h.repository.StartTurn(ctx, owner,
				chat.Message{ID: "user-a", SessionID: session, Role: "user", Content: "process", CreatedAt: now},
				chat.Message{ID: turn, SessionID: session, Role: "assistant", Content: "pending", CreatedAt: now},
				chat.Event{SessionID: session, Data: map[string]any{"type": "message.created"}, CreatedAt: now},
				chat.Stream{SessionID: session, Status: "active"},
				chat.Turn{OwnerID: owner, SessionID: session, AssistantMessageID: turn,
					State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			asset, err := h.inputs.Upload(ctx, owner, bytes.NewReader([]byte("glTF source")), "model/gltf-binary", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			inputs := &stoppedSubmissionInputs{InputAssetResolver: h.inputs,
				entered: make(chan struct{}), release: make(chan struct{})}
			module := preparedModuleWith(t, h.repository, product, inputs)
			prepared, err := module.PrepareInput(ctx, generation.PrepareInputCommand{
				OwnerID: owner, SessionID: session, Capability: generation.Rigging,
				OfferingID: "meshy/processing/rigging",
				Input:      []byte(`{"source_asset":"` + string(asset.ID) + `"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
			principal := executiongrant.Principal{OwnerID: owner, ActorID: "agent:" + turn,
				Source: executiongrant.AgentPending}
			key := "stop-admission-" + tc.name
			operation := "stop-operation-" + tc.name
			grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
				Principal: principal, SessionID: session, OfferingID: string(prepared.OfferingID),
				OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
				PreparedInputHash: prepared.InputHash, IdempotencyKey: key,
				OperationID: operation, PlanStepID: tc.planStep, TriggerTurnID: turn,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := grants.Decide(ctx, executiongrant.Principal{OwnerID: owner, ActorID: owner,
				Source: executiongrant.HumanBrowser}, grant.RequestID, executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
			inputs.block.Store(true)
			done := make(chan error, 1)
			go func() {
				_, submitErr := module.SubmitPrepared(context.Background(), generation.SubmitPreparedCommand{
					OwnerID: owner, SessionID: session, PreparedInputID: prepared.ID,
					IdempotencyKey: key, OperationID: operation, PlanStepID: tc.planStep,
					AuthorizationRef: grant.RequestID,
				})
				done <- submitErr
			}()
			select {
			case <-inputs.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("submission did not reach asset validation")
			}
			stopped, active, err := h.repository.RequestTurnStop(ctx, owner, session,
				[]string{"running"}, now.Add(time.Second))
			if err != nil || !active || !stopped.StopRequested || stopped.AssistantMessageID != turn {
				t.Fatalf("stop before admission = %+v active=%v err=%v", stopped, active, err)
			}
			close(inputs.release)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stopped turn created a paid Task")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("submission did not finish after input release")
			}
			if _, err := h.repository.GetByIdempotency(ctx, owner, key); !errors.Is(err, generation.RepositoryNotFound) {
				t.Fatalf("stopped submission persisted a Task: %v", err)
			}
			stored, err := h.repository.GetExecutionGrant(ctx, owner, grant.RequestID)
			if err != nil || stored.Status != executiongrant.Approved || stored.TaskID != "" {
				t.Fatalf("stopped submission consumed grant: %+v err=%v", stored, err)
			}
		})
	}
}

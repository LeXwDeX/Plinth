package generation

import (
	"context"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// PREPARING is a private continuation of the one admitted Attempt. It requires
// a durable first-dispatch receipt, never a fabricated upstream Job. The kernel
// does not inspect the provider's token or other Opaque payload semantics.
// Receipt authenticity is checked centrally before applyObservation persists
// this entry, preserving the existing non-mutating stale-receipt path.
func validPreparationEntry(attempt *Attempt, obs Observation, port ProviderPort, checkpoints *dispatchCheckpointer) bool {
	if attempt == nil || attempt.State != AttemptSubmitting || attempt.UpstreamJob != nil || attempt.LastObservationPhase != "" {
		return false
	}
	if _, ok := port.(ProviderPreparationResumer); !ok {
		return false
	}
	return validateObservation(obs) == nil && checkpoints != nil && obs.Dispatch != nil && obs.Dispatch.Sequence == 1 && obs.Dispatch.Result == DispatchResultDispatched
}
func (m *Module) invalidPreparation(ctx context.Context, lease Lease, attempt *Attempt) error {
	return m.finishFailed(ctx, lease, attempt, &Failure{Class: FailureProtocol, SafeDetail: "上游返回了不可识别的准备阶段状态"}, "attempt.invalid_preparation", nil)
}

func (m *Module) resumePreparation(ctx context.Context, lease Lease) error {
	task := lease.Task
	attempt := lease.CurrentAttempt
	if attempt == nil || attempt.State != AttemptRunning || attempt.LastObservationPhase != UpstreamPreparing || attempt.UpstreamJob != nil {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	port, ok := m.providers.Provider(task.Offering)
	if !ok {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	resumer, ok := port.(ProviderPreparationResumer)
	if !ok {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	if m.jobTimeout(task) > 0 && m.clock.Now().Sub(attempt.StartedAt) > m.jobTimeout(task) {
		return m.handleAttemptFailure(ctx, lease, attempt, &Failure{Class: FailureTimedOut, SafeDetail: "上游任务超过供给项允许的最长执行时间"}, nil)
	}
	callCtx, cancel := m.withOfferingTimeout(ctx, task.Offering.Timeout)
	defer cancel()
	checkpoints := m.dispatchCheckpointerFor(&lease, attempt)
	if checkpoints == nil {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	callCtx = WithDispatchCheckpointer(callCtx, checkpoints)
	obs, err := resumer.ResumePreparation(callCtx, PreparationRequest{
		TaskID:        task.ID,
		AttemptID:     attempt.ID,
		SubmissionKey: attempt.SubmissionKey,
		OwnerID:       task.OwnerID,
		Offering:      task.Offering,
		Input:         schema.WireInput(task.Offering, task.Input),
		Opaque:        attempt.Opaque,
	})
	if err != nil {
		// Resume can issue a non-idempotent mutation. A timeout or unknown response
		// must take M2, never the retryable read-only Observe rescheduling path.
		failure := providerFailure(err, true)
		return m.handleAttemptFailure(context.WithoutCancel(ctx), lease, attempt, failure, nil)
	}
	if validateObservation(obs) != nil {
		return m.invalidPreparation(ctx, lease, attempt)
	}
	switch obs.Phase {
	case UpstreamAccepted:
		if len(obs.Artifacts) != 0 || len(obs.ResourceUpdates) != 0 || obs.Failure != nil || obs.Dispatch == nil || obs.Dispatch.Sequence != 2 || obs.Dispatch.Result != DispatchResultDispatched {
			return m.invalidPreparation(ctx, lease, attempt)
		}
	case UpstreamFailed, UpstreamCanceled:
		// Explicit terminal rejections keep the existing receipt settlement path.
	default:
		return m.invalidPreparation(ctx, lease, attempt)
	}
	return m.applyObservation(ctx, lease, attempt, obs, "provider.preparation", checkpoints)
}

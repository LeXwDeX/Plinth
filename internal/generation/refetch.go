package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Upstream result refetch (#295).
//
// A Task can fail after its upstream job already finished and charged: the
// result could not be downloaded, stored or verified within the
// materialization attempts. Its Attempt keeps the accepted UpstreamJob. A
// refetch is a new public Task bound to that failed source Attempt. It never
// calls Start (no upstream create, no paid dispatch): it reads the source job
// again through the adapter's status call (Observe) under a denied dispatch
// checkpointer and runs the ordinary claim validation, download, fidelity and
// store pipeline (materialize). The source Task, its Attempt, checkpoint,
// failure and billing history stay unchanged; terminal states are not revived.
//
// The refetch Task freezes the source Offering with its billing declarations
// removed (refetchOffering), so it never shows or estimates credits. Provider
// calls still use the source's own frozen Offering and job.

// RefetchRequester records who asked for a refetch (audit only).
type RefetchRequester string

const (
	RefetchByOwner RefetchRequester = "owner"
	RefetchByAgent RefetchRequester = "agent"
)

// RefetchReason is the closed set of reasons a refetch is not offered. The
// texts live in Product Schema; the kernel only names the reason.
type RefetchReason string

const (
	// RefetchNotApplicable: the Task did not fail after its upstream result was
	// accepted, so there is nothing to fetch again. Clients hide the action.
	RefetchNotApplicable RefetchReason = "not_applicable"
	// RefetchNotSupported: the provider handed over a one-shot result (inline
	// bytes, no job) or declares no result_refetch.
	RefetchNotSupported RefetchReason = "not_supported"
	// RefetchExpired: past the provider's declared retention window.
	RefetchExpired RefetchReason = "expired"
	// RefetchProviderUnavailable: the provider adapter is not configured now.
	RefetchProviderUnavailable RefetchReason = "provider_unavailable"
	// RefetchRepeatedFailure: an earlier refetch failed in exactly the same
	// deterministic way as the source (repeatsSourceFailure). A refetch reuses
	// the source's frozen Offering and reads the same upstream result, so
	// another one would fail again and is not offered. FailureClass names the
	// cause.
	RefetchRepeatedFailure RefetchReason = "repeated_failure"
	// RefetchDelivered: a refetch of this Task already succeeded
	// (RefetchTaskID), so there is nothing left to fetch (#330). Asking again
	// returns that Task; clients read Eligible alone and open it instead.
	RefetchDelivered RefetchReason = "delivered"
)

// UpstreamRefetch binds a refetch Task to the failed source Attempt whose
// accepted upstream job it reads again.
type UpstreamRefetch struct {
	TaskID          TaskID
	OwnerID         OwnerID
	SessionID       string
	SourceTaskID    TaskID
	SourceAttemptID AttemptID
	Ordinal         int
	RequestedBy     RefetchRequester
	CreatedAt       time.Time
}

// UpstreamRefetchStore is implemented by the durable repository.
// CreateUpstreamRefetch inserts the Task, its submitted event and the binding
// in one transaction with the same owner idempotency and inflight admission
// as Create. A refetch needs no execution grant: it cannot create or charge
// upstream work.
type UpstreamRefetchStore interface {
	CreateUpstreamRefetch(context.Context, Task, Event, UpstreamRefetch, int) (Task, bool, error)
	// UpstreamRefetchOf returns RepositoryNotFound for an ordinary Task.
	UpstreamRefetchOf(context.Context, TaskID) (UpstreamRefetch, error)
	// UpstreamRefetchesOf lists the owner's refetches of one source, oldest first.
	UpstreamRefetchesOf(context.Context, OwnerID, TaskID) ([]UpstreamRefetch, error)
	// SessionUpstreamRefetches lists the owner's refetches in one session.
	SessionUpstreamRefetches(context.Context, OwnerID, string) ([]UpstreamRefetch, error)
}

// ResultRefetchPolicySource is the Catalog side: the Product Schema
// declaration of the provider connection serving an adapter key.
type ResultRefetchPolicySource interface {
	ResultRefetchPolicy(adapterKey string) (schema.ResultRefetchPolicy, bool)
}

// RefetchEligibility is the owner's view of whether the failed Task's result
// can be fetched again. Eligible is the one judgment clients read: true while
// a refetch can be started or one is running (asking again joins it), false
// once one delivered (reason delivered) or none is possible. RefetchTaskID
// names a refetch already running or done; asking again returns it instead of
// starting another one.
type RefetchEligibility struct {
	SourceTaskID   TaskID        `json:"source_task_id"`
	Eligible       bool          `json:"eligible"`
	Reason         RefetchReason `json:"reason,omitempty"`
	AvailableUntil *time.Time    `json:"available_until,omitempty"`
	RefetchTaskID  TaskID        `json:"refetch_task_id,omitempty"`
	RefetchState   TaskState     `json:"refetch_state,omitempty"`
	// FailureClass is the public class of the failure a refetch repeated
	// (reason repeated_failure only); clients show its Schema text as the cause.
	FailureClass FailureClass `json:"failure_class,omitempty"`
	// ChargedCredits is the frozen credit estimate of the failed Task when its
	// provider finished (and so billed) before the delivery failed, for a
	// credits-metered Offering (#330); clients say what was already charged.
	ChargedCredits int `json:"charged_credits,omitempty"`
	// FailedRefetches counts earlier refetches of this Task that failed or
	// were canceled; clients offer a paid retry once a free refetch failed.
	FailedRefetches int `json:"failed_refetches,omitempty"`
}

// RefetchResult is the outcome of a refetch request.
type RefetchResult struct {
	Refetch  RefetchEligibility `json:"refetch"`
	TaskID   TaskID             `json:"task_id,omitempty"`
	Replayed bool               `json:"replayed"`
}

// RefetchIdempotencyKey is stable per source Attempt and refetch ordinal, so a
// repeated or concurrent request maps to the same Task.
func RefetchIdempotencyKey(attempt AttemptID, ordinal int) string {
	return fmt.Sprintf("upstream-refetch:%s:%d", attempt, ordinal)
}

func (m *Module) refetchStore() (UpstreamRefetchStore, bool) {
	store, ok := m.repository.(UpstreamRefetchStore)
	return store, ok
}

func (m *Module) refetchPolicy(adapterKey string) (schema.ResultRefetchPolicy, bool) {
	source, ok := m.catalog.(ResultRefetchPolicySource)
	if !ok {
		return schema.ResultRefetchPolicy{}, false
	}
	return source.ResultRefetchPolicy(adapterKey)
}

// refetchOffering removes every billing declaration from the frozen source
// Offering. The refetch Task shows no credits and starts no post-success work
// (which could dispatch paid conversions); its outputs stay identical.
func refetchOffering(offering Offering) Offering {
	offering.CreditsMetered = false
	offering.EstimatedCredits = 0
	offering.EstimatedCreditsUnitParameter = ""
	offering.Cost = nil
	offering.CostPolicy = nil
	offering.NativePricing = nil
	offering.PostSuccess = nil
	return offering
}

// repeatsSourceFailure reports whether a failed refetch reproduced the
// source's failure deterministically, so that another refetch cannot succeed:
//
//   - both failures are integrity_failure, the class of a delivered result
//     that does not satisfy the frozen output contract (network, timeout,
//     upstream, expiry and refetch-specific failures have other classes);
//   - the kernel marked both non-retryable and unambiguous: the same bytes
//     checked against the same contract give the same verdict (wrong media
//     type, fidelity rejection). A retryable integrity failure (a store write
//     or I/O error) may pass on another try, so it keeps the refetch open;
//   - the safe detail and the internal forensic code and excerpt are equal:
//     the same slot failed the same check for the same reason.
//
// A refetch freezes the source Offering and reads the same upstream job, so
// such a failure repeats on every refetch.
func repeatsSourceFailure(source, refetch *Failure) bool {
	if source == nil || refetch == nil {
		return false
	}
	return refetch.Class == FailureIntegrity && !refetch.Retryable && !refetch.Ambiguous &&
		source.Class == refetch.Class && source.Retryable == refetch.Retryable &&
		source.Ambiguous == refetch.Ambiguous && source.SafeDetail == refetch.SafeDetail &&
		source.UpstreamCode == refetch.UpstreamCode && source.UpstreamExcerpt == refetch.UpstreamExcerpt
}

// upstreamResultAccepted reports whether the Attempt reached a completed
// upstream observation, i.e. the provider produced (and billed) a result.
func upstreamResultAccepted(attempt *Attempt) bool {
	return attempt != nil && attempt.LastObservationPhase == UpstreamCompleted
}

// chargedCredits is the credit estimate frozen with a Task whose provider
// finished its job: the same estimate the session total counts for it. It is
// 0 for an Offering that is not credits-metered or is priced natively.
func chargedCredits(task Task) int {
	if !task.Offering.CreditsMetered || task.Offering.NativePricing != nil {
		return 0
	}
	return task.Offering.EstimateTotalCredits(task.Input)
}

func (m *Module) RefetchEligibility(ctx context.Context, owner OwnerID, taskID TaskID) (RefetchEligibility, error) {
	view, _, _, err := m.refetchCandidate(ctx, owner, taskID)
	return view, err
}

func (m *Module) refetchCandidate(ctx context.Context, owner OwnerID, taskID TaskID) (RefetchEligibility, Aggregate, []UpstreamRefetch, error) {
	view := RefetchEligibility{SourceTaskID: taskID, Reason: RefetchNotApplicable}
	task, err := m.repository.Get(ctx, owner, taskID)
	if err != nil {
		return view, Aggregate{}, nil, mapRepositoryError("upstream_refetch", err)
	}
	store, ok := m.refetchStore()
	if !ok {
		return view, Aggregate{}, nil, nil
	}
	aggregate, err := m.repository.Load(ctx, taskID)
	if err != nil {
		return view, Aggregate{}, nil, mapRepositoryError("upstream_refetch", err)
	}
	if aggregate.Task.OwnerID != owner {
		return view, Aggregate{}, nil, mapRepositoryError("upstream_refetch", RepositoryNotFound)
	}
	view.ChargedCredits = chargedCredits(aggregate.Task)
	// A failed refetch holds no job of its own; its source is what can be
	// fetched again.
	if _, err := store.UpstreamRefetchOf(ctx, taskID); err == nil {
		return view, aggregate, nil, nil
	} else if !errors.Is(err, RepositoryNotFound) {
		return view, aggregate, nil, mapRepositoryError("upstream_refetch", err)
	}
	refetches, err := store.UpstreamRefetchesOf(ctx, owner, taskID)
	if err != nil {
		return view, aggregate, nil, mapRepositoryError("upstream_refetch", err)
	}
	// A delivered refetch answers even after the failed source itself became
	// SUCCEEDED through a local delivery completion (#330 third follow-ups).
	// The source Task and every refetch keep their own history.
	var repeated *Failure
	for index := len(refetches) - 1; index >= 0; index-- {
		existing, err := m.repository.Get(ctx, owner, refetches[index].TaskID)
		if errors.Is(err, RepositoryNotFound) {
			continue
		}
		if err != nil {
			return view, aggregate, nil, mapRepositoryError("upstream_refetch", err)
		}
		if existing.State == TaskFailed || existing.State == TaskCanceled {
			view.FailedRefetches++
			if repeated == nil && existing.State == TaskFailed && task.Failure != nil && repeatsSourceFailure(task.Failure, existing.Failure) {
				repeated = existing.Failure
			}
			continue
		}
		view.Eligible, view.Reason = true, ""
		view.RefetchTaskID, view.RefetchState = existing.ID, existing.State
		if existing.State == TaskSucceeded {
			view.Eligible, view.Reason = false, RefetchDelivered
		}
		return view, aggregate, refetches, nil
	}
	if task.State != TaskFailed {
		return view, aggregate, refetches, nil
	}
	if !upstreamResultAccepted(aggregate.CurrentAttempt) {
		return view, aggregate, refetches, nil
	}
	if repeated != nil {
		view.Reason, view.FailureClass = RefetchRepeatedFailure, repeated.Class
		return view, aggregate, refetches, nil
	}
	attempt := aggregate.CurrentAttempt
	policy, declared := m.refetchPolicy(task.Offering.AdapterKey)
	if attempt.UpstreamJob == nil || !declared {
		view.Reason = RefetchNotSupported
		return view, aggregate, refetches, nil
	}
	if _, registered := m.providers.Provider(aggregate.Task.Offering); !registered {
		view.Reason = RefetchProviderUnavailable
		return view, aggregate, refetches, nil
	}
	if policy.RetentionHours > 0 {
		completed := attempt.LastObservationAt
		if attempt.FinishedAt != nil {
			completed = *attempt.FinishedAt
		}
		until := completed.Add(time.Duration(policy.RetentionHours) * time.Hour)
		view.AvailableUntil = &until
		if !m.clock.Now().Before(until) {
			view.Reason = RefetchExpired
			return view, aggregate, refetches, nil
		}
	}
	view.Eligible, view.Reason = true, ""
	return view, aggregate, refetches, nil
}

// RefetchUpstreamResult starts (or returns the existing) refetch of a failed
// Task's accepted upstream result. Only the owner can ask; another owner's or
// a missing Task is not found. Nothing is submitted upstream and nothing is
// charged. A second request while one is running or after it succeeded
// returns the same Task (Replayed).
func (m *Module) RefetchUpstreamResult(ctx context.Context, owner OwnerID, taskID TaskID, by RefetchRequester) (RefetchResult, error) {
	view, aggregate, refetches, err := m.refetchCandidate(ctx, owner, taskID)
	result := RefetchResult{Refetch: view}
	if err != nil {
		return result, err
	}
	if view.RefetchTaskID != "" {
		result.TaskID, result.Replayed = view.RefetchTaskID, true
		m.logRefetchRequest(owner, aggregate.Task.ID, aggregate.CurrentAttempt, view.RefetchTaskID, by, true)
		return result, nil
	}
	if !view.Eligible {
		return result, &Error{Code: ErrConflict, Operation: "upstream_refetch", Detail: "该任务的上游结果不能重新取回"}
	}
	store, _ := m.refetchStore()
	source, attempt := aggregate.Task, aggregate.CurrentAttempt
	ordinal := 1
	for _, existing := range refetches {
		ordinal = max(ordinal, existing.Ordinal+1)
	}
	offering := refetchOffering(source.Offering)
	// An older frozen snapshot predates the refetch texts: freeze the current ones.
	if current, ok := m.catalog.Get(source.Offering.ID); ok {
		offering.ExecutionMessages = current.ExecutionMessages
	} else if listed := m.catalog.List(""); len(listed) > 0 {
		offering.ExecutionMessages = listed[0].ExecutionMessages
	}
	now := m.clock.Now()
	task := Task{
		ID:             m.ids.TaskID(),
		OwnerID:        owner,
		SessionID:      source.SessionID,
		Capability:     source.Capability,
		Offering:       offering,
		Input:          append(json.RawMessage(nil), source.Input...),
		RequestHash:    hashRequest(offering, source.Input),
		IdempotencyKey: RefetchIdempotencyKey(attempt.ID, ordinal),
		State:          TaskQueued,
		NextActionAt:   now,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
		Assets:         []Asset{},
	}
	event := Event{
		TaskID: task.ID,
		Type:   "task.submitted",
		Data: eventData(map[string]any{
			"offering_id": offering.ID, "capability": task.Capability,
			"refetch_of": source.ID, "requested_by": by,
		}),
		CreatedAt: now,
	}
	binding := UpstreamRefetch{
		TaskID: task.ID, OwnerID: owner, SessionID: source.SessionID,
		SourceTaskID: source.ID, SourceAttemptID: attempt.ID,
		Ordinal: ordinal, RequestedBy: by, CreatedAt: now,
	}
	created, replayed, err := store.CreateUpstreamRefetch(ctx, task, event, binding, m.perOwnerMaxInflightTasks)
	if err != nil {
		return result, mapRepositoryError("upstream_refetch", err)
	}
	m.logRefetchRequest(owner, source.ID, attempt, created.ID, by, replayed)
	result.Refetch.RefetchTaskID, result.Refetch.RefetchState = created.ID, created.State
	result.TaskID, result.Replayed = created.ID, replayed
	return result, nil
}

// logRefetchRequest writes the task.upstream_refetch audit line for every
// request that names a refetch Task: a new one, a concurrent request that
// landed on the same idempotency key, and a repeat answered by the refetch
// already running or done (replayed true).
func (m *Module) logRefetchRequest(owner OwnerID, source TaskID, attempt *Attempt, task TaskID, by RefetchRequester, replayed bool) {
	var attemptID AttemptID
	if attempt != nil {
		attemptID = attempt.ID
	}
	m.logger.Info("upstream result refetch requested",
		"event", "task.upstream_refetch",
		"owner_id", string(owner),
		"source_task_id", string(source),
		"source_attempt_id", string(attemptID),
		"task_id", string(task),
		"requested_by", string(by),
		"replayed", replayed)
}

// upstreamRefetchOf returns the binding of a refetch Task, or false for an
// ordinary Task.
func (m *Module) upstreamRefetchOf(ctx context.Context, id TaskID) (UpstreamRefetch, bool, error) {
	store, ok := m.refetchStore()
	if !ok {
		return UpstreamRefetch{}, false, nil
	}
	binding, err := store.UpstreamRefetchOf(ctx, id)
	if errors.Is(err, RepositoryNotFound) {
		return UpstreamRefetch{}, false, nil
	}
	if err != nil {
		return UpstreamRefetch{}, false, mapRepositoryError("upstream_refetch", err)
	}
	return binding, true, nil
}

// refetchSource loads the source aggregate a refetch reads and checks it is
// still the bound, owner-matching Attempt with an accepted upstream job.
func (m *Module) refetchSource(ctx context.Context, task Task, binding UpstreamRefetch) (Aggregate, error) {
	source, err := m.repository.Load(ctx, binding.SourceTaskID)
	if err != nil && !errors.Is(err, RepositoryNotFound) {
		return Aggregate{}, err
	}
	if err != nil {
		// The refetch Task already holds the frozen source identity. If the
		// source Task was deleted after this refetch started, its historical
		// Attempt and provider job still exist; a running delivery may finish
		// from them. Completion and any new refetch decision remain governed
		// by the tombstoned source elsewhere.
		attempt, attemptErr := m.refetchAttempt(ctx, task.OwnerID, binding.SourceTaskID, binding.SourceAttemptID, nil)
		if attemptErr != nil {
			return Aggregate{}, attemptErr
		}
		source = Aggregate{Task: Task{
			ID: binding.SourceTaskID, OwnerID: task.OwnerID, SessionID: task.SessionID,
			Capability: task.Capability, Offering: task.Offering, Input: task.Input,
		}, CurrentAttempt: attempt}
	}
	if source.Task.OwnerID != task.OwnerID || binding.OwnerID != task.OwnerID {
		return Aggregate{}, errRefetchSourceChanged
	}
	attempt, err := m.refetchAttempt(ctx, source.Task.OwnerID, binding.SourceTaskID, binding.SourceAttemptID, source.CurrentAttempt)
	if err != nil {
		return Aggregate{}, err
	}
	if attempt.UpstreamJob == nil || !upstreamResultAccepted(attempt) {
		return Aggregate{}, errRefetchSourceChanged
	}
	source.CurrentAttempt = attempt
	source.Claims = nil
	return source, nil
}

// refetchAttempt reads the exact Attempt the refetch binding named. The source
// Task's current Attempt changes when a local delivery completion succeeds;
// the historical Attempt remains the authoritative provider job.
func (m *Module) refetchAttempt(ctx context.Context, owner OwnerID, taskID TaskID, attemptID AttemptID, current *Attempt) (*Attempt, error) {
	if reader, ok := m.repository.(AttemptReader); ok {
		attempt, err := reader.LoadAttemptForTask(ctx, owner, taskID, attemptID)
		if err != nil {
			if errors.Is(err, RepositoryNotFound) {
				return nil, errRefetchSourceChanged
			}
			return nil, err
		}
		if attempt.ID != attemptID || attempt.TaskID != taskID {
			return nil, errRefetchSourceChanged
		}
		return &attempt, nil
	}
	if current != nil && current.ID == attemptID {
		return current, nil
	}
	return nil, errRefetchSourceChanged
}

var errRefetchSourceChanged = errors.New("refetch source changed")

// providerCall is the identity the kernel uses for adapter calls about a
// Task's result. For a refetch it is the failed source's frozen Offering,
// input, Attempt, job and checkpoint; otherwise the Task's own.
type providerCall struct {
	TaskID    TaskID
	AttemptID AttemptID
	Offering  Offering
	Input     json.RawMessage
	Job       *UpstreamJob
	Opaque    json.RawMessage
}

func (m *Module) providerCallFor(ctx context.Context, task Task, attempt *Attempt) (providerCall, error) {
	call := providerCall{TaskID: task.ID, Offering: task.Offering, Input: task.Input}
	if attempt != nil {
		call.AttemptID, call.Job, call.Opaque = attempt.ID, attempt.UpstreamJob, attempt.Opaque
	}
	if attempt != nil && attempt.UpstreamJob != nil {
		return call, nil
	}
	binding, ok, err := m.upstreamRefetchOf(ctx, task.ID)
	if err != nil || !ok {
		return call, err
	}
	source, err := m.refetchSource(ctx, task, binding)
	if err != nil {
		return call, err
	}
	sourceAttempt := source.CurrentAttempt
	return providerCall{
		TaskID: source.Task.ID, AttemptID: sourceAttempt.ID, Offering: source.Task.Offering,
		Input: source.Task.Input, Job: sourceAttempt.UpstreamJob, Opaque: sourceAttempt.Opaque,
	}, nil
}

func refetchFailure(detail string) *Failure {
	return &Failure{Class: FailureRejected, SafeDetail: detail}
}

// startRefetch replaces Start for a refetch Task: it opens an Attempt, reads
// the source job once through Observe with paid dispatch denied, and hands a
// completed observation to the ordinary pipeline. The source job is never
// bound to this Attempt (one Attempt owns a job; webhooks keep routing to the
// source), and its private checkpoint is not copied.
func (m *Module) startRefetch(ctx context.Context, lease Lease, binding UpstreamRefetch) error {
	task := lease.Task
	attempt := lease.CurrentAttempt
	switch {
	case attempt == nil || attempt.State == AttemptFailed:
		ordinal := 1
		if attempt != nil {
			ordinal = attempt.Ordinal + 1
		}
		attempt = m.newAttempt(task, ordinal)
		now := m.clock.Now()
		task.CurrentAttemptID = attempt.ID
		task.State = TaskRunning
		task.UpdatedAt = now
		task.Version++
		if err := m.repository.Commit(ctx, Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			Events: []Event{{
				TaskID:    task.ID,
				Type:      "attempt.started",
				Data:      eventData(map[string]any{"attempt_id": attempt.ID, "ordinal": attempt.Ordinal, "refetch_of": binding.SourceTaskID}),
				CreatedAt: now,
			}},
		}); err != nil {
			return mapRepositoryError("begin_refetch", err)
		}
		lease.Task = task
		lease.CurrentAttempt = attempt
	case attempt.State == AttemptSubmitting:
		// An interrupted refetch only read the upstream; reading again is safe.
	default:
		return m.finishFailed(ctx, lease, attempt, &Failure{Class: FailureInternal, SafeDetail: fmt.Sprintf("重新取回任务的尝试状态非法: %s", attempt.State)}, "attempt.invalid_state", nil)
	}
	messages := m.executionMessages(task)
	source, err := m.refetchSource(ctx, task, binding)
	if err != nil {
		if errors.Is(err, errRefetchSourceChanged) || errors.Is(err, RepositoryNotFound) {
			return m.finishFailed(ctx, lease, attempt, refetchFailure(messages.RefetchSourceUnavailable), "refetch.source_unavailable", nil)
		}
		return err
	}
	port, ok := m.providers.Provider(source.Task.Offering)
	if !ok {
		return m.finishFailed(ctx, lease, attempt, &Failure{Class: FailureInternal, SafeDetail: fmt.Sprintf("适配器 %q 未注册", source.Task.Offering.AdapterKey)}, "attempt.adapter_missing", nil)
	}
	sourceAttempt := source.CurrentAttempt
	callCtx, cancel := m.withOfferingTimeout(ctx, source.Task.Offering.Timeout)
	defer cancel()
	// No lease-bound dispatch fence exists for a read of someone else's job:
	// any paid dispatch the adapter might attempt is refused.
	callCtx = WithDeniedDispatchCheckpointer(callCtx)
	obs, err := port.Observe(callCtx, ObserveRequest{
		TaskID:    source.Task.ID,
		AttemptID: sourceAttempt.ID,
		Offering:  source.Task.Offering,
		Input:     schema.WireInput(source.Task.Offering, source.Task.Input),
		Job:       *sourceAttempt.UpstreamJob,
		Opaque:    sourceAttempt.Opaque,
	})
	if err != nil {
		failure := providerFailure(err, false)
		failure.Ambiguous = false
		return m.handleAttemptFailure(ctx, lease, attempt, failure, nil)
	}
	if validateObservation(obs) != nil || obs.Phase != UpstreamCompleted ||
		validateClaims(task.Offering.Outputs, obs.Artifacts) != nil {
		failure := refetchFailure(messages.RefetchResultUnavailable)
		if obs.Failure != nil {
			failure.UpstreamStatus, failure.UpstreamCode = obs.Failure.UpstreamStatus, obs.Failure.UpstreamCode
		}
		return m.finishFailed(ctx, lease, attempt, failure, "refetch.result_unavailable", nil)
	}
	obs.Job = nil
	obs.Opaque = nil
	obs.Dispatch = nil
	obs.ResourceUpdates = nil
	obs.Sequence = 0
	obs.ObservedAt = time.Time{}
	obs.DedupeKey = "refetch:" + string(attempt.ID)
	return m.applyObservation(ctx, lease, attempt, obs, "provider.refetch", nil)
}

// RefetchProvenance links a refetch Task to its failed source (owner only).
func (m *Module) refetchProvenance(ctx context.Context, task Task) (*RecoveryProvenance, error) {
	binding, ok, err := m.upstreamRefetchOf(ctx, task.ID)
	if err != nil || !ok {
		return nil, err
	}
	if binding.OwnerID != task.OwnerID {
		return nil, recoveryConflict()
	}
	return &RecoveryProvenance{
		Kind:                   RecoveryKindUpstreamRefetch,
		SourceTaskID:           binding.SourceTaskID,
		SourceAttemptID:        binding.SourceAttemptID,
		SourceOfferingID:       task.Offering.ID,
		SourceOfferingRevision: task.Offering.Revision,
	}, nil
}

// SessionRefetchSources maps each refetch Task of the owner's session to its
// source Task.
func (m *Module) SessionRefetchSources(ctx context.Context, owner OwnerID, session string) (map[TaskID]TaskID, error) {
	result := map[TaskID]TaskID{}
	store, ok := m.refetchStore()
	if !ok || session == "" {
		return result, nil
	}
	bindings, err := store.SessionUpstreamRefetches(ctx, owner, session)
	if err != nil {
		return nil, mapRepositoryError("upstream_refetch", err)
	}
	for _, binding := range bindings {
		result[binding.TaskID] = binding.SourceTaskID
	}
	return result, nil
}

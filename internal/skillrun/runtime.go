package skillrun

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// GenerationGateway is the sole paid execution path. The Skill ledger does not
// own Provider state or inspect Provider-private data.
type GenerationGateway interface {
	PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error)
	GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error)
	SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error)
	VerifyPreparedTaskReplay(context.Context, generation.SubmitPreparedCommand) (generation.Task, error)
	Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error)
}

type Runtime struct {
	Runs       *Service
	Generation GenerationGateway
	Grants     *executiongrant.Service
}

func NewRuntime(runs *Service, generationGateway GenerationGateway, grants *executiongrant.Service) (*Runtime, error) {
	if runs == nil || generationGateway == nil || grants == nil || grants.Grants == nil || grants.Prepared == nil {
		return nil, ErrInvalid
	}
	return &Runtime{Runs: runs, Generation: generationGateway, Grants: grants}, nil
}

func (r *Runtime) Create(ctx context.Context, cmd CreateCommand) (Run, error) {
	return r.Runs.Create(ctx, cmd)
}

func (r *Runtime) Get(ctx context.Context, owner generation.OwnerID, runID string) (Run, error) {
	return r.Runs.Get(ctx, owner, runID)
}

func (r *Runtime) List(ctx context.Context, owner generation.OwnerID, session, after string, limit int) ([]Run, error) {
	return r.Runs.List(ctx, owner, session, after, limit)
}

// ResolveAgentStep reads the first planner step and its exact grant. It never
// prepares or submits anything, including when submission accepted a Task but
// the Skill ledger binding failed. Stop uses the returned original step key.
func (r *Runtime) ResolveAgentStep(ctx context.Context, owner generation.OwnerID,
	session, runKey, turnID string,
) (Run, Execution, executiongrant.Grant, error) {
	if !validID(session) || !validID(turnID) {
		return Run{}, Execution{}, executiongrant.Grant{}, ErrInvalid
	}
	run, err := r.Runs.GetByIdempotency(ctx, owner, runKey)
	if err != nil {
		return Run{}, Execution{}, executiongrant.Grant{}, err
	}
	if run.OwnerID != owner || run.SessionID != session || run.IdempotencyKey != runKey || len(run.Steps) == 0 {
		return Run{}, Execution{}, executiongrant.Grant{}, ErrConflict
	}
	execution, err := r.Runs.ResolveExecution(ctx, owner, run.ID, run.Steps[0].Spec.Key)
	if err != nil {
		return Run{}, Execution{}, executiongrant.Grant{}, err
	}
	step := execution.Step
	if step.GrantRequestID == "" {
		if step.TaskID != "" || step.Phase == TaskBound || step.Phase == OutputReady {
			return Run{}, Execution{}, executiongrant.Grant{}, ErrConflict
		}
		return run, execution, executiongrant.Grant{}, nil
	}
	grant, err := r.Grants.Grants.GetExecutionGrant(ctx, string(owner), step.GrantRequestID)
	if err != nil {
		return Run{}, Execution{}, executiongrant.Grant{}, err
	}
	if grant.RequestID != step.GrantRequestID || grant.OwnerID != string(owner) || grant.SessionID != session ||
		grant.Source != executiongrant.AgentPending || grant.TriggerTurnID != turnID ||
		grant.PreparedInputID != string(step.PreparedInputID) || grant.PreparedInputHash != step.PreparedInputHash ||
		grant.OfferingID != string(step.Spec.OfferingID) || grant.OfferingRevision != step.Spec.OfferingRevision ||
		grant.OperationID != execution.OperationID || grant.IdempotencyKey != execution.IdempotencyKey ||
		grant.PlanStepID != execution.PlanStepID || grant.Harness != execution.Harness {
		return Run{}, Execution{}, executiongrant.Grant{}, ErrConflict
	}
	prepared, err := r.Generation.GetPreparedInput(ctx, owner, step.PreparedInputID)
	if err != nil {
		return Run{}, Execution{}, executiongrant.Grant{}, err
	}
	if prepared.ID != step.PreparedInputID || prepared.OwnerID != owner || prepared.SessionID != session ||
		prepared.InputHash != step.PreparedInputHash || prepared.Capability != step.Spec.Capability ||
		prepared.OfferingID != step.Spec.OfferingID || prepared.OfferingRevision != step.Spec.OfferingRevision ||
		!MatchesPreparedInput(step.Spec, execution.SourceAssetID, prepared.NormalizedInput) ||
		(grant.Status == executiongrant.Bound && grant.TaskID == "") ||
		(step.TaskID != "" && (grant.Status != executiongrant.Bound || grant.TaskID != string(step.TaskID))) {
		return Run{}, Execution{}, executiongrant.Grant{}, ErrConflict
	}
	return run, execution, grant, nil
}

// PrepareStep persists the frozen Generation input before any consent request.
// Repeated calls after restart recover that exact input.
func (r *Runtime) PrepareStep(ctx context.Context, owner generation.OwnerID, runID, stepKey string) (Run, generation.PreparedInput, error) {
	execution, err := r.Runs.ResolveExecution(ctx, owner, runID, stepKey)
	if err != nil {
		return Run{}, generation.PreparedInput{}, err
	}
	if execution.Step.PreparedInputID != "" {
		prepared, err := r.Generation.GetPreparedInput(ctx, owner, execution.Step.PreparedInputID)
		if err != nil {
			return Run{}, generation.PreparedInput{}, err
		}
		if prepared.InputHash != execution.Step.PreparedInputHash {
			return Run{}, generation.PreparedInput{}, ErrConflict
		}
		run, err := r.Runs.Get(ctx, owner, runID)
		return run, prepared, err
	}
	if execution.Step.Phase != Planned {
		return Run{}, generation.PreparedInput{}, ErrConflict
	}
	input, err := stepInput(execution.Step.Spec, execution.SourceAssetID)
	if err != nil {
		return Run{}, generation.PreparedInput{}, err
	}
	prepared, err := r.Generation.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: owner, SessionID: execution.SessionID, Capability: execution.Step.Spec.Capability,
		OfferingID: execution.Step.Spec.OfferingID, Input: input,
	})
	if err != nil {
		return Run{}, generation.PreparedInput{}, err
	}
	if prepared.OfferingRevision != execution.Step.Spec.OfferingRevision {
		return Run{}, generation.PreparedInput{}, ErrConflict
	}
	run, err := r.Runs.AttachPrepared(ctx, PreparedCommand{
		StepCommand: StepCommand{OwnerID: owner, RunID: runID, StepKey: stepKey,
			OperationID: execution.OperationID, IdempotencyKey: execution.IdempotencyKey},
		PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
	})
	if err != nil {
		return Run{}, generation.PreparedInput{}, err
	}
	return run, prepared, nil
}

func stepInput(spec StepSpec, source generation.AssetID) (json.RawMessage, error) {
	if spec.PromptParameter == "" || spec.SourceParameter == "" ||
		spec.PromptParameter == spec.SourceParameter || source == "" {
		return nil, ErrInvalid
	}
	input := make(map[string]json.RawMessage, len(spec.StaticInput)+2)
	for key, value := range spec.StaticInput {
		if key == "" || key == spec.PromptParameter || key == spec.SourceParameter || !json.Valid(value) {
			return nil, ErrInvalid
		}
		input[key] = append(json.RawMessage(nil), value...)
	}
	prompt, _ := json.Marshal(spec.PromptTemplate)
	assets, _ := json.Marshal([]generation.AssetID{source})
	input[spec.PromptParameter] = prompt
	input[spec.SourceParameter] = assets
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return encoded, nil
}

// MatchesPreparedInput checks the recipe-owned fields after Catalog
// normalization. Offering defaults may add fields, but no caller can replace
// the prompt or source asset while retaining the Skill checkpoint.
func MatchesPreparedInput(spec StepSpec, source generation.AssetID, normalized json.RawMessage) bool {
	if spec.PromptParameter == "" || spec.SourceParameter == "" {
		return false
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &input); err != nil {
		return false
	}
	var prompt string
	if err := json.Unmarshal(input[spec.PromptParameter], &prompt); err != nil ||
		prompt != spec.PromptTemplate {
		return false
	}
	var assets []generation.AssetID
	if err := json.Unmarshal(input[spec.SourceParameter], &assets); err != nil ||
		len(assets) != 1 || assets[0] != source {
		return false
	}
	for key, value := range spec.StaticInput {
		var expected, actual any
		if json.Unmarshal(value, &expected) != nil ||
			json.Unmarshal(input[key], &actual) != nil ||
			!reflect.DeepEqual(expected, actual) {
			return false
		}
	}
	return true
}

// RequestGrant creates a pending, exact-input grant. Unless the session approval
// policy approves the step by itself, a separate authenticated human decision
// must approve it before SubmitStep can create a paid Task.
func (r *Runtime) RequestGrant(ctx context.Context, principal executiongrant.Principal, runID, stepKey string) (Run, executiongrant.Grant, error) {
	if principal.Source != executiongrant.HumanBrowser || principal.OwnerID == "" || principal.ActorID == "" {
		return Run{}, executiongrant.Grant{}, ErrInvalid
	}
	return r.requestGrant(ctx, principal, runID, stepKey, "")
}

// RequestAgentGrant records a pending proposal from the planner. The Agent
// cannot approve it; only the separate authenticated human decision endpoint
// can move the grant to approved before SubmitStep accepts a paid Task.
func (r *Runtime) RequestAgentGrant(ctx context.Context, owner generation.OwnerID, runID, stepKey,
	assistantMessageID string) (Run, executiongrant.Grant, error) {
	if owner == "" || !validID(assistantMessageID) {
		return Run{}, executiongrant.Grant{}, ErrInvalid
	}
	return r.requestGrant(ctx, executiongrant.Principal{OwnerID: string(owner), ActorID: "chat-agent",
		Source: executiongrant.AgentPending}, runID, stepKey, assistantMessageID)
}

func (r *Runtime) requestGrant(ctx context.Context, principal executiongrant.Principal, runID, stepKey,
	triggerTurnID string) (Run, executiongrant.Grant, error) {
	owner := generation.OwnerID(principal.OwnerID)
	execution, err := r.Runs.ResolveExecution(ctx, owner, runID, stepKey)
	if err != nil {
		return Run{}, executiongrant.Grant{}, err
	}
	if execution.Step.GrantRequestID != "" {
		grant, err := r.Grants.Grants.GetExecutionGrant(ctx, principal.OwnerID, execution.Step.GrantRequestID)
		if err != nil {
			return Run{}, executiongrant.Grant{}, err
		}
		if grant.Source != principal.Source || grant.TriggerTurnID != triggerTurnID || grant.Harness != execution.Harness {
			return Run{}, executiongrant.Grant{}, ErrConflict
		}
		// Re-applying the session policy is idempotent and heals a restart
		// between the pending grant and its automatic approval.
		grant, err = r.Grants.ApplyPolicy(ctx, grant)
		if err != nil {
			return Run{}, executiongrant.Grant{}, err
		}
		run, err := r.Runs.Get(ctx, owner, runID)
		return run, grant, err
	}
	if execution.Step.Phase != Prepared {
		return Run{}, executiongrant.Grant{}, ErrConflict
	}
	grant, err := r.Grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: execution.SessionID, Harness: execution.Harness,
		OfferingID:        string(execution.Step.Spec.OfferingID),
		OfferingRevision:  execution.Step.Spec.OfferingRevision,
		PreparedInputID:   string(execution.Step.PreparedInputID),
		PreparedInputHash: execution.Step.PreparedInputHash,
		IdempotencyKey:    execution.IdempotencyKey,
		OperationID:       execution.OperationID, PlanStepID: execution.PlanStepID,
		TriggerTurnID: triggerTurnID,
	})
	if err != nil {
		return Run{}, executiongrant.Grant{}, err
	}
	run, err := r.Runs.AttachGrant(ctx, GrantCommand{
		StepCommand: StepCommand{OwnerID: owner, RunID: runID, StepKey: stepKey,
			OperationID: execution.OperationID, IdempotencyKey: execution.IdempotencyKey},
		GrantRequestID: grant.RequestID,
	})
	if err != nil {
		return run, grant, err
	}
	// The grant is recorded on the step first; only then may session policy
	// approve it. A step that needs a human stays pending.
	grant, err = r.Grants.ApplyPolicy(ctx, grant)
	return run, grant, err
}

func (r *Runtime) SubmitStep(ctx context.Context, owner generation.OwnerID, runID, stepKey string) (Run, generation.Accepted, error) {
	execution, err := r.Runs.ResolveExecution(ctx, owner, runID, stepKey)
	if err != nil {
		return Run{}, generation.Accepted{}, err
	}
	if execution.Step.TaskID != "" {
		if execution.Step.GrantRequestID != "" || !execution.Harness.IsZero() {
			verified, err := r.Generation.VerifyPreparedTaskReplay(ctx, generation.SubmitPreparedCommand{
				OwnerID: owner, SessionID: execution.SessionID, Harness: execution.Harness,
				PreparedInputID: execution.Step.PreparedInputID, IdempotencyKey: execution.IdempotencyKey,
				OperationID: execution.OperationID, PlanStepID: execution.PlanStepID, AuthorizationRef: execution.Step.GrantRequestID,
			})
			if err != nil {
				return Run{}, generation.Accepted{}, err
			}
			if verified.ID != execution.Step.TaskID || verified.OwnerID != owner || verified.SessionID != execution.SessionID {
				return Run{}, generation.Accepted{}, ErrConflict
			}
		}
		task, err := r.Generation.Get(ctx, owner, execution.Step.TaskID)
		if err != nil {
			return Run{}, generation.Accepted{}, err
		}
		if task.SessionID != execution.SessionID {
			return Run{}, generation.Accepted{}, ErrConflict
		}
		run, err := r.Runs.Get(ctx, owner, runID)
		return run, generation.Accepted{TaskID: task.ID, Replayed: true}, err
	}
	if execution.Step.Phase != GrantPending || execution.Step.GrantRequestID == "" {
		return Run{}, generation.Accepted{}, ErrConflict
	}
	grant, err := r.Grants.Grants.GetExecutionGrant(ctx, string(owner), execution.Step.GrantRequestID)
	if err != nil {
		return Run{}, generation.Accepted{}, err
	}
	if grant.Harness != execution.Harness {
		return Run{}, generation.Accepted{}, ErrConflict
	}
	if grant.Status != executiongrant.Approved && grant.Status != executiongrant.Bound {
		return Run{}, generation.Accepted{}, ErrConflict
	}
	accepted, err := r.Generation.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		Harness: execution.Harness,
		OwnerID: owner, SessionID: execution.SessionID,
		PreparedInputID: execution.Step.PreparedInputID,
		IdempotencyKey:  execution.IdempotencyKey, OperationID: execution.OperationID,
		PlanStepID: execution.PlanStepID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		return Run{}, generation.Accepted{}, err
	}
	run, err := r.Runs.BindTask(ctx, TaskCommand{
		StepCommand: StepCommand{OwnerID: owner, RunID: runID, StepKey: stepKey,
			OperationID: execution.OperationID, IdempotencyKey: execution.IdempotencyKey},
		TaskID: accepted.TaskID,
	})
	return run, accepted, err
}

// SyncOutput reads only the current Generation Task and records a locally
// materialized asset for the declared role. Pending or failed tasks remain
// visible as Tasks; they are not promoted to successful Skill outputs.
func (r *Runtime) SyncOutput(ctx context.Context, owner generation.OwnerID, runID, stepKey string) (Run, error) {
	execution, err := r.Runs.ResolveExecution(ctx, owner, runID, stepKey)
	if err != nil {
		return Run{}, err
	}
	if execution.Step.Phase == OutputReady {
		return r.Runs.Get(ctx, owner, runID)
	}
	if execution.Step.Phase != TaskBound || execution.Step.TaskID == "" {
		return Run{}, ErrConflict
	}
	task, err := r.Generation.Get(ctx, owner, execution.Step.TaskID)
	if err != nil {
		return Run{}, err
	}
	if task.SessionID != execution.SessionID || task.State != generation.TaskSucceeded {
		return Run{}, ErrConflict
	}
	for _, asset := range task.Assets {
		if asset.Role == execution.Step.Spec.OutputRole && asset.StorageKey != "" &&
			asset.SHA256 != "" && asset.Size > 0 {
			return r.Runs.RecordOutput(ctx, OutputCommand{
				OwnerID: owner, RunID: runID, StepKey: stepKey, AssetID: asset.ID,
			})
		}
	}
	return Run{}, ErrConflict
}

func (r *Runtime) Review(ctx context.Context, cmd ReviewCommand) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	return r.Runs.Review(ctx, cmd)
}

// RestartStep starts the next attempt of a step whose previous attempt ended
// without a result: its Task failed with a retryable, unambiguous failure or
// was canceled, or its approval was denied. It prepares nothing and submits nothing; the next
// attempt is a planned step with its own paid identity, which goes through
// PrepareStep, a grant and SubmitStep like any other. A step that is still
// running, waiting for a decision or done is never restarted.
//
// expectedAttempt is the attempt the caller saw. When the step has already
// moved past it, the request is a repeat of one that was served and the
// current run is returned unchanged.
func (r *Runtime) RestartStep(ctx context.Context, owner generation.OwnerID, runID, stepKey string,
	expectedAttempt int) (Run, error) {
	if expectedAttempt < 0 {
		return Run{}, ErrInvalid
	}
	run, err := r.Runs.Get(ctx, owner, runID)
	if err != nil {
		return Run{}, err
	}
	var step *Step
	for i := range run.Steps {
		if run.Steps[i].Spec.Key == stepKey {
			step = &run.Steps[i]
		}
	}
	if step == nil {
		return Run{}, ErrMissing
	}
	if step.Attempt > expectedAttempt {
		return run, nil
	}
	if step.Attempt < expectedAttempt {
		return Run{}, ErrConflict
	}
	command := RestartCommand{OwnerID: owner, RunID: runID, StepKey: stepKey, ExpectedAttempt: expectedAttempt}
	switch {
	case step.TaskID != "":
		task, err := r.Generation.Get(ctx, owner, step.TaskID)
		if err != nil {
			return Run{}, err
		}
		// A task the person canceled can be started again; a failed one only
		// when the failure is retryable and certain (the upstream did not
		// take the job), the same rule as retrying a standalone task.
		restartable := task.State == generation.TaskCanceled ||
			(task.State == generation.TaskFailed && task.Failure != nil &&
				task.Failure.Retryable && !task.Failure.Ambiguous)
		if task.SessionID != run.SessionID || !restartable {
			return Run{}, ErrConflict
		}
		command.FailedTaskID = task.ID
	case step.GrantRequestID != "" && step.Phase == GrantPending:
		grant, err := r.Grants.Grants.GetExecutionGrant(ctx, string(owner), step.GrantRequestID)
		if err != nil {
			return Run{}, err
		}
		if grant.Status != executiongrant.Denied {
			return Run{}, ErrConflict
		}
		command.DeniedGrantID = grant.RequestID
	default:
		return Run{}, ErrConflict
	}
	return r.Runs.RestartSkillStep(ctx, command)
}

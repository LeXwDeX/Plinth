package processing

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Revision operations an Agent may apply to a processing plan (#251). They
// are the only mutations of a plan's steps: insert a declared contingency after
// a failed checkpoint, replace or skip a step that never started, retry a
// failed step under the existing retry rules, or waive a failed checkpoint
// with a reason.
const (
	OperationInsert          = "insert"
	OperationReplace         = "replace"
	OperationSkip            = "skip"
	OperationRetry           = "retry"
	OperationWaiveCheckpoint = "waive_checkpoint"
)

// ErrRevisionOutsidePlan refuses a revision the approved plan does not cover.
// The tool layer turns it into an approval-required refusal.
var ErrRevisionOutsidePlan = errors.New("processing: revision outside the approved plan")

// ReviseCommand is one Agent-issued plan revision. The owner, session and
// harness come from the server-held tool call; the Agent cannot widen them.
type ReviseCommand struct {
	Harness             harness.Binding    `json:"-"`
	OwnerID             generation.OwnerID `json:"-"`
	SessionID           string             `json:"session_id"`
	PlanID              string             `json:"plan_id"`
	ExpectedPlanVersion int64              `json:"expected_plan_version"`
	Operation           string             `json:"operation"`
	// StepIndex targets the failed checkpoint (insert), or the step to
	// replace, skip or retry (0-based).
	StepIndex     int    `json:"step_index"`
	ContingencyID string `json:"contingency_id,omitempty"`
	// Contingency is the approved contingency the server resolved from the
	// session's production plan. The Agent names only its id; the tool layer
	// fills this from the approved plan, never from the arguments.
	Contingency *budget.PlanContingency `json:"-"`
	Capability  generation.Capability   `json:"capability,omitempty"`
	OfferingID  generation.OfferingID   `json:"offering_id,omitempty"`
	Parameters  json.RawMessage         `json:"parameters,omitempty"`
	Reason      string                  `json:"reason"`
}

// Revise applies one controlled revision. Every operation re-checks the
// plan's approved bounds: a step that already prepared, was granted or
// submitted is never moved; a new or replaced step is priced and bound again
// under the same source rules; the step count stays within MaxPlanSteps.
func (s *Service) Revise(ctx context.Context, command ReviseCommand) (Plan, error) {
	reason := strings.TrimSpace(command.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > MaxRevisionReasonCharacters ||
		command.ExpectedPlanVersion <= 0 || command.PlanID == "" || command.SessionID == "" {
		return Plan{}, ErrInvalid
	}
	plan, err := s.store.GetProcessingPlan(ctx, command.OwnerID, command.PlanID)
	if err != nil {
		return Plan{}, err
	}
	if plan.OwnerID != command.OwnerID || plan.SessionID != command.SessionID ||
		!harness.SameBinding(plan.Harness, command.Harness) {
		return Plan{}, ErrInvalid
	}
	if plan.Version != command.ExpectedPlanVersion {
		return Plan{}, ErrConflict
	}
	switch command.Operation {
	case OperationInsert:
		return s.reviseInsert(ctx, &plan, command, reason)
	case OperationReplace:
		return s.reviseReplace(ctx, &plan, command, reason)
	case OperationSkip:
		return s.reviseSkip(ctx, &plan, command, reason)
	case OperationRetry:
		return s.reviseRetry(ctx, &plan, command)
	case OperationWaiveCheckpoint:
		return s.reviseWaive(ctx, &plan, command, reason)
	}
	return Plan{}, ErrInvalid
}

func (s *Service) reviseInsert(ctx context.Context, plan *Plan, command ReviseCommand, reason string) (Plan, error) {
	if plan.State != Paused || plan.Failure != ReasonCheckpointFailed ||
		command.StepIndex < 0 || command.StepIndex >= len(plan.Steps) || len(plan.Steps) >= MaxPlanSteps {
		return Plan{}, ErrConflict
	}
	step := &plan.Steps[command.StepIndex]
	if step.State != Succeeded || step.Checkpoint == nil || step.Checkpoint.State != CheckpointFailed {
		return Plan{}, ErrConflict
	}
	contingency := command.Contingency
	if contingency == nil || contingency.ID != command.ContingencyID {
		return Plan{}, ErrRevisionOutsidePlan
	}
	triggered := failedChecks(step.Checkpoint)
	if !slices.ContainsFunc(contingency.Trigger, func(trigger string) bool { return slices.Contains(triggered, trigger) }) {
		return Plan{}, ErrRevisionOutsidePlan
	}
	if command.Capability != "" && string(command.Capability) != contingency.Step.Capability {
		return Plan{}, ErrRevisionOutsidePlan
	}
	if off := command.OfferingID; off != "" && contingency.Step.OfferingID != "" && string(off) != contingency.Step.OfferingID {
		return Plan{}, ErrRevisionOutsidePlan
	}
	offeringID := generation.OfferingID(contingency.Step.OfferingID)
	if offeringID == "" {
		offeringID = command.OfferingID
	}
	if offeringID == "" || len(command.Parameters) == 0 {
		return Plan{}, ErrInvalid
	}
	planned, err := s.revisedStep(generation.Capability(contingency.Step.Capability), offeringID, command.Parameters)
	if err != nil {
		return Plan{}, err
	}
	planned.ContingencyID, planned.RevisionOperation, planned.RevisionReason = contingency.ID, OperationInsert, reason
	steps := slices.Insert(slices.Clone(plan.Steps), command.StepIndex+1, planned)
	if err := s.validateRevisedChain(ctx, plan, steps); err != nil {
		return Plan{}, err
	}
	plan.Steps = steps
	plan.State, plan.Failure, plan.UpdatedAt = Pending, "", time.Now().UTC()
	resolveCheckpointFailures(plan)
	return s.finishRevision(ctx, plan, command.Operation, reason)
}

func (s *Service) reviseReplace(ctx context.Context, plan *Plan, command ReviseCommand, reason string) (Plan, error) {
	if command.StepIndex < 0 || command.StepIndex >= len(plan.Steps) {
		return Plan{}, ErrConflict
	}
	step := &plan.Steps[command.StepIndex]
	if !unstarted(*step) {
		return Plan{}, ErrConflict
	}
	offeringID := command.OfferingID
	if offeringID == "" {
		offeringID = step.OfferingID
	}
	capability := command.Capability
	if capability == "" {
		capability = step.Capability
	}
	planned, err := s.revisedStep(capability, offeringID, command.Parameters)
	if err != nil {
		return Plan{}, err
	}
	// A replaced step keeps the checkpoint its approved contract declared.
	planned.Checkpoint = step.Checkpoint
	planned.Retry = step.Retry
	planned.RevisionOperation, planned.RevisionReason = OperationReplace, reason
	steps := slices.Clone(plan.Steps)
	steps[command.StepIndex] = planned
	if err := s.validateRevisedChain(ctx, plan, steps); err != nil {
		return Plan{}, err
	}
	plan.Steps = steps
	if plan.State == Paused && plan.Failure == ReasonCheckpointFailed {
		plan.State, plan.Failure = Pending, ""
		resolveCheckpointFailures(plan)
	}
	plan.UpdatedAt = time.Now().UTC()
	return s.finishRevision(ctx, plan, command.Operation, reason)
}

func (s *Service) reviseSkip(ctx context.Context, plan *Plan, command ReviseCommand, reason string) (Plan, error) {
	if command.StepIndex < 0 || command.StepIndex >= len(plan.Steps) {
		return Plan{}, ErrConflict
	}
	step := &plan.Steps[command.StepIndex]
	if !unstarted(*step) || step.Checkpoint != nil {
		// A step with a checkpoint is a contract point; skipping it would drop
		// the approved acceptance check.
		return Plan{}, ErrConflict
	}
	steps := slices.Clone(plan.Steps)
	steps[command.StepIndex].State = Skipped
	steps[command.StepIndex].Failure = ""
	steps[command.StepIndex].RevisionOperation = OperationSkip
	steps[command.StepIndex].RevisionReason = reason
	if err := s.validateRevisedChain(ctx, plan, steps); err != nil {
		return Plan{}, err
	}
	plan.Steps = steps
	if noStepRemains(plan.Steps) && !hasFailedCheckpoint(plan.Steps) {
		plan.State, plan.Failure = Succeeded, ""
	} else if plan.State == Paused && plan.Failure == ReasonCheckpointFailed {
		plan.State, plan.Failure = Pending, ""
	}
	if plan.Failure == "" {
		resolveCheckpointFailures(plan)
	}
	plan.UpdatedAt = time.Now().UTC()
	return s.finishRevision(ctx, plan, command.Operation, reason)
}

func (s *Service) reviseRetry(ctx context.Context, plan *Plan, command ReviseCommand) (Plan, error) {
	if plan.State != Failed || command.StepIndex < 0 || command.StepIndex >= len(plan.Steps) ||
		plan.Steps[command.StepIndex].State != Failed {
		return Plan{}, ErrConflict
	}
	return s.retryStep(ctx, plan, command.StepIndex, plan.Steps[command.StepIndex].Revision)
}

func (s *Service) reviseWaive(ctx context.Context, plan *Plan, command ReviseCommand, reason string) (Plan, error) {
	if command.StepIndex < 0 || command.StepIndex >= len(plan.Steps) {
		return Plan{}, ErrConflict
	}
	step := &plan.Steps[command.StepIndex]
	if step.Checkpoint == nil || step.Checkpoint.State != CheckpointFailed || plan.Failure != ReasonCheckpointFailed {
		return Plan{}, ErrConflict
	}
	waived := 0
	for _, candidate := range plan.Steps {
		if candidate.Checkpoint != nil && candidate.Checkpoint.State == CheckpointWaived {
			waived++
		}
	}
	if waived >= MaxWaivedCheckpoints {
		return Plan{}, ErrRevisionOutsidePlan
	}
	step.Checkpoint.State, step.Checkpoint.Resolution = CheckpointWaived, reason
	step.RevisionOperation, step.RevisionReason = OperationWaiveCheckpoint, reason
	step.Failure, step.Retryable = "", false
	plan.State, plan.Failure, plan.UpdatedAt = Pending, "", time.Now().UTC()
	return s.finishRevision(ctx, plan, command.Operation, reason)
}

func (s *Service) finishRevision(ctx context.Context, plan *Plan, operation, reason string) (Plan, error) {
	// The reason itself stays in the step's provenance; the log records only
	// the operation and the length, never the Agent's free text.
	s.logger.Info("处理计划修订", "plan_id", plan.ID, "operation", operation, "reason_length", utf8.RuneCountInString(reason))
	if err := s.savePlan(ctx, plan); err != nil {
		return Plan{}, err
	}
	return *plan, nil
}

// revisedStep builds one new pending step under today's Catalog rules. input
// carries only ordinary parameters; a source parameter is refused.
func (s *Service) revisedStep(capability generation.Capability, offeringID generation.OfferingID, input json.RawMessage) (Step, error) {
	offering, ok := s.catalog.Get(offeringID)
	if !ok || !offering.Enabled || !s.catalog.OfferingAvailable(offeringID) ||
		offering.Domain != generation.DomainProcessing || offering.Capability != capability {
		return Step{}, ErrInvalid
	}
	var parameters map[string]json.RawMessage
	if len(input) == 0 || json.Unmarshal(input, &parameters) != nil || parameters == nil {
		return Step{}, ErrInvalid
	}
	declared := make(map[string]generation.Parameter, len(offering.Parameters))
	for _, parameter := range offering.Parameters {
		declared[parameter.Name] = parameter
	}
	for name := range parameters {
		parameter, known := declared[name]
		if _, source := schema.SourceParameterRole(parameter); !known || source ||
			name == offering.ExtraConsentParameter() {
			return Step{}, ErrInvalid
		}
	}
	for _, parameter := range offering.Parameters {
		if _, source := schema.SourceParameterRole(parameter); source || !parameter.Required {
			continue
		}
		if _, provided := parameters[parameter.Name]; !provided && len(parameter.Default) == 0 {
			return Step{}, ErrInvalid
		}
	}
	credits, quote, priceErr := processingPrice(offering, input)
	if priceErr != nil {
		return Step{}, ErrInvalid
	}
	return Step{Capability: offering.Capability, OfferingID: offering.ID, Input: input,
		EstimatedCredits: credits, NativeQuote: quote, Revision: offering.Revision, State: Pending}, nil
}

// validateRevisedChain re-runs the plan's source and face-budget rules over
// the full step list. Every step must bind a source and stay within the
// downstream source-model limit; a skipped step is passed over.
func (s *Service) validateRevisedChain(ctx context.Context, plan *Plan, steps []Step) error {
	source, err := s.gateway.Get(ctx, plan.OwnerID, plan.SourceTaskID)
	if err != nil {
		return err
	}
	previous := source.Offering
	chain := chainTriangles{}.after(source.Offering, source.Input)
	first := true
	for _, step := range steps {
		if step.State == Skipped {
			continue
		}
		offering, ok := s.catalog.Get(step.OfferingID)
		if !ok || !offering.Enabled || offering.Domain != generation.DomainProcessing {
			return ErrInvalid
		}
		if err := chain.check(offering); err != nil {
			return err
		}
		chain = chain.after(offering, step.Input)
		if first {
			if _, err := buildInput(offering, source, plan.SourceAssetID, s.sourcePreference, step.Input); err != nil {
				return ErrInvalid
			}
			first = false
		} else if _, err := generation.BindProjectedModelSource(offering, previous); err != nil {
			return ErrInvalid
		}
		previous = offering
	}
	return nil
}

func unstarted(step Step) bool {
	return step.State == Pending && step.TaskID == "" && step.PreparedInputID == "" &&
		step.AuthorizationRef == "" && step.SubmissionUncertain == false
}

func hasFailedCheckpoint(steps []Step) bool {
	for _, step := range steps {
		// A checkpoint that was already resolved (a contingency ran, a step
		// was replaced or skipped, or it was waived) keeps its failed result as
		// provenance but no longer blocks the plan.
		if step.Failure == ReasonCheckpointFailed {
			return true
		}
	}
	return false
}

// resolveCheckpointFailures drops the blocking failure reason from steps whose
// failed checkpoint the revision already handled. The checkpoint's failed
// state, results and resolution stay as the audit trail.
func resolveCheckpointFailures(plan *Plan) {
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.Failure == ReasonCheckpointFailed {
			step.Failure, step.Retryable = "", false
		}
	}
}

func failedChecks(checkpoint *Checkpoint) []string {
	var failed []string
	for _, result := range checkpoint.Results {
		if result.Status == "fail" {
			failed = append(failed, result.Check)
		}
	}
	return failed
}

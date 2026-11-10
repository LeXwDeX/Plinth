package processing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelcheck"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// Checkpoint states. pending waits for the step's Task to succeed; passed and
// failed are the server's judgement of the saved model; waived means the
// Agent (bounded and with a reason) let the plan continue despite a failure;
// unknown means the model could not be evaluated and the plan continued.
const (
	CheckpointPending = "pending"
	CheckpointPassed  = "passed"
	CheckpointFailed  = "failed"
	CheckpointWaived  = "waived"
	CheckpointUnknown = "unknown"
)

// CheckpointResolutionUserContinue is the resolution recorded when the person
// continues past a failed checkpoint from the processing panel. The Agent's
// waive records its own reason instead.
const CheckpointResolutionUserContinue = "user_continue"

// MaxCheckpointChecks bounds one checkpoint's checks; the Chat tool Schema's
// maxItems for a checkpoint is aligned with it.
const MaxCheckpointChecks = 8

// MaxWaivedCheckpoints bounds how often one plan may waive a failed
// checkpoint. The Schema's chat.processing.revision.max_waived_checkpoints is
// aligned with it.
const MaxWaivedCheckpoints = 1

// MaxRevisionReasonCharacters bounds the Agent's revision reason; the Chat
// tool Schema declares the same bound through
// chat.processing.revision.max_reason_characters.
const MaxRevisionReasonCharacters = 400

// CheckpointResult is one judged property of a checkpoint. It never carries
// bytes or upstream data: actual is the measured value only.
type CheckpointResult struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Actual any    `json:"actual"`
	Detail string `json:"detail,omitempty"`
}

// Checkpoint is the check after a step (#251). Checks and their server-derived
// thresholds are frozen when the plan is approved; the Agent may only select
// check ids.
type Checkpoint struct {
	Checks      []budget.PlanCheck `json:"checks"`
	Results     []CheckpointResult `json:"results,omitempty"`
	State       string             `json:"state"`
	Resolution  string             `json:"resolution,omitempty"`
	Attempts    int                `json:"attempts,omitempty"`
	EvaluatedAt *time.Time         `json:"evaluated_at,omitempty"`
}

// ModelReport reads the inspection report of one saved model of the owner.
// The application wires generationtool's bounded, cached loader; processing
// never opens model bytes itself.
type ModelReport func(ctx context.Context, owner generation.OwnerID, taskID generation.TaskID,
	assetID generation.AssetID) (modelinspect.Report, error)

// WithModelReport installs the checkpoint evaluator. A plan whose steps
// declare checkpoints cannot be created without it.
func (s *Service) WithModelReport(report ModelReport) *Service {
	s.modelReport = report
	return s
}

// effectiveCheckpoint returns the stored checkpoint, or nil for a historical
// step. A checkpoint in an unexpected state is treated as absent: nothing
// invented is evaluated.
func effectiveCheckpoint(step Step) *Checkpoint {
	return step.Checkpoint
}

func validateCheckpoint(checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return nil
	}
	if len(checkpoint.Checks) == 0 || len(checkpoint.Checks) > MaxCheckpointChecks {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, check := range checkpoint.Checks {
		if !modelcheck.Known(check.Check) || seen[check.Check] {
			return ErrInvalid
		}
		seen[check.Check] = true
	}
	return nil
}

// checkpointBlocked evaluates a checkpoint once the step succeeded and stores
// its judgement. It returns true when the plan must stop before the next step;
// the caller returns without saving again. An evaluation failure is recorded as
// unknown (the plan continues) so a storage fault cannot deadlock it.
func (s *Service) checkpointBlocked(ctx context.Context, plan *Plan, step *Step, task generation.Task) (bool, error) {
	checkpoint := effectiveCheckpoint(*step)
	if checkpoint == nil || checkpoint.State != CheckpointPending {
		return false, nil
	}
	now := time.Now().UTC()
	checkpoint.Attempts++
	checkpoint.EvaluatedAt = &now
	blocked, err := s.judgeCheckpoint(ctx, plan, task, checkpoint)
	if err != nil {
		s.logger.Warn("处理计划检查点无法评估，记为未知并继续", "plan_id", plan.ID, "error", err)
		checkpoint.State, checkpoint.Resolution = CheckpointUnknown, ReasonCheckpointUnavailable
		return false, nil
	}
	if !blocked {
		checkpoint.State = CheckpointPassed
		return false, nil
	}
	// The step itself carries the failure reason (#251). get_processing_plan
	// classifies a plan failure from planFailureStep; without a reason on the
	// succeeded step it reported an internal error and told the Agent to
	// contact an administrator instead of using the declared contingency.
	checkpoint.State, checkpoint.Resolution = CheckpointFailed, ReasonCheckpointFailed
	step.Failure, step.Retryable = ReasonCheckpointFailed, false
	plan.State, plan.Failure = Paused, ReasonCheckpointFailed
	return true, nil
}

func (s *Service) judgeCheckpoint(ctx context.Context, plan *Plan, task generation.Task,
	checkpoint *Checkpoint) (bool, error) {
	if s.modelReport == nil {
		return false, errors.New("processing: model report evaluator is unavailable")
	}
	// The saved model may have several formats; the inspection reads binary
	// glTF, so prefer the GLB the delivery always includes and fall back to
	// the first model file only when no GLB exists (the evaluator then records
	// an unknown rather than guessing another container).
	assetID := ""
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.StorageKey != "" && strings.EqualFold(asset.MediaType, "model/gltf-binary") {
			assetID = string(asset.ID)
			break
		}
	}
	if assetID == "" {
		for _, asset := range task.Assets {
			if asset.Role == "model" && asset.StorageKey != "" {
				assetID = string(asset.ID)
				break
			}
		}
	}
	if assetID == "" {
		return false, errors.New("processing: checkpoint source has no saved model")
	}
	report, err := s.modelReport(ctx, plan.OwnerID, task.ID, generation.AssetID(assetID))
	if err != nil {
		return false, err
	}
	results := modelcheck.Evaluate(report, checkpoint.Checks)
	checkpoint.Results = make([]CheckpointResult, 0, len(results))
	for _, result := range results {
		checkpoint.Results = append(checkpoint.Results, CheckpointResult{
			Check: result.Check, Status: result.Status, Actual: result.Actual, Detail: result.Detail,
		})
	}
	return modelcheck.Failed(results), nil
}

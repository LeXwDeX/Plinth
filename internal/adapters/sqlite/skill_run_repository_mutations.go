package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

func findSkillStep(run skillrun.Run, key string) (skillrun.Step, bool) {
	for _, step := range run.Steps {
		if step.Spec.Key == key {
			return step, true
		}
	}
	return skillrun.Step{}, false
}

// skillStepIdentityValid is the cheap pre-check before a write transaction.
// The identity itself depends on the step's attempt, which only the
// transaction can read; skillStepIdentityMatches repeats it there.
func skillStepIdentityValid(cmd skillrun.StepCommand) bool {
	return cmd.OwnerID != "" && cmd.RunID != "" && cmd.StepKey != "" &&
		cmd.OperationID != "" && cmd.IdempotencyKey != ""
}

func skillStepIdentityMatches(cmd skillrun.StepCommand, step skillrun.Step) bool {
	op, key, _ := skillrun.StepIdentityAt(cmd.RunID, cmd.StepKey, step.Attempt)
	return cmd.OperationID == op && cmd.IdempotencyKey == key
}

func (r *Repository) skillMutate(ctx context.Context, owner generation.OwnerID, id string,
	fn func(*sql.Conn, skillrun.Run) error) (skillrun.Run, error) {
	if owner == "" || id == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	var result skillrun.Run
	err := r.withSkillWrite(ctx, func(conn *sql.Conn) error {
		run, err := loadSkillRun(ctx, conn, owner, id)
		if err != nil {
			return err
		}
		if err = fn(conn, run); err != nil {
			return err
		}
		result, err = loadSkillRun(ctx, conn, owner, id)
		return err
	})
	return result, err
}

func touchSkillRun(ctx context.Context, conn *sql.Conn, run skillrun.Run) error {
	_, err := conn.ExecContext(ctx, `UPDATE skill_runs SET updated_at=? WHERE id=? AND owner_id=?`,
		formatTime(time.Now().UTC()), run.ID, run.OwnerID)
	return err
}

func preparedUsesAsset(prepared generation.PreparedInput, asset generation.AssetID) bool {
	for _, a := range prepared.Assets {
		if a.ID == asset {
			return true
		}
	}
	for _, dep := range prepared.Dependencies {
		for _, a := range dep.OutputAssets {
			if a.ID == asset {
				return true
			}
		}
	}
	return false
}

func expectedSkillSource(run skillrun.Run, step skillrun.Step) (generation.AssetID, error) {
	if step.Spec.SourceStepKey == "" {
		return run.SourceAssetID, nil
	}
	prior, ok := findSkillStep(run, step.Spec.SourceStepKey)
	if !ok || prior.Phase != skillrun.OutputReady || prior.OutputAssetID == "" ||
		prior.Review != skillrun.Accepted {
		return "", skillrun.ErrConflict
	}
	return prior.OutputAssetID, nil
}

func (r *Repository) AttachSkillPrepared(ctx context.Context, cmd skillrun.PreparedCommand) (skillrun.Run, error) {
	if !skillStepIdentityValid(cmd.StepCommand) || cmd.PreparedInputID == "" || cmd.PreparedInputHash == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return r.skillMutate(ctx, cmd.OwnerID, cmd.RunID, func(conn *sql.Conn, run skillrun.Run) error {
		step, ok := findSkillStep(run, cmd.StepKey)
		if !ok {
			return skillrun.ErrMissing
		}
		if !skillStepIdentityMatches(cmd.StepCommand, step) {
			return skillrun.ErrInvalid
		}
		if step.Phase != skillrun.Planned {
			if step.PreparedInputID == cmd.PreparedInputID && step.PreparedInputHash == cmd.PreparedInputHash {
				return nil
			}
			return skillrun.ErrConflict
		}
		source, err := expectedSkillSource(run, step)
		if err != nil {
			return err
		}
		var payload, storedHash string
		err = conn.QueryRowContext(ctx, `SELECT payload_json,input_hash FROM generation_prepared_inputs
			WHERE id=? AND owner_id=? AND session_id=?`, cmd.PreparedInputID, run.OwnerID, run.SessionID).
			Scan(&payload, &storedHash)
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrMissing
		}
		if err != nil {
			return err
		}
		var prepared generation.PreparedInput
		if err = json.Unmarshal([]byte(payload), &prepared); err != nil {
			return err
		}
		if storedHash != cmd.PreparedInputHash || prepared.InputHash != storedHash ||
			prepared.ID != cmd.PreparedInputID || prepared.OwnerID != run.OwnerID ||
			prepared.SessionID != run.SessionID || prepared.Capability != step.Spec.Capability ||
			prepared.OfferingID != step.Spec.OfferingID ||
			prepared.OfferingRevision != step.Spec.OfferingRevision || !preparedUsesAsset(prepared, source) {
			return skillrun.ErrConflict
		}
		if step.Spec.PromptParameter != "" || step.Spec.SourceParameter != "" {
			if !skillrun.MatchesPreparedInput(step.Spec, source, prepared.NormalizedInput) {
				return skillrun.ErrConflict
			}
		}
		_, err = conn.ExecContext(ctx, `UPDATE skill_run_steps SET phase=?,prepared_input_id=?,prepared_input_hash=?
			WHERE run_id=? AND step_key=? AND phase=?`, skillrun.Prepared, cmd.PreparedInputID,
			cmd.PreparedInputHash, run.ID, cmd.StepKey, skillrun.Planned)
		if err != nil {
			return err
		}
		return touchSkillRun(ctx, conn, run)
	})
}

func (r *Repository) AttachSkillGrant(ctx context.Context, cmd skillrun.GrantCommand) (skillrun.Run, error) {
	if !skillStepIdentityValid(cmd.StepCommand) || cmd.GrantRequestID == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return r.skillMutate(ctx, cmd.OwnerID, cmd.RunID, func(conn *sql.Conn, run skillrun.Run) error {
		step, ok := findSkillStep(run, cmd.StepKey)
		if !ok {
			return skillrun.ErrMissing
		}
		if !skillStepIdentityMatches(cmd.StepCommand, step) {
			return skillrun.ErrInvalid
		}
		grant, err := grantByRequest(ctx, conn, string(run.OwnerID), cmd.GrantRequestID)
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrConflict
		}
		if err != nil {
			return err
		}
		if grant.Harness != run.Harness {
			return skillrun.ErrConflict
		}
		if step.Phase != skillrun.Prepared {
			if step.GrantRequestID == cmd.GrantRequestID {
				return nil
			}
			return skillrun.ErrConflict
		}
		_, _, planStep := skillrun.StepIdentityAt(run.ID, cmd.StepKey, step.Attempt)
		var status string
		err = conn.QueryRowContext(ctx, `SELECT status FROM execution_grants WHERE request_id=?
			AND owner_id=? AND session_id=? AND prepared_input_id=? AND prepared_input_hash=?
			AND offering_id=? AND offering_revision=? AND operation_id=?
			AND idempotency_key=? AND plan_step_id=?`, cmd.GrantRequestID, run.OwnerID,
			run.SessionID, step.PreparedInputID, step.PreparedInputHash, step.Spec.OfferingID,
			step.Spec.OfferingRevision, cmd.OperationID, cmd.IdempotencyKey, planStep).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrConflict
		}
		if err != nil {
			return err
		}
		if status == "" {
			return skillrun.ErrConflict
		}
		_, err = conn.ExecContext(ctx, `UPDATE skill_run_steps SET phase=?,grant_request_id=?
			WHERE run_id=? AND step_key=? AND phase=?`, skillrun.GrantPending,
			cmd.GrantRequestID, run.ID, cmd.StepKey, skillrun.Prepared)
		if err != nil {
			return err
		}
		return touchSkillRun(ctx, conn, run)
	})
}

func (r *Repository) BindSkillTask(ctx context.Context, cmd skillrun.TaskCommand) (skillrun.Run, error) {
	if !skillStepIdentityValid(cmd.StepCommand) || cmd.TaskID == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return r.skillMutate(ctx, cmd.OwnerID, cmd.RunID, func(conn *sql.Conn, run skillrun.Run) error {
		step, ok := findSkillStep(run, cmd.StepKey)
		if !ok {
			return skillrun.ErrMissing
		}
		if !skillStepIdentityMatches(cmd.StepCommand, step) {
			return skillrun.ErrInvalid
		}
		grant, err := grantByRequest(ctx, conn, string(run.OwnerID), step.GrantRequestID)
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrConflict
		}
		if err != nil {
			return err
		}
		if grant.Harness != run.Harness {
			return skillrun.ErrConflict
		}
		if step.Phase != skillrun.GrantPending {
			if step.TaskID == cmd.TaskID {
				return nil
			}
			return skillrun.ErrConflict
		}
		var one int
		err = conn.QueryRowContext(ctx, `SELECT 1 FROM execution_grants g
			JOIN generation_tasks t ON t.id=g.task_id
			WHERE g.request_id=? AND g.owner_id=? AND g.session_id=? AND g.status='bound'
			AND g.task_id=? AND g.prepared_input_id=? AND g.prepared_input_hash=?
			AND g.operation_id=? AND g.idempotency_key=?
			AND t.owner_id=? AND t.session_id=? AND t.idempotency_key=?`,
			step.GrantRequestID, run.OwnerID, run.SessionID, cmd.TaskID, step.PreparedInputID,
			step.PreparedInputHash, cmd.OperationID, cmd.IdempotencyKey,
			run.OwnerID, run.SessionID, cmd.IdempotencyKey).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `UPDATE skill_run_steps SET phase=?,task_id=?
			WHERE run_id=? AND step_key=? AND phase=?`, skillrun.TaskBound, cmd.TaskID,
			run.ID, cmd.StepKey, skillrun.GrantPending)
		if err != nil {
			return err
		}
		return touchSkillRun(ctx, conn, run)
	})
}

func (r *Repository) RecordSkillOutput(ctx context.Context, cmd skillrun.OutputCommand) (skillrun.Run, error) {
	if cmd.OwnerID == "" || cmd.RunID == "" || cmd.StepKey == "" || cmd.AssetID == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return r.skillMutate(ctx, cmd.OwnerID, cmd.RunID, func(conn *sql.Conn, run skillrun.Run) error {
		step, ok := findSkillStep(run, cmd.StepKey)
		if !ok {
			return skillrun.ErrMissing
		}
		if step.Phase != skillrun.TaskBound {
			if step.Phase == skillrun.OutputReady && step.OutputAssetID == cmd.AssetID {
				return nil
			}
			return skillrun.ErrConflict
		}
		var one int
		err := conn.QueryRowContext(ctx, `SELECT 1 FROM generation_assets a
			JOIN generation_tasks t ON t.id=a.task_id
			WHERE a.id=? AND a.task_id=? AND a.role=? AND t.owner_id=?
			AND t.session_id=? AND t.state='SUCCEEDED' AND a.storage_key<>''
			AND a.sha256<>'' AND a.size_bytes>0`, cmd.AssetID, step.TaskID,
			step.Spec.OutputRole, run.OwnerID, run.SessionID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `UPDATE skill_run_steps SET phase=?,output_asset_id=?
			WHERE run_id=? AND step_key=? AND phase=?`, skillrun.OutputReady,
			cmd.AssetID, run.ID, cmd.StepKey, skillrun.TaskBound)
		if err != nil {
			return err
		}
		return touchSkillRun(ctx, conn, run)
	})
}

func (r *Repository) ReviewSkillOutput(ctx context.Context, cmd skillrun.ReviewCommand) (skillrun.Run, error) {
	if cmd.OwnerID == "" || cmd.RunID == "" || cmd.StepKey == "" ||
		(cmd.Decision != skillrun.Accepted && cmd.Decision != skillrun.Rejected) {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return r.skillMutate(ctx, cmd.OwnerID, cmd.RunID, func(conn *sql.Conn, run skillrun.Run) error {
		step, ok := findSkillStep(run, cmd.StepKey)
		if !ok {
			return skillrun.ErrMissing
		}
		if step.Phase != skillrun.OutputReady {
			return skillrun.ErrConflict
		}
		if step.Review != skillrun.Unreviewed {
			if step.Review == cmd.Decision {
				return nil
			}
			return skillrun.ErrConflict
		}
		_, err := conn.ExecContext(ctx, `UPDATE skill_run_steps SET review=?,reviewed_at=?
			WHERE run_id=? AND step_key=? AND review=?`, cmd.Decision,
			formatTime(time.Now().UTC()), run.ID, cmd.StepKey, skillrun.Unreviewed)
		if err != nil {
			return err
		}
		return touchSkillRun(ctx, conn, run)
	})
}

// RestartSkillStep moves a step whose attempt ended without a result to its
// next attempt. The transaction repeats every condition: the attempt is the
// one the caller saw, and the task it ended with failed (or the grant it
// waited on was denied) for this owner and session. The old task and grant
// are left untouched; only the step's pointers and paid identity move on.
func (r *Repository) RestartSkillStep(ctx context.Context, cmd skillrun.RestartCommand) (skillrun.Run, error) {
	if cmd.OwnerID == "" || cmd.RunID == "" || cmd.StepKey == "" || cmd.ExpectedAttempt < 0 ||
		(cmd.FailedTaskID == "") == (cmd.DeniedGrantID == "") {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return r.skillMutate(ctx, cmd.OwnerID, cmd.RunID, func(conn *sql.Conn, run skillrun.Run) error {
		step, ok := findSkillStep(run, cmd.StepKey)
		if !ok {
			return skillrun.ErrMissing
		}
		if step.Attempt > cmd.ExpectedAttempt {
			return nil // a repeat of a restart that already happened
		}
		if step.Attempt < cmd.ExpectedAttempt {
			return skillrun.ErrConflict
		}
		var one int
		var err error
		if cmd.FailedTaskID != "" {
			if step.TaskID != cmd.FailedTaskID {
				return skillrun.ErrConflict
			}
			err = conn.QueryRowContext(ctx, `SELECT 1 FROM generation_tasks
				WHERE id=? AND owner_id=? AND session_id=? AND state IN ('FAILED','CANCELED')`,
				cmd.FailedTaskID, run.OwnerID, run.SessionID).Scan(&one)
		} else {
			if step.GrantRequestID != cmd.DeniedGrantID || step.TaskID != "" {
				return skillrun.ErrConflict
			}
			err = conn.QueryRowContext(ctx, `SELECT 1 FROM execution_grants
				WHERE request_id=? AND owner_id=? AND session_id=? AND status='denied'`,
				cmd.DeniedGrantID, run.OwnerID, run.SessionID).Scan(&one)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return skillrun.ErrConflict
		}
		if err != nil {
			return err
		}
		next := step.Attempt + 1
		op, key, planStep := skillrun.StepIdentityAt(run.ID, cmd.StepKey, next)
		result, err := conn.ExecContext(ctx, `UPDATE skill_run_steps SET attempt=?,phase=?,
			operation_id=?,idempotency_key=?,plan_step_id=?,prepared_input_id=NULL,prepared_input_hash=NULL,
			grant_request_id=NULL,task_id=NULL,output_asset_id=NULL,review=?,reviewed_at=NULL
			WHERE run_id=? AND step_key=? AND attempt=?`, next, skillrun.Planned, op, key, planStep,
			skillrun.Unreviewed, run.ID, cmd.StepKey, step.Attempt)
		if err != nil {
			return err
		}
		if changed, e := result.RowsAffected(); e != nil || changed != 1 {
			if e != nil {
				return e
			}
			return skillrun.ErrConflict
		}
		return touchSkillRun(ctx, conn, run)
	})
}

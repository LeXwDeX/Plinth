package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

var _ skillrun.Store = (*Repository)(nil)

type skillQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (r *Repository) withSkillWrite(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err = fn(conn); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

const skillRunSelect = `SELECT id,owner_id,session_id,recipe_id,recipe_revision,recipe_hash,selected_offering_id,
source_asset_id,COALESCE(source_task_id,''),COALESCE(source_prepared_input_id,''),
idempotency_key,request_hash,created_at,updated_at,harness_revision,harness_digest FROM skill_runs`

func scanSkillRun(row interface{ Scan(...any) error }) (skillrun.Run, error) {
	var run skillrun.Run
	var created, updated string
	err := row.Scan(&run.ID, &run.OwnerID, &run.SessionID, &run.RecipeID,
		&run.RecipeRevision, &run.RecipeHash, &run.SelectedOfferingID, &run.SourceAssetID, &run.SourceTaskID,
		&run.SourcePreparedInputID, &run.IdempotencyKey, &run.RequestHash, &created, &updated, &run.Harness.Revision, &run.Harness.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return run, skillrun.ErrMissing
	}
	if err != nil {
		return run, err
	}
	if !run.Harness.Valid() {
		return run, skillrun.ErrInvalid
	}
	run.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return run, err
	}
	run.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return run, err
}

func loadSkillRun(ctx context.Context, q skillQuery, owner generation.OwnerID, id string) (skillrun.Run, error) {
	run, err := scanSkillRun(q.QueryRowContext(ctx, skillRunSelect+` WHERE owner_id=? AND id=?`, owner, id))
	if err != nil {
		return run, err
	}
	rows, err := q.QueryContext(ctx, `SELECT spec_json,phase,COALESCE(prepared_input_id,''),
COALESCE(prepared_input_hash,''),COALESCE(grant_request_id,''),COALESCE(task_id,''),
COALESCE(output_asset_id,''),review,COALESCE(reviewed_at,''),attempt
FROM skill_run_steps WHERE run_id=? ORDER BY ordinal`, id)
	if err != nil {
		return skillrun.Run{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var step skillrun.Step
		var spec, reviewed string
		if err = rows.Scan(&spec, &step.Phase, &step.PreparedInputID,
			&step.PreparedInputHash, &step.GrantRequestID, &step.TaskID,
			&step.OutputAssetID, &step.Review, &reviewed, &step.Attempt); err != nil {
			return skillrun.Run{}, err
		}
		if err = json.Unmarshal([]byte(spec), &step.Spec); err != nil {
			return skillrun.Run{}, err
		}
		if reviewed != "" {
			at, parseErr := time.Parse(time.RFC3339Nano, reviewed)
			if parseErr != nil {
				return skillrun.Run{}, parseErr
			}
			step.ReviewedAt = &at
		}
		run.Steps = append(run.Steps, step)
	}
	if err = rows.Err(); err != nil {
		return skillrun.Run{}, err
	}
	if len(run.Steps) == 0 {
		return skillrun.Run{}, skillrun.ErrConflict
	}
	return run, nil
}

func (r *Repository) GetSkillRun(ctx context.Context, owner generation.OwnerID, id string) (skillrun.Run, error) {
	if owner == "" || id == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	return loadSkillRun(ctx, r.db, owner, id)
}

func (r *Repository) GetSkillRunByIdempotency(ctx context.Context, owner generation.OwnerID, key string) (skillrun.Run, error) {
	if owner == "" || key == "" {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM skill_runs WHERE owner_id=? AND idempotency_key=?`, owner, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return skillrun.Run{}, skillrun.ErrMissing
	}
	if err != nil {
		return skillrun.Run{}, err
	}
	return loadSkillRun(ctx, r.db, owner, id)
}

// PendingGrant finds the oldest pending human grant attached to a SkillRun
// step in this owner's session, then checks every persisted identity field
// before returning it. A mismatched row is treated as corruption/conflict.
func (r *Repository) PendingGrant(ctx context.Context, owner generation.OwnerID, session string) (skillrun.PendingGrant, error) {
	if owner == "" || session == "" {
		return skillrun.PendingGrant{}, skillrun.ErrInvalid
	}
	const query = `SELECT sr.id,sr.owner_id,sr.session_id,ss.step_key,ss.phase,
		ss.grant_request_id,ss.prepared_input_id,ss.prepared_input_hash,
		ss.operation_id,ss.idempotency_key,ss.plan_step_id,
		COALESCE(json_extract(ss.spec_json,'$.offering_id'),''),
		COALESCE(json_extract(ss.spec_json,'$.offering_revision'),''),
		g.request_id,g.owner_id,g.session_id,g.status,g.prepared_input_id,g.prepared_input_hash,
		g.offering_id,g.offering_revision,g.operation_id,g.idempotency_key,g.plan_step_id,
 sr.harness_revision,sr.harness_digest,g.harness_revision,g.harness_digest
		FROM skill_runs sr
		JOIN skill_run_steps ss ON ss.run_id=sr.id
		JOIN execution_grants g ON g.request_id=ss.grant_request_id
		WHERE sr.owner_id=? AND sr.session_id=? AND ss.phase='grant_pending' AND g.status='pending'
		ORDER BY sr.created_at ASC,sr.id ASC,ss.ordinal ASC,g.request_id ASC LIMIT 1`
	var result skillrun.PendingGrant
	var runBinding, grantBinding harness.Binding
	var runOwner, runSession, phase, preparedID, preparedHash, operationID, key, planStep string
	var grantID, grantOwner, grantSession, status, grantPreparedID, grantPreparedHash string
	var offeringID, offeringRevision, grantOfferingID, grantOfferingRevision, grantOperationID, grantKey, grantPlanStep string
	err := r.db.QueryRowContext(ctx, query, owner, session).Scan(
		&result.RunID, &runOwner, &runSession, &result.StepKey, &phase,
		&result.GrantRequestID, &preparedID, &preparedHash, &operationID, &key, &planStep,
		&offeringID, &offeringRevision, &grantID, &grantOwner, &grantSession, &status,
		&grantPreparedID, &grantPreparedHash, &grantOfferingID, &grantOfferingRevision,
		&grantOperationID, &grantKey, &grantPlanStep, &runBinding.Revision, &runBinding.Digest, &grantBinding.Revision, &grantBinding.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return skillrun.PendingGrant{}, skillrun.ErrMissing
	}
	if err != nil {
		return skillrun.PendingGrant{}, err
	}
	if !runBinding.Valid() || runBinding != grantBinding || runOwner != string(owner) || runSession != session || phase != string(skillrun.GrantPending) ||
		status != "pending" || grantID != result.GrantRequestID || grantOwner != runOwner || grantSession != runSession ||
		preparedID == "" || preparedHash == "" || operationID == "" || key == "" || planStep == "" ||
		preparedID != grantPreparedID || preparedHash != grantPreparedHash ||
		offeringID != grantOfferingID || offeringRevision != grantOfferingRevision ||
		operationID != grantOperationID || key != grantKey || planStep != grantPlanStep {
		return skillrun.PendingGrant{}, skillrun.ErrConflict
	}
	result.PreparedInputID = generation.PreparedInputID(preparedID)
	result.PreparedInputHash = preparedHash
	result.OfferingID = generation.OfferingID(offeringID)
	result.OfferingRevision = offeringRevision
	result.OperationID = operationID
	result.IdempotencyKey = key
	return result, nil
}

func (r *Repository) ListSkillRuns(ctx context.Context, owner generation.OwnerID, session, after string, limit int) ([]skillrun.Run, error) {
	if owner == "" || session == "" || limit < 1 || limit > 100 {
		return nil, skillrun.ErrInvalid
	}
	var rows *sql.Rows
	var err error
	if after == "" {
		rows, err = r.db.QueryContext(ctx, `SELECT id FROM skill_runs
			WHERE owner_id=? AND session_id=? ORDER BY created_at DESC,id DESC LIMIT ?`,
			owner, session, limit)
	} else {
		var cursorTime string
		err = r.db.QueryRowContext(ctx, `SELECT created_at FROM skill_runs
			WHERE owner_id=? AND session_id=? AND id=?`, owner, session, after).Scan(&cursorTime)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, skillrun.ErrMissing
		}
		if err != nil {
			return nil, err
		}
		rows, err = r.db.QueryContext(ctx, `SELECT id FROM skill_runs WHERE owner_id=? AND session_id=?
			AND (created_at<? OR (created_at=? AND id<?))
			ORDER BY created_at DESC,id DESC LIMIT ?`, owner, session, cursorTime,
			cursorTime, after, limit)
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	runs := make([]skillrun.Run, 0, len(ids))
	for _, id := range ids {
		run, loadErr := r.GetSkillRun(ctx, owner, id)
		if loadErr != nil {
			return nil, loadErr
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (r *Repository) CreateSkillRun(ctx context.Context, candidate skillrun.Run) (skillrun.Run, error) {
	if !candidate.Harness.Valid() || candidate.ID == "" || candidate.OwnerID == "" || candidate.SessionID == "" ||
		candidate.RecipeID == "" || candidate.RecipeRevision == "" || candidate.RecipeHash == "" ||
		candidate.SelectedOfferingID == "" ||
		candidate.SourceAssetID == "" || candidate.IdempotencyKey == "" || candidate.RequestHash == "" ||
		len(candidate.Steps) == 0 {
		return skillrun.Run{}, skillrun.ErrInvalid
	}
	recipe := skillrun.Recipe{ID: candidate.RecipeID, Revision: candidate.RecipeRevision}
	for _, step := range candidate.Steps {
		if step.Phase != skillrun.Planned || step.Review != skillrun.Unreviewed ||
			step.PreparedInputID != "" || step.GrantRequestID != "" || step.TaskID != "" {
			return skillrun.Run{}, skillrun.ErrInvalid
		}
		recipe.Steps = append(recipe.Steps, step.Spec)
	}
	if err := skillrun.ValidateRecipe(recipe); err != nil {
		return skillrun.Run{}, err
	}
	selectedUsed := false
	for _, step := range recipe.Steps {
		if step.OfferingID == candidate.SelectedOfferingID {
			selectedUsed = true
		}
	}
	if !selectedUsed {
		return skillrun.Run{}, skillrun.ErrConflict
	}
	encodedRecipe, err := json.Marshal(recipe)
	if err != nil {
		return skillrun.Run{}, err
	}
	recipeDigest := sha256.Sum256(encodedRecipe)
	if candidate.RecipeHash != hex.EncodeToString(recipeDigest[:]) {
		return skillrun.Run{}, skillrun.ErrConflict
	}
	encodedRequest, err := json.Marshal(struct {
		OwnerID               generation.OwnerID         `json:"owner_id"`
		SessionID             string                     `json:"session_id"`
		RecipeHash            string                     `json:"recipe_hash"`
		SelectedOfferingID    generation.OfferingID      `json:"selected_offering_id"`
		SourceAssetID         generation.AssetID         `json:"source_asset_id"`
		SourceTaskID          generation.TaskID          `json:"source_task_id"`
		SourcePreparedInputID generation.PreparedInputID `json:"source_prepared_input_id"`
	}{candidate.OwnerID, candidate.SessionID, candidate.RecipeHash, candidate.SelectedOfferingID, candidate.SourceAssetID,
		candidate.SourceTaskID, candidate.SourcePreparedInputID})
	if err != nil {
		return skillrun.Run{}, err
	}
	if !candidate.Harness.IsZero() {
		encodedRequest, err = json.Marshal(struct {
			Request json.RawMessage `json:"request"`
			Harness harness.Binding `json:"harness"`
		}{encodedRequest, candidate.Harness})
		if err != nil {
			return skillrun.Run{}, err
		}
	}
	requestDigest := sha256.Sum256(encodedRequest)
	if candidate.RequestHash != hex.EncodeToString(requestDigest[:]) {
		return skillrun.Run{}, skillrun.ErrConflict
	}
	var stored skillrun.Run
	err = r.withSkillWrite(ctx, func(conn *sql.Conn) error {
		var existingID, existingHash string
		err := conn.QueryRowContext(ctx, `SELECT id,request_hash FROM skill_runs
			WHERE owner_id=? AND idempotency_key=?`, candidate.OwnerID, candidate.IdempotencyKey).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != candidate.RequestHash {
				return skillrun.ErrConflict
			}
			stored, err = loadSkillRun(ctx, conn, candidate.OwnerID, existingID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if ok, e := skillSourceReady(ctx, conn, candidate); e != nil {
			return e
		} else if !ok {
			return skillrun.ErrMissing
		}
		_, err = conn.ExecContext(ctx, `INSERT INTO skill_runs
			(id,owner_id,session_id,recipe_id,recipe_revision,recipe_hash,selected_offering_id,source_asset_id,
			source_task_id,source_prepared_input_id,idempotency_key,request_hash,created_at,updated_at,harness_revision,harness_digest)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, candidate.ID, candidate.OwnerID, candidate.SessionID,
			candidate.RecipeID, candidate.RecipeRevision, candidate.RecipeHash, candidate.SelectedOfferingID, candidate.SourceAssetID,
			nullSceneString(string(candidate.SourceTaskID)), nullSceneString(string(candidate.SourcePreparedInputID)),
			candidate.IdempotencyKey, candidate.RequestHash, formatTime(candidate.CreatedAt), formatTime(candidate.UpdatedAt), candidate.Harness.Revision, candidate.Harness.Digest)
		if err != nil {
			return err
		}
		for i, step := range candidate.Steps {
			encoded, e := json.Marshal(step.Spec)
			if e != nil {
				return e
			}
			op, key, planStep := skillrun.StepIdentity(candidate.ID, step.Spec.Key)
			_, e = conn.ExecContext(ctx, `INSERT INTO skill_run_steps
				(run_id,ordinal,step_key,spec_json,phase,operation_id,idempotency_key,plan_step_id,review)
				VALUES (?,?,?,?,?,?,?,?,?)`, candidate.ID, i, step.Spec.Key, string(encoded),
				skillrun.Planned, op, key, planStep, skillrun.Unreviewed)
			if e != nil {
				return e
			}
		}
		stored, err = loadSkillRun(ctx, conn, candidate.OwnerID, candidate.ID)
		return err
	})
	return stored, err
}

func skillSourceReady(ctx context.Context, q skillQuery, run skillrun.Run) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM go_chat_sessions WHERE id=? AND owner_id=?`, run.SessionID, run.OwnerID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run.SourceTaskID == "" {
		err = q.QueryRowContext(ctx, `SELECT 1 FROM generation_input_assets WHERE id=? AND owner_id=?
			AND storage_key<>'' AND sha256<>''`, run.SourceAssetID, run.OwnerID).Scan(&one)
	} else {
		err = q.QueryRowContext(ctx, `SELECT 1 FROM generation_assets a
			JOIN generation_tasks t ON t.id=a.task_id WHERE a.id=? AND t.id=?
			AND t.owner_id=? AND t.session_id=? AND t.state='SUCCEEDED'
			AND a.storage_key<>'' AND a.sha256<>'' AND a.size_bytes>0`, run.SourceAssetID, run.SourceTaskID,
			run.OwnerID, run.SessionID).Scan(&one)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run.SourcePreparedInputID != "" {
		if run.SourceTaskID == "" {
			return false, skillrun.ErrInvalid
		}
		err = q.QueryRowContext(ctx, `SELECT 1 FROM generation_prepared_inputs p
			JOIN execution_grants g ON g.prepared_input_id=p.id
			WHERE p.id=? AND p.owner_id=? AND p.session_id=?
			AND g.owner_id=? AND g.session_id=? AND g.status='bound' AND g.task_id=?`,
			run.SourcePreparedInputID, run.OwnerID, run.SessionID,
			run.OwnerID, run.SessionID, run.SourceTaskID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

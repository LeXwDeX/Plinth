package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var _ executiongrant.Repository = (*Repository)(nil)

func randomGrantID() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "eg_" + hex.EncodeToString(b[:]), nil
}

// turnOwnsStoppedGrantNot is a condition on an execution_grants row: it holds
// unless the grant's trigger turn was stopped while the grant is still the
// turn's own (no detached agent operation carries it). Such a grant can never
// be claimed (ClaimExecutionGrantOnConn refuses it), so it must not be
// approved. A detached card left the turn and is unaffected by a Stop.
const turnOwnsStoppedGrantNot = `NOT EXISTS (SELECT 1 FROM go_chat_turns t
  WHERE t.assistant_message_id=execution_grants.trigger_turn_id AND t.owner_id=execution_grants.owner_id
  AND t.session_id=execution_grants.session_id AND t.stop_requested<>0
  AND NOT EXISTS (SELECT 1 FROM agent_generation_operations o
   WHERE o.owner_id=execution_grants.owner_id AND o.grant_request_id=execution_grants.request_id
   AND o.detached_at<>''))`

func (r *Repository) grantWriteConn(ctx context.Context) (*sql.Conn, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func rollbackGrantConn(conn *sql.Conn) {
	_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	_ = conn.Close()
}

func commitGrantConn(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	return conn.Close()
}

func scanGrant(row interface{ Scan(...any) error }) (executiongrant.Grant, error) {
	var g executiongrant.Grant
	var taskID sql.NullString
	var decidedBy, decidedAt sql.NullString
	var created, updated string
	err := row.Scan(&g.RequestID, &g.OwnerID, &g.SessionID, &g.OfferingID,
		&g.OfferingRevision, &g.PreparedInputID, &g.PreparedInputHash,
		&g.IdempotencyKey, &g.OperationID, &g.PlanStepID, &g.TriggerTurnID, &g.Status,
		&taskID, &g.Source, &g.RequestVersion, &decidedBy, &decidedAt, &created, &updated, &g.Harness.Revision, &g.Harness.Digest)
	if err != nil {
		return g, err
	}
	g.TaskID = taskID.String
	g.DecidedBy = decidedBy.String
	if decidedAt.Valid {
		when, err := time.Parse(time.RFC3339Nano, decidedAt.String)
		if err != nil {
			return g, err
		}
		g.DecidedAt = &when
	}
	g.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return g, err
	}
	g.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return g, err
}

const grantSelect = `SELECT request_id, owner_id, session_id, offering_id,
 offering_revision, prepared_input_id, prepared_input_hash, idempotency_key,
 operation_id, plan_step_id, trigger_turn_id, status, task_id, source, request_version,
 decided_by, decided_at, created_at, updated_at, harness_revision, harness_digest FROM execution_grants`

func grantByRequest(ctx context.Context, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, requestID string) (executiongrant.Grant, error) {
	return scanGrant(conn.QueryRowContext(ctx, grantSelect+` WHERE owner_id = ? AND request_id = ?`, owner, requestID))
}

func sameGrantIdentity(a, b executiongrant.Identity) bool { return a == b }

func taskGrantIdentity(task generation.Task) (string, executiongrant.Identity, error) {
	if task.Admission == nil || task.Admission.RequestID == "" {
		return "", executiongrant.Identity{}, generation.RepositoryAuthorizationRequired
	}
	i := executiongrant.Identity{
		OwnerID: string(task.OwnerID), SessionID: task.SessionID,
		OfferingID: string(task.Offering.ID), OfferingRevision: task.Offering.Revision,
		PreparedInputID:   string(task.Admission.PreparedInputID),
		PreparedInputHash: task.Admission.PreparedInputHash,
		IdempotencyKey:    task.IdempotencyKey,
		OperationID:       task.Admission.OperationID, PlanStepID: task.Admission.PlanStepID, Harness: task.Admission.Harness,
	}
	if !i.Valid() {
		return "", executiongrant.Identity{}, generation.RepositoryAuthorizationRequired
	}
	return task.Admission.RequestID, i, nil
}

func (r *Repository) claimTaskExecutionGrant(ctx context.Context, conn *sql.Conn, task generation.Task) error {
	requestID, identity, err := taskGrantIdentity(task)
	if err != nil {
		return err
	}
	boundTask, err := r.ClaimExecutionGrantOnConn(ctx, conn, requestID, identity)
	if err != nil {
		return fmt.Errorf("%w: %w", generation.RepositoryAuthorizationRequired, err)
	}
	if boundTask != "" {
		return generation.RepositoryIdempotencyConflict
	}
	return nil
}

func (r *Repository) bindTaskExecutionGrant(ctx context.Context, conn *sql.Conn, task generation.Task) error {
	requestID, identity, err := taskGrantIdentity(task)
	if err != nil {
		return err
	}
	if err := r.BindExecutionGrantOnConn(ctx, conn, requestID, identity, string(task.ID)); err != nil {
		return fmt.Errorf("%w: %w", generation.RepositoryAuthorizationRequired, err)
	}
	return nil
}

// PreparePending issues a fresh unpredictable request ID for one operation.
// A changed pending/denied request replaces its former ID; an approved or
// consumed request cannot be changed. Reuse of the original key is exact-only.
func (r *Repository) PreparePending(ctx context.Context, identity executiongrant.Identity, source, triggerTurnID string) (executiongrant.Grant, error) {
	if !identity.Valid() || (source != executiongrant.HumanBrowser && source != executiongrant.AgentPending) {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	if (source == executiongrant.AgentPending && triggerTurnID == "") ||
		(source == executiongrant.HumanBrowser && triggerTurnID != "") {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	defer conn.Close()
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	old, err := scanGrant(conn.QueryRowContext(ctx, grantSelect+`
 WHERE owner_id = ? AND session_id = ? AND operation_id = ? AND plan_step_id = ?`,
		identity.OwnerID, identity.SessionID, identity.OperationID, identity.PlanStepID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return executiongrant.Grant{}, err
	}
	if err == nil {
		if old.Source != source || old.TriggerTurnID != triggerTurnID {
			return executiongrant.Grant{}, executiongrant.ErrConflict
		}
		if sameGrantIdentity(old.Identity, identity) {
			if old.Status != executiongrant.Denied {
				if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
					return executiongrant.Grant{}, err
				}
				committed = true
				return old, nil
			}
		}
		if old.Status != executiongrant.Pending && old.Status != executiongrant.Denied {
			return executiongrant.Grant{}, executiongrant.ErrConflict
		}
		if old.IdempotencyKey == identity.IdempotencyKey && !sameGrantIdentity(old.Identity, identity) {
			return executiongrant.Grant{}, executiongrant.ErrConflict
		}
	}
	// A key already associated with another operation cannot be repurposed.
	var usedRequest string
	keyErr := conn.QueryRowContext(ctx, `SELECT request_id FROM execution_grants WHERE owner_id = ? AND idempotency_key = ?`,
		identity.OwnerID, identity.IdempotencyKey).Scan(&usedRequest)
	if keyErr == nil && old.RequestID != usedRequest || (keyErr != nil && !errors.Is(keyErr, sql.ErrNoRows)) {
		if keyErr != nil {
			return executiongrant.Grant{}, keyErr
		}
		return executiongrant.Grant{}, executiongrant.ErrConflict
	}
	requestID, err := randomGrantID()
	if err != nil {
		return executiongrant.Grant{}, err
	}
	now := time.Now().UTC()
	if old.RequestID != "" {
		oldIdentityJSON, err := json.Marshal(old.Identity)
		if err != nil {
			return executiongrant.Grant{}, err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
			(request_id,owner_id,request_version,event,actor_id,identity_json,occurred_at)
			VALUES (?,?,?,'superseded','system',?,?)`,
			old.RequestID, old.OwnerID, old.RequestVersion, string(oldIdentityJSON), now.Format(time.RFC3339Nano)); err != nil {
			return executiongrant.Grant{}, err
		}
		_, err = conn.ExecContext(ctx, `UPDATE execution_grants SET request_id=?, offering_id=?, offering_revision=?,
 prepared_input_id=?, prepared_input_hash=?, idempotency_key=?, status='pending', task_id=NULL,
 request_version=request_version+1, decided_by=NULL, decided_at=NULL, trigger_turn_id=?, updated_at=?, harness_revision=?, harness_digest=?
 WHERE request_id=? AND owner_id=? AND status IN ('pending','denied')`,
			requestID, identity.OfferingID, identity.OfferingRevision, identity.PreparedInputID,
			identity.PreparedInputHash, identity.IdempotencyKey, triggerTurnID, now.Format(time.RFC3339Nano), identity.Harness.Revision, identity.Harness.Digest, old.RequestID, identity.OwnerID)
	} else {
		_, err = conn.ExecContext(ctx, `INSERT INTO execution_grants
 (request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,
 idempotency_key,operation_id,plan_step_id,trigger_turn_id,status,source,request_version,created_at,updated_at,harness_revision,harness_digest)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,'pending',?,1,?,?,?,?)`, requestID, identity.OwnerID, identity.SessionID,
			identity.OfferingID, identity.OfferingRevision, identity.PreparedInputID,
			identity.PreparedInputHash, identity.IdempotencyKey, identity.OperationID,
			identity.PlanStepID, triggerTurnID, source, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), identity.Harness.Revision, identity.Harness.Digest)
	}
	if err != nil {
		return executiongrant.Grant{}, err
	}
	g, err := grantByRequest(ctx, conn, identity.OwnerID, requestID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return executiongrant.Grant{}, err
	}
	committed = true
	return g, nil
}

// DecideExecutionGrant must be called only from a separate authenticated human
// decision action. The data layer never infers approval from request payloads.
func (r *Repository) DecideExecutionGrant(ctx context.Context, owner, requestID, authenticatedActorID string, decision executiongrant.Decision) (executiongrant.Grant, error) {
	if owner == "" || requestID == "" || authenticatedActorID == "" || (decision != executiongrant.Approve && decision != executiongrant.Deny) {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	defer conn.Close()
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	status := executiongrant.Approved
	if decision == executiongrant.Deny {
		status = executiongrant.Denied
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// An approval the trigger turn still owns is refused in the same statement
	// when that turn was stopped: a Stop that commits between a caller's turn
	// check and this write must not leave an approved grant nobody can claim.
	// Denying is always allowed.
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants SET status=?, decided_by=?, decided_at=?, updated_at=?
 WHERE request_id=? AND owner_id=? AND source IN ('human_browser','agent_human_decision') AND status='pending'
 AND (?='denied' OR `+turnOwnsStoppedGrantNot+`)`,
		status, authenticatedActorID, now, now, requestID, owner, status)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if n != 1 {
		// The human decision may have committed while the HTTP response was
		// lost. Repeating the same decision by the same authenticated actor is
		// read-only; it must not create another audit event or change a denial.
		previous, readErr := grantByRequest(ctx, conn, owner, requestID)
		if readErr != nil || previous.DecidedBy != authenticatedActorID {
			return executiongrant.Grant{}, executiongrant.ErrState
		}
		if decision == executiongrant.Approve &&
			(previous.Status == executiongrant.Approved || previous.Status == executiongrant.Claimed || previous.Status == executiongrant.Bound) {
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				return executiongrant.Grant{}, err
			}
			committed = true
			return previous, nil
		}
		if decision == executiongrant.Deny && previous.Status == executiongrant.Denied {
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				return executiongrant.Grant{}, err
			}
			committed = true
			return previous, nil
		}
		return executiongrant.Grant{}, executiongrant.ErrState
	}
	g, err := grantByRequest(ctx, conn, owner, requestID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	identityJSON, err := json.Marshal(g.Identity)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
		(request_id,owner_id,request_version,event,actor_id,identity_json,occurred_at) VALUES (?,?,?,?,?,?,?)`,
		requestID, owner, g.RequestVersion, status, authenticatedActorID, string(identityJSON), now); err != nil {
		return executiongrant.Grant{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return executiongrant.Grant{}, err
	}
	committed = true
	return g, nil
}

// ApproveExecutionGrantByPolicy is the only non-human approval path. The caller
// (executiongrant.Service) has already checked session mode and capability; the
// data layer still refuses anything but a pending grant of the same owner and a
// "policy:" actor, and records the approval in the immutable audit table.
func (r *Repository) ApproveExecutionGrantByPolicy(ctx context.Context, owner, requestID, decidedBy string) (executiongrant.Grant, error) {
	if owner == "" || requestID == "" || !strings.HasPrefix(decidedBy, executiongrant.PolicyActorPrefix) ||
		len(decidedBy) == len(executiongrant.PolicyActorPrefix) {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	defer conn.Close()
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants SET status='approved', decided_by=?, decided_at=?, updated_at=?
 WHERE request_id=? AND owner_id=? AND source IN ('human_browser','agent_human_decision') AND status='pending'`,
		decidedBy, now, now, requestID, owner)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if n != 1 {
		previous, readErr := grantByRequest(ctx, conn, owner, requestID)
		if readErr != nil || previous.DecidedBy != decidedBy ||
			(previous.Status != executiongrant.Approved && previous.Status != executiongrant.Claimed && previous.Status != executiongrant.Bound) {
			return executiongrant.Grant{}, executiongrant.ErrState
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return executiongrant.Grant{}, err
		}
		committed = true
		return previous, nil
	}
	g, err := grantByRequest(ctx, conn, owner, requestID)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	identityJSON, err := json.Marshal(g.Identity)
	if err != nil {
		return executiongrant.Grant{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
		(request_id,owner_id,request_version,event,actor_id,identity_json,occurred_at) VALUES (?,?,?,?,?,?,?)`,
		requestID, owner, g.RequestVersion, executiongrant.Approved, decidedBy, string(identityJSON), now); err != nil {
		return executiongrant.Grant{}, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return executiongrant.Grant{}, err
	}
	committed = true
	return g, nil
}

func (r *Repository) GetExecutionGrant(ctx context.Context, owner, requestID string) (executiongrant.Grant, error) {
	if owner == "" || requestID == "" {
		return executiongrant.Grant{}, executiongrant.ErrInvalid
	}
	return grantByRequest(ctx, r.db, owner, requestID)
}

// ClaimExecutionGrantOnConn runs after BEGIN IMMEDIATE on the SAME connection
// that inserts generation_tasks. Caller must pass the exact prepared identity
// and commit only after BindExecutionGrantOnConn succeeds. A bound replay returns
// its existing Task ID; a claimed grant never auto-releases after uncertainty.
func (r *Repository) ClaimExecutionGrantOnConn(ctx context.Context, conn *sql.Conn, requestID string, identity executiongrant.Identity) (string, error) {
	if conn == nil || requestID == "" || !identity.Valid() {
		return "", executiongrant.ErrInvalid
	}
	g, err := grantByRequest(ctx, conn, identity.OwnerID, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", executiongrant.ErrConflict
	}
	if err != nil {
		return "", err
	}
	if !sameGrantIdentity(g.Identity, identity) {
		return "", executiongrant.ErrConflict
	}
	if g.Status == executiongrant.Bound {
		if g.TaskID == "" {
			return "", executiongrant.ErrState
		}
		return g.TaskID, nil
	}
	if g.Status != executiongrant.Approved {
		return "", executiongrant.ErrState
	}
	if g.Source == executiongrant.AgentPending || !identity.Harness.IsZero() {
		if err := verifyCurrentHarness(ctx, conn, identity.OwnerID, identity.SessionID, identity.Harness); err != nil {
			return "", err
		}
	}
	if g.Source == executiongrant.AgentPending {
		if g.TriggerTurnID == "" {
			return "", executiongrant.ErrState
		}
		var owner, session string
		var stopRequested, detached int
		err := conn.QueryRowContext(ctx, `SELECT t.owner_id, t.session_id, t.stop_requested,
 EXISTS (SELECT 1 FROM agent_generation_operations o
  WHERE o.owner_id=? AND o.grant_request_id=? AND o.detached_at<>'')
 FROM go_chat_turns t WHERE t.assistant_message_id = ?`,
			identity.OwnerID, requestID, g.TriggerTurnID).Scan(&owner, &session, &stopRequested, &detached)
		if errors.Is(err, sql.ErrNoRows) {
			return "", executiongrant.ErrState
		}
		if err != nil {
			return "", err
		}
		if owner != identity.OwnerID || session != identity.SessionID {
			return "", executiongrant.ErrConflict
		}
		// Stop cancels what the turn still owns. A detached approval left the
		// turn when its card was created: it waits for the person, not for the
		// turn, so stopping the turn (for instance while the agent streams its
		// closing text) must not make the card unusable.
		if stopRequested != 0 && detached == 0 {
			return "", executiongrant.ErrState
		}
	}
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants SET status='claimed', updated_at=?
 WHERE request_id=? AND owner_id=? AND status='approved'`, time.Now().UTC().Format(time.RFC3339Nano), requestID, identity.OwnerID)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", executiongrant.ErrState
	}
	return "", nil
}

// BindExecutionGrantOnConn verifies the accepted Task and consumes the claim.
// It must run in the same transaction as task insertion.
func (r *Repository) BindExecutionGrantOnConn(ctx context.Context, conn *sql.Conn, requestID string, identity executiongrant.Identity, taskID string) error {
	if conn == nil || requestID == "" || taskID == "" || !identity.Valid() {
		return executiongrant.ErrInvalid
	}
	g, err := grantByRequest(ctx, conn, identity.OwnerID, requestID)
	if err != nil {
		return err
	}
	if !sameGrantIdentity(g.Identity, identity) {
		return executiongrant.ErrConflict
	}
	if g.Status != executiongrant.Claimed {
		return executiongrant.ErrState
	}
	var owner, session, key, offeringJSON string
	var inputJSON, requestHash string
	var nativeQuoteJSON sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT owner_id, COALESCE(session_id,''), idempotency_key, offering_json
	, input_json, request_hash,native_quote_json FROM generation_tasks WHERE id = ?`, taskID).Scan(&owner, &session, &key, &offeringJSON, &inputJSON, &requestHash, &nativeQuoteJSON)
	if err != nil {
		return err
	}
	var offering struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal([]byte(offeringJSON), &offering); err != nil {
		return err
	}
	if owner != identity.OwnerID || session != identity.SessionID || key != identity.IdempotencyKey ||
		offering.ID != identity.OfferingID || offering.Revision != identity.OfferingRevision {
		return executiongrant.ErrConflict
	}
	var storedOwner, storedSession, storedHash, preparedJSON string
	var quoteRequired int
	err = conn.QueryRowContext(ctx, `SELECT owner_id, session_id, input_hash, payload_json,native_quote_required
 FROM generation_prepared_inputs WHERE id = ?`, identity.PreparedInputID).Scan(
		&storedOwner, &storedSession, &storedHash, &preparedJSON, &quoteRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return executiongrant.ErrConflict
	}
	if err != nil {
		return err
	}
	var prepared struct {
		ID               string          `json:"id"`
		OwnerID          string          `json:"owner_id"`
		SessionID        string          `json:"session_id"`
		OfferingID       string          `json:"offering_id"`
		OfferingRevision string          `json:"offering_revision"`
		NormalizedInput  json.RawMessage `json:"normalized_input"`
		InputHash        string          `json:"input_hash"`
	}
	if err := json.Unmarshal([]byte(preparedJSON), &prepared); err != nil {
		return err
	}
	var nativePrepared generation.PreparedInput
	if err := json.Unmarshal([]byte(preparedJSON), &nativePrepared); err != nil {
		return err
	}
	if (quoteRequired == 1) != (nativePrepared.NativeQuote != nil) || (quoteRequired != 0 && quoteRequired != 1) {
		return executiongrant.ErrConflict
	}
	if nativeQuoteJSON.Valid || nativePrepared.NativeQuote != nil {
		if generation.VerifyPreparedPricingHash(nativePrepared) != nil || nativePrepared.NativeQuote == nil {
			return executiongrant.ErrConflict
		}
		want, _ := json.Marshal(nativePrepared.NativeQuote)
		if string(want) != nativeQuoteJSON.String {
			return executiongrant.ErrConflict
		}
	}
	expectedRequestHash := generation.ExpectedRequestHash(generation.OfferingID(identity.OfferingID), prepared.NormalizedInput)
	if storedOwner != identity.OwnerID || storedSession != identity.SessionID ||
		storedHash != identity.PreparedInputHash || prepared.ID != identity.PreparedInputID ||
		prepared.OwnerID != identity.OwnerID || prepared.SessionID != identity.SessionID ||
		prepared.OfferingID != identity.OfferingID || prepared.OfferingRevision != identity.OfferingRevision ||
		prepared.InputHash != identity.PreparedInputHash ||
		inputJSON != string(prepared.NormalizedInput) || requestHash != expectedRequestHash {
		return executiongrant.ErrConflict
	}
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants SET status='bound', task_id=?, updated_at=?
 WHERE request_id=? AND owner_id=? AND status='claimed'`, taskID, time.Now().UTC().Format(time.RFC3339Nano), requestID, identity.OwnerID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("bind execution grant: %w", executiongrant.ErrState)
	}
	return nil
}

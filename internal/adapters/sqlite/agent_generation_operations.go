package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

var _ generationtool.OperationStore = (*Repository)(nil)
var _ generationtool.DetachedOperationStore = (*Repository)(nil)

func scanAgentOperation(row interface{ Scan(...any) error }) (generationtool.Operation, error) {
	var op generationtool.Operation
	var referenceSource string
	err := row.Scan(&op.OwnerID, &op.SessionID, &op.AssistantMessageID, &op.CallID,
		&op.ToolName, &op.CommandHash, &op.OperationID, &op.IdempotencyKey,
		&op.PreparedInputID, &op.PreparedInputHash, &op.OfferingID,
		&op.OfferingRevision, &op.GrantRequestID, &op.Seq, &op.Detached,
		&op.PolicyEvaluated, &op.Settled, &op.Harness.Revision, &op.Harness.Digest, &op.RechargeTaskID,
		&op.ReferenceCount, &op.ReferenceTotal, &referenceSource)
	if errors.Is(err, sql.ErrNoRows) {
		return op, generationtool.ErrOperationNotFound
	}
	if err != nil {
		return op, err
	}
	if referenceSource != "" {
		if err := json.Unmarshal([]byte(referenceSource), &op.ReferenceSource); err != nil {
			return generationtool.Operation{}, err
		}
	}
	return op, nil
}

const agentOperationSelect = `SELECT o.owner_id,o.session_id,o.assistant_message_id,o.call_id,
 o.tool_name,o.command_hash,o.operation_id,o.idempotency_key,o.prepared_input_id,
 o.prepared_input_hash,o.offering_id,o.offering_revision,o.grant_request_id,
 o.rowid,o.detached_at<>'',o.policy_evaluated_at<>'',o.settled_at<>'',o.harness_revision,o.harness_digest,
 o.recharge_task_id,o.reference_count,o.reference_total,o.reference_source
 FROM agent_generation_operations o`

func (r *Repository) LoadAgentOperation(ctx context.Context, owner, session, assistantMessageID, callID string) (generationtool.Operation, error) {
	if owner == "" || session == "" || assistantMessageID == "" || callID == "" {
		return generationtool.Operation{}, generationtool.ErrOperationConflict
	}
	return scanAgentOperation(r.db.QueryRowContext(ctx, agentOperationSelect+`
 WHERE owner_id=? AND session_id=? AND assistant_message_id=? AND call_id=?`,
		owner, session, assistantMessageID, callID))
}

func sameReferenceSource(a, b generationtool.ReferenceSourceFact) bool {
	a.Trusted, b.Trusted = false, false
	return a == b
}

func sameAgentOperation(a, b generationtool.Operation) bool {
	return a.OwnerID == b.OwnerID && a.SessionID == b.SessionID &&
		a.AssistantMessageID == b.AssistantMessageID && a.CallID == b.CallID &&
		a.ToolName == b.ToolName && a.CommandHash == b.CommandHash &&
		a.IdempotencyKey == b.IdempotencyKey &&
		a.PreparedInputHash == b.PreparedInputHash && a.OfferingID == b.OfferingID &&
		a.OfferingRevision == b.OfferingRevision && a.Harness == b.Harness &&
		sameReferenceSource(a.ReferenceSource, b.ReferenceSource)
}

func (r *Repository) InsertAgentOperation(ctx context.Context, candidate generationtool.Operation) (generationtool.Operation, error) {
	if candidate.OwnerID == "" || candidate.SessionID == "" || candidate.AssistantMessageID == "" ||
		candidate.CallID == "" || candidate.ToolName == "" || candidate.CommandHash == "" ||
		candidate.IdempotencyKey == "" || candidate.PreparedInputID == "" ||
		candidate.PreparedInputHash == "" || candidate.OfferingID == "" || candidate.OfferingRevision == "" ||
		candidate.GrantRequestID != "" || !candidate.Harness.Valid() {
		return generationtool.Operation{}, generationtool.ErrOperationConflict
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return generationtool.Operation{}, err
	}
	candidate.OperationID = "agent_" + hex.EncodeToString(nonce[:])
	referenceSource := ""
	if candidate.ReferenceSource != (generationtool.ReferenceSourceFact{}) {
		encoded, err := json.Marshal(candidate.ReferenceSource)
		if err != nil {
			return generationtool.Operation{}, err
		}
		referenceSource = string(encoded)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO agent_generation_operations
 (owner_id,session_id,assistant_message_id,call_id,tool_name,command_hash,operation_id,
 idempotency_key,prepared_input_id,prepared_input_hash,offering_id,offering_revision,created_at,harness_revision,harness_digest,
 recharge_task_id,reference_count,reference_total,reference_source)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_id,session_id,assistant_message_id,call_id) DO NOTHING`,
		candidate.OwnerID, candidate.SessionID, candidate.AssistantMessageID, candidate.CallID,
		candidate.ToolName, candidate.CommandHash, candidate.OperationID,
		candidate.IdempotencyKey, candidate.PreparedInputID, candidate.PreparedInputHash,
		candidate.OfferingID, candidate.OfferingRevision, time.Now().UTC().Format(time.RFC3339Nano), candidate.Harness.Revision, candidate.Harness.Digest,
		candidate.RechargeTaskID, candidate.ReferenceCount, candidate.ReferenceTotal, referenceSource)
	if err != nil {
		return generationtool.Operation{}, err
	}
	stored, err := r.LoadAgentOperation(ctx, candidate.OwnerID, candidate.SessionID,
		candidate.AssistantMessageID, candidate.CallID)
	if err != nil {
		return generationtool.Operation{}, err
	}
	if !sameAgentOperation(stored, candidate) {
		return generationtool.Operation{}, generationtool.ErrOperationConflict
	}
	return stored, nil
}

func (r *Repository) AttachAgentGrantRequest(ctx context.Context, operation generationtool.Operation, requestID string) (generationtool.Operation, error) {
	if operation.OperationID == "" || requestID == "" {
		return generationtool.Operation{}, generationtool.ErrOperationConflict
	}
	_, err := r.db.ExecContext(ctx, `UPDATE agent_generation_operations SET grant_request_id=?
 WHERE owner_id=? AND session_id=? AND assistant_message_id=? AND call_id=?
 AND operation_id=? AND grant_request_id IN ('',?)`,
		requestID, operation.OwnerID, operation.SessionID, operation.AssistantMessageID,
		operation.CallID, operation.OperationID, requestID)
	if err != nil {
		return generationtool.Operation{}, err
	}
	stored, err := r.LoadAgentOperation(ctx, operation.OwnerID, operation.SessionID,
		operation.AssistantMessageID, operation.CallID)
	if err != nil {
		return generationtool.Operation{}, err
	}
	if !sameAgentOperation(stored, operation) || stored.PreparedInputID != operation.PreparedInputID ||
		stored.OperationID != operation.OperationID || stored.GrantRequestID != requestID {
		return generationtool.Operation{}, generationtool.ErrOperationConflict
	}
	return stored, nil
}

// PendingAgentOperation exposes only the durable exact grant request identity
// to an authenticated owner/session HTTP endpoint.
func (r *Repository) PendingAgentOperation(ctx context.Context, owner, session string) (generationtool.Operation, error) {
	op, err := scanAgentOperation(r.db.QueryRowContext(ctx, agentOperationSelect+`
 WHERE owner_id=? AND session_id=? AND grant_request_id<>''
 ORDER BY o.rowid DESC LIMIT 1`, owner, session))
	if err != nil {
		return op, err
	}
	grant, err := r.GetExecutionGrant(ctx, owner, op.GrantRequestID)
	if err != nil {
		return generationtool.Operation{}, err
	}
	if grant.Status == executiongrant.Bound {
		return generationtool.Operation{}, generationtool.ErrOperationNotFound
	}
	return op, nil
}

// DetachAgentOperation marks the operation detached only while its grant is
// still pending and its trigger turn was not stopped. The checks and the write
// are one statement, so a decision or a Stop that commits first wins and the
// caller sees the operation as not detached. (A Stop after the detach does not
// touch the card: see ClaimExecutionGrantOnConn.)
func (r *Repository) DetachAgentOperation(ctx context.Context, operation generationtool.Operation) (generationtool.Operation, bool, error) {
	if operation.OperationID == "" || operation.GrantRequestID == "" {
		return generationtool.Operation{}, false, generationtool.ErrOperationConflict
	}
	_, err := r.db.ExecContext(ctx, `UPDATE agent_generation_operations SET detached_at=?
 WHERE owner_id=? AND session_id=? AND assistant_message_id=? AND call_id=?
 AND operation_id=? AND grant_request_id=? AND detached_at=''
 AND EXISTS (SELECT 1 FROM execution_grants g
  WHERE g.owner_id=agent_generation_operations.owner_id
  AND g.request_id=agent_generation_operations.grant_request_id AND g.status='pending')
 AND NOT EXISTS (SELECT 1 FROM go_chat_turns t
  WHERE t.assistant_message_id=agent_generation_operations.assistant_message_id
  AND t.owner_id=agent_generation_operations.owner_id
  AND t.session_id=agent_generation_operations.session_id AND t.stop_requested<>0)`,
		time.Now().UTC().Format(time.RFC3339Nano), operation.OwnerID, operation.SessionID,
		operation.AssistantMessageID, operation.CallID, operation.OperationID, operation.GrantRequestID)
	if err != nil {
		return generationtool.Operation{}, false, err
	}
	stored, err := r.LoadAgentOperation(ctx, operation.OwnerID, operation.SessionID,
		operation.AssistantMessageID, operation.CallID)
	if err != nil {
		return generationtool.Operation{}, false, err
	}
	if !sameAgentOperation(stored, operation) || stored.OperationID != operation.OperationID ||
		stored.GrantRequestID != operation.GrantRequestID {
		return generationtool.Operation{}, false, generationtool.ErrOperationConflict
	}
	return stored, stored.Detached, nil
}

func (r *Repository) MarkAgentPolicyEvaluated(ctx context.Context, operation generationtool.Operation) error {
	if operation.OperationID == "" {
		return generationtool.ErrOperationConflict
	}
	_, err := r.db.ExecContext(ctx, `UPDATE agent_generation_operations SET policy_evaluated_at=?
 WHERE owner_id=? AND session_id=? AND assistant_message_id=? AND call_id=?
 AND operation_id=? AND policy_evaluated_at=''`,
		time.Now().UTC().Format(time.RFC3339Nano), operation.OwnerID, operation.SessionID,
		operation.AssistantMessageID, operation.CallID, operation.OperationID)
	return err
}

func (r *Repository) LoadAgentOperationByGrant(ctx context.Context, owner, requestID string) (generationtool.Operation, error) {
	if owner == "" || requestID == "" {
		return generationtool.Operation{}, generationtool.ErrOperationNotFound
	}
	return scanAgentOperation(r.db.QueryRowContext(ctx, agentOperationSelect+`
 WHERE o.owner_id=? AND o.grant_request_id=?`, owner, requestID))
}

func (r *Repository) scanOperations(rows *sql.Rows) ([]generationtool.Operation, error) {
	defer rows.Close()
	var result []generationtool.Operation
	for rows.Next() {
		op, err := scanAgentOperation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, rows.Err()
}

// ListPendingDetached lists the session's pending approvals, newest first. A
// Stop of the trigger turn does not hide one: once detached, the card belongs
// to the pending-approval service. A grant that started waiting at or before
// pendingAfter has expired and is left out (julianday compares the stored
// RFC 3339 text as an instant, to the millisecond).
func (r *Repository) ListPendingDetached(ctx context.Context, owner, session string, limit int, pendingAfter time.Time) ([]generationtool.Operation, error) {
	if owner == "" || session == "" || limit <= 0 {
		return nil, nil
	}
	bound := ""
	if !pendingAfter.IsZero() {
		bound = pendingAfter.UTC().Format(time.RFC3339Nano)
	}
	rows, err := r.db.QueryContext(ctx, agentOperationSelect+`
 JOIN execution_grants g ON g.owner_id=o.owner_id AND g.request_id=o.grant_request_id
 JOIN go_chat_turns t ON t.assistant_message_id=o.assistant_message_id
  AND t.owner_id=o.owner_id AND t.session_id=o.session_id
 WHERE o.owner_id=? AND o.session_id=? AND o.detached_at<>'' AND g.status='pending'
  AND (?='' OR julianday(g.updated_at)>julianday(?))
 ORDER BY o.rowid DESC LIMIT ?`, owner, session, bound, bound, limit)
	if err != nil {
		return nil, err
	}
	return r.scanOperations(rows)
}

// ListWaitingDetached returns detached operations after the cursor whose grant
// is still pending and whose outcome was not posted, oldest first. The sweep
// looks through them for approvals that expired without a decision.
func (r *Repository) ListWaitingDetached(ctx context.Context, afterSeq int64, limit int) ([]generationtool.Operation, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, agentOperationSelect+`
 JOIN execution_grants g ON g.owner_id=o.owner_id AND g.request_id=o.grant_request_id
 WHERE o.rowid>? AND o.detached_at<>'' AND o.settled_at='' AND g.status='pending'
 ORDER BY o.rowid ASC LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return r.scanOperations(rows)
}

// ListStoppedUndetached returns operations after the cursor whose grant is
// still pending or approved but not claimed, that were never detached, and
// whose trigger turn was stopped. Nothing can approve a pending one any more
// and nothing can claim an approved one (its approval committed before the
// Stop, or by policy while the Stop landed); the sweep cancels both.
func (r *Repository) ListStoppedUndetached(ctx context.Context, afterSeq int64, limit int) ([]generationtool.Operation, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, agentOperationSelect+`
 JOIN execution_grants g ON g.owner_id=o.owner_id AND g.request_id=o.grant_request_id
 JOIN go_chat_turns t ON t.assistant_message_id=o.assistant_message_id
  AND t.owner_id=o.owner_id AND t.session_id=o.session_id
 WHERE o.rowid>? AND o.grant_request_id<>'' AND o.detached_at='' AND o.settled_at=''
  AND g.status IN ('pending','approved') AND g.source='agent_human_decision' AND t.stop_requested<>0
 ORDER BY o.rowid ASC LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return r.scanOperations(rows)
}

// CancelStoppedAgentOperation ends an approval that no one can ever finish: its
// grant is pending (nobody can decide it) or approved but unclaimed (nobody can
// claim it), the operation was not detached, and its trigger turn was stopped.
// In one transaction it denies the grant as the system
// (executiongrant.StoppedActor, audited) and detaches the operation, so the
// pending-approval service posts the visible cancellation exactly once. It
// reports false, changing nothing, when any of those no longer holds (the
// decision or the detach committed first).
func (r *Repository) CancelStoppedAgentOperation(ctx context.Context, operation generationtool.Operation) (bool, error) {
	if operation.OperationID == "" || operation.GrantRequestID == "" {
		return false, generationtool.ErrOperationConflict
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants SET status='denied', decided_by=?, decided_at=?, updated_at=?
 WHERE owner_id=? AND request_id=? AND status IN ('pending','approved') AND source='agent_human_decision'
 AND EXISTS (SELECT 1 FROM agent_generation_operations o
  WHERE o.owner_id=execution_grants.owner_id AND o.grant_request_id=execution_grants.request_id
  AND o.operation_id=? AND o.detached_at='' AND o.settled_at='')
 AND EXISTS (SELECT 1 FROM go_chat_turns t
  WHERE t.assistant_message_id=execution_grants.trigger_turn_id AND t.owner_id=execution_grants.owner_id
  AND t.session_id=execution_grants.session_id AND t.stop_requested<>0)`,
		executiongrant.StoppedActor, now, now, operation.OwnerID, operation.GrantRequestID, operation.OperationID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}
	grant, err := grantByRequest(ctx, conn, operation.OwnerID, operation.GrantRequestID)
	if err != nil {
		return false, err
	}
	identityJSON, err := json.Marshal(grant.Identity)
	if err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
		(request_id,owner_id,request_version,event,actor_id,identity_json,occurred_at) VALUES (?,?,?,?,?,?,?)`,
		grant.RequestID, grant.OwnerID, grant.RequestVersion, executiongrant.Denied,
		executiongrant.StoppedActor, string(identityJSON), now); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE agent_generation_operations SET detached_at=?
 WHERE owner_id=? AND grant_request_id=? AND operation_id=? AND detached_at=''`,
		now, operation.OwnerID, operation.GrantRequestID, operation.OperationID); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

func (r *Repository) ListUnsettledDetached(ctx context.Context, afterSeq int64, limit int) ([]generationtool.Operation, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, agentOperationSelect+`
 JOIN execution_grants g ON g.owner_id=o.owner_id AND g.request_id=o.grant_request_id
 WHERE o.rowid>? AND o.detached_at<>'' AND o.settled_at='' AND g.status<>'pending'
 ORDER BY o.rowid ASC LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return r.scanOperations(rows)
}

func (r *Repository) SettleAgentOperation(ctx context.Context, operation generationtool.Operation) error {
	if operation.OperationID == "" {
		return generationtool.ErrOperationConflict
	}
	_, err := r.db.ExecContext(ctx, `UPDATE agent_generation_operations SET settled_at=?
 WHERE owner_id=? AND session_id=? AND assistant_message_id=? AND call_id=?
 AND operation_id=? AND detached_at<>'' AND settled_at=''`,
		time.Now().UTC().Format(time.RFC3339Nano), operation.OwnerID, operation.SessionID,
		operation.AssistantMessageID, operation.CallID, operation.OperationID)
	return err
}

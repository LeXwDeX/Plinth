package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

var _ executiongrant.StepGrantStore = (*Repository)(nil)

// ApproveExecutionGrantWithSteps is the atomic 3D step decision (#330). The
// capability, the owner's setting, the asset's used steps and the grant
// decision are read and written in one BEGIN IMMEDIATE transaction, so two
// concurrent steps of one asset never both take the last step of its cap.
//
// A capability the policy does not count (2D, LLM, anything else) is approved
// without reading the settings. For a counted one: the agent and unlimited
// modes approve; the cap mode approves while the asset's used steps are below
// the cap and otherwise denies the grant, decided by executiongrant.StepsActor.
// No Task exists for a denied grant, so nothing is created or charged. Both
// outcomes are written to the immutable grant audit.
//
// The same transaction decides the grants of a personal access key (#332):
// request.Actor names executiongrant.AccessKeyActor, which settles only grants
// of source access_key, under the owner's MCP setting. A step that needs a
// person (request.NeedsPerson) is denied by AccessKeyNeedsPersonActor without
// reading the capability or the settings.
func (r *Repository) ApproveExecutionGrantWithSteps(ctx context.Context, owner, requestID string, request executiongrant.StepRequest) (executiongrant.Grant, executiongrant.StepDecision, error) {
	actor := request.Actor
	if actor == "" {
		actor = executiongrant.StepsActor
	}
	if owner == "" || requestID == "" || !approvalpolicy.ValidStepEntry(request.Entry) || !validStepActor(actor, request) {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrInvalid
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackGrantConn(conn)
		}
	}()
	grant, err := grantByRequest(ctx, conn, owner, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrConflict
	}
	if err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	if grant.Status != executiongrant.Pending {
		// A repeat of a decision this actor already made returns it unchanged
		// and writes nothing; any other decider's grant is not ours to touch.
		if grant.DecidedBy == actor || (actor == executiongrant.AccessKeyActor && grant.DecidedBy == executiongrant.AccessKeyNeedsPersonActor) {
			var decision executiongrant.StepDecision
			switch {
			case grant.Status == executiongrant.Approved || grant.Status == executiongrant.Claimed || grant.Status == executiongrant.Bound:
				decision.Approved = true
			case grant.Status == executiongrant.Denied && grant.DecidedBy == executiongrant.AccessKeyNeedsPersonActor:
				decision.NeedsPerson = true
			case grant.Status == executiongrant.Denied:
				if decision.Summary, err = stepSummaryOnConn(ctx, conn, owner, grant.SessionID, request); err != nil {
					return executiongrant.Grant{}, executiongrant.StepDecision{}, err
				}
			default:
				return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrState
			}
			if err := commitGrantConn(ctx, conn); err != nil {
				return executiongrant.Grant{}, executiongrant.StepDecision{}, err
			}
			committed = true
			return grant, decision, nil
		}
		return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrState
	}
	if grant.OwnerID != owner || !stepActorSettles(actor, grant.Source) {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrConflict
	}
	var decision executiongrant.StepDecision
	approve, decidedBy := true, actor
	if request.NeedsPerson {
		approve, decidedBy, decision.NeedsPerson = false, executiongrant.AccessKeyNeedsPersonActor, true
	} else {
		capability, err := grantCapabilityOnConn(ctx, conn, grant)
		if err != nil {
			return executiongrant.Grant{}, executiongrant.StepDecision{}, err
		}
		if request.Steps.Counts(capability) {
			decision.Summary, err = stepSummaryOnConn(ctx, conn, owner, grant.SessionID, request)
			if err != nil {
				return executiongrant.Grant{}, executiongrant.StepDecision{}, err
			}
			approve = decision.Summary.Mode != approvalpolicy.StepModeCap || decision.Summary.Used < decision.Summary.Cap
		}
	}
	decision.Approved = approve
	status, event := executiongrant.Denied, "denied"
	if approve {
		status, event = executiongrant.Approved, "approved"
	}
	stamp := formatTime(time.Now().UTC())
	result, err := conn.ExecContext(ctx, `UPDATE execution_grants SET status = ?, decided_by = ?, decided_at = ?, updated_at = ?
 WHERE request_id = ? AND owner_id = ? AND status = 'pending'`,
		string(status), decidedBy, stamp, stamp, requestID, owner)
	if err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	} else if n != 1 {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, executiongrant.ErrState
	}
	identityJSON, err := json.Marshal(grant.Identity)
	if err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO execution_grant_audit
 (request_id, owner_id, request_version, event, actor_id, identity_json, occurred_at)
 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		requestID, owner, grant.RequestVersion, event, decidedBy, string(identityJSON), stamp); err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	decided, err := grantByRequest(ctx, conn, owner, requestID)
	if err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		return executiongrant.Grant{}, executiongrant.StepDecision{}, err
	}
	committed = true
	return decided, decision, nil
}

// validStepActor accepts the two deciders of this transaction and the options
// that belong to each: the access key's actor decides under the MCP entry only
// and alone may carry NeedsPerson.
func validStepActor(actor string, request executiongrant.StepRequest) bool {
	switch actor {
	case executiongrant.StepsActor:
		return !request.NeedsPerson
	case executiongrant.AccessKeyActor:
		return request.Entry == approvalpolicy.StepEntryMCP
	}
	return false
}

// stepActorSettles reports whether the actor may decide a grant of the source:
// the steps mode the Agent and browser grants of a session, the access key's
// policy only the grants of an access key.
func stepActorSettles(actor, source string) bool {
	if actor == executiongrant.AccessKeyActor {
		return source == executiongrant.AccessKey
	}
	return source == executiongrant.AgentPending || source == executiongrant.HumanBrowser
}

// StepUsage reads the asset's 3D step standing under the owner's setting of
// the entry. It decides nothing and takes no write lock.
func (r *Repository) StepUsage(ctx context.Context, owner, sessionID string, request executiongrant.StepRequest) (executiongrant.StepSummary, error) {
	if owner == "" || sessionID == "" || !approvalpolicy.ValidStepEntry(request.Entry) {
		return executiongrant.StepSummary{}, executiongrant.ErrInvalid
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return executiongrant.StepSummary{}, err
	}
	defer conn.Close()
	return stepSummaryOnConn(ctx, conn, owner, sessionID, request)
}

// StepCapReached reports whether the session's newest turn was refused a paid
// step because its asset used all its 3D steps (#328 §2.5). It matches the
// grants executiongrant.Grant.StepCapReached reads (a denial by the steps or
// access-key policy actor) decided at or after that turn started; a denial of
// an older turn does not leak into the next one. It decides nothing and takes
// no write lock.
func (r *Repository) StepCapReached(ctx context.Context, ownerID, sessionID string) (bool, error) {
	if ownerID == "" || sessionID == "" {
		return false, nil
	}
	var reached bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM execution_grants
 WHERE owner_id = ? AND session_id = ? AND status = 'denied'
 AND decided_by IN (?, ?)
 AND julianday(decided_at) >= COALESCE(julianday(
   (SELECT created_at FROM go_chat_turns
    WHERE owner_id = ? AND session_id = ?
    ORDER BY julianday(created_at) DESC LIMIT 1)), 0)
)`, ownerID, sessionID, executiongrant.StepsActor, executiongrant.AccessKeyActor,
		ownerID, sessionID).Scan(&reached)
	if err != nil {
		return false, fmt.Errorf("read step cap denial: %w", err)
	}
	return reached, nil
}

// grantCapabilityOnConn is the capability of the prepared input the grant
// froze. It is read here, from the same transaction that decides, and never
// taken from the caller.
func grantCapabilityOnConn(ctx context.Context, conn *sql.Conn, grant executiongrant.Grant) (string, error) {
	var capability sql.NullString
	err := conn.QueryRowContext(ctx, `SELECT json_extract(payload_json, '$.capability')
 FROM generation_prepared_inputs WHERE id = ? AND owner_id = ? AND session_id = ?`,
		grant.PreparedInputID, grant.OwnerID, grant.SessionID).Scan(&capability)
	if errors.Is(err, sql.ErrNoRows) {
		return "", executiongrant.ErrConflict
	}
	if err != nil {
		return "", err
	}
	return capability.String, nil
}

// stepSummaryOnConn reads the owner's setting of the entry and, for the cap
// mode, the asset's used steps. The agent and unlimited modes never count.
func stepSummaryOnConn(ctx context.Context, conn *sql.Conn, owner, sessionID string, request executiongrant.StepRequest) (executiongrant.StepSummary, error) {
	setting, ok := request.Steps.Default(request.Entry)
	if !ok {
		return executiongrant.StepSummary{}, executiongrant.ErrInvalid
	}
	stored, found, err := accountSettingsOn(ctx, conn, owner)
	if err != nil {
		return executiongrant.StepSummary{}, err
	}
	if found {
		if setting, ok = stored.Entry(request.Entry); !ok {
			return executiongrant.StepSummary{}, executiongrant.ErrInvalid
		}
	}
	summary := executiongrant.StepSummary{Entry: request.Entry, Mode: setting.Mode, Cap: setting.Cap}
	if setting.Mode == approvalpolicy.StepModeCap {
		summary.Used, err = stepsUsedOnConn(ctx, conn, owner, sessionID, request.Steps.Counted)
		if err != nil {
			return executiongrant.StepSummary{}, err
		}
	}
	return summary, nil
}

// stepsUsedOnConn counts the 3D steps of one asset (one chat session of the
// owner). One public Task is one step, however many outputs it delivers. A
// step is a Task of a counted capability that was or may have been charged
// (taskMayBeChargedSQL: a failed or canceled Task that certainly was not
// charged is left out), or a grant that is approved but has no Task yet.
// An approved grant whose trigger turn was stopped can never be claimed
// (turnOwnsStoppedGrantNot), and one whose operation ended without a Task is
// denied by abandonUnboundGrantOnConn, so neither holds a step.
func stepsUsedOnConn(ctx context.Context, conn *sql.Conn, owner, sessionID string, counted []string) (int, error) {
	if len(counted) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(counted)), ",")
	args := func(extra ...any) []any {
		values := append([]any{}, extra...)
		for _, capability := range counted {
			values = append(values, capability)
		}
		return values
	}
	var tasks int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_tasks AS task
 WHERE task.owner_id = ? AND task.session_id = ? AND task.capability IN (`+placeholders+`)
 AND `+taskMayBeChargedSQL("task"), args(owner, sessionID)...).Scan(&tasks); err != nil {
		return 0, err
	}
	var held int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_grants
 JOIN generation_prepared_inputs AS prepared
   ON prepared.id = execution_grants.prepared_input_id AND prepared.owner_id = execution_grants.owner_id
 WHERE execution_grants.owner_id = ? AND execution_grants.session_id = ?
 AND execution_grants.status IN ('approved', 'claimed')
 AND json_extract(prepared.payload_json, '$.capability') IN (`+placeholders+`)
 AND `+turnOwnsStoppedGrantNot, args(owner, sessionID)...).Scan(&held); err != nil {
		return 0, err
	}
	return tasks + held, nil
}

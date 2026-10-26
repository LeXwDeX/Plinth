package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// stopFixture is one Agent operation with a pending grant on its own running
// turn: the state right after the approval card was created.
func stopFixture(t *testing.T, r *Repository, call string) (generationtool.Operation, executiongrant.Identity, executiongrant.Grant) {
	t.Helper()
	ctx := context.Background()
	turn := "turn-" + call
	insertGrantTurn(t, r, turn, "owner-a", "session-a")
	i := grantIdentity()
	i.PreparedInputID, i.PreparedInputHash = "prepared-"+call, "hash-"+call
	i.IdempotencyKey, i.PlanStepID = "key-"+call, ""
	createGrantPrepared(t, r, i, []byte(`{}`))
	op, err := r.InsertAgentOperation(ctx, generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a",
		AssistantMessageID: turn, CallID: call, ToolName: "create_model_3d", CommandHash: "command-" + call,
		IdempotencyKey: i.IdempotencyKey, PreparedInputID: generation.PreparedInputID(i.PreparedInputID),
		PreparedInputHash: i.PreparedInputHash, OfferingID: generation.OfferingID(i.OfferingID),
		OfferingRevision: i.OfferingRevision})
	if err != nil {
		t.Fatal(err)
	}
	i.OperationID = op.OperationID
	grant, err := r.PreparePending(ctx, i, executiongrant.AgentPending, turn)
	if err != nil {
		t.Fatal(err)
	}
	if op, err = r.AttachAgentGrantRequest(ctx, op, grant.RequestID); err != nil {
		t.Fatal(err)
	}
	return op, i, grant
}

func stopTurn(t *testing.T, r *Repository, call string) {
	t.Helper()
	if _, err := r.db.Exec(`UPDATE go_chat_turns SET stop_requested=1 WHERE assistant_message_id=?`, "turn-"+call); err != nil {
		t.Fatal(err)
	}
}

func claimOnce(t *testing.T, r *Repository, requestID string, i executiongrant.Identity) error {
	t.Helper()
	conn, err := r.grantWriteConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGrantConn(conn)
	_, err = r.ClaimExecutionGrantOnConn(context.Background(), conn, requestID, i)
	return err
}

// Defect 1: Stop while the agent streams its closing text must not kill a card
// that was already detached; before the detach the stop still refuses the claim.
func TestClaimAfterStopHonoursADetachedApprovalOnly(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)

	detachedOp, detachedIdentity, detachedGrant := stopFixture(t, r, "detached")
	if _, detached, err := r.DetachAgentOperation(ctx, detachedOp); err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", detachedGrant.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	stopTurn(t, r, "detached")
	if err := claimOnce(t, r, detachedGrant.RequestID, detachedIdentity); err != nil {
		t.Fatalf("a detached approval must survive Stop of its trigger turn: %v", err)
	}

	_, liveIdentity, liveGrant := stopFixture(t, r, "live")
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", liveGrant.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	stopTurn(t, r, "live")
	if err := claimOnce(t, r, liveGrant.RequestID, liveIdentity); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("an approval still owned by its turn must stay refused after Stop: %v", err)
	}
}

// Defect 1 (race): a Stop that lands before the detach must win, so the card is
// never detached into a state nobody can finish.
func TestDetachIsRefusedOnceTheTriggerTurnIsStopped(t *testing.T) {
	r := openGrantTestRepo(t)
	op, _, grant := stopFixture(t, r, "race")
	stopTurn(t, r, "race")
	stored, detached, err := r.DetachAgentOperation(context.Background(), op)
	if err != nil || detached || stored.Detached {
		t.Fatalf("detached after stop: %+v %v %v", stored, detached, err)
	}
	if g, err := r.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID); err != nil || g.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v %v", g, err)
	}
}

// Defect 1: the pending list keeps a detached card whose trigger turn was stopped.
func TestPendingListKeepsADetachedCardAfterStop(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	op, _, _ := stopFixture(t, r, "listed")
	if _, detached, err := r.DetachAgentOperation(ctx, op); err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	stopTurn(t, r, "listed")
	listed, err := r.ListPendingDetached(ctx, "owner-a", "session-a", 10, time.Time{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed = %+v err=%v", listed, err)
	}
}

// Defect 2: an expired approval is left out of the list by the instant it
// started waiting, and so does not use up the list's limit.
func TestPendingListLeavesOutExpiredApprovalsBeforeTheLimit(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	for _, call := range []string{"old", "new"} {
		op, _, _ := stopFixture(t, r, call)
		if _, detached, err := r.DetachAgentOperation(ctx, op); err != nil || !detached {
			t.Fatalf("detach %s: %v %v", call, detached, err)
		}
	}
	// "old" started waiting an hour ago, "new" just now.
	long := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := r.db.Exec(`UPDATE execution_grants SET updated_at=? WHERE idempotency_key='key-old'`, long); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-30 * time.Minute)
	listed, err := r.ListPendingDetached(ctx, "owner-a", "session-a", 1, cutoff)
	if err != nil || len(listed) != 1 || listed[0].CallID != "new" {
		t.Fatalf("listed = %+v err=%v", listed, err)
	}
	// Reversed insertion order: the newest row first would hide the valid one
	// behind the limit if the expired one were not filtered in the query.
	if _, err := r.db.Exec(`UPDATE execution_grants SET updated_at=? WHERE idempotency_key='key-new'`, long); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE execution_grants SET updated_at=? WHERE idempotency_key='key-old'`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	listed, err = r.ListPendingDetached(ctx, "owner-a", "session-a", 1, cutoff)
	if err != nil || len(listed) != 1 || listed[0].CallID != "old" {
		t.Fatalf("limit must apply after the expiry filter: %+v err=%v", listed, err)
	}
	if all, err := r.ListPendingDetached(ctx, "owner-a", "session-a", 10, time.Time{}); err != nil || len(all) != 2 {
		t.Fatalf("without a cutoff both are listed: %+v err=%v", all, err)
	}
}

func TestSweepListsWaitingAndStoppedUndetached(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	detached, _, _ := stopFixture(t, r, "waiting")
	if _, ok, err := r.DetachAgentOperation(ctx, detached); err != nil || !ok {
		t.Fatalf("detach: %v %v", ok, err)
	}
	stopFixture(t, r, "live")
	stopFixture(t, r, "stopped")
	stopTurn(t, r, "stopped")

	waiting, err := r.ListWaitingDetached(ctx, 0, 10)
	if err != nil || len(waiting) != 1 || waiting[0].CallID != "waiting" {
		t.Fatalf("waiting = %+v err=%v", waiting, err)
	}
	stopped, err := r.ListStoppedUndetached(ctx, 0, 10)
	if err != nil || len(stopped) != 1 || stopped[0].CallID != "stopped" {
		t.Fatalf("stopped undetached = %+v err=%v", stopped, err)
	}
}

func TestCancelStoppedAgentOperationIsAtomicAndAudited(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)

	live, _, liveGrant := stopFixture(t, r, "live")
	if ok, err := r.CancelStoppedAgentOperation(ctx, live); err != nil || ok {
		t.Fatalf("a live turn's approval was cancelled: %v %v", ok, err)
	}
	detached, _, detachedGrant := stopFixture(t, r, "detached")
	if _, ok, err := r.DetachAgentOperation(ctx, detached); err != nil || !ok {
		t.Fatalf("detach: %v %v", ok, err)
	}
	stopTurn(t, r, "detached")
	if ok, err := r.CancelStoppedAgentOperation(ctx, detached); err != nil || ok {
		t.Fatalf("a detached approval was cancelled by Stop: %v %v", ok, err)
	}
	stopped, _, stoppedGrant := stopFixture(t, r, "stopped")
	stopTurn(t, r, "stopped")
	if ok, err := r.CancelStoppedAgentOperation(ctx, stopped); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	if ok, err := r.CancelStoppedAgentOperation(ctx, stopped); err != nil || ok {
		t.Fatalf("second cancel: %v %v", ok, err)
	}
	for _, check := range []struct {
		grant  executiongrant.Grant
		status executiongrant.Status
	}{{liveGrant, executiongrant.Pending}, {detachedGrant, executiongrant.Pending}, {stoppedGrant, executiongrant.Denied}} {
		got, err := r.GetExecutionGrant(ctx, "owner-a", check.grant.RequestID)
		if err != nil || got.Status != check.status {
			t.Fatalf("grant %s = %+v err=%v, want %s", check.grant.RequestID, got, err, check.status)
		}
	}
	got, _ := r.GetExecutionGrant(ctx, "owner-a", stoppedGrant.RequestID)
	if !got.CancelledByStop() || got.DecidedAt == nil {
		t.Fatalf("cancelled grant = %+v", got)
	}
	var audits int
	if err := r.db.QueryRow(`SELECT count(*) FROM execution_grant_audit WHERE request_id=? AND event='denied' AND actor_id=?`,
		stoppedGrant.RequestID, executiongrant.StoppedActor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
	after, err := r.LoadAgentOperationByGrant(ctx, "owner-a", stoppedGrant.RequestID)
	if err != nil || !after.Detached || after.Settled {
		t.Fatalf("operation after cancel = %+v err=%v", after, err)
	}
	// It is now decided-but-unsettled work for the sweep, not a pending card.
	if listed, _ := r.ListPendingDetached(ctx, "owner-a", "session-a", 10, time.Time{}); len(listed) != 1 || listed[0].CallID != "detached" {
		t.Fatalf("pending list after cancel = %+v", listed)
	}
	if unsettled, _ := r.ListUnsettledDetached(ctx, 0, 10); len(unsettled) != 1 || unsettled[0].CallID != "stopped" {
		t.Fatalf("unsettled = %+v", unsettled)
	}
}

// F1: the approve must be refused by the same statement that records it when
// the trigger turn was stopped and the card never left the turn; a Stop that
// commits between the handler's turn check and the decision cannot leave an
// approved grant nobody can claim. Denying and a detached card are unaffected.
func TestApproveIsRefusedOnceTheTriggerTurnIsStopped(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)

	_, _, grant := stopFixture(t, r, "undetached")
	stopTurn(t, r, "undetached")
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", grant.RequestID, "human-a", executiongrant.Approve); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("approve of a stopped, undetached grant = %v, want ErrState", err)
	}
	if got, err := r.GetExecutionGrant(ctx, "owner-a", grant.RequestID); err != nil || got.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v, want still pending", got, err)
	}
	var audits int
	if err := r.db.QueryRow(`SELECT count(*) FROM execution_grant_audit WHERE request_id=?`, grant.RequestID).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("a refused approve was audited: %d %v", audits, err)
	}
	if got, err := r.DecideExecutionGrant(ctx, "owner-a", grant.RequestID, "human-a", executiongrant.Deny); err != nil || got.Status != executiongrant.Denied {
		t.Fatalf("deny of a stopped grant = %+v %v", got, err)
	}

	detached, _, detachedGrant := stopFixture(t, r, "detached")
	if _, ok, err := r.DetachAgentOperation(ctx, detached); err != nil || !ok {
		t.Fatalf("detach: %v %v", ok, err)
	}
	stopTurn(t, r, "detached")
	if got, err := r.DecideExecutionGrant(ctx, "owner-a", detachedGrant.RequestID, "human-a", executiongrant.Approve); err != nil || got.Status != executiongrant.Approved {
		t.Fatalf("a detached card must stay approvable after Stop: %+v %v", got, err)
	}
}

// F1: an approval that committed before the Stop but was never claimed is
// listed and cancelled by the same sweep that handles pending ones.
func TestSweepCancelsAnApprovedGrantOnAStoppedUndetachedOperation(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)

	approved, _, approvedGrant := stopFixture(t, r, "approved")
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", approvedGrant.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	stopTurn(t, r, "approved")
	// Neither a live turn's approval nor a detached card is touched.
	live, _, liveGrant := stopFixture(t, r, "live")
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", liveGrant.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	detached, _, detachedGrant := stopFixture(t, r, "detached")
	if _, ok, err := r.DetachAgentOperation(ctx, detached); err != nil || !ok {
		t.Fatalf("detach: %v %v", ok, err)
	}
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", detachedGrant.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	stopTurn(t, r, "detached")

	listed, err := r.ListStoppedUndetached(ctx, 0, 10)
	if err != nil || len(listed) != 1 || listed[0].CallID != "approved" {
		t.Fatalf("stopped undetached = %+v err=%v", listed, err)
	}
	if ok, err := r.CancelStoppedAgentOperation(ctx, live); err != nil || ok {
		t.Fatalf("a live turn's approved grant was cancelled: %v %v", ok, err)
	}
	if ok, err := r.CancelStoppedAgentOperation(ctx, detached); err != nil || ok {
		t.Fatalf("a detached approved grant was cancelled: %v %v", ok, err)
	}
	if ok, err := r.CancelStoppedAgentOperation(ctx, approved); err != nil || !ok {
		t.Fatalf("cancel approved: %v %v", ok, err)
	}
	got, err := r.GetExecutionGrant(ctx, "owner-a", approvedGrant.RequestID)
	if err != nil || !got.CancelledByStop() {
		t.Fatalf("cancelled grant = %+v err=%v", got, err)
	}
	var events []string
	rows, err := r.db.Query(`SELECT event FROM execution_grant_audit WHERE request_id=? ORDER BY rowid`, approvedGrant.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 2 || events[0] != "approved" || events[1] != "denied" {
		t.Fatalf("audit events = %v, want approved then denied", events)
	}
	if op, err := r.LoadAgentOperationByGrant(ctx, "owner-a", approvedGrant.RequestID); err != nil || !op.Detached {
		t.Fatalf("operation after cancel = %+v err=%v", op, err)
	}
}

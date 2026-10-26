package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func grantIdentity() executiongrant.Identity {
	return executiongrant.Identity{
		OwnerID: "owner-a", SessionID: "session-a", OfferingID: "provider/model/action",
		OfferingRevision: "rev-1", PreparedInputID: "prepared-a", PreparedInputHash: "hash-a",
		IdempotencyKey: "original-key", OperationID: "operation-a", PlanStepID: "step-a",
	}
}

func openGrantTestRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "grants.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	_, err = r.db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('session-a','owner-a','owner','test','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	createGrantPrepared(t, r, grantIdentity(), []byte(`{}`))
	return r
}

func createGrantPrepared(t *testing.T, r *Repository, i executiongrant.Identity, input []byte) {
	t.Helper()
	err := r.CreatePreparedInput(context.Background(), generation.PreparedInput{
		ID: generation.PreparedInputID(i.PreparedInputID), OwnerID: generation.OwnerID(i.OwnerID),
		SessionID: i.SessionID, OfferingID: generation.OfferingID(i.OfferingID),
		OfferingRevision: i.OfferingRevision, NormalizedInput: input,
		InputHash: i.PreparedInputHash, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertGrantedTask(t *testing.T, ctx context.Context, conn *sql.Conn, id string, i executiongrant.Identity) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	hash := sha256.Sum256([]byte(i.OfferingID + "\x00{}"))
	_, err := conn.ExecContext(ctx, `INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key,
 state, progress, materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, ?, 'text-to-image', ?, '{}', ?, ?, 'QUEUED', 0, 0, 1, ?, ?, ?)`,
		id, i.OwnerID, `{"id":"`+i.OfferingID+`","revision":"`+i.OfferingRevision+`"}`,
		hex.EncodeToString(hash[:]), i.IdempotencyKey, now, now, i.SessionID)
	if err != nil {
		t.Fatal(err)
	}
}

func insertGrantTurn(t *testing.T, r *Repository, id, owner, session string) {
	t.Helper()
	_, err := r.db.Exec(`INSERT INTO go_chat_messages
 (id,session_id,role,content,image_urls,tool_calls,created_at)
 VALUES (?,?,'assistant','','[]','[]','2026-01-01T00:00:00Z')`, id, session)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.db.Exec(`INSERT INTO go_chat_turns
 (assistant_message_id,session_id,owner_id,options_json,state,created_at,updated_at)
 VALUES (?,?,?,'{}','RUNNING','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, session, owner)
	if err != nil {
		t.Fatal(err)
	}
}

func approvedAgentGrant(t *testing.T, r *Repository, turnID string) (executiongrant.Identity, executiongrant.Grant) {
	t.Helper()
	ctx := context.Background()
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.AgentPending, turnID)
	if err != nil {
		t.Fatal(err)
	}
	if g.TriggerTurnID != turnID {
		t.Fatalf("turn binding = %q, want %q", g.TriggerTurnID, turnID)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	return i, g
}

func TestExecutionGrantAgentTurnStopBeforeClaim(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	i, g := approvedAgentGrant(t, r, "turn-a")
	if _, err := r.db.Exec(`UPDATE go_chat_turns SET stop_requested=1 WHERE assistant_message_id='turn-a'`); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("claim after stop: %v", err)
	}
	rollbackGrantConn(conn)
	var count int
	if err := r.db.QueryRow(`SELECT count(*) FROM generation_tasks WHERE id='task-a'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("task created after stop: count=%d err=%v", count, err)
	}
}

func TestExecutionGrantTurnSourceRules(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	if _, err := r.PreparePending(ctx, i, executiongrant.AgentPending, ""); !errors.Is(err, executiongrant.ErrInvalid) {
		t.Fatalf("agent grant without turn: %v", err)
	}
	if _, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "turn-a"); !errors.Is(err, executiongrant.ErrInvalid) {
		t.Fatalf("browser grant with turn: %v", err)
	}
}

func TestExecutionGrantAgentBoundReplayAfterStop(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	i, g := approvedAgentGrant(t, r, "turn-a")
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil || task != "" {
		t.Fatalf("first claim: task=%q err=%v", task, err)
	}
	insertGrantedTask(t, ctx, conn, "task-a", i)
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "task-a"); err != nil {
		t.Fatal(err)
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE go_chat_turns SET stop_requested=1 WHERE assistant_message_id='turn-a'`); err != nil {
		t.Fatal(err)
	}
	conn, err = r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGrantConn(conn)
	if task, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil || task != "task-a" {
		t.Fatalf("bound replay after stop: task=%q err=%v", task, err)
	}
}

func TestExecutionGrantAgentTurnMismatchAndReplay(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	if _, err := r.db.Exec(`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES ('session-other','owner-a','owner','other','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	insertGrantTurn(t, r, "turn-a", "owner-b", "session-a")
	i, g := approvedAgentGrant(t, r, "turn-a")
	if _, err := r.PreparePending(ctx, i, executiongrant.AgentPending, "turn-other"); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("turn retarget: %v", err)
	}
	for _, tc := range []struct {
		owner, session string
	}{
		{"owner-b", "session-a"},
		{"owner-a", "session-other"},
	} {
		if _, err := r.db.Exec(`UPDATE go_chat_turns SET owner_id=?, session_id=? WHERE assistant_message_id='turn-a'`, tc.owner, tc.session); err != nil {
			t.Fatal(err)
		}
		conn, err := r.grantWriteConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i)
		rollbackGrantConn(conn)
		if !errors.Is(err, executiongrant.ErrConflict) {
			t.Fatalf("turn owner/session %q/%q: %v", tc.owner, tc.session, err)
		}
	}
	if _, err := r.db.Exec(`DELETE FROM go_chat_turns WHERE assistant_message_id='turn-a'`); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i)
	rollbackGrantConn(conn)
	if !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("missing turn: %v", err)
	}
}

func TestExecutionGrantBoundReplayAndExactIdentity(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if g.RequestID == "" || g.Status != executiongrant.Pending {
		t.Fatalf("bad request: %+v", g)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil || task != "" {
		t.Fatalf("claim: task=%q err=%v", task, err)
	}
	insertGrantedTask(t, ctx, conn, "task-a", i)
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "task-a"); err != nil {
		t.Fatal(err)
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	}
	bound, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || bound.Status != executiongrant.Bound || bound.TaskID != "task-a" {
		t.Fatalf("bound: %+v %v", bound, err)
	}
	conn, err = r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGrantConn(conn)
	if task, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil || task != "task-a" {
		t.Fatalf("replay: task=%q err=%v", task, err)
	}
	mutations := []func(*executiongrant.Identity){
		func(x *executiongrant.Identity) { x.OwnerID = "other" },
		func(x *executiongrant.Identity) { x.SessionID = "other" },
		func(x *executiongrant.Identity) { x.OfferingID = "other" },
		func(x *executiongrant.Identity) { x.OfferingRevision = "other" },
		func(x *executiongrant.Identity) { x.PreparedInputID = "other" },
		func(x *executiongrant.Identity) { x.PreparedInputHash = "other" },
		func(x *executiongrant.Identity) { x.IdempotencyKey = "other" },
		func(x *executiongrant.Identity) { x.OperationID = "other" },
		func(x *executiongrant.Identity) { x.PlanStepID = "other" },
	}
	for n, mutate := range mutations {
		changed := i
		mutate(&changed)
		if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, changed); !errors.Is(err, executiongrant.ErrConflict) {
			t.Errorf("changed field %d accepted: %v", n, err)
		}
	}
}

func TestExecutionGrantDecisionRetryIsIdempotentForSameHumanOnly(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve)
	if err != nil || first.Status != executiongrant.Approved {
		t.Fatalf("first approve: %+v %v", first, err)
	}
	again, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve)
	if err != nil || again.RequestID != first.RequestID || again.Status != executiongrant.Approved {
		t.Fatalf("retry after lost response: %+v %v", again, err)
	}
	for _, attempt := range []struct {
		actor    string
		decision executiongrant.Decision
	}{
		{"human-b", executiongrant.Approve},
		{"human-a", executiongrant.Deny},
	} {
		if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, attempt.actor, attempt.decision); !errors.Is(err, executiongrant.ErrState) {
			t.Fatalf("actor=%q decision=%q changed existing approval: %v", attempt.actor, attempt.decision, err)
		}
	}
	var audits int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM execution_grant_audit WHERE request_id=? AND event='approved'`, g.RequestID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("decision audit count=%d err=%v", audits, err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil {
		t.Fatal(err)
	}
	insertGrantedTask(t, ctx, conn, "task-a", i)
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "task-a"); err != nil {
		t.Fatal(err)
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	}
	bound, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve)
	if err != nil || bound.Status != executiongrant.Bound || bound.TaskID != "task-a" {
		t.Fatalf("retry after lost submit response: %+v %v", bound, err)
	}
}

func TestExecutionGrantStaleDecisionAndUnknownClaim(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	first, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	i.PreparedInputID, i.PreparedInputHash, i.IdempotencyKey = "prepared-b", "hash-b", "second-key"
	createGrantPrepared(t, r, i, []byte(`{}`))
	second, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestID == second.RequestID {
		t.Fatal("replacement reused request ID")
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, first.RequestID, "human-a", executiongrant.Approve); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("stale approval accepted: %v", err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, second.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, second.RequestID, i); err != nil {
		t.Fatal(err)
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	} // simulates unknown commit
	conn, err = r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGrantConn(conn)
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, second.RequestID, i); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("uncertain claim was released: %v", err)
	}
}

func TestExecutionGrantDeniedRetryRequiresNewDecision(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	first, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, first.RequestID, "human-a", executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	second, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if second.RequestID == first.RequestID || second.Status != executiongrant.Pending {
		t.Fatalf("denied retry did not require new decision: %+v", second)
	}
	if second.RequestVersion != 2 || second.Source != "human_browser" || second.DecidedBy != "" {
		t.Fatalf("retry retained a decision or lost provenance: %+v", second)
	}
	var actor, event string
	if err := r.db.QueryRowContext(ctx, `SELECT event, actor_id FROM execution_grant_audit
 WHERE request_id=? AND event='denied'`, first.RequestID).Scan(&event, &actor); err != nil || event != "denied" || actor != "human-a" {
		t.Fatalf("denial audit lost: event=%q actor=%q err=%v", event, actor, err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGrantConn(conn)
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, second.RequestID, i); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("retry used old decision: %v", err)
	}
}

func TestExecutionGrantFailedBindRollsBackClaim(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil {
		t.Fatal(err)
	}
	wrong := i
	wrong.OfferingRevision = "different-revision"
	insertGrantedTask(t, ctx, conn, "wrong-task", wrong)
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "wrong-task"); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("wrong accepted task bound: %v", err)
	}
	rollbackGrantConn(conn)
	state, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || state.Status != executiongrant.Approved {
		t.Fatalf("claim was not rolled back: %+v %v", state, err)
	}
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_tasks WHERE id='wrong-task'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("wrong task survived rollback: count=%d err=%v", count, err)
	}
}

func TestExecutionGrantRejectsTaskWithOtherPreparedInput(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i); err != nil {
		t.Fatal(err)
	}
	insertGrantedTask(t, ctx, conn, "task-b", i)
	otherInput := `{"prompt":"B"}`
	otherHash := sha256.Sum256([]byte(i.OfferingID + "\x00" + otherInput))
	if _, err := conn.ExecContext(ctx, `UPDATE generation_tasks SET input_json=?, request_hash=? WHERE id='task-b'`,
		otherInput, hex.EncodeToString(otherHash[:])); err != nil {
		t.Fatal(err)
	}
	if err := r.BindExecutionGrantOnConn(ctx, conn, g.RequestID, i, "task-b"); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("grant for prepared input A accepted Task B: %v", err)
	}
	rollbackGrantConn(conn)
	state, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || state.Status != executiongrant.Approved {
		t.Fatalf("claim did not roll back: %+v %v", state, err)
	}
}

func TestExecutionGrantConcurrentClaimsConsumeOnce(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := r.grantWriteConn(ctx)
			if err != nil {
				results <- err
				return
			}
			defer rollbackGrantConn(conn)
			_, err = r.ClaimExecutionGrantOnConn(ctx, conn, g.RequestID, i)
			if err == nil {
				err = commitGrantConn(ctx, conn)
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, failed := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, executiongrant.ErrState) {
			failed++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || failed != 1 {
		t.Fatalf("success=%d failed=%d", success, failed)
	}
}

func TestExecutionGrantServiceUsesPersistedPreparedIdentity(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i := grantIdentity()
	service := executiongrant.Service{Grants: r, Prepared: r}
	cmd := executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: i.OwnerID, ActorID: "human-a", Source: executiongrant.HumanBrowser},
		SessionID: i.SessionID, OfferingID: i.OfferingID, OfferingRevision: i.OfferingRevision,
		PreparedInputID: i.PreparedInputID, PreparedInputHash: i.PreparedInputHash,
		IdempotencyKey: i.IdempotencyKey, OperationID: i.OperationID, PlanStepID: i.PlanStepID,
	}
	wrong := cmd
	wrong.PreparedInputHash = "other-input"
	if _, err := service.Prepare(ctx, wrong); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("client substituted frozen hash: %v", err)
	}
	g, err := service.Prepare(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(ctx, executiongrant.Principal{OwnerID: "other", ActorID: "other", Source: executiongrant.HumanBrowser},
		g.RequestID, executiongrant.Approve); err == nil {
		t.Fatal("other owner decided grant")
	}
	decided, err := service.Decide(ctx, cmd.Principal, g.RequestID, executiongrant.Approve)
	if err != nil || decided.Status != executiongrant.Approved || decided.DecidedBy != "human-a" {
		t.Fatalf("human decision: %+v %v", decided, err)
	}
}

package sqlite

import (
	"context"
	"strconv"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #324 (from #187 note 98966): a Meshy Retexture failed upstream and was not charged, yet the
// session header went from 80 to 95. The session total counts what was or may still be
// charged: running and succeeded Tasks, and failed ones whose upstream finished (the result was
// produced and billed but not stored) or whose submission is ambiguous. A Task that failed or
// was canceled before the upstream finished was not charged and is left out.
func TestSessionCreditsLeaveOutFailedTasksThatWereNotCharged(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s", "owner", chat.Session{})
	insert := func(id, state, failure, phase string, credits int) {
		t.Helper()
		var failureJSON any
		if failure != "" {
			failureJSON = failure
		}
		offering := `{"id":"o","estimated_credits":` + strconv.Itoa(credits) + `}`
		if _, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key, state, progress, failure_json,
  current_attempt_id, materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, 'owner', 'retexture', ?, '{}', ?, ?, ?, 0, ?, ?, 0, 1, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z', 's')`,
			id, offering, "hash-"+id, "key-"+id, state, failureJSON, "attempt-"+id); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key,
  started_at, updated_at, last_observation_phase) VALUES (?, ?, 1, ?, 'FAILED', ?, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z', ?)`,
			"attempt-"+id, id, offering, "submission-"+id, phase); err != nil {
			t.Fatal(err)
		}
	}
	insert("done", "SUCCEEDED", "", "COMPLETED", 80)
	insert("running", "RUNNING", "", "RUNNING", 1)
	insert("rejected", "FAILED", `{"class":"rejected","retryable":false,"detail":"x"}`, "", 15)
	insert("upstream-failed", "FAILED", `{"class":"provider","retryable":false,"detail":"x"}`, "FAILED", 100)
	insert("canceled", "CANCELED", "", "", 200)
	insert("stored-failed", "FAILED", `{"class":"integrity","retryable":false,"detail":"x"}`, "COMPLETED", 10)
	insert("ambiguous", "FAILED", `{"class":"provider","retryable":false,"ambiguous":true,"detail":"x"}`, "", 1000)

	session, _, err := r.Session(ctx, "owner", "s")
	if err != nil {
		t.Fatal(err)
	}
	// 80 + 1 + 10 (produced and billed, not stored) + 1000 (may have been charged).
	if session.EstimatedCredits != 1091 {
		t.Fatalf("session credits = %d; want 1091", session.EstimatedCredits)
	}
}

// #330 third follow-ups: a paid conversion can become SUCCEEDED through local
// delivery completion. Its charge must stay exactly what the original failure
// fact was: a paid run whose provider never completed remains free, while one
// whose provider completed remains charged. A free refetch stays zero. An
// imported or historical completion can have cleared task.failure_json already,
// so an ambiguous original Attempt must still count on its own.
func TestLocalDeliveryCompletionKeepsTheOriginalChargeFact(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s", "owner", chat.Session{})
	stamp := "2026-10-22T00:00:00Z"
	insertTask := func(id, state string, credits int, phase, failure string) {
		t.Helper()
		var failureJSON any
		if failure != "" {
			failureJSON = failure
		}
		offering := `{"id":"o-` + id + `","estimated_credits":` + strconv.Itoa(credits) + `}`
		if _, err := r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key, state, progress, failure_json,
  current_attempt_id, materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, 'owner', 'format-conversion', ?, '{}', ?, ?, ?, 0, ?, ?, 0, 1, ?, ?, 's')`,
			id, offering, "hash-"+id, "key-"+id, state, failureJSON, "attempt-"+id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key,
  started_at, updated_at, last_observation_phase) VALUES (?, ?, 1, ?, 'FAILED', ?, ?, ?, ?)`,
			"attempt-"+id, id, offering, "submission-"+id, stamp, stamp, phase); err != nil {
			t.Fatal(err)
		}
	}
	insertTask("paid-not-started", "FAILED", 7, "", `{"class":"transport","retryable":true,"detail":"x"}`)
	insertTask("paid-produced", "FAILED", 7, "COMPLETED", `{"class":"integrity","retryable":true,"detail":"x"}`)
	insertTask("paid-ambiguous", "FAILED", 7, "", `{"class":"provider","retryable":false,"ambiguous":true,"detail":"x"}`)
	// A historical or imported completion can clear task.failure_json while the
	// original failed Attempt still holds the only ambiguous fee evidence.
	if _, err := r.db.Exec(`UPDATE generation_attempts SET failure_json=?
 WHERE id='attempt-paid-ambiguous'`, `{"class":"provider","retryable":false,"ambiguous":true,"detail":"x"}`); err != nil {
		t.Fatal(err)
	}
	insertTask("paid-plan-retry", "SUCCEEDED", 7, "COMPLETED", "")
	insertTask("free-refetch", "SUCCEEDED", 0, "COMPLETED", "")
	before, _, err := r.Session(ctx, "owner", "s")
	if err != nil {
		t.Fatal(err)
	}
	if before.EstimatedCredits != 21 {
		t.Fatalf("before completion = %d; want 21", before.EstimatedCredits)
	}
	for _, id := range []string{"paid-not-started", "paid-produced", "paid-ambiguous"} {
		offering := `{"id":"o-` + id + `","estimated_credits":7}`
		if _, err := r.db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key,
 started_at, updated_at, delivery_completion_json) VALUES (?,?,2,?,'DELIVERY_COMPLETED',?,?,?, '{"kind":"refetch"}')`,
			"completion-"+id, id, offering, "completion-key-"+id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Exec(`UPDATE generation_tasks SET state='SUCCEEDED', failure_json=NULL,
 current_attempt_id=?, version=2, finished_at=? WHERE id=?`, "completion-"+id, stamp, id); err != nil {
			t.Fatal(err)
		}
	}
	after, _, err := r.Session(ctx, "owner", "s")
	if err != nil {
		t.Fatal(err)
	}
	if after.EstimatedCredits != before.EstimatedCredits || after.EstimatedCredits != 21 {
		t.Fatalf("after completion = %d; before = %d; want the same 21", after.EstimatedCredits, before.EstimatedCredits)
	}
}

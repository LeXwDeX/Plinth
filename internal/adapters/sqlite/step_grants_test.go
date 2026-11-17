package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// stepFixture is a repository with one owner session and the 3D step policy of
// the product: the 3D capabilities are counted, 2D ones are not, and a person
// who never saved a setting has the Agent manage their steps.
type stepFixture struct {
	r     *Repository
	steps approvalpolicy.StepsPolicy
	seq   int
}

func newStepFixture(t *testing.T) *stepFixture {
	t.Helper()
	return &stepFixture{
		r: openGrantTestRepo(t),
		steps: approvalpolicy.StepsPolicy{
			Counted:     []string{"text-to-3d", "image-to-3d", "retexture", "rigging", "model-split"},
			DefaultList: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
			DefaultMCP:  approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20},
		},
	}
}

func (f *stepFixture) session(t *testing.T, owner, session string) {
	t.Helper()
	if _, err := f.r.db.Exec(`INSERT OR IGNORE INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at)
 VALUES (?,?,'owner','test','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, session, owner); err != nil {
		t.Fatal(err)
	}
}

// grant freezes a prepared input for the capability and creates a pending
// grant for it in the owner's session. A non-empty turn makes it an Agent grant
// of that turn.
func (f *stepFixture) grant(t *testing.T, owner, session, capability, turn string) executiongrant.Grant {
	t.Helper()
	f.session(t, owner, session)
	f.seq++
	identity := executiongrant.Identity{
		OwnerID: owner, SessionID: session, OfferingID: "provider/model/action", OfferingRevision: "rev-1",
		PreparedInputID: fmt.Sprintf("prepared-step-%d", f.seq), PreparedInputHash: fmt.Sprintf("hash-step-%d", f.seq),
		IdempotencyKey: fmt.Sprintf("key-step-%d", f.seq), OperationID: fmt.Sprintf("op-step-%d", f.seq),
	}
	if err := f.r.CreatePreparedInput(context.Background(), generation.PreparedInput{
		ID: generation.PreparedInputID(identity.PreparedInputID), OwnerID: generation.OwnerID(owner),
		SessionID: session, Capability: generation.Capability(capability),
		OfferingID: generation.OfferingID(identity.OfferingID), OfferingRevision: identity.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: identity.PreparedInputHash, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	source := executiongrant.HumanBrowser
	if turn != "" {
		source = executiongrant.AgentPending
		insertGrantTurn(t, f.r, turn, owner, session)
	}
	grant, err := f.r.PreparePending(context.Background(), identity, source, turn)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *stepFixture) decide(grant executiongrant.Grant, entry string) (executiongrant.Grant, executiongrant.StepDecision, error) {
	return f.r.ApproveExecutionGrantWithSteps(context.Background(), grant.OwnerID, grant.RequestID,
		executiongrant.StepRequest{Entry: entry, Steps: f.steps})
}

func (f *stepFixture) mustApprove(t *testing.T, grant executiongrant.Grant, entry string) executiongrant.Grant {
	t.Helper()
	decided, decision, err := f.decide(grant, entry)
	if err != nil || !decision.Approved || decided.Status != executiongrant.Approved || decided.DecidedBy != executiongrant.StepsActor {
		t.Fatalf("decision = %+v %+v err=%v; want approved by policy:steps", decided, decision, err)
	}
	return decided
}

func (f *stepFixture) mustRefuse(t *testing.T, grant executiongrant.Grant, entry string) (executiongrant.Grant, executiongrant.StepDecision) {
	t.Helper()
	decided, decision, err := f.decide(grant, entry)
	if err != nil || decision.Approved || decided.Status != executiongrant.Denied || !decided.StepCapReached() {
		t.Fatalf("decision = %+v %+v err=%v; want denied by policy:steps", decided, decision, err)
	}
	return decided, decision
}

func (f *stepFixture) setSettings(t *testing.T, owner string, list, mcp approvalpolicy.StepSetting) {
	t.Helper()
	expected := int64(0)
	if stored, found, err := f.r.AccountSettings(context.Background(), owner); err != nil {
		t.Fatal(err)
	} else if found {
		expected = stored.Version
	}
	if _, err := f.r.PutAccountSettings(context.Background(), owner, expected,
		accountsettings.Settings{List: list, MCP: mcp}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func capSetting(limit int) approvalpolicy.StepSetting {
	return approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: limit}
}

// task inserts one Task of the session the way the kernel stores it. phase is
// the last observation phase of its only Attempt ("" for none).
func (f *stepFixture) task(t *testing.T, id, owner, session, capability, state, failure, phase string) {
	t.Helper()
	f.session(t, owner, session)
	var failureJSON any
	if failure != "" {
		failureJSON = failure
	}
	offering := `{"id":"o","estimated_credits":10}`
	if _, err := f.r.db.Exec(`INSERT INTO generation_tasks
 (id, owner_id, capability, offering_json, input_json, request_hash, idempotency_key, state, progress, failure_json,
  current_attempt_id, materialize_failures, version, created_at, updated_at, session_id)
 VALUES (?, ?, ?, ?, '{}', ?, ?, ?, 0, ?, ?, 0, 1, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z', ?)`,
		id, owner, capability, offering, "hash-"+id, "key-"+id, state, failureJSON, "attempt-"+id, session); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.db.Exec(`INSERT INTO generation_attempts (id, task_id, ordinal, offering_json, state, submission_key,
  started_at, updated_at, last_observation_phase) VALUES (?, ?, 1, ?, 'FAILED', ?, '2026-10-22T00:00:00Z', '2026-10-22T00:00:00Z', ?)`,
		"attempt-"+id, id, offering, "submission-"+id, phase); err != nil {
		t.Fatal(err)
	}
}

func (f *stepFixture) used(t *testing.T, owner, session, entry string) executiongrant.StepSummary {
	t.Helper()
	summary, err := f.r.StepUsage(context.Background(), owner, session,
		executiongrant.StepRequest{Entry: entry, Steps: f.steps})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func (f *stepFixture) taskCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.r.db.QueryRow(`SELECT COUNT(*) FROM generation_tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *stepFixture) auditCount(t *testing.T, requestID, event string) int {
	t.Helper()
	var count int
	if err := f.r.db.QueryRow(`SELECT COUNT(*) FROM execution_grant_audit WHERE request_id=? AND event=? AND actor_id=?`,
		requestID, event, executiongrant.StepsActor).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A capability the policy does not count is never governed: it is approved
// without reading the settings, whatever the mode and however many 3D steps
// the asset already used. Only the 3D steps are limited.
func TestStepsApproveTwoDimensionalStepsInEveryModeWithoutCounting(t *testing.T) {
	for name, setting := range map[string]approvalpolicy.StepSetting{
		"default agent": {Mode: approvalpolicy.StepModeAgent, Cap: 20},
		"unlimited":     {Mode: approvalpolicy.StepModeUnlimited, Cap: 20},
		"cap reached":   capSetting(1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newStepFixture(t)
			f.setSettings(t, "owner-a", setting, setting)
			// One 3D step is already used, so the cap mode is at its limit.
			f.task(t, "used", "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
			for _, capability := range []string{"text-to-image", "image-to-image", "background-removal", "unknown-capability"} {
				for _, entry := range []string{approvalpolicy.StepEntryList, approvalpolicy.StepEntryMCP} {
					decided, decision, err := f.decide(f.grant(t, "owner-a", "session-a", capability, ""), entry)
					if err != nil || !decision.Approved || decided.Status != executiongrant.Approved || decided.DecidedBy != executiongrant.StepsActor {
						t.Fatalf("%s/%s = %+v %+v err=%v", capability, entry, decided, decision, err)
					}
					if decision.Summary != (executiongrant.StepSummary{}) {
						t.Fatalf("%s: a step that is not counted read the settings: %+v", capability, decision.Summary)
					}
				}
			}
		})
	}
}

func TestStepsAgentAndUnlimitedModesNeverCap(t *testing.T) {
	for _, mode := range []string{approvalpolicy.StepModeAgent, approvalpolicy.StepModeUnlimited} {
		t.Run(mode, func(t *testing.T) {
			f := newStepFixture(t)
			f.setSettings(t, "owner-a",
				approvalpolicy.StepSetting{Mode: mode, Cap: 1}, approvalpolicy.StepSetting{Mode: mode, Cap: 1})
			for index := range 4 {
				f.task(t, fmt.Sprintf("done-%d", index), "owner-a", "session-a", "image-to-3d", "SUCCEEDED", "", "COMPLETED")
			}
			for _, entry := range []string{approvalpolicy.StepEntryList, approvalpolicy.StepEntryMCP} {
				f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), entry)
			}
		})
	}
}

func TestStepsWithoutAStoredSettingFollowTheSchemaDefault(t *testing.T) {
	f := newStepFixture(t)
	for index := range 30 {
		f.task(t, fmt.Sprintf("done-%d", index), "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	}
	// The default is the Agent managing its own steps: no platform cap.
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "image-to-3d", ""), approvalpolicy.StepEntryList)

	// A Schema whose default is the cap mode makes the same person capped.
	f.steps.DefaultList = capSetting(5)
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "image-to-3d", ""), approvalpolicy.StepEntryList)
	// The default of the other entry is separate.
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "image-to-3d", ""), approvalpolicy.StepEntryMCP)
}

func TestStepsCapApprovesWhileUsedIsBelowTheCapAndDeniesAtIt(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(3), capSetting(3))
	for index := range 3 {
		grant := f.grant(t, "owner-a", "session-a", "text-to-3d", "")
		approved := f.mustApprove(t, grant, approvalpolicy.StepEntryList)
		if f.auditCount(t, approved.RequestID, "approved") != 1 {
			t.Fatalf("step %d: approval is not in the audit", index+1)
		}
		// The step becomes a Task; the grant is bound to it.
		f.task(t, fmt.Sprintf("task-%d", index), "owner-a", "session-a", "text-to-3d", "RUNNING", "", "")
		if _, err := f.r.db.Exec(`UPDATE execution_grants SET status='bound', task_id=? WHERE request_id=?`,
			fmt.Sprintf("task-%d", index), approved.RequestID); err != nil {
			t.Fatal(err)
		}
	}
	tasksBefore := f.taskCount(t)
	refused := f.grant(t, "owner-a", "session-a", "image-to-3d", "")
	decided, decision := f.mustRefuse(t, refused, approvalpolicy.StepEntryList)
	if decision.Summary.Used != 3 || decision.Summary.Cap != 3 || decision.Summary.Mode != approvalpolicy.StepModeCap ||
		decision.Summary.Entry != approvalpolicy.StepEntryList {
		t.Fatalf("summary = %+v", decision.Summary)
	}
	if f.taskCount(t) != tasksBefore {
		t.Fatal("a refused step created a Task")
	}
	if f.auditCount(t, decided.RequestID, "denied") != 1 {
		t.Fatal("the refusal is not in the audit")
	}
	stored, err := f.r.GetExecutionGrant(context.Background(), "owner-a", decided.RequestID)
	if err != nil || !stored.StepCapReached() || stored.Status != executiongrant.Denied || stored.TaskID != "" {
		t.Fatalf("stored grant = %+v err=%v", stored, err)
	}
	// Deciding the same grant again repeats the refusal and writes nothing.
	again, repeat, err := f.decide(decided, approvalpolicy.StepEntryList)
	if err != nil || repeat.Approved || !again.StepCapReached() || repeat.Summary.Used != 3 {
		t.Fatalf("repeat = %+v %+v err=%v", again, repeat, err)
	}
	if f.auditCount(t, decided.RequestID, "denied") != 1 {
		t.Fatal("a repeated decision wrote a second audit entry")
	}
	// A 2D step of the same asset still goes through.
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-image", ""), approvalpolicy.StepEntryList)
}

func TestStepsCapCountsApprovedStepsThatHaveNoTaskYet(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(2), capSetting(2))
	first := f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	second := f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 2 {
		t.Fatalf("two approved steps without a Task count %d", used)
	}
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	// A claimed step (a submit is in flight) still holds its step.
	if _, err := f.r.db.Exec(`UPDATE execution_grants SET status='claimed' WHERE request_id=?`, first.RequestID); err != nil {
		t.Fatal(err)
	}
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 2 {
		t.Fatalf("a claimed step counts %d", used)
	}
	_ = second
}

func TestStepsCapLeavesOutTasksThatWereCertainlyNotCharged(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(2), capSetting(2))
	// Not charged: failed or canceled before the upstream finished.
	f.task(t, "rejected", "owner-a", "session-a", "text-to-3d", "FAILED", `{"class":"rejected","retryable":false,"detail":"x"}`, "")
	f.task(t, "canceled", "owner-a", "session-a", "text-to-3d", "CANCELED", "", "")
	f.task(t, "transport", "owner-a", "session-a", "image-to-3d", "FAILED", `{"class":"transport","retryable":true,"detail":"x"}`, "")
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 0 {
		t.Fatalf("tasks that were certainly not charged count %d", used)
	}
	// Charged or possibly charged: the upstream finished, or the submission is ambiguous.
	f.task(t, "billed", "owner-a", "session-a", "text-to-3d", "FAILED", `{"class":"integrity","retryable":false,"detail":"x"}`, "COMPLETED")
	f.task(t, "ambiguous", "owner-a", "session-a", "image-to-3d", "FAILED", `{"class":"provider","retryable":false,"ambiguous":true,"detail":"x"}`, "")
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 2 {
		t.Fatalf("billed and ambiguous failures count %d, want 2", used)
	}
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
}

// One public Task is one step, whatever number of outputs it delivers: a split
// that produced several parts is a single Task.
func TestStepsCapCountsOneStepPerTaskWhateverItsOutputs(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(2), capSetting(2))
	f.task(t, "split", "owner-a", "session-a", "model-split", "SUCCEEDED", "", "COMPLETED")
	for index := range 6 {
		if _, err := f.r.db.Exec(`INSERT INTO generation_artifact_claims (attempt_id, claim_id, role, locator, media_type)
 VALUES ('attempt-split', ?, 'model-part', ?, 'model/gltf-binary')`, fmt.Sprintf("part-%d", index), fmt.Sprintf("https://example.test/part-%d.glb", index)); err != nil {
			t.Fatal(err)
		}
	}
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 1 {
		t.Fatalf("a split with six outputs counts %d steps, want 1", used)
	}
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
}

func TestStepsCapLeavesOutApprovedStepsOfAStoppedTurn(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	held := f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", "turn-held"), approvalpolicy.StepEntryList)
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 1 {
		t.Fatalf("an approved step of a running turn counts %d", used)
	}
	// Stop cancels what the turn still owns: the approved grant can never be
	// claimed, so it no longer holds the asset's step.
	if _, err := f.r.db.Exec(`UPDATE go_chat_turns SET stop_requested=1 WHERE assistant_message_id='turn-held'`); err != nil {
		t.Fatal(err)
	}
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 0 {
		t.Fatalf("an approved step of a stopped turn counts %d", used)
	}
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	_ = held
}

// A step the policy approved whose operation then ended without a Task (a
// permanently refused submit) is denied by the abandon path; it must give its
// step back instead of occupying the cap forever.
func TestStepsAbandonedStepStopsCountingAgainstTheCap(t *testing.T) {
	ctx := context.Background()
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	approved := f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	blocked := f.grant(t, "owner-a", "session-a", "text-to-3d", "")
	f.mustRefuse(t, blocked, approvalpolicy.StepEntryList)

	if err := f.r.ReleaseUnboundBudgetReservation(ctx, "owner-a", approved.RequestID); err != nil {
		t.Fatal(err)
	}
	abandoned, err := f.r.GetExecutionGrant(ctx, "owner-a", approved.RequestID)
	if err != nil || abandoned.Status != executiongrant.Denied || abandoned.DecidedBy != executiongrant.AbandonedActor {
		t.Fatalf("abandoned grant = %+v err=%v", abandoned, err)
	}
	if abandoned.StepCapReached() {
		t.Fatal("an abandoned step reads as a step-cap refusal")
	}
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != 0 {
		t.Fatalf("an abandoned step counts %d", used)
	}
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)

	// The abandon path leaves a bound grant and one a person approved alone.
	f.setSettings(t, "owner-a", capSetting(10), capSetting(10))
	bound := f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.task(t, "bound-task", "owner-a", "session-a", "text-to-3d", "RUNNING", "", "")
	if _, err := f.r.db.Exec(`UPDATE execution_grants SET status='bound', task_id='bound-task' WHERE request_id=?`, bound.RequestID); err != nil {
		t.Fatal(err)
	}
	human := f.grant(t, "owner-a", "session-a", "text-to-3d", "")
	if _, err := f.r.DecideExecutionGrant(ctx, "owner-a", human.RequestID, "person-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	for _, requestID := range []string{bound.RequestID, human.RequestID} {
		if err := f.r.ReleaseUnboundBudgetReservation(ctx, "owner-a", requestID); err != nil {
			t.Fatal(err)
		}
		stored, err := f.r.GetExecutionGrant(ctx, "owner-a", requestID)
		if err != nil || stored.Status == executiongrant.Denied {
			t.Fatalf("the abandon path touched %s: %+v err=%v", requestID, stored, err)
		}
	}
}

// The count is per asset (one chat session) and per owner.
func TestStepsCapIsScopedToTheSessionAndTheOwner(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	f.setSettings(t, "owner-b", capSetting(1), capSetting(1))
	f.task(t, "other-session", "owner-a", "session-other", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.task(t, "other-owner", "owner-b", "session-b", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	// The other session of the same owner and another owner are counted apart.
	f.mustRefuse(t, f.grant(t, "owner-a", "session-other", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.mustRefuse(t, f.grant(t, "owner-b", "session-b", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.mustApprove(t, f.grant(t, "owner-a", "session-fresh", "text-to-3d", ""), approvalpolicy.StepEntryList)
	// Another owner's settings never govern this owner's steps.
	f.setSettings(t, "owner-b", approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 1},
		approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 1})
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
}

func TestStepsEntryReadsItsOwnSetting(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(1), approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 1})
	f.task(t, "used", "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryMCP)

	f.setSettings(t, "owner-a", approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 1}, capSetting(1))
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
	f.mustRefuse(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryMCP)
}

// Raising the cap makes room again without touching what was decided.
func TestStepsRaisingTheCapAllowsTheNextStep(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	f.task(t, "used", "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	refused := f.grant(t, "owner-a", "session-a", "text-to-3d", "")
	f.mustRefuse(t, refused, approvalpolicy.StepEntryList)
	f.setSettings(t, "owner-a", capSetting(2), capSetting(2))
	f.mustApprove(t, f.grant(t, "owner-a", "session-a", "text-to-3d", ""), approvalpolicy.StepEntryList)
}

// The grant decision is one transaction with the usage read: concurrent steps of
// one asset never exceed its cap.
func TestStepsConcurrentDecisionsNeverExceedTheCap(t *testing.T) {
	const capLimit, contenders = 3, 10
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(capLimit), capSetting(capLimit))
	grants := make([]executiongrant.Grant, contenders)
	for index := range grants {
		grants[index] = f.grant(t, "owner-a", "session-a", "text-to-3d", "")
	}
	type result struct {
		decision executiongrant.StepDecision
		err      error
	}
	results := make(chan result, contenders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, grant := range grants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, decision, err := f.decide(grant, approvalpolicy.StepEntryList)
			results <- result{decision: decision, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	approved, refused := 0, 0
	for outcome := range results {
		if outcome.err != nil {
			t.Fatalf("concurrent decision failed: %v", outcome.err)
		}
		if outcome.decision.Approved {
			approved++
		} else {
			refused++
		}
	}
	if approved != capLimit || refused != contenders-capLimit {
		t.Fatalf("approved=%d refused=%d, want %d and %d", approved, refused, capLimit, contenders-capLimit)
	}
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList).Used; used != capLimit {
		t.Fatalf("used = %d, want %d", used, capLimit)
	}
}

func TestStepsDecisionRefusesWhatItDoesNotOwn(t *testing.T) {
	ctx := context.Background()
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(5), capSetting(5))
	grant := f.grant(t, "owner-a", "session-a", "text-to-3d", "")
	request := executiongrant.StepRequest{Entry: approvalpolicy.StepEntryList, Steps: f.steps}

	// Another owner cannot decide it, whatever the request ID.
	if _, _, err := f.r.ApproveExecutionGrantWithSteps(ctx, "owner-b", grant.RequestID, request); err == nil {
		t.Fatal("another owner decided the grant")
	}
	// Invalid input.
	for name, call := range map[string]func() error{
		"empty owner": func() error {
			_, _, err := f.r.ApproveExecutionGrantWithSteps(ctx, "", grant.RequestID, request)
			return err
		},
		"empty request": func() error { _, _, err := f.r.ApproveExecutionGrantWithSteps(ctx, "owner-a", "", request); return err },
		"unknown entry": func() error {
			_, _, err := f.r.ApproveExecutionGrantWithSteps(ctx, "owner-a", grant.RequestID, executiongrant.StepRequest{Entry: "web", Steps: f.steps})
			return err
		},
	} {
		if err := call(); !errors.Is(err, executiongrant.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	stored, err := f.r.GetExecutionGrant(ctx, "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("a refused call changed the grant: %+v err=%v", stored, err)
	}
	// A grant a person decided is not this mode's to touch.
	if _, err := f.r.DecideExecutionGrant(ctx, "owner-a", grant.RequestID, "person-a", executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.decide(grant, approvalpolicy.StepEntryList); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("a person's denial was decided again: %v", err)
	}
	// A grant approved by another policy actor is not either.
	other := f.grant(t, "owner-a", "session-a", "text-to-image", "")
	if _, err := f.r.ApproveExecutionGrantByPolicy(ctx, "owner-a", other.RequestID, "policy:auto"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.decide(other, approvalpolicy.StepEntryList); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("a grant another policy approved was decided again: %v", err)
	}
}

func TestStepUsageReadsWithoutDeciding(t *testing.T) {
	f := newStepFixture(t)
	if summary := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList); summary.Mode != approvalpolicy.StepModeAgent || summary.Cap != 20 || summary.Used != 0 {
		t.Fatalf("default summary = %+v", summary)
	}
	f.setSettings(t, "owner-a", capSetting(4), capSetting(7))
	f.task(t, "a", "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	f.task(t, "b", "owner-a", "session-a", "text-to-image", "SUCCEEDED", "", "COMPLETED")
	list := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryList)
	mcp := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryMCP)
	if list != (executiongrant.StepSummary{Entry: "list", Mode: "cap", Cap: 4, Used: 1}) ||
		mcp != (executiongrant.StepSummary{Entry: "mcp", Mode: "cap", Cap: 7, Used: 1}) {
		t.Fatalf("list=%+v mcp=%+v", list, mcp)
	}
	if _, err := f.r.StepUsage(context.Background(), "owner-a", "session-a",
		executiongrant.StepRequest{Entry: "web", Steps: f.steps}); !errors.Is(err, executiongrant.ErrInvalid) {
		t.Fatalf("unknown entry: %v", err)
	}
}

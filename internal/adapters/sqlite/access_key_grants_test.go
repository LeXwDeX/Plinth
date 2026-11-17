package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// keyGrant freezes a prepared input for the capability and issues a pending
// grant of the personal access key for it, in the owner's session.
func (f *stepFixture) keyGrant(t *testing.T, owner, session, capability, keyID string) (executiongrant.Identity, executiongrant.Grant) {
	t.Helper()
	f.session(t, owner, session)
	f.seq++
	identity := executiongrant.Identity{
		OwnerID: owner, SessionID: session, OfferingID: "provider/model/action", OfferingRevision: "rev-1",
		PreparedInputID: fmt.Sprintf("prepared-key-%d", f.seq), PreparedInputHash: fmt.Sprintf("hash-key-%d", f.seq),
		IdempotencyKey: fmt.Sprintf("key-key-%d", f.seq), OperationID: fmt.Sprintf("op-key-%d", f.seq),
	}
	if err := f.r.CreatePreparedInput(context.Background(), generation.PreparedInput{
		ID: generation.PreparedInputID(identity.PreparedInputID), OwnerID: generation.OwnerID(owner),
		SessionID: session, Capability: generation.Capability(capability),
		OfferingID: generation.OfferingID(identity.OfferingID), OfferingRevision: identity.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: identity.PreparedInputHash, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	grant, err := f.r.PreparePendingForAccessKey(context.Background(), identity, keyID)
	if err != nil {
		t.Fatal(err)
	}
	return identity, grant
}

// decideKey runs the access key's decision: the steps transaction told the key
// actor and the MCP entry.
func (f *stepFixture) decideKey(grant executiongrant.Grant, needsPerson bool) (executiongrant.Grant, executiongrant.StepDecision, error) {
	return f.r.ApproveExecutionGrantWithSteps(context.Background(), grant.OwnerID, grant.RequestID, executiongrant.StepRequest{
		Entry: approvalpolicy.StepEntryMCP, Steps: f.steps, Actor: executiongrant.AccessKeyActor, NeedsPerson: needsPerson,
	})
}

func (f *stepFixture) mustApproveKey(t *testing.T, grant executiongrant.Grant) executiongrant.Grant {
	t.Helper()
	decided, decision, err := f.decideKey(grant, false)
	if err != nil || !decision.Approved || decided.Status != executiongrant.Approved || decided.DecidedBy != executiongrant.AccessKeyActor {
		t.Fatalf("decision = %+v %+v err=%v; want approved by policy:access_key", decided, decision, err)
	}
	return decided
}

func (f *stepFixture) mustRefuseKey(t *testing.T, grant executiongrant.Grant) (executiongrant.Grant, executiongrant.StepDecision) {
	t.Helper()
	decided, decision, err := f.decideKey(grant, false)
	if err != nil || decision.Approved || decision.NeedsPerson || decided.Status != executiongrant.Denied ||
		decided.DecidedBy != executiongrant.AccessKeyActor || !decided.StepCapReached() {
		t.Fatalf("decision = %+v %+v err=%v; want denied at the cap by policy:access_key", decided, decision, err)
	}
	return decided, decision
}

func (f *stepFixture) keyAuditCount(t *testing.T, requestID, event, actor string) int {
	t.Helper()
	var count int
	if err := f.r.db.QueryRow(`SELECT COUNT(*) FROM execution_grant_audit WHERE request_id=? AND event=? AND actor_id=?`,
		requestID, event, actor).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A grant of a personal access key records the key, is stored with its own
// source, and reads back with both.
func TestAccessKeyGrantRecordsTheKeyAndTheSource(t *testing.T) {
	f := newStepFixture(t)
	identity, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	if grant.Source != executiongrant.AccessKey || grant.AccessKeyID != "key-one" || grant.Status != executiongrant.Pending ||
		grant.TriggerTurnID != "" || grant.BudgetEligible {
		t.Fatalf("grant = %+v", grant)
	}
	var source, key string
	if err := f.r.db.QueryRow(`SELECT source, access_key_id FROM execution_grants WHERE request_id=?`, grant.RequestID).Scan(&source, &key); err != nil ||
		source != "access_key" || key != "key-one" {
		t.Fatalf("stored source/key = %q %q err=%v", source, key, err)
	}
	read, err := f.r.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || read.AccessKeyID != "key-one" || read.Source != executiongrant.AccessKey {
		t.Fatalf("read = %+v err=%v", read, err)
	}
	// The same operation prepared again while pending is the same grant.
	again, err := f.r.PreparePendingForAccessKey(context.Background(), identity, "key-one")
	if err != nil || again.RequestID != grant.RequestID {
		t.Fatalf("replay = %+v err=%v", again, err)
	}
	// A browser or Agent grant carries no key.
	if browser := f.grant(t, "owner-a", "session-a", "text-to-image", ""); browser.AccessKeyID != "" || browser.Source != executiongrant.HumanBrowser {
		t.Fatalf("browser grant = %+v", browser)
	}
}

func TestAccessKeyGrantPreparationRefusesWhatIsNotAKeyGrant(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	identity, _ := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	other := identity
	other.OperationID, other.IdempotencyKey = "op-other", "key-other"
	for name, call := range map[string]func() error{
		"no key":                func() error { _, err := f.r.PreparePendingForAccessKey(ctx, other, ""); return err },
		"key on the plain path": func() error { _, err := f.r.PreparePending(ctx, other, executiongrant.AccessKey, ""); return err },
		"key with a turn":       func() error { _, err := f.r.PreparePending(ctx, other, executiongrant.AccessKey, "turn-a"); return err },
		"browser with a turn": func() error {
			_, err := f.r.PreparePending(ctx, other, executiongrant.HumanBrowser, "turn-a")
			return err
		},
		"budget-eligible key": func() error {
			_, err := f.r.PreparePendingWithBudget(ctx, other, executiongrant.AccessKey, "", true)
			return err
		},
		"invalid identity": func() error {
			_, err := f.r.PreparePendingForAccessKey(ctx, executiongrant.Identity{}, "key-one")
			return err
		},
		"unknown source": func() error { _, err := f.r.PreparePending(ctx, other, "robot", ""); return err },
	} {
		if err := call(); !errors.Is(err, executiongrant.ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
	// The same operation cannot change who started it.
	if _, err := f.r.PreparePending(ctx, identity, executiongrant.HumanBrowser, ""); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("a browser took over a key operation: %v", err)
	}
	// The table itself keeps the pairing: a key grant has a key, no other grant has one.
	for name, statement := range map[string]string{
		"key source without a key": `INSERT INTO execution_grants (request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,source,request_version,created_at,updated_at)
 VALUES ('r1','o','s','of','rev','p','h','k1','op1','pending','access_key',1,'t','t')`,
		"a key on a browser grant": `INSERT INTO execution_grants (request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,source,request_version,created_at,updated_at,access_key_id)
 VALUES ('r2','o','s','of','rev','p','h','k2','op2','pending','human_browser',1,'t','t','key-x')`,
		"unknown source": `INSERT INTO execution_grants (request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,source,request_version,created_at,updated_at)
 VALUES ('r3','o','s','of','rev','p','h','k3','op3','pending','robot',1,'t','t')`,
	} {
		if _, err := f.r.db.Exec(statement); err == nil {
			t.Errorf("%s: the table accepted it", name)
		}
	}
}

// A denied or pending grant is issued again for a replay, under the key that
// replays it; an approved one is never issued again.
func TestAccessKeyGrantIsIssuedAgainAfterADenialUnderTheCurrentKey(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	f.task(t, "used", "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	identity, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	denied, _ := f.mustRefuseKey(t, grant)

	// The person raises the cap; the caller retries the same call, now with a
	// rotated key.
	f.setSettings(t, "owner-a", capSetting(5), capSetting(5))
	retry, err := f.r.PreparePendingForAccessKey(ctx, identity, "key-two")
	if err != nil || retry.RequestID == denied.RequestID || retry.Status != executiongrant.Pending ||
		retry.AccessKeyID != "key-two" || retry.RequestVersion != denied.RequestVersion+1 {
		t.Fatalf("retry = %+v err=%v", retry, err)
	}
	approved := f.mustApproveKey(t, retry)
	// Once approved, a replay returns it unchanged, whatever key replays it.
	replay, err := f.r.PreparePendingForAccessKey(ctx, identity, "key-three")
	if err != nil || replay.RequestID != approved.RequestID || replay.AccessKeyID != "key-two" || replay.Status != executiongrant.Approved {
		t.Fatalf("replay of an approved grant = %+v err=%v", replay, err)
	}
	if f.keyAuditCount(t, denied.RequestID, "superseded", "system") != 1 {
		t.Fatal("the denied version was not marked superseded in the audit")
	}
}

// A capability the policy does not count is approved without reading the
// setting; the decision is the key's, and it is audited.
func TestAccessKeyApprovesTwoDimensionalStepsInEveryModeWithoutCounting(t *testing.T) {
	for name, setting := range map[string]approvalpolicy.StepSetting{
		"default agent": {Mode: approvalpolicy.StepModeAgent, Cap: 20},
		"unlimited":     {Mode: approvalpolicy.StepModeUnlimited, Cap: 20},
		"cap reached":   capSetting(1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newStepFixture(t)
			f.setSettings(t, "owner-a", setting, setting)
			f.task(t, "used", "owner-a", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
			for _, capability := range []string{"text-to-image", "image-to-image", "background-removal", "unknown-capability"} {
				_, grant := f.keyGrant(t, "owner-a", "session-a", capability, "key-one")
				decided, decision, err := f.decideKey(grant, false)
				if err != nil || !decision.Approved || decided.Status != executiongrant.Approved || decided.DecidedBy != executiongrant.AccessKeyActor {
					t.Fatalf("%s = %+v %+v err=%v", capability, decided, decision, err)
				}
				if decision.Summary != (executiongrant.StepSummary{}) {
					t.Fatalf("%s: a step that is not counted read the settings: %+v", capability, decision.Summary)
				}
				if f.keyAuditCount(t, decided.RequestID, "approved", executiongrant.AccessKeyActor) != 1 {
					t.Fatalf("%s: the approval is not in the audit", capability)
				}
			}
		})
	}
}

// The default of a person who never saved a setting, and the unlimited mode,
// never limit a 3D step; the key reads the MCP entry, not the asset list's.
func TestAccessKey3DStepsFollowTheMCPSettingNotTheListSetting(t *testing.T) {
	f := newStepFixture(t)
	for index := range 5 {
		f.task(t, fmt.Sprintf("done-%d", index), "owner-a", "session-a", "image-to-3d", "SUCCEEDED", "", "COMPLETED")
	}
	// No stored setting: the Schema default is the Agent managing its steps.
	_, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	f.mustApproveKey(t, grant)

	// The asset list is capped at one step, the MCP entry is not: the key goes through.
	f.setSettings(t, "owner-a", capSetting(1), approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 1})
	_, grant = f.keyGrant(t, "owner-a", "session-a", "image-to-3d", "key-one")
	f.mustApproveKey(t, grant)

	// And the other way round: the MCP entry is capped, the list is not.
	f.setSettings(t, "owner-a", approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 99}, capSetting(5))
	_, grant = f.keyGrant(t, "owner-a", "session-a", "image-to-3d", "key-one")
	decided, decision := f.mustRefuseKey(t, grant)
	if decision.Summary.Entry != approvalpolicy.StepEntryMCP || decision.Summary.Cap != 5 || decision.Summary.Used < 5 || decided.AccessKeyID != "key-one" {
		t.Fatalf("summary = %+v grant = %+v", decision.Summary, decided)
	}
}

// Under the cap mode the key's steps are counted per asset: the Tasks of the
// session whoever started them, and the key's own approved steps that have no
// Task yet. At the cap the grant is denied in place and nothing is created.
func TestAccessKeyCapCountsPerAssetAndDeniesInPlaceAtTheCap(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(3), capSetting(3))
	// One earlier step came from the browser, in the same asset.
	f.task(t, "browser-step", "owner-a", "asset-session", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	for index := range 2 {
		identity, grant := f.keyGrant(t, "owner-a", "asset-session", "text-to-3d", "key-one")
		approved := f.mustApproveKey(t, grant)
		if used := f.used(t, "owner-a", "asset-session", approvalpolicy.StepEntryMCP).Used; used != index+2 {
			t.Fatalf("after approving step %d the asset used %d", index+1, used)
		}
		if index == 0 {
			// The first becomes a Task: it counts once, as a Task, not twice.
			bindBudgetTask(t, f.r, identity, approved.RequestID, "key-task-0")
			// The helper stores a 2D Task; the step's real Task is a 3D one.
			if _, err := f.r.db.Exec(`UPDATE generation_tasks SET capability='text-to-3d' WHERE id='key-task-0'`); err != nil {
				t.Fatal(err)
			}
			if used := f.used(t, "owner-a", "asset-session", approvalpolicy.StepEntryMCP).Used; used != 2 {
				t.Fatalf("an approved grant that became a Task counts %d", used)
			}
		}
	}
	tasksBefore := f.taskCount(t)
	_, refusedGrant := f.keyGrant(t, "owner-a", "asset-session", "image-to-3d", "key-one")
	denied, decision := f.mustRefuseKey(t, refusedGrant)
	if decision.Summary.Used != 3 || decision.Summary.Cap != 3 || decision.Summary.Mode != approvalpolicy.StepModeCap ||
		decision.Summary.Entry != approvalpolicy.StepEntryMCP {
		t.Fatalf("summary = %+v", decision.Summary)
	}
	if f.taskCount(t) != tasksBefore {
		t.Fatal("a refused step created a Task")
	}
	if f.keyAuditCount(t, denied.RequestID, "denied", executiongrant.AccessKeyActor) != 1 {
		t.Fatal("the refusal is not in the audit")
	}
	// Deciding the same grant again repeats the refusal and writes nothing.
	again, repeat, err := f.decideKey(denied, false)
	if err != nil || repeat.Approved || !again.StepCapReached() || repeat.Summary.Used != 3 {
		t.Fatalf("repeat = %+v %+v err=%v", again, repeat, err)
	}
	if f.keyAuditCount(t, denied.RequestID, "denied", executiongrant.AccessKeyActor) != 1 {
		t.Fatal("a repeated decision wrote a second audit entry")
	}
	// Another asset of the same person has its own count, and 2D never counts.
	_, other := f.keyGrant(t, "owner-a", "other-asset", "text-to-3d", "key-one")
	f.mustApproveKey(t, other)
	_, twoD := f.keyGrant(t, "owner-a", "asset-session", "text-to-image", "key-one")
	f.mustApproveKey(t, twoD)
}

// An approved key grant is approved once: deciding it again returns it and
// writes no second audit entry, and a later change of the setting never undoes it.
func TestAccessKeyDecisionIsIdempotent(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(2), capSetting(2))
	_, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	approved := f.mustApproveKey(t, grant)
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	again, decision, err := f.decideKey(approved, false)
	if err != nil || !decision.Approved || again.RequestID != approved.RequestID || again.Status != executiongrant.Approved {
		t.Fatalf("repeat = %+v %+v err=%v", again, decision, err)
	}
	if f.keyAuditCount(t, approved.RequestID, "approved", executiongrant.AccessKeyActor) != 1 {
		t.Fatal("a repeated approval wrote a second audit entry")
	}
	// A claimed or bound grant repeats as approved too.
	if _, err := f.r.db.Exec(`UPDATE execution_grants SET status='claimed' WHERE request_id=?`, approved.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, decision, err = f.decideKey(approved, false); err != nil || !decision.Approved {
		t.Fatalf("claimed repeat = %+v err=%v", decision, err)
	}
}

// Two keys of the same asset deciding at once never both take the last step.
func TestAccessKeyConcurrentDecisionsNeverPassTheCap(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(3), capSetting(3))
	var grants []executiongrant.Grant
	for index := range 8 {
		_, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", fmt.Sprintf("key-%d", index))
		grants = append(grants, grant)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	approved, refused := 0, 0
	for _, grant := range grants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, decision, err := f.decideKey(grant, false)
			if err != nil {
				t.Errorf("decide: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if decision.Approved {
				approved++
			} else {
				refused++
			}
		}()
	}
	wg.Wait()
	if approved != 3 || refused != 5 {
		t.Fatalf("approved %d refused %d; want 3 and 5 under a cap of 3", approved, refused)
	}
}

// A step only a person could approve is denied with its own reason, without
// reading the capability or the settings, and never left pending.
func TestAccessKeyNeedsPersonDeniesInPlaceWithItsOwnReason(t *testing.T) {
	f := newStepFixture(t)
	for _, capability := range []string{"text-to-3d", "text-to-image"} {
		_, grant := f.keyGrant(t, "owner-a", "session-a", capability, "key-one")
		denied, decision, err := f.decideKey(grant, true)
		if err != nil || decision.Approved || !decision.NeedsPerson || decision.Summary != (executiongrant.StepSummary{}) ||
			denied.Status != executiongrant.Denied || denied.DecidedBy != executiongrant.AccessKeyNeedsPersonActor ||
			!denied.AccessKeyNeedsPerson() || denied.StepCapReached() {
			t.Fatalf("%s: denied = %+v %+v err=%v", capability, denied, decision, err)
		}
		if f.keyAuditCount(t, denied.RequestID, "denied", executiongrant.AccessKeyNeedsPersonActor) != 1 {
			t.Fatalf("%s: the refusal is not in the audit", capability)
		}
		// The same decision again, with or without the flag, repeats the denial.
		for _, needsPerson := range []bool{true, false} {
			again, repeat, err := f.decideKey(denied, needsPerson)
			if err != nil || repeat.Approved || !repeat.NeedsPerson || !again.AccessKeyNeedsPerson() {
				t.Fatalf("%s: repeat = %+v %+v err=%v", capability, again, repeat, err)
			}
		}
		if f.keyAuditCount(t, denied.RequestID, "denied", executiongrant.AccessKeyNeedsPersonActor) != 1 {
			t.Fatalf("%s: a repeated decision wrote a second audit entry", capability)
		}
	}
	// A denied grant holds no step: nothing was spent.
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryMCP).Used; used != 0 {
		t.Fatalf("denied grants count %d steps", used)
	}
}

// Another person's settings, Tasks and grants never touch the decision.
func TestAccessKeyDecisionIsOwnerIsolated(t *testing.T) {
	f := newStepFixture(t)
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	f.setSettings(t, "owner-b", capSetting(1), capSetting(1))
	// owner-b used its only step; owner-a used none.
	f.task(t, "b-used", "owner-b", "session-a", "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	_, grantA := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-a")
	f.mustApproveKey(t, grantA)
	_, grantB := f.keyGrant(t, "owner-b", "session-a", "text-to-3d", "key-b")
	f.mustRefuseKey(t, grantB)

	// Deciding another owner's grant is a conflict, not a decision.
	_, grantA2 := f.keyGrant(t, "owner-a", "session-a", "text-to-image", "key-a")
	if _, _, err := f.r.ApproveExecutionGrantWithSteps(context.Background(), "owner-b", grantA2.RequestID, executiongrant.StepRequest{
		Entry: approvalpolicy.StepEntryMCP, Steps: f.steps, Actor: executiongrant.AccessKeyActor,
	}); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("a foreign decision: %v", err)
	}
	if stored, err := f.r.GetExecutionGrant(context.Background(), "owner-a", grantA2.RequestID); err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("the foreign decision changed the grant: %+v err=%v", stored, err)
	}
}

// The two deciders keep to their own sources, and no person or generic policy
// can decide a key's grant: it has no card.
func TestAccessKeyGrantsAreDecidedOnlyByTheKeyPolicy(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	_, keyGrant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	browser := f.grant(t, "owner-a", "session-a", "text-to-3d", "")
	agent := f.grant(t, "owner-a", "session-a", "text-to-3d", "turn-a")

	// The steps decider settles neither the key's grant...
	if _, _, err := f.decide(keyGrant, approvalpolicy.StepEntryMCP); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("steps mode decided a key grant: %v", err)
	}
	// ...and the key's decider settles no other source.
	for name, grant := range map[string]executiongrant.Grant{"browser": browser, "agent": agent} {
		if _, _, err := f.decideKey(grant, false); !errors.Is(err, executiongrant.ErrConflict) {
			t.Errorf("the key policy decided a %s grant: %v", name, err)
		}
		if _, _, err := f.decideKey(grant, true); !errors.Is(err, executiongrant.ErrConflict) {
			t.Errorf("the key policy denied a %s grant: %v", name, err)
		}
	}
	// No person decides it, and neither does the generic policy approval.
	if _, err := f.r.DecideExecutionGrant(ctx, "owner-a", keyGrant.RequestID, "human-a", executiongrant.Approve); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("a person decided a key grant: %v", err)
	}
	if _, err := f.r.DecideExecutionGrant(ctx, "owner-a", keyGrant.RequestID, "human-a", executiongrant.Deny); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("a person denied a key grant: %v", err)
	}
	if _, err := f.r.ApproveExecutionGrantByPolicy(ctx, "owner-a", keyGrant.RequestID, "policy:auto"); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("the generic policy approved a key grant: %v", err)
	}
	if stored, err := f.r.GetExecutionGrant(ctx, "owner-a", keyGrant.RequestID); err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("a refused decision changed the grant: %+v err=%v", stored, err)
	}
	// The option and entry rules of each decider.
	for name, request := range map[string]executiongrant.StepRequest{
		"key policy under the list entry": {Entry: approvalpolicy.StepEntryList, Steps: f.steps, Actor: executiongrant.AccessKeyActor},
		"steps with a person flag":        {Entry: approvalpolicy.StepEntryMCP, Steps: f.steps, NeedsPerson: true},
		"unknown actor":                   {Entry: approvalpolicy.StepEntryMCP, Steps: f.steps, Actor: "policy:robot"},
		"the person actor":                {Entry: approvalpolicy.StepEntryMCP, Steps: f.steps, Actor: executiongrant.AccessKeyNeedsPersonActor},
	} {
		if _, _, err := f.r.ApproveExecutionGrantWithSteps(ctx, "owner-a", keyGrant.RequestID, request); !errors.Is(err, executiongrant.ErrInvalid) {
			t.Errorf("%s: err = %v; want ErrInvalid", name, err)
		}
	}
}

// An approved key grant whose operation ended without a Task gives its step
// back, like a steps grant; a grant that bound a Task keeps counting as its Task.
func TestAccessKeyAbandonedGrantStopsHoldingAStep(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	f.setSettings(t, "owner-a", capSetting(1), capSetting(1))
	_, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	approved := f.mustApproveKey(t, grant)
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryMCP).Used; used != 1 {
		t.Fatalf("an approved step holds %d", used)
	}
	if err := f.r.ReleaseUnboundBudgetReservation(ctx, "owner-a", approved.RequestID); err != nil {
		t.Fatal(err)
	}
	released, err := f.r.GetExecutionGrant(ctx, "owner-a", approved.RequestID)
	if err != nil || released.Status != executiongrant.Denied || released.DecidedBy != executiongrant.AbandonedActor {
		t.Fatalf("abandoned grant = %+v err=%v", released, err)
	}
	if used := f.used(t, "owner-a", "session-a", approvalpolicy.StepEntryMCP).Used; used != 0 {
		t.Fatalf("an abandoned grant still holds %d", used)
	}
	_, next := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	f.mustApproveKey(t, next)
}

// An approved key grant is claimed and bound to its Task the way every grant
// is: the generation module needs nothing special for the source.
func TestAccessKeyGrantClaimsAndBindsATask(t *testing.T) {
	f := newStepFixture(t)
	ctx := context.Background()
	identity, grant := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	approved := f.mustApproveKey(t, grant)
	bindBudgetTask(t, f.r, identity, approved.RequestID, "key-task")
	bound, err := f.r.GetExecutionGrant(ctx, "owner-a", approved.RequestID)
	if err != nil || bound.Status != executiongrant.Bound || bound.TaskID != "key-task" || bound.AccessKeyID != "key-one" ||
		bound.Source != executiongrant.AccessKey {
		t.Fatalf("bound grant = %+v err=%v", bound, err)
	}
	// An unapproved key grant cannot be claimed.
	identity2, pending := f.keyGrant(t, "owner-a", "session-a", "text-to-3d", "key-one")
	conn, err := f.r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGrantConn(conn)
	if _, err := f.r.ClaimExecutionGrantOnConn(ctx, conn, pending.RequestID, identity2); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("a pending key grant was claimed: %v", err)
	}
}

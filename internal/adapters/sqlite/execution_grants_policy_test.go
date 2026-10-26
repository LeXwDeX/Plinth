package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fixedModes struct{ mode string }

func (m fixedModes) SessionApprovalMode(context.Context, string, string) (string, error) {
	return m.mode, nil
}

func testApprovalPolicy() approvalpolicy.Policy {
	return approvalpolicy.New("manual",
		[]string{"auto", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"},
		[]string{"text-to-image", "image-to-image", "image-to-3d", "text-to-3d", "retexture"})
}

// policyGrant freezes a prepared input for the capability and creates a pending
// human-source grant for it.
func policyGrant(t *testing.T, r *Repository, capability string) (executiongrant.Identity, executiongrant.Grant) {
	t.Helper()
	i := grantIdentity()
	i.PreparedInputID = "prepared-" + capability
	i.PreparedInputHash = "hash-" + capability
	i.IdempotencyKey = "key-" + capability
	i.OperationID = "op-" + capability
	i.PlanStepID = ""
	err := r.CreatePreparedInput(context.Background(), generation.PreparedInput{
		ID: generation.PreparedInputID(i.PreparedInputID), OwnerID: generation.OwnerID(i.OwnerID),
		SessionID: i.SessionID, Capability: generation.Capability(capability),
		OfferingID: generation.OfferingID(i.OfferingID), OfferingRevision: i.OfferingRevision,
		NormalizedInput: []byte(`{}`), InputHash: i.PreparedInputHash, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := r.PreparePending(context.Background(), i, executiongrant.HumanBrowser, "")
	if err != nil {
		t.Fatal(err)
	}
	return i, g
}

func policyService(r *Repository, mode string) executiongrant.Service {
	return executiongrant.Service{Grants: r, Prepared: r, Policy: testApprovalPolicy(), Modes: fixedModes{mode: mode}}
}

func TestApproveByPolicyMatrix(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, mode, capability string
		approved               bool
	}{
		{"manual image", "manual", "text-to-image", true},
		{"manual image-to-3d", "manual", "image-to-3d", false},
		{"manual text-to-3d", "manual", "text-to-3d", false},
		{"manual retexture", "manual", "retexture", false},
		{"auto image-to-3d", "auto", "image-to-3d", true},
		{"auto retexture", "auto", "retexture", true},
		{"unknown mode is manual", "bogus", "image-to-3d", false},
		{"unknown mode still approves image", "bogus", "text-to-image", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := openGrantTestRepo(t)
			i, g := policyGrant(t, r, tc.capability)
			service := policyService(r, tc.mode)
			got, err := service.ApproveByPolicy(ctx, i.OwnerID, g.RequestID, tc.mode)
			if tc.approved {
				if err != nil || got.Status != executiongrant.Approved || !got.ApprovedByPolicy() {
					t.Fatalf("grant = %+v err=%v", got, err)
				}
				if got.DecidedBy != executiongrant.PolicyActorPrefix+service.Policy.NormalizeMode(tc.mode) {
					t.Fatalf("decided_by = %q", got.DecidedBy)
				}
				return
			}
			if !errors.Is(err, executiongrant.ErrHumanRequired) {
				t.Fatalf("err = %v, want ErrHumanRequired", err)
			}
			stored, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
			if err != nil || stored.Status != executiongrant.Pending || stored.DecidedBy != "" {
				t.Fatalf("grant must stay pending: %+v %v", stored, err)
			}
		})
	}
}

func TestApproveByPolicyOwnerMismatchAndStateRules(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i, g := policyGrant(t, r, "text-to-image")
	service := policyService(r, "manual")

	if _, err := service.ApproveByPolicy(ctx, "owner-b", g.RequestID, "auto"); err == nil {
		t.Fatal("another owner approved by policy")
	}
	stored, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("grant after foreign attempt: %+v %v", stored, err)
	}
	if _, err := service.ApproveByPolicy(ctx, i.OwnerID, "", "auto"); !errors.Is(err, executiongrant.ErrInvalid) {
		t.Fatalf("empty request id: %v", err)
	}

	first, err := service.ApproveByPolicy(ctx, i.OwnerID, g.RequestID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	// Replaying the same policy decision is idempotent and writes no second audit row.
	again, err := service.ApproveByPolicy(ctx, i.OwnerID, g.RequestID, "manual")
	if err != nil || again.RequestVersion != first.RequestVersion || again.DecidedBy != first.DecidedBy {
		t.Fatalf("replay: %+v %v", again, err)
	}
	var audits int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM execution_grant_audit WHERE request_id=? AND actor_id=?`,
		g.RequestID, "policy:manual").Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("policy audit rows = %d err=%v", audits, err)
	}
	// A different policy actor cannot rewrite the decision.
	if _, err := service.ApproveByPolicy(ctx, i.OwnerID, g.RequestID, "auto"); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("other mode replay: %v", err)
	}
}

func TestApproveByPolicyRefusesHumanDecidedAndDeniedGrants(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i, approvedByHuman := policyGrant(t, r, "text-to-image")
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, approvedByHuman.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	service := policyService(r, "auto")
	if _, err := service.ApproveByPolicy(ctx, i.OwnerID, approvedByHuman.RequestID, "auto"); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("human-approved grant: %v", err)
	}

	r2 := openGrantTestRepo(t)
	i2, denied := policyGrant(t, r2, "text-to-image")
	if _, err := r2.DecideExecutionGrant(ctx, i2.OwnerID, denied.RequestID, "human-a", executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	if _, err := policyService(r2, "auto").ApproveByPolicy(ctx, i2.OwnerID, denied.RequestID, "auto"); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("denied grant: %v", err)
	}
	stored, err := r2.GetExecutionGrant(ctx, i2.OwnerID, denied.RequestID)
	if err != nil || stored.Status != executiongrant.Denied {
		t.Fatalf("denied grant changed: %+v %v", stored, err)
	}
}

func TestApproveExecutionGrantByPolicyRequiresPolicyActor(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i, g := policyGrant(t, r, "text-to-image")
	if _, err := r.ApproveExecutionGrantByPolicy(ctx, i.OwnerID, g.RequestID, "human-a"); err == nil {
		t.Fatal("repository accepted a non-policy actor")
	}
	stored, err := r.GetExecutionGrant(ctx, i.OwnerID, g.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("grant changed: %+v %v", stored, err)
	}
}

func TestApplyPolicyUsesSessionModeAndLeavesHumanStepsPending(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	_, image := policyGrant(t, r, "text-to-image")
	_, model := policyGrant(t, r, "image-to-3d")

	manual := policyService(r, "manual")
	got, err := manual.ApplyPolicy(ctx, image)
	if err != nil || got.Status != executiongrant.Approved || !got.ApprovedByPolicy() {
		t.Fatalf("manual image: %+v %v", got, err)
	}
	got, err = manual.ApplyPolicy(ctx, model)
	if err != nil || got.Status != executiongrant.Pending {
		t.Fatalf("manual image-to-3d must wait: %+v %v", got, err)
	}
	got, err = policyService(r, "auto").ApplyPolicy(ctx, model)
	if err != nil || got.Status != executiongrant.Approved || got.DecidedBy != "policy:auto" {
		t.Fatalf("auto image-to-3d: %+v %v", got, err)
	}
	// Without a mode reader nothing is approved by policy.
	plain := executiongrant.Service{Grants: r, Prepared: r}
	_, other := policyGrant(t, r, "retexture")
	got, err = plain.ApplyPolicy(ctx, other)
	if err != nil || got.Status != executiongrant.Pending {
		t.Fatalf("no mode reader: %+v %v", got, err)
	}
}

func TestPolicyApprovedGrantCanBeClaimedAndBound(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	i, g := policyGrant(t, r, "text-to-image")
	approved, err := policyService(r, "manual").ApproveByPolicy(ctx, i.OwnerID, g.RequestID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if task, err := r.ClaimExecutionGrantOnConn(ctx, conn, approved.RequestID, i); err != nil || task != "" {
		t.Fatalf("claim policy-approved grant: task=%q err=%v", task, err)
	}
	insertGrantedTask(t, ctx, conn, "task-policy", i)
	if err := r.BindExecutionGrantOnConn(ctx, conn, approved.RequestID, i, "task-policy"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := commitGrantConn(ctx, conn); err != nil {
		t.Fatal(err)
	}
	bound, err := r.GetExecutionGrant(ctx, i.OwnerID, approved.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status != executiongrant.Bound || !bound.ApprovedByPolicy() {
		t.Fatalf("bound grant = %+v", bound)
	}
}

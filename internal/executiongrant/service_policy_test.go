package executiongrant

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestMultiviewAgentNativeExtraApprovalPreservesTwoDimensionalPolicy(t *testing.T) {
	policy := approvalpolicy.New("manual", []string{"auto", "manual"}, []string{"text-to-3d", "image-to-3d", "image-to-splat", "retexture"}, []string{"image-to-multiview", "edit-multiview"})
	for _, tc := range []struct {
		capability string
		quote      *schema.NativeQuote
		manualWait bool
	}{
		{"image-to-multiview", &schema.NativeQuote{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits", BaseAmount: "10", ExtraAmount: "0", TotalAmount: "10"}, false},
		{"edit-multiview", &schema.NativeQuote{Version: 1, Unit: "tripo.credit", UnitLabel: "Tripo credits", BaseAmount: "0", ExtraAmount: "5", TotalAmount: "5", Components: []schema.NativeCharge{{ID: "front", Label: "Front", Amount: "5"}}}, true},
	} {
		t.Run(tc.capability, func(t *testing.T) {
			for _, mode := range []string{"manual", "auto"} {
				store := policyFixture(tc.capability)
				store.grant.Source = AgentPending
				store.snapshot.NativeQuote = tc.quote
				service := Service{Grants: store, Prepared: store, Policy: policy}
				got, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", mode)
				if mode == "manual" && tc.manualWait {
					if !errors.Is(err, ErrHumanRequired) || store.grant.Status != Pending {
						t.Fatalf("manual extra edit was approved: %+v %v", got, err)
					}
				} else if err != nil || got.Status != Approved {
					t.Fatalf("%s %s should auto approve: %+v %v", tc.capability, mode, got, err)
				}
			}
		})
	}
}

// fakeGrantStore holds one grant and records policy approvals.
type fakeGrantStore struct {
	grant    Grant
	snapshot PreparedSnapshot
	approved int
}

func (f *fakeGrantStore) PreparePending(context.Context, Identity, string, string) (Grant, error) {
	return Grant{}, ErrInvalid
}

func (f *fakeGrantStore) DecideExecutionGrant(context.Context, string, string, string, Decision) (Grant, error) {
	return Grant{}, ErrState
}

func (f *fakeGrantStore) GetExecutionGrant(_ context.Context, owner, requestID string) (Grant, error) {
	if f.grant.OwnerID != owner || f.grant.RequestID != requestID {
		return Grant{}, ErrConflict
	}
	return f.grant, nil
}

func (f *fakeGrantStore) ApproveExecutionGrantByPolicy(_ context.Context, owner, requestID, decidedBy string) (Grant, error) {
	if f.grant.OwnerID != owner || f.grant.RequestID != requestID || f.grant.Status != Pending {
		return Grant{}, ErrState
	}
	f.grant.Status, f.grant.DecidedBy = Approved, decidedBy
	f.approved++
	return f.grant, nil
}

func (f *fakeGrantStore) GetExecutionPreparedSnapshot(_ context.Context, owner, preparedID string) (PreparedSnapshot, error) {
	if f.snapshot.OwnerID != owner || f.snapshot.ID != preparedID {
		return PreparedSnapshot{}, ErrConflict
	}
	return f.snapshot, nil
}

type modeReader struct {
	mode  string
	err   error
	calls int
}

func (m *modeReader) SessionApprovalMode(context.Context, string, string) (string, error) {
	m.calls++
	return m.mode, m.err
}

func policyFixture(capability string) *fakeGrantStore {
	identity := Identity{OwnerID: "owner-a", SessionID: "session-a", OfferingID: "offering", OfferingRevision: "r1",
		PreparedInputID: "prepared-a", PreparedInputHash: "hash-a", IdempotencyKey: "key-a", OperationID: "op-a"}
	return &fakeGrantStore{
		grant: Grant{RequestID: "request-a", Identity: identity, Status: Pending, Source: HumanBrowser},
		snapshot: PreparedSnapshot{ID: "prepared-a", OwnerID: "owner-a", SessionID: "session-a", OfferingID: "offering",
			OfferingRevision: "r1", InputHash: "hash-a", Capability: capability},
	}
}

func policyService(store *fakeGrantStore, modes SessionModeReader) Service {
	policy := approvalpolicy.New("manual", []string{"auto", "manual"},
		[]string{"text-to-3d", "image-to-3d", "retexture"}, []string{"text-to-image", "image-to-3d"})
	return Service{Grants: store, Prepared: store, Policy: policy, Modes: modes}
}

func TestApplyPolicyFailsClosedWhenModeLookupFails(t *testing.T) {
	store := policyFixture("text-to-image")
	modes := &modeReader{err: errors.New("session lookup failed")}
	got, err := policyService(store, modes).ApplyPolicy(context.Background(), store.grant)
	if err == nil || !errors.Is(err, modes.err) {
		t.Fatalf("err = %v, want the lookup error", err)
	}
	if got.Status != Pending || store.approved != 0 || store.grant.Status != Pending {
		t.Fatalf("grant changed without a known mode: %+v approvals=%d", store.grant, store.approved)
	}
}

func TestApplyPolicySkipsNonPendingGrantsWithoutReadingMode(t *testing.T) {
	store := policyFixture("text-to-image")
	modes := &modeReader{mode: "auto"}
	for _, status := range []Status{Approved, Claimed, Bound, Denied} {
		store.grant.Status = status
		got, err := policyService(store, modes).ApplyPolicy(context.Background(), store.grant)
		if err != nil || got.Status != status {
			t.Fatalf("%s: got %+v err=%v", status, got, err)
		}
	}
	if modes.calls != 0 || store.approved != 0 {
		t.Fatalf("mode read %d times, approvals=%d", modes.calls, store.approved)
	}
}

func TestApproveByPolicyUsesFrozenCapabilityAndIdentity(t *testing.T) {
	// The capability comes from the frozen snapshot: a 3D step stays pending in
	// manual mode no matter what the caller believes.
	store := policyFixture("image-to-3d")
	service := policyService(store, &modeReader{mode: "manual"})
	if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("manual image-to-3d: %v", err)
	}
	if store.approved != 0 || store.grant.Status != Pending {
		t.Fatalf("grant changed: %+v", store.grant)
	}
	// A snapshot that no longer matches the grant identity is a conflict, never
	// an approval, even for a capability that needs no human.
	store = policyFixture("text-to-image")
	store.snapshot.InputHash = "other-hash"
	service = policyService(store, &modeReader{mode: "manual"})
	if _, err := service.ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrConflict) {
		t.Fatalf("identity mismatch: %v", err)
	}
	if store.approved != 0 {
		t.Fatalf("approved despite identity mismatch")
	}
	// The matching case approves once and records the policy actor.
	store = policyFixture("text-to-image")
	got, err := policyService(store, &modeReader{mode: "manual"}).ApplyPolicy(context.Background(), store.grant)
	if err != nil || got.Status != Approved || got.DecidedBy != "policy:manual" || !got.ApprovedByPolicy() || store.approved != 1 {
		t.Fatalf("manual text-to-image: %+v err=%v approvals=%d", got, err, store.approved)
	}
}

// An Agent step that spends extra credits is the person's to approve in manual
// mode even on a capability that is otherwise automatic, and automatic mode
// approves it with the rest. The panel's own grants (a checkbox the person
// ticked themselves) keep following the capability alone.
func TestExtraCreditsOfAnAgentStepNeedAHumanInManualModeOnly(t *testing.T) {
	agent := func() *fakeGrantStore {
		store := policyFixture("text-to-image")
		store.grant.Source = AgentPending
		store.grant.TriggerTurnID = "turn-a"
		store.snapshot.ExtraCredits = 10
		return store
	}
	store := agent()
	if _, err := policyService(store, &modeReader{mode: "manual"}).ApproveByPolicy(context.Background(), "owner-a", "request-a", "manual"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("manual agent step with extra credits: %v", err)
	}
	if store.approved != 0 || store.grant.Status != Pending {
		t.Fatalf("grant changed: %+v", store.grant)
	}
	store = agent()
	if _, err := policyService(store, &modeReader{mode: "bogus"}).ApproveByPolicy(context.Background(), "owner-a", "request-a", "bogus"); !errors.Is(err, ErrHumanRequired) {
		t.Fatalf("unknown mode must behave as manual: %v", err)
	}
	store = agent()
	got, err := policyService(store, &modeReader{mode: "auto"}).ApplyPolicy(context.Background(), store.grant)
	if err != nil || got.Status != Approved || got.DecidedBy != "policy:auto" || store.approved != 1 {
		t.Fatalf("auto agent step with extra credits: %+v err=%v", got, err)
	}
	// No extra credits: the capability decides, as before.
	store = agent()
	store.snapshot.ExtraCredits = 0
	got, err = policyService(store, &modeReader{mode: "manual"}).ApplyPolicy(context.Background(), store.grant)
	if err != nil || got.Status != Approved || store.approved != 1 {
		t.Fatalf("manual agent step without extra credits: %+v err=%v", got, err)
	}
	// A grant of the panel is not an Agent step.
	store = policyFixture("text-to-image")
	store.snapshot.ExtraCredits = 10
	got, err = policyService(store, &modeReader{mode: "manual"}).ApplyPolicy(context.Background(), store.grant)
	if err != nil || got.Status != Approved {
		t.Fatalf("panel grant: %+v err=%v", got, err)
	}
}

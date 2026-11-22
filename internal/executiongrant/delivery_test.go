package executiongrant

import (
	"context"
	"testing"
)

// #461: the platform keeps its own delivery promise. A grant it asks for while
// completing a format a generation still owes is decided by policy in every
// approval mode: it is never left to a person (no card), it never counts a 3D
// step, and it never touches the session budget. The audit must read as the
// platform, not as a browser.

func deliveryFixture(capability string) *fakeGrantStore {
	store := policyFixture(capability)
	store.grant.Source = Delivery
	return store
}

func TestDeliveryGrantIsApprovedInEveryModeWithoutAStep(t *testing.T) {
	for _, mode := range []string{"auto", "manual", "budget", "steps"} {
		t.Run(mode, func(t *testing.T) {
			store := deliveryFixture("format-conversion")
			modes := &modeReader{mode: mode}
			got, err := policyService(store, modes).ApplyPolicy(context.Background(), store.grant)
			if err != nil {
				t.Fatalf("mode %s: %v", mode, err)
			}
			if got.Status != Approved || got.DecidedBy != DeliveryActor || !got.ApprovedByPolicy() {
				t.Fatalf("mode %s: grant = %+v", mode, got)
			}
			if store.approved != 1 {
				t.Fatalf("mode %s: %d approvals", mode, store.approved)
			}
		})
	}
}

// The delivery grant is not budget eligible: the automatic completion's own
// ceiling is what bounds it, so the session's plan is untouched (D7).
func TestDeliveryGrantIsNeverBudgetEligible(t *testing.T) {
	store := deliveryFixture("format-conversion")
	modes := &modeReader{mode: "budget"}
	service := policyService(store, modes)
	service.Grants = store
	if _, err := service.ApplyPolicy(context.Background(), store.grant); err != nil {
		t.Fatal(err)
	}
	if store.grant.BudgetEligible {
		t.Fatalf("grant = %+v, want it outside the session budget", store.grant)
	}
}

// A delivery grant whose prepared input no longer matches is a conflict, never
// an approval.
func TestDeliveryGrantChecksItsFrozenIdentity(t *testing.T) {
	store := deliveryFixture("format-conversion")
	store.snapshot.InputHash = "other-hash"
	if _, err := policyService(store, &modeReader{mode: "auto"}).
		ApproveByPolicy(context.Background(), "owner-a", "request-a", "auto"); err != ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if store.approved != 0 || store.grant.Status != Pending {
		t.Fatalf("grant changed: %+v approvals=%d", store.grant, store.approved)
	}
}

// Only the platform's own actor asks for a delivery grant: another actor with
// the same source is refused before anything is stored.
func TestDeliveryGrantComesFromThePlatformActor(t *testing.T) {
	store := policyFixture("format-conversion")
	service := policyService(store, &modeReader{mode: "auto"})
	prepared := store.snapshot
	_, err := service.Prepare(context.Background(), PrepareCommand{
		Harness:   store.grant.Harness,
		Principal: Principal{OwnerID: "owner-a", ActorID: "processing-plan", Source: Delivery},
		SessionID: "session-a",
		OfferingID: "offering", OfferingRevision: "r1",
		PreparedInputID: prepared.ID, PreparedInputHash: prepared.InputHash,
	})
	if err != ErrInvalid {
		t.Fatalf("err = %v, want ErrInvalid for a non-platform actor", err)
	}
}
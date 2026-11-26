package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// #665: the session policy reads the approval mode and then approves. A Stop
// of the grant's trigger turn that commits in between must not leave an
// approved grant nobody can claim: the policy write refuses it like a person's
// approval does, and the grant stays pending for the Stop sweep.
func TestPolicyApproveIsRefusedOnceTheTriggerTurnIsStopped(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)

	_, _, grant := stopFixture(t, r, "policy-stopped")
	stopTurn(t, r, "policy-stopped")
	if _, err := r.ApproveExecutionGrantByPolicy(ctx, "owner-a", grant.RequestID, "policy:auto"); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("policy approve of a stopped turn's grant = %v, want ErrState", err)
	}
	if got, err := r.GetExecutionGrant(ctx, "owner-a", grant.RequestID); err != nil || got.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v, want still pending", got, err)
	}

	_, _, live := stopFixture(t, r, "policy-live")
	if got, err := r.ApproveExecutionGrantByPolicy(ctx, "owner-a", live.RequestID, "policy:auto"); err != nil || got.Status != executiongrant.Approved {
		t.Fatalf("policy approve of a running turn's grant = %+v %v", got, err)
	}
}

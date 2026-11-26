package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// A person denying a card the expiry sweep already denied gets the denied
// grant back instead of a state error; approving it is still refused (#665).
func TestDenyAfterExpirySweepIsIdempotent665(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	insertGrantTurn(t, r, "turn-a", "owner-a", "session-a")
	i := grantIdentity()
	g, err := r.PreparePending(ctx, i, executiongrant.AgentPending, "turn-a")
	if err != nil {
		t.Fatal(err)
	}
	if expired, err := r.ExpireExecutionGrant(ctx, i.OwnerID, g.RequestID); err != nil || !expired.ExpiredUndecided() {
		t.Fatalf("expire: %+v %v", expired, err)
	}
	denied, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Deny)
	if err != nil {
		t.Fatalf("deny after expiry: %v", err)
	}
	if denied.Status != executiongrant.Denied || denied.DecidedBy != executiongrant.ExpiredActor {
		t.Fatalf("deny changed the expired denial: %+v", denied)
	}
	if _, err := r.DecideExecutionGrant(ctx, i.OwnerID, g.RequestID, "human-a", executiongrant.Approve); !errors.Is(err, executiongrant.ErrState) {
		t.Fatalf("approve after expiry: %v", err)
	}
	var audits int
	if err := r.db.QueryRow(`SELECT count(*) FROM execution_grant_audit WHERE request_id=?`, g.RequestID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit events = %d err=%v, want only the expiry", audits, err)
	}
	service := executiongrant.Service{Grants: r}
	principal := executiongrant.Principal{OwnerID: i.OwnerID, ActorID: "human-a", Source: executiongrant.HumanBrowser}
	if _, err := service.Decide(ctx, principal, g.RequestID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrExpired) {
		t.Fatalf("service approve after expiry: %v", err)
	}
	if got, err := service.Decide(ctx, principal, g.RequestID, executiongrant.Deny); err != nil || got.Status != executiongrant.Denied {
		t.Fatalf("service deny after expiry: %+v %v", got, err)
	}
}

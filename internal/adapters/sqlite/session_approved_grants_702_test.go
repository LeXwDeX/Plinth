package sqlite

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// #702: the checkpoint window counts the session's approved grants (approved,
// claimed or bound) of every source but delivery; pending and denied grants,
// and other sessions or owners, do not count.
func TestSessionApprovedGrantsCountsApprovedNonDeliveryGrants(t *testing.T) {
	ctx := context.Background()
	r := openGrantTestRepo(t)
	prepare := func(name, source string) executiongrant.Grant {
		t.Helper()
		i := grantIdentity()
		i.PreparedInputID, i.PreparedInputHash = "prepared-"+name, "hash-"+name
		i.IdempotencyKey, i.OperationID = "key-"+name, "operation-"+name
		createGrantPrepared(t, r, i, []byte(`{}`))
		g, err := r.PreparePending(ctx, i, source, "")
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	count := func() int {
		t.Helper()
		n, err := r.SessionApprovedGrants(ctx, "owner-a", "session-a")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("empty session = %d", n)
	}
	byPerson := prepare("person", executiongrant.HumanBrowser)
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", byPerson.RequestID, "human-a", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	byPolicy := prepare("policy", executiongrant.HumanBrowser)
	if _, err := r.ApproveExecutionGrantByPolicy(ctx, "owner-a", byPolicy.RequestID, executiongrant.PolicyActorPrefix+"auto"); err != nil {
		t.Fatal(err)
	}
	denied := prepare("denied", executiongrant.HumanBrowser)
	if _, err := r.DecideExecutionGrant(ctx, "owner-a", denied.RequestID, "human-a", executiongrant.Deny); err != nil {
		t.Fatal(err)
	}
	prepare("pending", executiongrant.HumanBrowser)
	delivery := prepare("delivery", executiongrant.Delivery)
	if _, err := r.ApproveExecutionGrantByPolicy(ctx, "owner-a", delivery.RequestID, executiongrant.DeliveryActor); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("approved non-delivery grants = %d, want 2", n)
	}
	// A claimed or bound grant was approved too.
	if _, err := r.db.Exec(`UPDATE execution_grants SET status = 'claimed' WHERE request_id = ?`, byPolicy.RequestID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("after claim = %d, want 2", n)
	}
	if n, err := r.SessionApprovedGrants(ctx, "owner-b", "session-a"); err != nil || n != 0 {
		t.Fatalf("another owner = %d err=%v", n, err)
	}
	if _, err := r.SessionApprovedGrants(ctx, "", "session-a"); err == nil {
		t.Fatal("an empty owner was accepted")
	}
}

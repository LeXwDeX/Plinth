package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// prepareWithEligibility issues the grant of one operation with or without
// production-plan eligibility, the way executiongrant.Service.Prepare chooses
// between the two store calls.
func prepareWithEligibility(ctx context.Context, r *Repository, i executiongrant.Identity, eligible bool) (executiongrant.Grant, error) {
	if eligible {
		return r.PreparePendingWithBudget(ctx, i, executiongrant.HumanBrowser, "", true)
	}
	return r.PreparePending(ctx, i, executiongrant.HumanBrowser, "")
}

// #120 item 9: budget eligibility belongs to the operation. Replaying or
// re-requesting the same operation with the other eligibility is an identity
// conflict, and the stored grant keeps its standing; it is never flipped in
// place.
func TestPreparePendingRefusesAChangedBudgetEligibility(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		eligible bool
		// settle moves the first grant to the status the replay meets.
		settle func(t *testing.T, r *Repository, g executiongrant.Grant)
	}{
		{name: "pending eligible", eligible: true},
		{name: "pending ineligible", eligible: false},
		{name: "approved eligible", eligible: true, settle: func(t *testing.T, r *Repository, g executiongrant.Grant) {
			t.Helper()
			if _, err := r.DecideExecutionGrant(context.Background(), g.OwnerID, g.RequestID, "human-a", executiongrant.Approve); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "denied ineligible", eligible: false, settle: func(t *testing.T, r *Repository, g executiongrant.Grant) {
			t.Helper()
			if _, err := r.DecideExecutionGrant(context.Background(), g.OwnerID, g.RequestID, "human-a", executiongrant.Deny); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			r := openGrantTestRepo(t)
			i := grantIdentity()
			first, err := prepareWithEligibility(ctx, r, i, testCase.eligible)
			if err != nil {
				t.Fatal(err)
			}
			if first.BudgetEligible != testCase.eligible {
				t.Fatalf("first grant eligibility = %v", first.BudgetEligible)
			}
			if testCase.settle != nil {
				testCase.settle(t, r, first)
			}
			before, err := r.GetExecutionGrant(ctx, i.OwnerID, first.RequestID)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := prepareWithEligibility(ctx, r, i, !testCase.eligible); !errors.Is(err, executiongrant.ErrConflict) {
				t.Fatalf("replay with changed eligibility: %v", err)
			}
			after, err := r.GetExecutionGrant(ctx, i.OwnerID, first.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			if after.BudgetEligible != before.BudgetEligible || after.Status != before.Status ||
				after.RequestVersion != before.RequestVersion {
				t.Fatalf("grant changed by a refused replay: before=%+v after=%+v", before, after)
			}

			// The same operation with its own eligibility still replays.
			if before.Status == executiongrant.Denied {
				return
			}
			again, err := prepareWithEligibility(ctx, r, i, testCase.eligible)
			if err != nil || again.RequestID != first.RequestID || again.BudgetEligible != testCase.eligible {
				t.Fatalf("exact replay = %+v err=%v", again, err)
			}
		})
	}
}

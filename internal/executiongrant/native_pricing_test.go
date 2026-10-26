package executiongrant

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativeSmallestExtraNeverBecomesFreePolicy(t *testing.T) {
	for _, tc := range []struct {
		source, mode string
		human        bool
	}{{AgentPending, "manual", true}, {AgentPending, "auto", false}, {HumanBrowser, "manual", false}} {
		t.Run(tc.source+tc.mode, func(t *testing.T) {
			store := policyFixture("text-to-image")
			store.grant.Source = tc.source
			store.snapshot.NativeQuote = &schema.NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0.000001", TotalAmount: "0.500001", Components: []schema.NativeCharge{{ID: "quality", Label: "Quality", Amount: "0.000001"}}}
			_, e := policyService(store, &modeReader{mode: tc.mode}).ApproveByPolicy(context.Background(), "owner-a", "request-a", tc.mode)
			if tc.human {
				if !errors.Is(e, ErrHumanRequired) || store.approved != 0 {
					t.Fatalf("tiny extra approved: %v", e)
				}
			} else if e != nil || store.approved != 1 {
				t.Fatalf("policy difference changed: %v", e)
			}
		})
	}
	store := policyFixture("text-to-image")
	store.snapshot.NativeQuote = &schema.NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0"}
	if _, e := policyService(store, &modeReader{mode: "auto"}).ApproveByPolicy(context.Background(), "owner-a", "request-a", "auto"); !errors.Is(e, ErrConflict) || store.approved != 0 {
		t.Fatal("corrupt quote approved")
	}
}

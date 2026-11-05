package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// #135: a production plan's approval (budget:<id>) is the session policy's,
// like policy:<mode>; a person's decision stays human, and a grant that was
// never read has no source.
func TestApprovalSourceNamesPlanApprovalsAsPolicy(t *testing.T) {
	for name, testCase := range map[string]struct {
		grant executiongrant.Grant
		want  string
	}{
		"production plan": {executiongrant.Grant{RequestID: "r1", Status: executiongrant.Approved, DecidedBy: executiongrant.BudgetActorPrefix + "pb_1"}, approvalByPolicy},
		"approval mode":   {executiongrant.Grant{RequestID: "r1", Status: executiongrant.Approved, DecidedBy: executiongrant.PolicyActorPrefix + "manual"}, approvalByPolicy},
		"person":          {executiongrant.Grant{RequestID: "r1", Status: executiongrant.Approved, DecidedBy: "owner-a"}, approvalByHuman},
		"unknown grant":   {executiongrant.Grant{}, ""},
	} {
		if got := approvalSource(testCase.grant); got != testCase.want {
			t.Errorf("%s: approval source = %q, want %q", name, got, testCase.want)
		}
	}
}

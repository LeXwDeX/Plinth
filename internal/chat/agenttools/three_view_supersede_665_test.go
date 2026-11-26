package agenttools

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// abandoningGrant records the grants the system denies as abandoned.
type abandoningGrant struct {
	oneGrant
	abandoned *[]string
	fail      error
}

func (g abandoningGrant) AbandonRefusedExecutionGrant(_ context.Context, owner, requestID string) error {
	if g.fail != nil {
		return g.fail
	}
	*g.abandoned = append(*g.abandoned, owner+"/"+requestID)
	return nil
}

// #665 wave 4: a new group for the same source after the earlier group's turn
// ended denies that group's approved, unsubmitted view (system actor, audited
// by the store), so the person cannot continue the old view from the panel
// and pay for the same views twice. An approval whose turn still runs, a card
// still waiting and an approval of a stopped turn are left as they are.
func TestNewThreeViewGroupDeniesTheEndedGroupsApproval665(t *testing.T) {
	waiting := []skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}}
	grantOf := func(status executiongrant.Status) executiongrant.Grant {
		return executiongrant.Grant{RequestID: "grant-earlier", Identity: executiongrant.Identity{OwnerID: "owner",
			SessionID: "session"}, TriggerTurnID: "turn-earlier", Status: status, Source: executiongrant.AgentPending,
			DecidedBy: "human:owner"}
	}
	for _, tt := range []struct {
		name           string
		status         executiongrant.Status
		stopped, ended bool
		fail           error
		abandoned      int
		created        bool
	}{
		{"approval, turn ended", executiongrant.Approved, false, true, nil, 1, true},
		{"approval, turn running", executiongrant.Approved, false, false, nil, 0, false},
		{"approval, turn stopped", executiongrant.Approved, true, true, nil, 0, true},
		{"pending card, turn ended", executiongrant.Pending, false, true, nil, 0, false},
		{"denying fails", executiongrant.Approved, false, true, errors.New("store down"), 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			creates := 0
			var abandoned []string
			executor, command := duplicateGuardExecutor(t, waiting, grantOf(tt.status), tt.stopped, tt.ended, &creates)
			executor.skillRuntime.Grants = &executiongrant.Service{Grants: abandoningGrant{
				oneGrant: oneGrant{grant: grantOf(tt.status)}, abandoned: &abandoned, fail: tt.fail}}
			executor.maxGeneration, executor.usage = 1, map[string]generationUsage{}
			_, err := executor.createThreeView(context.Background(), command, nil)
			if len(abandoned) != tt.abandoned || (tt.abandoned == 1 && abandoned[0] != "owner/grant-earlier") {
				t.Fatalf("abandoned = %v, want %d", abandoned, tt.abandoned)
			}
			if created := errors.Is(err, errDuplicateTestCreate); created != tt.created {
				t.Fatalf("new group created = %v (err %v), want %v", created, err, tt.created)
			}
			if tt.fail != nil && executor.generationLimitWouldBeReached(executor.scopeKey(command), command.AssistantMessageID, "next", 0, 1) {
				t.Fatal("a call that could not replace the old group used up the turn's generation")
			}
		})
	}
}

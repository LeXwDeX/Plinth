package agenttools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// resumeRuns is supersedeRuns whose created run is found again by its key, as
// the store does on the replay of the same call.
type resumeRuns struct{ *supersedeRuns }

func (s resumeRuns) GetSkillRunByIdempotency(context.Context, generation.OwnerID, string) (skillrun.Run, error) {
	if s.created == nil {
		return skillrun.Run{}, skillrun.ErrMissing
	}
	return *s.created, nil
}

// #665: the call creates its run, then fails before the grant request (here:
// preparing the first view fails; a restart in the same window is the same
// state). The replay finds the run without a grant and finishes the start: it
// replaces the ended group's approval and requests the grant, and it counts the
// call again (once), so the turn's generation limit applies to it. Before, the
// replay requested a plain grant and left the old approval and the count alone.
func TestThreeViewReplayFinishesAStartThatStoppedAfterCreate665(t *testing.T) {
	waiting := []skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}}
	earlierGrant := executiongrant.Grant{RequestID: "grant-earlier", Identity: executiongrant.Identity{OwnerID: "owner",
		SessionID: "session"}, TriggerTurnID: "turn-earlier", Status: executiongrant.Approved,
		Source: executiongrant.AgentPending, DecidedBy: "human:owner"}
	for _, tt := range []struct {
		name    string
		prior   int
		limited bool
	}{
		{name: "replay finishes the start"},
		{name: "replay is counted against the turn", prior: 1, limited: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			unused := 0
			executor, command := duplicateGuardExecutor(t, waiting, earlierGrant, false, true, &unused)
			store := &supersedeRuns{earlier: skillrun.Run{ID: "run-earlier", RecipeID: threeViewRecipeID,
				SourceAssetID: "asset-source", IdempotencyKey: "chat-skill:session:other", Steps: waiting},
				readErr: errors.New("store down")}
			runs, err := skillrun.NewService(resumeRuns{store}, supersedeRecipes{})
			if err != nil {
				t.Fatal(err)
			}
			grants := &supersedeGrants{grant: earlierGrant}
			executor.skillRuntime = &skillrun.Runtime{Runs: runs, Generation: supersedeGateway{},
				Grants: &executiongrant.Service{Grants: grants}}
			executor.maxGeneration, executor.usage = 1, map[string]generationUsage{}

			first, err := executor.createThreeView(context.Background(), command, nil)
			if err == nil || store.creates != 1 || len(grants.abandoned) != 0 || !first.GenerationUnitsTakenBack {
				t.Fatalf("first attempt: result %+v err %v creates %d abandoned %v", first, err, store.creates, grants.abandoned)
			}

			// The replay of the same call: the store answers again, and other
			// calls of the turn may have used generations meanwhile.
			store.readErr = nil
			command.PriorGenerationCalls = tt.prior
			replay, err := executor.createThreeView(context.Background(), command, nil)
			if store.creates != 1 {
				t.Fatalf("the replay created another run: %d", store.creates)
			}
			if tt.limited {
				if err != nil || replay.AgentContent != executor.limitText(command) || !replay.NoSubmission ||
					len(grants.abandoned) != 0 || grants.grant.Status != executiongrant.Approved {
					t.Fatalf("over the limit: result %+v err %v abandoned %v", replay, err, grants.abandoned)
				}
				if executor.countedGenerationUnits(executor.scopeKey(command), command.AssistantMessageID, command.Call.ID) != 0 {
					t.Fatal("a refused replay stays counted")
				}
				return
			}
			// The fake run is not in the prepared phase, so reaching the grant
			// request answers ErrConflict.
			if !errors.Is(err, skillrun.ErrConflict) {
				t.Fatalf("the replay did not reach the grant request: result %+v err %v", replay, err)
			}
			if len(grants.abandoned) != 1 || grants.grant.Status != executiongrant.Denied {
				t.Fatalf("the replay did not replace the ended group's approval: %v %s", grants.abandoned, grants.grant.Status)
			}
		})
	}
}

// #665: a grant to replace that a person continued between the guard's read
// and the start is seen before the new run is created: the call answers
// duplicate_run and leaves no run without a grant.
func TestThreeViewStartChecksTheReplacedGrantBeforeCreate665(t *testing.T) {
	waiting := []skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}}
	approved := executiongrant.Grant{RequestID: "grant-earlier", Identity: executiongrant.Identity{OwnerID: "owner",
		SessionID: "session"}, TriggerTurnID: "turn-earlier", Status: executiongrant.Approved,
		Source: executiongrant.AgentPending, DecidedBy: "human:owner"}
	unused := 0
	executor, command := duplicateGuardExecutor(t, waiting, approved, false, true, &unused)
	store := &supersedeRuns{earlier: skillrun.Run{ID: "run-earlier", RecipeID: threeViewRecipeID,
		SourceAssetID: "asset-source", IdempotencyKey: "chat-skill:session:other", Steps: waiting}}
	runs, err := skillrun.NewService(store, supersedeRecipes{})
	if err != nil {
		t.Fatal(err)
	}
	claimed := approved
	claimed.Status = executiongrant.Claimed
	grants := &movingGrants{reads: []executiongrant.Grant{approved, claimed}}
	executor.skillRuntime = &skillrun.Runtime{Runs: runs, Generation: supersedeGateway{},
		Grants: &executiongrant.Service{Grants: grants}}
	executor.maxGeneration, executor.usage = 1, map[string]generationUsage{}
	result, err := executor.createThreeView(context.Background(), command, nil)
	if err != nil || !strings.HasPrefix(result.AgentContent, "already running: run-earlier") {
		t.Fatalf("result %+v err %v", result, err)
	}
	if store.creates != 0 || grants.abandoned != 0 {
		t.Fatalf("creates %d, abandoned %d; want none", store.creates, grants.abandoned)
	}
	if executor.countedGenerationUnits(executor.scopeKey(command), command.AssistantMessageID, command.Call.ID) != 0 {
		t.Fatal("a call that started nothing is counted")
	}
}

// movingGrants answers its reads in order (the last one repeats).
type movingGrants struct {
	executiongrant.Repository
	reads     []executiongrant.Grant
	abandoned int
}

func (g *movingGrants) GetExecutionGrant(context.Context, string, string) (executiongrant.Grant, error) {
	grant := g.reads[0]
	if len(g.reads) > 1 {
		g.reads = g.reads[1:]
	}
	return grant, nil
}

func (g *movingGrants) AbandonRefusedExecutionGrant(context.Context, string, string) error {
	g.abandoned++
	return nil
}

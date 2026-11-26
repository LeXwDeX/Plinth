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

// supersedeGrants holds the earlier group's grant and changes it as the store
// would: the system denial applies only to a grant still approved.
type supersedeGrants struct {
	executiongrant.Repository
	grant     executiongrant.Grant
	abandoned []string
	fail      error
	// claimFirst moves the grant to claimed just before the denial, as a
	// person continuing the view between the guard's read and the denial.
	claimFirst bool
}

func (g *supersedeGrants) GetExecutionGrant(context.Context, string, string) (executiongrant.Grant, error) {
	return g.grant, nil
}

func (g *supersedeGrants) AbandonRefusedExecutionGrant(_ context.Context, owner, requestID string) error {
	if g.fail != nil {
		return g.fail
	}
	if g.claimFirst {
		g.grant.Status = executiongrant.Claimed
	}
	if g.grant.Status == executiongrant.Approved {
		g.grant.Status = executiongrant.Denied
		g.abandoned = append(g.abandoned, owner+"/"+requestID)
	}
	return nil
}

// supersedeRuns holds the earlier group and creates the new one, whose first
// view is already prepared; creating or reading it can fail.
type supersedeRuns struct {
	skillrun.Store
	earlier            skillrun.Run
	created            *skillrun.Run
	creates            int
	createErr, readErr error
}

func (s *supersedeRuns) GetSkillRunByIdempotency(context.Context, generation.OwnerID, string) (skillrun.Run, error) {
	return skillrun.Run{}, skillrun.ErrMissing
}

func (s *supersedeRuns) ListSkillRuns(context.Context, generation.OwnerID, string, string, int) ([]skillrun.Run, error) {
	return []skillrun.Run{s.earlier}, nil
}

func (s *supersedeRuns) CreateSkillRun(_ context.Context, run skillrun.Run) (skillrun.Run, error) {
	s.creates++
	if s.createErr != nil {
		return skillrun.Run{}, s.createErr
	}
	run.Steps[0].PreparedInputID, run.Steps[0].PreparedInputHash = "prepared-new", "hash-new"
	s.created = &run
	return run, nil
}

func (s *supersedeRuns) GetSkillRun(context.Context, generation.OwnerID, string) (skillrun.Run, error) {
	if s.readErr != nil || s.created == nil {
		return skillrun.Run{}, errors.Join(errors.New("read failed"), s.readErr)
	}
	return *s.created, nil
}

type supersedeRecipes struct{ skillrun.RecipeResolver }

func (supersedeRecipes) ResolveRecipe(context.Context, string, generation.OfferingID, string) (skillrun.Recipe, bool, error) {
	return skillrun.Recipe{ID: threeViewRecipeID, Revision: "r1", Steps: []skillrun.StepSpec{{
		Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "selected-image-offering",
		OfferingRevision: "r7", PromptTemplate: "front view", PromptParameter: "prompt",
		SourceParameter: "reference_assets", OutputRole: "image"}}}, true, nil
}

type supersedeGateway struct{ skillrun.GenerationGateway }

func (supersedeGateway) GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error) {
	return generation.PreparedInput{ID: "prepared-new", InputHash: "hash-new"}, nil
}

// #665: a new group for the same source after the earlier group's turn ended
// denies that group's approved, unsubmitted view (system actor, audited by the
// store), so the person cannot continue the old view from the panel and pay
// for the same views twice. The denial comes only once the new group is
// created and its first view prepared: a failure to create or prepare leaves
// the old approval as it was. A grant the person continued between the guard's
// read and the denial (claimed) is not denied and the new group requests no
// grant. Every failure after counting takes the call's count back. An approval
// whose turn still runs, a card still waiting and an approval of a stopped
// turn are left as they are.
func TestNewThreeViewGroupDeniesTheEndedGroupsApproval665(t *testing.T) {
	waiting := []skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}}
	grantOf := func(status executiongrant.Status) executiongrant.Grant {
		return executiongrant.Grant{RequestID: "grant-earlier", Identity: executiongrant.Identity{OwnerID: "owner",
			SessionID: "session"}, TriggerTurnID: "turn-earlier", Status: status, Source: executiongrant.AgentPending,
			DecidedBy: "human:owner"}
	}
	storeDown := errors.New("store down")
	for _, tt := range []struct {
		name               string
		status             executiongrant.Status
		stopped, ended     bool
		fail               error
		createErr, readErr error
		claimFirst         bool
		abandoned, creates int
		after              executiongrant.Status
		// requested: the new group reached its grant request (the fake run
		// is not in the prepared phase, so the request answers ErrConflict).
		requested, duplicate bool
	}{
		{name: "approval, turn ended", status: executiongrant.Approved, ended: true, abandoned: 1, creates: 1,
			after: executiongrant.Denied, requested: true},
		{name: "approval, turn running", status: executiongrant.Approved, after: executiongrant.Approved, duplicate: true},
		{name: "approval, turn stopped", status: executiongrant.Approved, stopped: true, ended: true, creates: 1,
			after: executiongrant.Approved, requested: true},
		{name: "pending card, turn ended", status: executiongrant.Pending, ended: true, after: executiongrant.Pending,
			duplicate: true},
		{name: "denying fails", status: executiongrant.Approved, ended: true, fail: storeDown, creates: 1,
			after: executiongrant.Approved},
		{name: "creating the new group fails", status: executiongrant.Approved, ended: true, createErr: storeDown,
			creates: 1, after: executiongrant.Approved},
		{name: "preparing the new group fails", status: executiongrant.Approved, ended: true, readErr: storeDown,
			creates: 1, after: executiongrant.Approved},
		{name: "old grant claimed before the denial", status: executiongrant.Approved, ended: true, claimFirst: true,
			creates: 1, after: executiongrant.Claimed, duplicate: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			unused := 0
			executor, command := duplicateGuardExecutor(t, waiting, grantOf(tt.status), tt.stopped, tt.ended, &unused)
			store := &supersedeRuns{earlier: skillrun.Run{ID: "run-earlier", RecipeID: threeViewRecipeID,
				SourceAssetID: "asset-source", IdempotencyKey: "chat-skill:session:other", Steps: waiting},
				createErr: tt.createErr, readErr: tt.readErr}
			runs, err := skillrun.NewService(store, supersedeRecipes{})
			if err != nil {
				t.Fatal(err)
			}
			grants := &supersedeGrants{grant: grantOf(tt.status), fail: tt.fail, claimFirst: tt.claimFirst}
			executor.skillRuntime = &skillrun.Runtime{Runs: runs, Generation: supersedeGateway{},
				Grants: &executiongrant.Service{Grants: grants}}
			executor.maxGeneration, executor.usage = 1, map[string]generationUsage{}
			result, err := executor.createThreeView(context.Background(), command, nil)
			if len(grants.abandoned) != tt.abandoned || (tt.abandoned == 1 && grants.abandoned[0] != "owner/grant-earlier") {
				t.Fatalf("abandoned = %v, want %d", grants.abandoned, tt.abandoned)
			}
			if store.creates != tt.creates || grants.grant.Status != tt.after {
				t.Fatalf("creates = %d, old grant %s; want %d, %s", store.creates, grants.grant.Status, tt.creates, tt.after)
			}
			if requested := errors.Is(err, skillrun.ErrConflict); requested != tt.requested {
				t.Fatalf("grant requested = %v (result %+v, err %v), want %v", requested, result, err, tt.requested)
			}
			if duplicate := strings.HasPrefix(result.AgentContent, "already running: run-earlier"); duplicate != tt.duplicate {
				t.Fatalf("duplicate text = %q (err %v), want duplicate %v", result.AgentContent, err, tt.duplicate)
			}
			if tt.duplicate && err != nil {
				t.Fatalf("a duplicate answer carries an error: %v", err)
			}
			// No case here leaves a grant on the new group, so none uses the
			// turn's only generation.
			if executor.generationLimitWouldBeReached(executor.scopeKey(command), command.AssistantMessageID, "next", 0, 1) {
				t.Fatal("a call that started no grant used up the turn's generation")
			}
			if tt.creates > 0 && !result.GenerationUnitsTakenBack {
				t.Fatalf("a counted call that failed does not take its units back: %+v", result)
			}
		})
	}
}

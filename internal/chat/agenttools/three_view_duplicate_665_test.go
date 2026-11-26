package agenttools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

var errDuplicateTestCreate = errors.New("create reached")

// earlierThreeViewRuns holds one earlier group of the session on the same
// source picture; creating a new run is observable.
type earlierThreeViewRuns struct {
	skillrun.Store
	earlier skillrun.Run
	creates *int
}

func (earlierThreeViewRuns) GetSkillRunByIdempotency(context.Context, generation.OwnerID, string) (skillrun.Run, error) {
	return skillrun.Run{}, skillrun.ErrMissing
}

func (s earlierThreeViewRuns) ListSkillRuns(context.Context, generation.OwnerID, string, string, int) ([]skillrun.Run, error) {
	return []skillrun.Run{s.earlier}, nil
}

func (s earlierThreeViewRuns) CreateSkillRun(context.Context, skillrun.Run) (skillrun.Run, error) {
	*s.creates++
	return skillrun.Run{}, errDuplicateTestCreate
}

type duplicateTestRecipes struct{ skillrun.RecipeResolver }

func (duplicateTestRecipes) ResolveRecipe(context.Context, string, generation.OfferingID, string) (skillrun.Recipe, bool, error) {
	return skillrun.Recipe{}, false, errDuplicateTestCreate
}

type oneGrant struct {
	executiongrant.Repository
	grant executiongrant.Grant
}

func (g oneGrant) GetExecutionGrant(context.Context, string, string) (executiongrant.Grant, error) {
	return g.grant, nil
}

type sourceReference struct{}

func (sourceReference) Resolve(context.Context, references.ReferenceScope, []string) ([]generation.AssetID, error) {
	return []generation.AssetID{"asset-source"}, nil
}

// #665: a second create_three_view on the same source picture while the earlier
// group still waits on its card is refused with the Schema's words, before any
// run, front view or card is made. Once the earlier group is no longer active,
// asking again starts a new group as before.
func TestThreeViewSecondGroupOfTheSameSourceIsRefusedWhileTheFirstWaits(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  executiongrant.Status
		refused bool
	}{{"pending card", executiongrant.Pending, true}, {"denied card", executiongrant.Denied, false}} {
		t.Run(tt.name, func(t *testing.T) {
			creates := 0
			store := earlierThreeViewRuns{creates: &creates, earlier: skillrun.Run{ID: "run-earlier",
				RecipeID: threeViewRecipeID, SourceAssetID: "asset-source", IdempotencyKey: "chat-skill:session:other",
				Steps: []skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}}}}
			runs, err := skillrun.NewService(store, duplicateTestRecipes{})
			if err != nil {
				t.Fatal(err)
			}
			grants := &executiongrant.Service{Grants: oneGrant{grant: executiongrant.Grant{RequestID: "grant-earlier",
				Status: tt.status, Source: executiongrant.AgentPending}}}
			executor := &Executor{
				skillRuntime: &skillrun.Runtime{Runs: runs, Grants: grants}, skillReferences: sourceReference{},
				skillOfferings: referenceSelectionOfferings{}, skillRecipes: map[string]string{threeViewRecipeID: "r1"},
				skillTurnStopped: func(context.Context, string, string, string) (bool, error) { return false, nil },
			}
			executor.SetThreeView(catalog.ChatThreeViewSchema{Messages: catalog.ChatThreeViewMessages{
				DuplicateRun: "already running: {run}", ReferenceRequired: "one image"}})
			command := chat.ToolCommand{
				OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn",
				Call:    agents.ToolCall{ID: "call", Name: ToolCreateThreeView, Arguments: []byte(`{}`)},
				Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "selected-image-model"},
				History: []chat.Message{{Role: "user", ImageURLs: []string{"/source.png"}}},
			}
			result, err := executor.createThreeView(context.Background(), command, nil)
			if tt.refused {
				if err != nil || !result.NoSubmission || result.AgentContent != "already running: run-earlier" || creates != 0 {
					t.Fatalf("result=%+v err=%v creates=%d", result, err, creates)
				}
				return
			}
			if !errors.Is(err, errDuplicateTestCreate) {
				t.Fatalf("an inactive earlier group must not block a new one: result=%+v err=%v", result, err)
			}
		})
	}
}

// #665: include_right must be a JSON boolean; a string "true" used to make
// three views silently. The refusal names the expected type before anything is
// prepared.
func TestThreeViewIncludeRightMustBeABoolean(t *testing.T) {
	executor := &Executor{skillRuntime: &skillrun.Runtime{}, skillReferences: unusedThreeViewReferences{},
		skillTurnStopped: func(context.Context, string, string, string) (bool, error) { return false, nil }}
	runs, err := skillrun.NewService(noExistingThreeViewRun{}, unusedThreeViewRecipes{})
	if err != nil {
		t.Fatal(err)
	}
	executor.skillRuntime.Runs = runs
	command := chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn",
		Call: agents.ToolCall{ID: "call", Name: ToolCreateThreeView, Arguments: []byte(`{"include_right":"true"}`)},
	}
	result, err := executor.createThreeView(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || !strings.Contains(result.Error, "include_right must be a JSON boolean") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for raw, want := range map[string]bool{``: false, `null`: false, `true`: true, `false`: false} {
		if got, err := threeViewIncludeRight([]byte(raw)); err != nil || got != want {
			t.Fatalf("%q: %v %v", raw, got, err)
		}
	}
}

// duplicateGuardExecutor builds the guard over one earlier group of the session
// with the given steps and grant, a stop reader and a turn-state reader.
func duplicateGuardExecutor(t *testing.T, steps []skillrun.Step, grant executiongrant.Grant, stopped, ended bool,
	creates *int) (*Executor, chat.ToolCommand) {
	t.Helper()
	store := earlierThreeViewRuns{creates: creates, earlier: skillrun.Run{ID: "run-earlier",
		RecipeID: threeViewRecipeID, SourceAssetID: "asset-source", IdempotencyKey: "chat-skill:session:other",
		Steps: steps}}
	runs, err := skillrun.NewService(store, duplicateTestRecipes{})
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{
		skillRuntime:    &skillrun.Runtime{Runs: runs, Grants: &executiongrant.Service{Grants: oneGrant{grant: grant}}},
		skillReferences: sourceReference{}, skillOfferings: referenceSelectionOfferings{},
		skillRecipes: map[string]string{threeViewRecipeID: "r1"},
		skillTurnStopped: func(_ context.Context, owner, session, turn string) (bool, error) {
			if turn == "turn-earlier" {
				return stopped, nil
			}
			return false, nil
		},
	}
	executor.SetThreeViewTurnEnded(func(_ context.Context, owner, session, turn string) (bool, error) {
		if owner != "owner" || session != "session" || turn != "turn-earlier" {
			t.Errorf("turn read %s/%s/%s", owner, session, turn)
		}
		return ended, nil
	})
	executor.SetThreeView(catalog.ChatThreeViewSchema{Messages: catalog.ChatThreeViewMessages{
		DuplicateRun: "already running: {run}", ReferenceRequired: "one image"}})
	command := chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn",
		Call:    agents.ToolCall{ID: "call", Name: ToolCreateThreeView, Arguments: []byte(`{}`)},
		Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "selected-image-model"},
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/source.png"}}},
	}
	return executor, command
}

// #665: a card or approval whose turn was stopped can never be approved or
// claimed, so it does not keep the group active; an approval whose turn still
// runs does. A group between two views whose continuation turn still runs is
// active; once that turn ended or stopped it is paused and does not block.
func TestThreeViewDuplicateGuardFollowsTheTurn665(t *testing.T) {
	grantOf := func(status executiongrant.Status, decidedBy string) executiongrant.Grant {
		return executiongrant.Grant{RequestID: "grant-earlier", Identity: executiongrant.Identity{OwnerID: "owner",
			SessionID: "session"}, TriggerTurnID: "turn-earlier", Status: status, Source: executiongrant.AgentPending,
			BudgetEligible: true, DecidedBy: decidedBy}
	}
	waiting := []skillrun.Step{{Phase: skillrun.GrantPending, GrantRequestID: "grant-earlier"}}
	between := []skillrun.Step{
		{Spec: skillrun.StepSpec{Key: "front"}, Phase: skillrun.OutputReady, GrantRequestID: "grant-earlier",
			TaskID: "task-front"},
		{Spec: skillrun.StepSpec{Key: "left"}, Phase: skillrun.Planned},
	}
	for _, tt := range []struct {
		name           string
		steps          []skillrun.Step
		grant          executiongrant.Grant
		stopped, ended bool
		refused        bool
	}{
		{"pending card of a running turn", waiting, grantOf(executiongrant.Pending, ""), false, false, true},
		{"pending card of a stopped turn", waiting, grantOf(executiongrant.Pending, ""), true, false, false},
		{"approval of a running turn", waiting, grantOf(executiongrant.Approved, "policy:auto"), false, false, true},
		{"approval of a stopped turn", waiting, grantOf(executiongrant.Approved, "policy:auto"), true, false, false},
		{"between views, turn running", between, grantOf(executiongrant.Bound, "policy:auto"), false, false, true},
		{"between views, budget turn running", between, grantOf(executiongrant.Bound, "budget:plan"), false, false, true},
		{"between views, turn ended", between, grantOf(executiongrant.Bound, "policy:auto"), false, true, false},
		{"between views, turn stopped", between, grantOf(executiongrant.Bound, "policy:auto"), true, false, false},
		{"between views, strict mode", between, grantOf(executiongrant.Bound, "policy:manual"), false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			creates := 0
			executor, command := duplicateGuardExecutor(t, tt.steps, tt.grant, tt.stopped, tt.ended, &creates)
			if tt.grant.Status == executiongrant.Bound {
				// A finished front view: its Task ended.
				executor.skillRuntime.Generation = finishedTasks{}
			}
			result, err := executor.createThreeView(context.Background(), command, nil)
			if tt.refused {
				if err != nil || result.AgentContent != "already running: run-earlier" || creates != 0 {
					t.Fatalf("result=%+v err=%v creates=%d", result, err, creates)
				}
				return
			}
			if !errors.Is(err, errDuplicateTestCreate) {
				t.Fatalf("an inactive earlier group blocked a new one: result=%+v err=%v", result, err)
			}
		})
	}
}

type finishedTasks struct{ skillrun.GenerationGateway }

func (finishedTasks) Get(_ context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	return generation.Task{ID: id, OwnerID: owner, State: generation.TaskSucceeded}, nil
}

// #665: the per-session lock keeps no entry once nobody holds or waits for
// it, and a waiter gives up when its context ends.
func TestThreeViewSessionLockIsEvictedAndHonoursTheContext665(t *testing.T) {
	executor := &Executor{}
	release, err := executor.lockThreeViewSession(context.Background(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error, 1)
	go func() {
		_, err := executor.lockThreeViewSession(ctx, "owner", "session")
		waited <- err
	}()
	cancel()
	if err := <-waited; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want the context's", err)
	}
	release()
	release() // idempotent
	if size := executor.threeViewSources.size(); size != 0 {
		t.Fatalf("lock entries after release = %d, want 0", size)
	}
	again, err := executor.lockThreeViewSession(context.Background(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	again()
	if size := executor.threeViewSources.size(); size != 0 {
		t.Fatalf("lock entries = %d, want 0", size)
	}
}

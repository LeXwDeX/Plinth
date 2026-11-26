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

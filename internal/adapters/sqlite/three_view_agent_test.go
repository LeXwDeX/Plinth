package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/mcpagent"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/chat/agenttools"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
	"github.com/LeXwDeX/Plinth/internal/threeview"
)

type ownsSessions struct{ repo *Repository }

func (o ownsSessions) OwnsSession(ctx context.Context, owner, session string) (bool, error) {
	_, found, err := o.repo.Session(ctx, owner, session)
	return found, err
}

// The Agent tool starts the front view in its turn. The group the person then
// sees is that same run, and its Schema text says the other views do not start
// on their own.
func TestAgentStartedFrontViewIsTheFirstSlotOfTheGroup(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
	gateway := &skillRecoveryGateway{repo: repo}
	recipe := skillrun.Recipe{ID: "image-three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
		Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "offering-i2i", OfferingRevision: "r7",
		PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}
	runs, err := skillrun.NewService(repo, testRecipeResolver{recipe})
	if err != nil {
		t.Fatal(err)
	}
	service := &executiongrant.Service{Grants: repo, Prepared: repo,
		Policy: testApprovalPolicy(), Modes: sessionModes{repo: repo}}
	runtime, err := skillrun.NewRuntime(runs, gateway, service)
	if err != nil {
		t.Fatal(err)
	}
	schema := catalog.ChatThreeViewSchema{RecipeID: "image-three-view", ModelSourceStepKey: "front",
		Label: "views", MaximumListed: 5, ModelSourceNote: "front only",
		Messages: catalog.ChatThreeViewMessages{
			AgentStarted:  "run {run} {view} {approval} {credits} {task}",
			ApprovalHuman: "by a person", ApprovalPolicy: "by policy",
		}}
	executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(), Generate: unusedSkillGenerator{},
		TaskObservationTimeout: time.Minute, ConsentReconcileContent: "Task observation unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.SetSkillRuntime(runtime, &skillRecoveryReferences{}, &skillRecoveryOfferings{},
		[]catalog.ChatSkillRecipeSchema{{ID: recipe.ID, Revision: recipe.Revision}}, repo.TurnStopRequested); err != nil {
		t.Fatal(err)
	}
	executor.SetThreeView(schema)
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call:    agents.ToolCall{ID: "stable-call", Name: "create_three_view", Arguments: []byte(`{}`)},
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/owned/source-a"}}}, Options: chat.TurnOptions{AIModel: "original-model"}}
	result, err := mcpagent.Wrap(executor, nil).Execute(ctx, command, func(chat.ToolObservation) error { return nil })
	if err != nil || result.TaskID == "" {
		t.Fatalf("agent tool: %+v %v", result, err)
	}
	if !strings.Contains(result.AgentContent, "run skill_") || !strings.Contains(result.AgentContent, "Front") ||
		!strings.Contains(result.AgentContent, "by policy") || !strings.Contains(result.AgentContent, string(result.TaskID)) ||
		strings.Contains(result.AgentContent, "{") {
		t.Fatalf("the Agent text must come from the Schema with every field filled: %q", result.AgentContent)
	}

	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := views.List(ctx, "owner-a", "session-a")
	if err != nil || len(groups) != 1 || len(groups[0].Slots) != 1 {
		t.Fatalf("groups = %+v %v", groups, err)
	}
	front := groups[0].Slots[0]
	if front.Key != "front" || front.TaskID != result.TaskID || front.Status == threeview.NotStarted || front.Startable {
		t.Fatalf("the Agent's task is not the front slot: %+v", front)
	}
	// Starting the view again, as a person might, finds the task and adds none.
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	again, err := views.Advance(ctx, principal, "session-a", groups[0].RunID, "front", threeview.Start, 0)
	if err != nil || again.Slots[0].TaskID != front.TaskID || gateway.submits != 1 {
		t.Fatalf("start of a started view: %+v %v submits=%d", again.Slots, err, gateway.submits)
	}
	// Another owner sees nothing of it, and cannot start it.
	if _, err := views.List(ctx, "owner-b", "session-a"); err == nil {
		t.Fatal("another owner listed the session's groups")
	}
	foreign := executiongrant.Principal{OwnerID: "owner-b", ActorID: "owner-b", Source: executiongrant.HumanBrowser}
	if _, err := views.Advance(ctx, foreign, "session-b", groups[0].RunID, "front", threeview.Start, 0); err == nil {
		t.Fatal("another owner started a view of someone else's run")
	}
	// Only the person at the browser can spend credits.
	agent := executiongrant.Principal{OwnerID: "owner-a", ActorID: "agent", Source: executiongrant.AgentPending}
	if _, err := views.Advance(ctx, agent, "session-a", groups[0].RunID, "front", threeview.Start, 0); err == nil {
		t.Fatal("a non-human principal advanced a view")
	}
}

// A model made from a view of an older group still uses that group's front
// view: SourceOf looks at every group of the session, not only the newest
// ones the card list shows.
func TestModelSourceOfAnOlderGroupIsStillItsFrontView(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
	gateway := &skillRecoveryGateway{repo: repo}
	recipe := skillrun.Recipe{ID: "image-three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
		Key: "front", Label: "Front", Capability: generation.ImageToImage, OfferingID: "offering-i2i", OfferingRevision: "r7",
		PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}
	runs, err := skillrun.NewService(repo, testRecipeResolver{recipe})
	if err != nil {
		t.Fatal(err)
	}
	service := &executiongrant.Service{Grants: repo, Prepared: repo,
		Policy: testApprovalPolicy(), Modes: sessionModes{repo: repo}}
	runtime, err := skillrun.NewRuntime(runs, gateway, service)
	if err != nil {
		t.Fatal(err)
	}
	schema := catalog.ChatThreeViewSchema{RecipeID: "image-three-view", ModelSourceStepKey: "front",
		Label: "views", MaximumListed: 1, ModelSourceNote: "front only"}
	views, err := threeview.New(threeview.Config{Runtime: runtime, Sessions: ownsSessions{repo}, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	create := func(key string) skillrun.Run {
		run, err := runtime.Create(ctx, skillrun.CreateCommand{OwnerID: "owner-a", SessionID: "session-a",
			RecipeID: recipe.ID, ExpectedRevision: recipe.Revision, SelectedOfferingID: "offering-i2i",
			SourceAssetID: "source-a", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	older := create("older-group")
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	started, err := views.Advance(ctx, principal, "session-a", older.ID, "front", threeview.Start, 0)
	if err != nil || started.Slots[0].TaskID == "" {
		t.Fatalf("start the older front view: %+v %v", started, err)
	}
	time.Sleep(5 * time.Millisecond) // the newer run sorts first
	newer := create("newer-group")

	listed, err := views.List(ctx, "owner-a", "session-a")
	if err != nil || len(listed) != 1 || listed[0].RunID != newer.ID {
		t.Fatalf("the list shows only the newest group: %+v %v", listed, err)
	}
	source, member, err := views.SourceOf(ctx, "owner-a", "session-a", started.Slots[0].TaskID)
	if err != nil || !member || source.Slot != "front" {
		t.Fatalf("a view of the older group is still a group view: %+v member=%v err=%v", source, member, err)
	}
	if _, member, err := views.SourceOf(ctx, "owner-b", "session-b", started.Slots[0].TaskID); err != nil || member {
		t.Fatalf("another owner's session must not see the view: member=%v err=%v", member, err)
	}
}

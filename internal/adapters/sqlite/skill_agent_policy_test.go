package sqlite

import (
	"context"
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
)

// runThreeViewUnderPolicy runs the Agent three-view tool once with a recipe step
// of the given capability, under the session approval mode.
func runThreeViewUnderPolicy(t *testing.T, mode string, capability generation.Capability) (chat.ToolResult, *skillRecoveryGateway, []chat.ToolObservation) {
	t.Helper()
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	insertGrantTurn(t, repo, "turn-a", "owner-a", "session-a")
	gateway := &skillRecoveryGateway{repo: repo}
	recipe := skillrun.Recipe{ID: "image-three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
		Key: "front", Label: "Front", Capability: capability, OfferingID: "offering-i2i", OfferingRevision: "r7",
		PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}
	runs, err := skillrun.NewService(repo, testRecipeResolver{recipe})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE go_chat_sessions SET approval_mode=? WHERE id='session-a'`, mode); err != nil {
		t.Fatal(err)
	}
	service := &executiongrant.Service{Grants: repo, Prepared: repo,
		Policy: testApprovalPolicy(), Modes: sessionModes{repo: repo}}
	runtime, err := skillrun.NewRuntime(runs, gateway, service)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := agenttools.New(agenttools.Config{Params: agentparams.NewStore(), Generate: unusedSkillGenerator{},
		TaskObservationTimeout: time.Minute, ConsentReconcileContent: "Task observation unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.SetSkillRuntime(runtime, &skillRecoveryReferences{}, &skillRecoveryOfferings{},
		[]catalog.ChatSkillRecipeSchema{{ID: recipe.ID, Revision: recipe.Revision}}, repo.TurnStopRequested); err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call:    agents.ToolCall{ID: "stable-call", Name: "create_three_view", Arguments: []byte(`{}`)},
		History: []chat.Message{{Role: "user", ImageURLs: []string{"/owned/source-a"}}}, Options: chat.TurnOptions{AIModel: "original-model"}}
	var observed []chat.ToolObservation
	result, err := mcpagent.Wrap(executor, nil).Execute(ctx, command, func(o chat.ToolObservation) error {
		observed = append(observed, o)
		return nil
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return result, gateway, observed
}

// sessionModes reads the persisted session mode the same way the chat service does.
type sessionModes struct{ repo *Repository }

func (m sessionModes) SessionApprovalMode(ctx context.Context, owner, session string) (string, error) {
	s, found, err := m.repo.Session(ctx, owner, session)
	if err != nil || !found {
		return "", err
	}
	return sessionApprovalModeOrManual(s.ApprovalMode), nil
}

func TestSkillRunManualModeImageStepIsApprovedByPolicy(t *testing.T) {
	result, gateway, observed := runThreeViewUnderPolicy(t, "manual", generation.ImageToImage)
	if result.Suspend || gateway.submits != 1 {
		t.Fatalf("manual image step waited: %+v submits=%d", result, gateway.submits)
	}
	if len(observed) == 0 || observed[0].ApprovalSource != "policy" {
		t.Fatalf("approval source missing from observation: %+v", observed)
	}
}

// requestSkillGrant drives the standalone Skill runtime to the grant request
// of a one-step recipe with the given capability.
func requestSkillGrant(t *testing.T, mode string, capability generation.Capability) executiongrant.Grant {
	t.Helper()
	ctx := context.Background()
	repo, _, _ := skillFixture(t)
	recipe := skillrun.Recipe{ID: "three-view", Revision: "r1", Steps: []skillrun.StepSpec{{
		Key: "front", Label: "Front", Capability: capability, OfferingID: "offering-i2i", OfferingRevision: "r7",
		PromptTemplate: "front view", PromptParameter: "prompt", SourceParameter: "reference_assets", OutputRole: "image"}}}
	runs, err := skillrun.NewService(repo, testRecipeResolver{recipe})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE go_chat_sessions SET approval_mode=? WHERE id='session-a'`, mode); err != nil {
		t.Fatal(err)
	}
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: testApprovalPolicy(), Modes: sessionModes{repo: repo}}
	runtime, err := skillrun.NewRuntime(runs, &skillRecoveryGateway{repo: repo}, service)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runtime.Create(ctx, skillCreateCommand())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.PrepareStep(ctx, "owner-a", run.ID, "front"); err != nil {
		t.Fatal(err)
	}
	principal := executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}
	_, grant, err := runtime.RequestGrant(ctx, principal, run.ID, "front")
	if err != nil {
		t.Fatal(err)
	}
	// A repeated request converges to the same decision and never double approves.
	_, again, err := runtime.RequestGrant(ctx, principal, run.ID, "front")
	if err != nil || again.RequestID != grant.RequestID || again.Status != grant.Status {
		t.Fatalf("repeat request: %+v vs %+v err=%v", again, grant, err)
	}
	return grant
}

func TestSkillRunRequestGrantManualModeThreeDWaitsForHuman(t *testing.T) {
	grant := requestSkillGrant(t, "manual", generation.TextTo3D)
	if grant.Status != executiongrant.Pending || grant.DecidedBy != "" {
		t.Fatalf("3D step must wait for a person: %+v", grant)
	}
}

func TestSkillRunRequestGrantManualModeImageStepIsApprovedByPolicy(t *testing.T) {
	grant := requestSkillGrant(t, "manual", generation.ImageToImage)
	if grant.Status != executiongrant.Approved || grant.DecidedBy != "policy:manual" {
		t.Fatalf("image step not approved by policy: %+v", grant)
	}
}

func TestSkillRunRequestGrantAutoModeThreeDIsApprovedByPolicy(t *testing.T) {
	grant := requestSkillGrant(t, "auto", generation.TextTo3D)
	if grant.Status != executiongrant.Approved || grant.DecidedBy != "policy:auto" {
		t.Fatalf("auto 3D step not approved by policy: %+v", grant)
	}
}

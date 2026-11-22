package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #417: the plan quotes the step at the quote it freezes. Before the estimate
// included the default billed components, a Meshy multiview step was budgeted at
// 20 while its prepared input froze 40, so the step left the budget for a person
// with over_budget. Here the plan is proposed and approved the way the product
// does it, and the step then runs inside the budget without a card.
func TestMeshyMultiviewPlanEstimateCoversItsFrozenQuoteAndRunsInBudget(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "agent.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	blobs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	c := qualifiedMultiviewCatalog(t)
	app := c.Application()
	offering, ok := c.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !ok {
		t.Fatal("missing Meshy multiview Offering")
	}
	estimate := budget.EstimateOffering(offering)
	if estimate.Micros != 40000000 || estimate.Unit != "meshy.credit" {
		t.Fatalf("plan estimate = %+v, want 40 meshy credits", estimate)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("meshy.v2", mock.New()); err != nil {
		t.Fatal(err)
	}
	m, err := generation.New(generation.Dependencies{Conversions: generation.NoConversions, Repository: repo, Catalog: c, Providers: registry, Inputs: inputs, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "Owner", Title: "Budget", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner", chat.Message{ID: "user", SessionID: "session", Role: "user", Content: "两张图做个模型", CreatedAt: now},
		chat.Message{ID: "turn", SessionID: "session", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session", Status: "active"},
		chat.Turn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	urls := make([]string, 0, 2)
	for index := range 2 {
		picture := image.NewNRGBA(image.Rect(0, 0, 32, 32))
		picture.SetNRGBA(0, 0, color.NRGBA{R: uint8(30 * (index + 1)), G: 60, B: 90, A: 255})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			t.Fatal(err)
		}
		asset, err := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(encoded.Bytes()), MediaType: "image/png", AssetType: "image", MaxBytes: 20 << 20})
		if err != nil {
			t.Fatal(err)
		}
		urls = append(urls, "/api/v1/assets/"+string(asset.ID))
	}
	var manual, known []string
	for _, value := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(value))
	}
	for _, value := range app.Capabilities {
		known = append(known, string(value.ID))
	}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Budgets: repo,
		Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known),
		Modes:  sessionMode{mode: "budget"}}

	// Propose the plan the way the Agent does: the server quotes the step and
	// the Agent's budget has to cover that quote.
	plans := &chat.Service{}
	plans.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:     app.Chat.ApprovalPolicy.Budget,
		Offerings:  func() []generation.Offering { return []generation.Offering{offering} },
		Store:      grants,
		Usage:      app.Chat.Model3DUsage,
		Production: app.Chat.ModelReview.Inspect.Production,
		Candidates: app.Chat.Candidates,
	})
	proposed, err := plans.ProposeProductionPlan(ctx, chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", FrozenIdempotencyKey: "plan-idem",
		Call: agents.ToolCall{ID: "plan-call", Name: "propose_production_plan", Arguments: []byte(
			`{"title":"Meshy 多视图","steps":[{"capability":"image-to-3d","model":"meshy/meshy-7.1-multiview/image-to-3d","purpose":"主体"}],` +
				`"budgets":[{"unit":"` + estimate.Unit + `","amount":` + estimate.Decimal() + `}]}`)},
	})
	if err != nil || proposed.Error != "" {
		t.Fatalf("propose plan = %+v err=%v", proposed, err)
	}
	var proposedContent map[string]any
	if err := json.Unmarshal([]byte(proposed.AgentContent), &proposedContent); err != nil {
		t.Fatal(err)
	}
	budgetID, _ := proposedContent["budget_id"].(string)
	if budgetID == "" {
		t.Fatalf("no plan id in %s", proposed.AgentContent)
	}
	if _, err := grants.DecideBudget(ctx, executiongrant.Principal{OwnerID: "owner", ActorID: "owner", Source: executiongrant.HumanBrowser},
		budgetID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}

	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	executor, err := generationtool.New(generationtool.Config{Gateway: m, References: uploadedReferences{}, Operations: repo, Grants: grants,
		Chat: app.Chat, TaskStates: app.TaskStates, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", Tool: tool,
		Call:    agents.ToolCall{ID: "call", Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"a stool"}`)},
		Options: chat.TurnOptions{AIModel: "meshy-7.1-multiview", CreativeDomain: "model-3d"},
		History: []chat.Message{{Role: app.Chat.Roles.User, ImageURLs: urls}}}
	result, err := executor.Execute(ctx, command, nil)
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if len(result.AgentContent) > 0 {
		if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
			t.Fatalf("agent content %q: %v", result.AgentContent, err)
		}
	}
	if result.NoSubmission || result.TaskID == "" {
		t.Fatalf("the planned step left the budget instead of running: result=%+v content=%v", result, content)
	}
	op, err := repo.LoadAgentOperation(ctx, "owner", "session", "turn", "call")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := m.GetPreparedInput(ctx, "owner", op.PreparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	frozen := budget.QuoteAmount(prepared.NativeQuote, prepared.EstimatedCredits)
	if frozen != estimate {
		t.Fatalf("frozen quote = %+v, want the plan estimate %+v", frozen, estimate)
	}
	if prepared.NativeQuote == nil || prepared.NativeQuote.TotalAmount != "40" || prepared.NativeQuote.ExtraAmount != "20" {
		t.Fatalf("frozen native quote = %+v, want base 20 + extra 20", prepared.NativeQuote)
	}
	grant, err := repo.GetExecutionGrant(ctx, "owner", op.GrantRequestID)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.ApprovedByBudget() {
		t.Fatalf("the step was not approved by its plan: %+v", grant)
	}
	// The step reserved its frozen quote, and the plan has nothing left.
	summary, found, err := repo.ProductionBudgetSummary(ctx, "owner", "session")
	if err != nil || !found {
		t.Fatalf("plan summary = %+v found=%v err=%v", summary, found, err)
	}
	reserved, _ := summary.Unit("meshy.credit")
	if reserved.Reserved != 40000000 || reserved.Remaining != 0 {
		t.Fatalf("plan unit = %+v, want 40 reserved and 0 left", reserved)
	}
}

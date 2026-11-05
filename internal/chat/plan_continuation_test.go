package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// continuationPlanStore holds the session's current plan and the stored plan
// the continuation reads its proposing turn from.
type continuationPlanStore struct {
	promptBudgetStore
	production budget.ProductionBudget
}

func (store continuationPlanStore) GetBudget(_ context.Context, _ string, budgetID string) (budget.ProductionBudget, error) {
	if budgetID != store.production.ID {
		return budget.ProductionBudget{}, budget.ErrNotFound
	}
	return store.production, nil
}

type continuationRig struct {
	service  *chat.Service
	schema   catalog.ChatSchema
	store    *continuationPlanStore
	session  chat.Session
	proposer chat.Message
	brief    chat.ProductionBrief
}

// newContinuationRig is a session whose Agent turn proposed an approved plan
// pb_1, with a brief and an explicit 3D model frozen by the service.
func newContinuationRig(t *testing.T) continuationRig {
	t.Helper()
	ctx := context.Background()
	shipped, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := configuredChatSchema(t)
	schema.ApprovalPolicy = shipped.Application().Chat.ApprovalPolicy
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, nil)
	store := &continuationPlanStore{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:    schema.ApprovalPolicy.Budget,
		Offerings: func() []generation.Offering { return nil },
		Store:     planStoreView{store},
	})
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetExplicitModel3D(ctx, "owner-a", session.ID, "rodin-gen-2.5"); err != nil {
		t.Fatal(err)
	}
	brief := chat.ProductionBrief{"purpose": json.RawMessage(`"game_prop"`)}
	if _, _, err := repository.ReplaceSessionBrief(ctx, "owner-a", session.ID, nil, brief); err != nil {
		t.Fatal(err)
	}
	proposer, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner-a", SessionID: session.ID, Content: "做一个骑士",
		Options: chat.TurnOptions{CreativeDomain: "model-3d", AIModel: "tripo-3d",
			DomainModels: map[string]string{"model-3d": "tripo-3d"}, Locale: "en"}})
	if err != nil {
		t.Fatal(err)
	}
	store.production = budget.ProductionBudget{ID: "pb_1", OwnerID: "owner-a", SessionID: session.ID,
		MessageID: proposer.ID, State: budget.StateActive, Limits: []budget.Amount{budget.Legacy(40)},
		Plan: budget.Plan{Title: "骑士", Steps: []budget.PlanStep{{Capability: "text-to-3d", Purpose: "主体", Estimate: budget.Legacy(30)}}}}
	store.summary = budget.SummaryOf(store.production, nil)
	store.found = true
	return continuationRig{service: service, schema: schema, store: store, session: session, proposer: proposer, brief: brief}
}

// planStoreView serves the rig's current store state to the service.
type planStoreView struct{ store *continuationPlanStore }

func (view planStoreView) CreateProductionPlan(ctx context.Context, owner, session, key, message, call string,
	plan budget.Plan, limits []budget.Amount) (budget.ProductionBudget, error) {
	return view.store.CreateProductionPlan(ctx, owner, session, key, message, call, plan, limits)
}

func (view planStoreView) BudgetSummary(ctx context.Context, owner, session string) (budget.Summary, bool, error) {
	return view.store.BudgetSummary(ctx, owner, session)
}

func (view planStoreView) PendingBudgets(ctx context.Context, owner, session string) ([]budget.ProductionBudget, error) {
	return view.store.PendingBudgets(ctx, owner, session)
}

func (view planStoreView) GetBudget(ctx context.Context, owner, budgetID string) (budget.ProductionBudget, error) {
	return view.store.GetBudget(ctx, owner, budgetID)
}

// #134: approving a plan starts an ordinary turn that runs it. The service
// freezes the brief and the model lock as for any turn, carries over the
// proposing turn's workspace choices and locale, and marks the turn with the
// plan; only the session's active plan continues.
func TestApprovedPlanStartsAnOrdinaryContinuationTurn(t *testing.T) {
	ctx := context.Background()
	rig := newContinuationRig(t)
	assistant, options, err := rig.service.StartPlanContinuation(ctx, "owner-a", rig.session.ID, "pb_1", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := rig.service.GetTurn(ctx, "owner-a", rig.session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen := turn.Options
	if frozen.PlanContinuation != "pb_1" || options.PlanContinuation != "pb_1" ||
		frozen.CreativeDomain != "model-3d" || frozen.AIModel != "tripo-3d" || frozen.DomainModels["model-3d"] != "tripo-3d" ||
		frozen.Locale != "en" || frozen.ExplicitModel3D != "rodin-gen-2.5" || !reflect.DeepEqual(frozen.ProductionBrief, rig.brief) {
		t.Fatalf("continuation turn options = %+v (scheduled %+v)", frozen, options)
	}
	messages, err := rig.service.ListMessages(ctx, "owner-a", rig.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(rig.schema.ApprovalPolicy.Budget.Continuation.Message, "{title}", "骑士")
	if len(messages) < 2 || messages[len(messages)-2].Content != want || messages[len(messages)-1].ID != assistant.ID {
		t.Fatalf("continuation messages = %+v, want the go-ahead %q", messages, want)
	}

	// Only the session's active plan continues.
	if _, _, err := rig.service.StartPlanContinuation(ctx, "owner-a", rig.session.ID, "pb_other", ""); !errors.Is(err, chat.ErrPlanNotActive) {
		t.Fatalf("another plan: %v", err)
	}
	rig.store.summary.State = budget.StateExhausted
	if _, _, err := rig.service.StartPlanContinuation(ctx, "owner-a", rig.session.ID, "pb_1", ""); !errors.Is(err, chat.ErrPlanNotActive) {
		t.Fatalf("exhausted plan: %v", err)
	}
	if _, _, err := (&chat.Service{}).StartPlanContinuation(ctx, "owner-a", rig.session.ID, "pb_1", ""); !errors.Is(err, chat.ErrProductionPlanUnavailable) {
		t.Fatalf("deployment without plans: %v", err)
	}
}

// The continuation's request carries the Schema's continuation text after the
// go-ahead, so the Agent runs the plan instead of waiting to be told; an
// ordinary turn never does, whatever the caller puts in its options.
func TestPlanContinuationTurnTellsTheAgentToRunThePlan(t *testing.T) {
	ctx := context.Background()
	rig := newContinuationRig(t)
	agentTemplate := rig.schema.ApprovalPolicy.Budget.Continuation.AgentTemplate
	lastUser := func(t *testing.T, assistantID string, options chat.TurnOptions) string {
		t.Helper()
		agent := &scriptedAgent{responses: []agents.Completion{{Content: "ok"}}}
		orchestrator, err := chat.NewOrchestrator(chat.OrchestratorConfig{
			Service: rig.service, Schema: rig.schema, Model: "m", Agent: agent, Tools: &scopeToolExecutor{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Process(ctx, chat.ProcessTurn{OwnerID: "owner-a", SessionID: rig.session.ID,
			AssistantMessageID: assistantID, Options: options}); err != nil {
			t.Fatal(err)
		}
		if len(agent.requests) == 0 {
			t.Fatal("the Agent was never called")
		}
		request := agent.requests[0]
		for index := len(request.Messages) - 1; index >= 0; index-- {
			message := request.Messages[index]
			if message.Role != rig.schema.Roles.User {
				continue
			}
			text := message.Content
			for _, part := range message.Parts {
				text += part.Text
			}
			return text
		}
		t.Fatal("no user message in the request")
		return ""
	}

	// The proposing turn is ordinary, even when the caller claims a plan.
	if text := lastUser(t, rig.proposer.ID, chat.TurnOptions{PlanContinuation: "pb_1"}); strings.Contains(text, agentTemplate) {
		t.Fatalf("an ordinary turn read the continuation text: %q", text)
	}
	assistant, options, err := rig.service.StartPlanContinuation(ctx, "owner-a", rig.session.ID, "pb_1", "")
	if err != nil {
		t.Fatal(err)
	}
	// The scheduler may hand Process only part of the options; the mark is the turn's.
	options.PlanContinuation = ""
	text := lastUser(t, assistant.ID, options)
	if !strings.Contains(text, "骑士") || !strings.Contains(text, agentTemplate) {
		t.Fatalf("continuation request user message = %q", text)
	}
}

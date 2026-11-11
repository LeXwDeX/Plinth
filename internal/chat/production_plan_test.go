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
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// recordingProductionPlans is the plan store of the proposal tool. It keeps
// exactly what the tool asked the server to persist.
type recordingProductionPlans struct {
	created   []budget.ProductionBudget
	keys      []string
	plans     []budget.Plan
	limits    [][]budget.Amount
	ownerIDs  []string
	sessions  []string
	messageID []string
	callIDs   []string
	state     string
	err       error
}

func (store *recordingProductionPlans) CreateProductionPlan(_ context.Context, ownerID, sessionID, requestKey,
	messageID, callID string, plan budget.Plan, limits []budget.Amount) (budget.ProductionBudget, error) {
	if store.err != nil {
		return budget.ProductionBudget{}, store.err
	}
	state := store.state
	if state == "" {
		state = budget.StatePending
	}
	stored := budget.ProductionBudget{ID: "pb_" + requestKey, OwnerID: ownerID, SessionID: sessionID,
		Plan: plan, Limits: limits, State: state, MessageID: messageID, CallID: callID}
	store.created = append(store.created, stored)
	store.keys = append(store.keys, requestKey)
	store.plans = append(store.plans, plan)
	store.limits = append(store.limits, limits)
	store.ownerIDs = append(store.ownerIDs, ownerID)
	store.sessions = append(store.sessions, sessionID)
	store.messageID = append(store.messageID, messageID)
	store.callIDs = append(store.callIDs, callID)
	return stored, nil
}

func (store *recordingProductionPlans) BudgetSummary(context.Context, string, string) (budget.Summary, bool, error) {
	return budget.Summary{}, false, nil
}

func (store *recordingProductionPlans) PendingBudgets(context.Context, string, string) ([]budget.ProductionBudget, error) {
	return nil, nil
}

func (store *recordingProductionPlans) GetBudget(_ context.Context, _ string, budgetID string) (budget.ProductionBudget, error) {
	for _, created := range store.created {
		if created.ID == budgetID {
			return created, nil
		}
	}
	return budget.ProductionBudget{}, budget.ErrNotFound
}

func planOfferings() []generation.Offering {
	return []generation.Offering{
		{ID: "image-default", Revision: "v1", Capability: "text-to-image", Enabled: true, Default: true, EstimatedCredits: 5},
		{ID: "image-other", Revision: "v1", Capability: "text-to-image", Enabled: true, EstimatedCredits: 9},
		{ID: "three-default", Revision: "v1", Capability: "image-to-3d", Enabled: true, Default: true, EstimatedCredits: 7},
		{ID: "three-pinned", Revision: "v1", Capability: "image-to-3d", Enabled: true, EstimatedCredits: 20},
		{ID: "retexture-default", Revision: "v1", Capability: "retexture", Enabled: true, Default: true, EstimatedCredits: 4},
		{ID: "remesh-default", Revision: "v1", Capability: "remesh", Domain: generation.DomainProcessing, Enabled: true, Default: true, EstimatedCredits: 6},
		{ID: "resize-default", Revision: "v1", Capability: "resize", Domain: generation.DomainProcessing, Enabled: true, Default: true, EstimatedCredits: 3},
		{ID: "disabled", Revision: "v1", Capability: "image-to-3d", Enabled: false, EstimatedCredits: 1},
	}
}

func proposalService(t *testing.T, store chat.ProductionPlanStore, budgetSchema catalog.ChatApprovalBudgetSchema) *chat.Service {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	service := &chat.Service{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:      budgetSchema,
		Offerings:   planOfferings,
		Store:       store,
		Checkpoints: []string{"self_check"},
		Usage:       application.Chat.Model3DUsage,
		Production:  application.Chat.ModelReview.Inspect.Production,
		Candidates:  application.Chat.Candidates,
	})
	return service
}

func shippedBudgetSchema(t *testing.T) catalog.ChatApprovalBudgetSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat.ApprovalPolicy.Budget
}

func planCommand(arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		AssistantMessageID:   "turn-a",
		FrozenIdempotencyKey: "idem-a",
		Call:                 agents.ToolCall{ID: "call-a", Name: "propose_production_plan", Arguments: []byte(arguments)},
	}
}

// The proposal quotes every step from the runtime Catalog and ignores the
// Agent's own estimate. The stored plan, the card data and the tool result all
// carry the server's number.
func TestProposeProductionPlanQuotesEveryStepServerSide(t *testing.T) {
	store := &recordingProductionPlans{}
	budgetSchema := shippedBudgetSchema(t)
	service := proposalService(t, store, budgetSchema)
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{
		"title":"  从草图做骑士  ",
		"steps":[
			{"capability":"text-to-image","purpose":"生成概念图","estimated_credits":1},
			{"capability":"image-to-3d","model":"three-pinned","purpose":"生成低模","estimated_credits":2}
		],
		"budgets":[{"unit":"legacy.credits","amount":40}],
		"correction_capabilities":["retexture","retexture"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.Error != "" || result.Suspend {
		t.Fatalf("proposal result = %+v", result)
	}
	if len(store.created) != 1 {
		t.Fatalf("stored plans = %d", len(store.created))
	}
	plan := store.plans[0]
	if totals := plan.EstimatedTotals(); plan.Title != "从草图做骑士" || len(totals) != 1 || totals[0] != budget.Legacy(25) || len(plan.Steps) != 2 {
		t.Fatalf("stored plan = %+v", plan)
	}
	if plan.Steps[0].Capability != "text-to-image" || plan.Steps[0].OfferingID != "" || plan.Steps[0].Estimate != budget.Legacy(5) ||
		plan.Steps[1].Capability != "image-to-3d" || plan.Steps[1].OfferingID != "three-pinned" || plan.Steps[1].Estimate != budget.Legacy(20) {
		t.Fatalf("server quotes = %+v", plan.Steps)
	}
	if len(plan.CorrectionCapabilities) != 1 || plan.CorrectionCapabilities[0] != "retexture" {
		t.Fatalf("correction capabilities = %v", plan.CorrectionCapabilities)
	}
	if len(store.limits[0]) != 1 || store.limits[0][0] != budget.Legacy(40) || store.ownerIDs[0] != "owner-a" || store.sessions[0] != "session-a" ||
		store.messageID[0] != "turn-a" || store.callIDs[0] != "call-a" || store.keys[0] == "" {
		t.Fatalf("stored identity = %+v", store)
	}

	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatalf("agent content %q: %v", result.AgentContent, err)
	}
	legacy := map[string]any{"unit": budget.LegacyUnit, "unit_label": budget.LegacyUnit, "amount": "40"}
	if content["success"] != true || content["title"] != "从草图做骑士" ||
		content["state"] != budget.StatePending || !reflect.DeepEqual(content["budgets"], []any{legacy}) ||
		content["budget_id"] != store.created[0].ID {
		t.Fatalf("proposal content = %v", content)
	}
	legacy["amount"] = "25"
	if !reflect.DeepEqual(content["estimated_totals"], []any{legacy}) || content["limit_credits"] != nil || content["estimated_credits"] != nil {
		t.Fatalf("proposal totals = %v", content)
	}
	steps, ok := content["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("proposal steps = %v", content["steps"])
	}
	first := steps[0].(map[string]any)
	second := steps[1].(map[string]any)
	if first["estimate"].(map[string]any)["amount"] != "5" || second["estimate"].(map[string]any)["amount"] != "20" ||
		second["estimate"].(map[string]any)["unit"] != budget.LegacyUnit || second["offering_id"] != "three-pinned" {
		t.Fatalf("proposal step quotes = %v / %v", first, second)
	}
	if content["message"] != budgetSchema.ToolMessages.Proposed {
		t.Fatalf("proposal message = %q, want the Schema's %q", content["message"], budgetSchema.ToolMessages.Proposed)
	}
}

// #246: a step may declare a Schema checkpoint. It is stored on the step,
// shown on the plan card and repeated by the plan's prompt line.
func TestProposeProductionPlanCarriesStepCheckpoints(t *testing.T) {
	store := &recordingProductionPlans{}
	budgetSchema := shippedBudgetSchema(t)
	service := proposalService(t, store, budgetSchema)
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{
		"title":"检查点",
		"steps":[
			{"capability":"text-to-image","purpose":"生成概念图","check":"self_check"},
			{"capability":"image-to-3d","purpose":"生成低模"}
		],
		"budgets":[{"unit":"legacy.credits","amount":12}]
	}`))
	if err != nil || result.Error != "" || len(store.created) != 1 {
		t.Fatalf("proposal = %+v err=%v stored=%d", result, err, len(store.created))
	}
	plan := store.plans[0]
	if plan.Steps[0].Check != "self_check" || plan.Steps[1].Check != "" {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	steps := content["steps"].([]any)
	if steps[0].(map[string]any)["check"] != "self_check" || steps[1].(map[string]any)["check"] != nil {
		t.Fatalf("content steps = %v", steps)
	}
	format := budget.AmountFormat{LegacyLabel: "积分"}
	if line := plan.PromptLine(format); !strings.Contains(line, "检查点 self_check") {
		t.Fatalf("prompt line = %q", line)
	}
	// A step stored before #246 (no check) still decodes and re-encodes
	// without the member.
	encoded, err := json.Marshal(plan.Steps[1])
	if err != nil || strings.Contains(string(encoded), `"check"`) {
		t.Fatalf("encoded step = %s err=%v", encoded, err)
	}
}

// A metered model the server cannot quote cannot be covered by a total budget:
// the proposal is refused instead of storing a zero-estimate step. A free model
// remains plannable at zero.
func TestProposeProductionPlanRefusesUnpricedMeteredSteps(t *testing.T) {
	store := &recordingProductionPlans{}
	service := &chat.Service{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget: shippedBudgetSchema(t),
		Offerings: func() []generation.Offering {
			return []generation.Offering{
				{ID: "free", Revision: "v1", Capability: "text-to-image", Enabled: true, Default: true},
				{ID: "metered-unpriced", Revision: "v1", Capability: "text-to-image", Enabled: true, CreditsMetered: true},
			}
		},
		Store: store,
	})
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(
		`{"title":"p","steps":[{"capability":"text-to-image","model":"metered-unpriced","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`))
	want := "step text-to-image has no server credit estimate and cannot be budgeted"
	if err != nil || result.Error != want || len(store.created) != 0 {
		t.Fatalf("unpriced metered step = %+v err=%v stored=%d", result, err, len(store.created))
	}
	result, err = service.ProposeProductionPlan(context.Background(), planCommand(
		`{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`))
	if err != nil || result.Error != "" || len(store.created) != 1 || store.plans[0].Steps[0].Estimate.Micros != 0 {
		t.Fatalf("free step = %+v err=%v stored=%d", result, err, len(store.created))
	}
}

// Every refusal leaves no stored plan and tells the Agent why in the same
// object shape.
func TestProposeProductionPlanRefusesInvalidPlansWithoutStoringThem(t *testing.T) {
	base := shippedBudgetSchema(t)
	for name, testCase := range map[string]struct {
		arguments string
		mutate    func(*catalog.ChatApprovalBudgetSchema)
		want      string
	}{
		"budget below the server estimate": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"},{"capability":"text-to-image","purpose":"b"}],"budgets":[{"unit":"legacy.credits","amount":9}]}`,
			want:      "budgets must give every unit the steps are quoted in at least the server estimate of that unit (10 legacy.credits); missing or too low: 10 legacy.credits. Units are never added or converted",
		},
		"budget above the Schema maximum": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":31}]}`,
			mutate:    func(schema *catalog.ChatApprovalBudgetSchema) { schema.MaxCredits = 30 },
			want:      "budget for unit legacy.credits exceeds the Schema maximum of 30",
		},
		"missing budget": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}]}`,
			want:      "budgets is required: one amount per credit unit",
		},
		"unknown correction capability": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}],"correction_capabilities":["paint"]}`,
			want:      "correction capability paint is unknown",
		},
		"no steps": {
			arguments: `{"title":"p","steps":[],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "steps must be between 1 and 12",
		},
		"too many steps": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"},{"capability":"text-to-image","purpose":"b"},{"capability":"text-to-image","purpose":"c"}],"budgets":[{"unit":"legacy.credits","amount":15}]}`,
			mutate:    func(schema *catalog.ChatApprovalBudgetSchema) { schema.MaxSteps = 2 },
			want:      "steps must be between 1 and 2",
		},
		"step without purpose": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "every step needs a purpose",
		},
		"step without capability": {
			arguments: `{"title":"p","steps":[{"purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "every step needs a capability",
		},
		"offering not available": {
			arguments: `{"title":"p","steps":[{"capability":"image-to-3d","model":"disabled","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "no available offering for capability image-to-3d",
		},
		"capability without an available offering": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-3d","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "no available offering for capability text-to-3d",
		},
		"empty title": {
			arguments: `{"title":"   ","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "title is required",
		},
		"title too long": {
			arguments: `{"title":"knight","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			mutate:    func(schema *catalog.ChatApprovalBudgetSchema) { schema.MaxTitleCharacters = 5 },
			want:      "title is too long",
		},
		"purpose too long": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"concept"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			mutate:    func(schema *catalog.ChatApprovalBudgetSchema) { schema.MaxPurposeCharacters = 3 },
			want:      "step purpose is too long",
		},
		"unknown checkpoint": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a","check":"delivery"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`,
			want:      "step checkpoint delivery is not declared",
		},
		"unknown field": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}],"secret":true}`,
			want:      "arguments must match the declared plan object",
		},
		"not an object": {
			arguments: `[]`,
			want:      "arguments must match the declared plan object",
		},
		"trailing json": {
			arguments: `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]} {}`,
			want:      "arguments must match the declared plan object",
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingProductionPlans{}
			schema := base
			if testCase.mutate != nil {
				testCase.mutate(&schema)
			}
			service := proposalService(t, store, schema)
			result, err := service.ProposeProductionPlan(context.Background(), planCommand(testCase.arguments))
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || result.Error != testCase.want || len(store.created) != 0 {
				t.Fatalf("refusal = %+v stored=%d want=%q", result, len(store.created), testCase.want)
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil ||
				content["success"] != false || content["error"] != testCase.want {
				t.Fatalf("refusal content = %q err=%v", result.AgentContent, err)
			}
		})
	}
}

// The store refusal reasons the transport can act on keep their own wording.
func TestProposeProductionPlanReportsStoreRefusals(t *testing.T) {
	for name, testCase := range map[string]struct {
		err  error
		want string
	}{
		"invalid plan":  {executiongrant.ErrInvalid, "the plan is invalid"},
		"replayed call": {executiongrant.ErrConflict, "a different plan was already proposed for this tool call"},
		"missing chat":  {chat.ErrSessionNotFound, "session not found"},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingProductionPlans{err: testCase.err}
			service := proposalService(t, store, shippedBudgetSchema(t))
			result, err := service.ProposeProductionPlan(context.Background(), planCommand(
				`{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`))
			if err != nil || result.Error != testCase.want {
				t.Fatalf("store refusal = %+v err=%v want=%q", result, err, testCase.want)
			}
		})
	}
}

// A repeated call keeps its identity, so the store can return the same plan
// instead of opening a second card; a different call is a different plan.
func TestProposeProductionPlanKeepsTheToolCallIdentity(t *testing.T) {
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	arguments := `{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`
	first := planCommand(arguments)
	second := planCommand(arguments)
	second.Call.ID = "call-b"
	for _, command := range []chat.ToolCommand{first, first, second} {
		if _, err := service.ProposeProductionPlan(context.Background(), command); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.keys) != 3 || store.keys[0] == "" || store.keys[0] != store.keys[1] || store.keys[0] == store.keys[2] {
		t.Fatalf("request keys = %v", store.keys)
	}
	if store.callIDs[0] != "call-a" || store.callIDs[2] != "call-b" {
		t.Fatalf("call ids = %v", store.callIDs)
	}
}

// A historical Catalog without the budget configuration installs no tool, and
// the tool does not panic when called anyway.
func TestProposeProductionPlanIsUnavailableWithoutTheSchema(t *testing.T) {
	service := &chat.Service{}
	service.SetProductionPlans(chat.ProductionPlanPolicy{
		Budget:    catalog.ChatApprovalBudgetSchema{},
		Offerings: planOfferings,
		Store:     &recordingProductionPlans{},
	})
	if service.ProductionPlanEnabled() {
		t.Fatal("a plan tool without max_credits was enabled")
	}
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{}`))
	if err != nil || result.Error != "production plans are unavailable" || !result.NoSubmission {
		t.Fatalf("unavailable tool = %+v err=%v", result, err)
	}
}

// Store failures that are not refusals reach the caller as failures, never as
// a successful proposal.
func TestProposeProductionPlanPropagatesStorageFailure(t *testing.T) {
	store := &recordingProductionPlans{err: errors.New("disk lost")}
	service := proposalService(t, store, shippedBudgetSchema(t))
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(
		`{"title":"p","steps":[{"capability":"text-to-image","purpose":"a"}],"budgets":[{"unit":"legacy.credits","amount":5}]}`))
	if err == nil || !result.NoSubmission || result.AgentContent != "" {
		t.Fatalf("storage failure = %+v err=%v", result, err)
	}
}

// #242: a step may declare parallel candidates. The server expands them into
// candidate_group/candidate_index steps, quotes every one, and counts them all
// in the uncovered totals and the approved limit.
func TestProposeProductionPlanExpandsCandidateGroups(t *testing.T) {
	store := &recordingProductionPlans{}
	service := proposalService(t, store, shippedBudgetSchema(t))
	result, err := service.ProposeProductionPlan(context.Background(), planCommand(`{
		"title":"两个候选",
		"steps":[
			{"capability":"image-to-3d","purpose":"候选方向","candidates":{"source":"seed","count":2}}
		],
		"budgets":[{"unit":"legacy.credits","amount":20}]
	}`))
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("proposal = %+v err=%v", result, err)
	}
	plan := store.plans[0]
	if len(plan.Steps) != 2 {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	if plan.Steps[0].CandidateGroup != 1 || plan.Steps[0].CandidateIndex != 1 ||
		plan.Steps[1].CandidateGroup != 1 || plan.Steps[1].CandidateIndex != 2 {
		t.Fatalf("candidate identity = %+v", plan.Steps)
	}
	if totals := plan.EstimatedTotals(); len(totals) != 1 || totals[0] != budget.Legacy(14) {
		t.Fatalf("totals = %+v, want two default quotes", totals)
	}

	// One model per candidate: every candidate keeps its own server quote.
	store = &recordingProductionPlans{}
	service = proposalService(t, store, shippedBudgetSchema(t))
	result, err = service.ProposeProductionPlan(context.Background(), planCommand(`{
		"title":"不同模型",
		"steps":[
			{"capability":"image-to-3d","purpose":"候选方向","candidates":{"source":"provider","models":["three-default","three-pinned"]}}
		],
		"budgets":[{"unit":"legacy.credits","amount":40}]
	}`))
	if err != nil || result.Error != "" {
		t.Fatalf("proposal = %+v err=%v", result, err)
	}
	plan = store.plans[0]
	if len(plan.Steps) != 2 || plan.Steps[0].OfferingID != "three-default" || plan.Steps[0].Estimate != budget.Legacy(7) ||
		plan.Steps[1].OfferingID != "three-pinned" || plan.Steps[1].Estimate != budget.Legacy(20) {
		t.Fatalf("per-candidate quotes = %+v", plan.Steps)
	}
}

func TestProposeProductionPlanRefusesUndeclaredCandidates(t *testing.T) {
	cases := []string{
		`{"title":"坏来源","steps":[{"capability":"image-to-3d","purpose":"x","candidates":{"source":"magic","count":2}}],"budgets":[{"unit":"legacy.credits","amount":40}]}`,
		`{"title":"两个都给","steps":[{"capability":"image-to-3d","purpose":"x","candidates":{"source":"seed","count":2,"models":["three-default","three-pinned"]}}],"budgets":[{"unit":"legacy.credits","amount":40}]}`,
		`{"title":"太多","steps":[{"capability":"image-to-3d","purpose":"x","candidates":{"source":"seed","count":5}}],"budgets":[{"unit":"legacy.credits","amount":40}]}`,
	}
	for _, arguments := range cases {
		store := &recordingProductionPlans{}
		service := proposalService(t, store, shippedBudgetSchema(t))
		result, err := service.ProposeProductionPlan(context.Background(), planCommand(arguments))
		if err != nil || result.Error == "" || len(store.created) != 0 {
			t.Fatalf("arguments %s accepted: %+v err=%v", arguments, result, err)
		}
	}
}

package generationtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

type agentPlanCatalog struct{ *catalog.Catalog }

func (c agentPlanCatalog) OfferingAvailable(id generation.OfferingID) bool {
	offering, ok := c.Get(id)
	return ok && offering.Enabled
}

// The plan worker uses a contract fake of the task kernel. Approval decisions,
// frozen inputs and plans use real SQLite stores and the real grant service.
type agentPlanGateway struct {
	*processingGateway
	accepted      map[string]generation.Task
	commands      []generation.SubmitPreparedCommand
	preparedCount int
	policyActors  []string
}

func (g *agentPlanGateway) LookupByIdempotency(_ context.Context, owner generation.OwnerID, key string) (generation.Task, error) {
	if task, ok := g.accepted[key]; ok && task.OwnerID == owner {
		return task, nil
	}
	return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
}
func (g *agentPlanGateway) VerifyPreparedTaskReplay(ctx context.Context, command generation.SubmitPreparedCommand) (generation.Task, error) {
	return g.LookupByIdempotency(ctx, command.OwnerID, command.IdempotencyKey)
}
func (g *agentPlanGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.preparedCount++
	offering, _ := g.product.Get(cmd.OfferingID)
	prepared := generation.PreparedInput{ID: generation.PreparedInputID(fmt.Sprintf("plan-input-%d", g.preparedCount)),
		OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, Capability: cmd.Capability,
		OfferingID: cmd.OfferingID, OfferingRevision: offering.Revision, NormalizedInput: cmd.Input,
		InputHash: fmt.Sprintf("hash-%d", g.preparedCount), SchemaVersion: "1", CreatedAt: time.Now().UTC()}
	return prepared, g.repo.CreatePreparedInput(ctx, prepared)
}
func (g *agentPlanGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	grant, err := g.repo.GetExecutionGrant(ctx, string(cmd.OwnerID), cmd.AuthorizationRef)
	if err != nil || grant.Status != executiongrant.Approved || grant.PreparedInputID != string(cmd.PreparedInputID) ||
		grant.OperationID != cmd.OperationID || grant.PlanStepID != cmd.PlanStepID || grant.IdempotencyKey != cmd.IdempotencyKey {
		return generation.Accepted{}, &generation.Error{Code: generation.ErrForbidden}
	}
	prepared, err := g.repo.GetPreparedInput(ctx, cmd.OwnerID, cmd.PreparedInputID)
	if err != nil {
		return generation.Accepted{}, err
	}
	offering, _ := g.product.Get(prepared.OfferingID)
	id := generation.TaskID(fmt.Sprintf("plan-task-%d", len(g.commands)+1))
	task := modelTask(string(id), string(cmd.OwnerID), cmd.SessionID, offering.ProviderID, offering.Capability, "glb")
	task.Offering, task.Input = offering, prepared.NormalizedInput
	g.accepted[cmd.IdempotencyKey], g.tasks[id] = task, task
	g.commands = append(g.commands, cmd)
	g.policyActors = append(g.policyActors, grant.DecidedBy)
	return generation.Accepted{TaskID: id}, nil
}

type agentPlanHarness struct {
	*processingHarness
	plans   *processing.Service
	gateway *agentPlanGateway
	grants  *executiongrant.Service
	store   *agentPlanSweepStore
}

// Stop on a persisted transition (or an empty sweep), never in the middle of
// SQLite work because a short wall-clock deadline happened to elapse.
type agentPlanSweepStore struct {
	processing.Store
	cancel context.CancelFunc
}

func (s *agentPlanSweepStore) ListActiveProcessingPlans(ctx context.Context, after string, limit int) ([]processing.Plan, error) {
	plans, err := s.Store.ListActiveProcessingPlans(ctx, after, limit)
	if s.cancel != nil && (err != nil || len(plans) == 0) {
		s.cancel()
	}
	return plans, err
}

func (s *agentPlanSweepStore) SaveProcessingPlan(ctx context.Context, plan processing.Plan) error {
	before, err := s.GetProcessingPlan(ctx, plan.OwnerID, plan.ID)
	if err != nil {
		return err
	}
	if err := s.Store.SaveProcessingPlan(ctx, plan); err != nil {
		return err
	}
	if s.cancel != nil {
		for i, step := range plan.Steps {
			if step.State != before.Steps[i].State || step.TaskID != before.Steps[i].TaskID {
				s.cancel()
				break
			}
		}
	}
	return nil
}

func newAgentPlanHarness(t *testing.T, mode string, requireHuman bool) *agentPlanHarness {
	h := newProcessingHarness(t, mode, func(h *processingHarness) []generation.Task {
		return []generation.Task{modelTask("model-1", "owner-a", "session-a", h.meshy, generation.TextTo3D, "glb", "fbx"),
			modelTask("other-model", "owner-b", "session-a", h.meshy, generation.TextTo3D, "glb"),
			modelTask("other-session", "owner-a", "session-b", h.meshy, generation.TextTo3D, "glb")}
	})
	gateway := &agentPlanGateway{processingGateway: h.gateway, accepted: map[string]generation.Task{}}
	manual := h.manual
	if requireHuman {
		manual = append(append([]string(nil), manual...), "remesh", "rigging", "animation")
	}
	grants := &executiongrant.Service{Grants: h.gateway.repo, Prepared: h.gateway.repo,
		Policy: approvalpolicy.New("manual", []string{"manual", "auto"}, manual, h.known), Modes: sessionMode{mode: mode}}
	c := agentPlanCatalog{h.gateway.product}
	store := &agentPlanSweepStore{Store: h.gateway.repo}
	plans, err := processing.New(store, c, gateway, nil)
	if err != nil {
		t.Fatal(err)
	}
	plans.WithGrants(grants)
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Operations: h.gateway.repo,
		Grants: grants, Processing: plans, PlanCatalog: c, Chat: h.app.Chat,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	return &agentPlanHarness{h, plans, gateway, grants, store}
}

const agentPlanArguments = `{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{"target_polycount":20000}},{"capability":"rigging","parameters":{}},{"capability":"animation","parameters":{"action_id":10}}]}`

func (h *agentPlanHarness) command(arguments string) chat.ToolCommand {
	return chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "plan-call", Name: "plan_model_processing", Arguments: []byte(arguments)}, Tool: h.tool("plan_model_processing")}
}
func (h *agentPlanHarness) propose(arguments string) (chat.ToolResult, processing.Plan) {
	h.t.Helper()
	result, err := h.executor.Execute(context.Background(), h.command(arguments), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if result.Error != "" {
		h.t.Fatalf("proposal failed: %+v", result)
	}
	id := h.content(result)["plan_id"].(string)
	plan, err := h.plans.Get(context.Background(), "owner-a", id)
	if err != nil {
		h.t.Fatal(err)
	}
	return result, plan
}

// Run advances immediately. The store ends the sweep at its persisted boundary;
// the deadline only detects a broken fixture, not normal sweep completion.
func (h *agentPlanHarness) tick() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.store.cancel = cancel
	defer func() { h.store.cancel = nil }()
	if err := h.plans.Run(ctx); err != nil {
		h.t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		h.t.Fatal("plan sweep did not reach a persisted transition or an empty page")
	}
}
func (h *agentPlanHarness) get(id string) processing.Plan {
	h.t.Helper()
	plan, err := h.plans.Get(context.Background(), "owner-a", id)
	if err != nil {
		h.t.Fatal(err)
	}
	return plan
}

func TestAgentPlanCreationReplayAndPanelList(t *testing.T) {
	h := newAgentPlanHarness(t, "manual", true)
	result, plan := h.propose(agentPlanArguments)
	if plan.State != processing.Pending || len(plan.Steps) != 3 || plan.SourceAssetID != "model-1-glb" || len(h.gateway.commands) != 0 || h.gateway.preparedCount != 0 {
		t.Fatalf("creation must only persist proposal: %+v", plan)
	}
	if strings.Contains(result.AgentContent, "UPSTREAM-SECRET") || result.TaskID != "" || !result.NoSubmission {
		t.Fatalf("result=%+v", result)
	}
	list, err := h.plans.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(list) != 1 || list[0].ID != plan.ID {
		t.Fatalf("panel list=%v err=%v", list, err)
	}
	if total := h.content(result)["estimated_credits"].(float64); total <= 0 {
		t.Fatalf("estimate=%v", total)
	}
	for _, step := range plan.Steps {
		var parameters map[string]any
		if json.Unmarshal(step.Input, &parameters) != nil || parameters == nil || parameters["source_task"] != nil || parameters["source_asset"] != nil {
			t.Fatalf("proposal must carry normalized parameters without preview sources: %s", step.Input)
		}
	}
	var remesh map[string]any
	json.Unmarshal(plan.Steps[0].Input, &remesh)
	if remesh["target_formats"] == nil || remesh["topology"] == nil {
		t.Fatalf("Catalog defaults were not frozen: %s", plan.Steps[0].Input)
	}
	// Replays return the persisted proposal even after its source/catalog goes
	// away, rather than rebinding and creating another chain.
	delete(h.gateway.tasks, "model-1")
	_, replay := h.propose(agentPlanArguments)
	if replay.ID != plan.ID || replay.Version != plan.Version {
		t.Fatalf("replay=%+v", replay)
	}
	command := h.command(agentPlanArguments)
	command.AssistantMessageID = "next-turn"
	if generationtool.ProcessingPlanKey(command) == generationtool.ProcessingPlanKey(h.command(agentPlanArguments)) {
		t.Fatal("turn identities collided")
	}
}

func TestAgentPlanValidatesEntireProposalBeforeCreatingIt(t *testing.T) {
	cases := []string{
		`[]`, `null`, `{"source_task_id":7,"steps":[]}`,
		`{"source_task_id":"model-1","steps":null}`,
		`{"source_task_id":"model-1","steps":[{"capability":"explode","parameters":{}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"animation","parameters":{"action_id":10}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"rigging","parameters":{}},{"capability":"animation","parameters":{}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{}},{"capability":"rigging","parameters":{"action_id":10}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{"target_polycount":-1}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{"source_task_id":"other-model"}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{"unknown":true}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","offering_id":"not-registered","parameters":{}}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":{},"grant":"approved"}]}`,
		`{"source_task_id":"model-1","steps":[{"capability":"remesh","parameters":[]}],"owner_id":"owner-b"}`,
		`{"source_task_id":"other-model","steps":[{"capability":"remesh","parameters":{}}]}`,
		`{"source_task_id":"other-session","steps":[{"capability":"remesh","parameters":{}}]}`,
		`{"source_task_id":"model-1","source_asset_id":"other-model-glb","steps":[{"capability":"remesh","parameters":{}}]}`,
	}
	for _, arguments := range cases {
		t.Run(arguments, func(t *testing.T) {
			h := newAgentPlanHarness(t, "auto", false)
			result, err := h.executor.Execute(context.Background(), h.command(arguments), nil)
			if err != nil || result.Error == "" || !result.NoSubmission {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			list, err := h.plans.List(context.Background(), "owner-a", "session-a")
			if err != nil || len(list) != 0 || len(h.gateway.commands) != 0 || h.gateway.preparedCount != 0 {
				t.Fatalf("invalid proposal caused work: plans=%v err=%v", list, err)
			}
		})
	}
}

func TestAgentPlanSourceHandleKeepsTheSelectedAsset(t *testing.T) {
	h := newAgentPlanHarness(t, "manual", true)
	command := h.command(strings.Replace(agentPlanArguments, "model-1", "@model3", 1))
	command.History = modelHistory()
	command.History[0].Attachments[0].AssetID = "model-1-glb"
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || result.Error != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	id := h.content(result)["plan_id"].(string)
	plan := h.get(id)
	if plan.SourceTaskID != "model-1" || plan.SourceAssetID != "model-1-glb" {
		t.Fatalf("plan source=%+v", plan)
	}
}

func TestAgentPlanPreservesShippedManualApprovalPolicy(t *testing.T) {
	h := newAgentPlanHarness(t, "manual", false)
	_, plan := h.propose(agentPlanArguments)
	h.tick()
	plan = h.get(plan.ID)
	if plan.State != processing.Running || len(h.gateway.commands) != 1 || h.gateway.policyActors[0] != "policy:manual" {
		t.Fatalf("shipped manual policy must still auto-approve remesh: plan=%+v actors=%v", plan, h.gateway.policyActors)
	}
}

func TestAgentPlanHumanConsentGatesEveryStepAndUsesPlanIdentity(t *testing.T) {
	h := newAgentPlanHarness(t, "manual", true)
	_, plan := h.propose(agentPlanArguments)
	h.tick()
	plan = h.get(plan.ID)
	if plan.State != processing.Paused || plan.Failure != processing.ReasonConsentRequired || len(h.gateway.commands) != 0 {
		t.Fatalf("manual=%+v submits=%d", plan, len(h.gateway.commands))
	}
	// The pending grant does not belong to an Agent operation, so the existing
	// processing panel must prepare its exact human grant and drive Resume.
	for i := 0; i < 3; i++ {
		step := plan.Steps[i]
		identity, err := h.plans.ExecutionGrantIdentity(context.Background(), "owner-a", plan.ID, string(step.PreparedInputID), step.PlanStepID)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := h.grants.Prepare(context.Background(), executiongrant.PrepareCommand{
			Principal: executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser},
			SessionID: identity.SessionID, OfferingID: string(step.OfferingID), OfferingRevision: step.Revision,
			PreparedInputID: string(step.PreparedInputID), PreparedInputHash: step.PreparedInputHash,
			OperationID: identity.OperationID, PlanStepID: identity.PlanStepID, IdempotencyKey: identity.IdempotencyKey})
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.grants.Decide(context.Background(), executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser}, grant.RequestID, executiongrant.Approve)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.plans.Resume(context.Background(), processing.ResumeCommand{OwnerID: "owner-a", PlanID: plan.ID, ExpectedPlanVersion: plan.Version, ExpectedOfferingRevision: step.Revision, AuthorizationRef: grant.RequestID})
		if err != nil {
			t.Fatal(err)
		}
		h.tick() // submit this approved step
		if len(h.gateway.commands) != i+1 {
			t.Fatalf("step %d submissions=%d", i, len(h.gateway.commands))
		}
		h.tick() // observe completion
		if i < 2 {
			h.tick()
		} // next step prepares and pauses
		plan = h.get(plan.ID)
		if i < 2 && (plan.State != processing.Paused || len(h.gateway.commands) != i+1) {
			t.Fatalf("next step bypassed consent: %+v", plan)
		}
	}
	if plan.State != processing.Succeeded {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestAgentPlanAutoPolicyAndMiddleFailureKeepUpstreamTasks(t *testing.T) {
	h := newAgentPlanHarness(t, "auto", true)
	_, plan := h.propose(agentPlanArguments)
	h.tick()
	h.tick()
	h.tick() // remesh succeeds; rigging accepted
	if len(h.gateway.commands) != 2 {
		t.Fatalf("submissions=%v", h.gateway.commands)
	}
	for _, actor := range h.gateway.policyActors {
		if actor != "policy:auto" {
			t.Fatalf("policy actor=%s", actor)
		}
	}
	plan = h.get(plan.ID)
	upstream := plan.Steps[0].TaskID
	failedTask := h.gateway.tasks[plan.Steps[1].TaskID]
	failedTask.State = generation.TaskFailed
	h.gateway.tasks[failedTask.ID] = failedTask
	h.tick()
	h.tick()
	plan = h.get(plan.ID)
	if plan.State != processing.Failed || plan.Steps[0].TaskID != upstream || plan.Steps[0].State != processing.Succeeded || len(h.gateway.commands) != 2 {
		t.Fatalf("failed plan=%+v", plan)
	}
	_, err := h.plans.Retry(context.Background(), processing.RetryCommand{OwnerID: "owner-a", PlanID: plan.ID, ExpectedPlanVersion: plan.Version, ExpectedOfferingRevision: plan.Steps[1].Revision})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		h.tick()
	}
	plan = h.get(plan.ID)
	if plan.State != processing.Succeeded || plan.Steps[0].TaskID != upstream || len(h.gateway.commands) != 4 {
		t.Fatalf("retried plan=%+v commands=%v", plan, h.gateway.commands)
	}
	var input map[string]any
	json.Unmarshal(h.gateway.tasks[plan.Steps[2].TaskID].Input, &input)
	if input["source_task"] != string(plan.Steps[1].TaskID) {
		t.Fatalf("animation lineage=%v", input)
	}
}

func TestAgentPlanObservationIsReadOnlyOwnerAndSessionScoped(t *testing.T) {
	h := newAgentPlanHarness(t, "manual", true)
	_, plan := h.propose(agentPlanArguments)
	h.tick()
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", Call: agents.ToolCall{Name: "get_processing_plan", Arguments: []byte(fmt.Sprintf(`{"plan_id":%q}`, plan.ID))}}
	result, err := h.executor.ReadProcessingPlan(context.Background(), command)
	if err != nil || result.Error != "" || !strings.Contains(result.AgentContent, "「加工」") || strings.Contains(result.AgentContent, "prepared_input_hash") || strings.Contains(result.AgentContent, "authorization_request_id") {
		t.Fatalf("observation=%+v err=%v", result, err)
	}
	for _, scope := range [][2]string{{"owner-b", "session-a"}, {"owner-a", "session-b"}} {
		command.OwnerID, command.SessionID = scope[0], scope[1]
		result, err = h.executor.ReadProcessingPlan(context.Background(), command)
		if err != nil || result.Error == "" {
			t.Fatalf("scope %v result=%+v err=%v", scope, result, err)
		}
	}
	if len(h.gateway.commands) != 0 {
		t.Fatal("observation submitted work")
	}
}

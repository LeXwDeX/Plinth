package generationtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

type harnessGateway struct{ *processingGateway }

func (g harnessGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.frozen = append(g.frozen, cmd)
	offering, _ := g.product.Get(cmd.OfferingID)
	p := generation.PreparedInput{ID: generation.PreparedInputID(fmt.Sprintf("harness-prepared-%d", len(g.frozen))), OwnerID: cmd.OwnerID, SessionID: cmd.SessionID, OfferingID: cmd.OfferingID, OfferingRevision: offering.Revision, Capability: cmd.Capability, NormalizedInput: cmd.Input, InputHash: fmt.Sprintf("harness-hash-%d", len(g.frozen)), CreatedAt: time.Now()}
	return p, g.repo.CreatePreparedInput(ctx, p)
}
func activeHarness(t *testing.T) (*processingHarness, chat.ToolCommand) {
	t.Helper()
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	ctx := context.Background()
	repo := h.gateway.repo
	resolver := resolve.Service{Store: repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
	offering, _ := h.gateway.product.Get("meshy/processing/retexture")
	selection := harness.DefaultSelection("owner-a", "session-a")
	selection.OperationID = "retexture"
	selection.EnabledOperations = []string{"retexture"}
	selection.OfferingID = string(offering.ID)
	selection.OfferingRevision = offering.Revision
	selection.Sources = []harness.Source{{Parameter: "source_task", TaskID: "model-1", AssetID: "model-1-glb"}}
	// Meshy Retexture's only adjustable parameter is its prompt (#329: no
	// negative_prompt); the selection fixes it, so a call may only echo it.
	selection.FixedParameters = map[string]json.RawMessage{"prompt": json.RawMessage(`"wood"`)}
	selection.FillableParameters = []string{}
	if _, err := resolver.Update(ctx, "owner-a", "session-a", 0, selection); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolver.Snapshot(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: h.policy(), Modes: sessionMode{mode: "auto"}}
	h.executor, err = generationtool.New(generationtool.Config{Gateway: harnessGateway{h.gateway}, Harness: repo, References: &fakeReferences{}, Operations: repo, Grants: grants, Chat: h.app.Chat, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", Call: agents.ToolCall{ID: "active-call", Name: "retexture_model", Arguments: []byte(`{"parameters":{"prompt":"wood"}}`)}, Tool: h.tool("retexture_model"), Options: chat.TurnOptions{Harness: &snapshot, AIModel: "ignored", SkillID: "ignored", Parameters: map[string]json.RawMessage{"prompt": json.RawMessage(`"legacy override"`)}}}
	return h, command
}
func TestActiveHarnessFreezesAllowedInputAndBinding(t *testing.T) {
	h, command := activeHarness(t)
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || result.NoSubmission || len(h.gateway.frozen) != 1 || len(h.gateway.submitted) != 1 {
		t.Fatalf("active submit %+v %v prepares=%d submits=%d", result, err, len(h.gateway.frozen), len(h.gateway.submitted))
	}
	var input map[string]any
	if err := json.Unmarshal(h.gateway.frozen[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if input["prompt"] != "wood" || input["source_task"] != "model-1" {
		t.Fatalf("input differs from selection %+v", input)
	}
	op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "active-call")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := h.gateway.repo.GetExecutionGrant(context.Background(), "owner-a", op.GrantRequestID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Harness != command.Options.Harness.Selection.Binding || grant.Harness != op.Harness || h.gateway.submitted[0].Harness != op.Harness {
		t.Fatal("binding propagation lost")
	}
}
func TestActiveHarnessRejectsCallOverridesBeforePreparing(t *testing.T) {
	cases := map[string]func(*processingHarness, *chat.ToolCommand){
		"tool": func(h *processingHarness, c *chat.ToolCommand) {
			c.Tool = h.tool("process_model")
			c.Call.Name = c.Tool.Name
			c.Call.Arguments = []byte(`{"step":"rigging","source_task_id":"model-1"}`)
		},
		"offering": func(_ *processingHarness, c *chat.ToolCommand) {
			c.Call.Arguments = []byte(`{"offering_id":"meshy/processing/remesh","parameters":{}}`)
		},
		"fixed": func(_ *processingHarness, c *chat.ToolCommand) {
			c.Call.Arguments = []byte(`{"parameters":{"prompt":"change"}}`)
		},
		"source": func(_ *processingHarness, c *chat.ToolCommand) {
			c.Call.Arguments = []byte(`{"parameters":{"source_task":"other-owner"}}`)
		},
		"unknown": func(_ *processingHarness, c *chat.ToolCommand) {
			c.Call.Arguments = []byte(`{"parameters":{"unknown":true}}`)
		},
		"legacy-top-level": func(_ *processingHarness, c *chat.ToolCommand) { c.Call.Arguments = []byte(`{"prompt":"override"}`) },
		"changed-source": func(h *processingHarness, _ *chat.ToolCommand) {
			task := h.gateway.tasks["model-1"]
			task.Assets[0].Size++
			h.gateway.tasks["model-1"] = task
		},
		"changed-selection": func(h *processingHarness, c *chat.ToolCommand) {
			s := c.Options.Harness.Selection
			s.FixedParameters = map[string]json.RawMessage{"prompt": json.RawMessage(`"new"`)}
			if _, err := h.gateway.repo.CompareAndSwapHarnessSelection(context.Background(), s, s.Revision); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			h, command := activeHarness(t)
			change(h, &command)
			result, err := h.executor.Execute(context.Background(), command, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || len(h.gateway.frozen) != 0 || len(h.gateway.submitted) != 0 {
				t.Fatalf("unsafe call prepared %+v %v", result, err)
			}
			if _, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "active-call"); err == nil {
				t.Fatal("refusal created operation/grant")
			}
		})
	}
}

func TestActiveHarnessAcceptsFixedEchoAndRejectsDuplicateKeys(t *testing.T) {
	h, command := activeHarness(t)
	command.Call.Arguments = []byte(`{"offering_id":"meshy/processing/retexture","parameters":{"prompt":"wood"}}`)
	if result, err := h.executor.Execute(context.Background(), command, nil); err != nil || result.NoSubmission {
		t.Fatalf("matching fixed echo %+v %v", result, err)
	}
	h, command = activeHarness(t)
	command.Call.Arguments = []byte(`{"parameters":{"prompt":"wood","prompt":"metal"}}`)
	if result, err := h.executor.Execute(context.Background(), command, nil); err != nil || !result.NoSubmission || len(h.gateway.frozen) != 0 {
		t.Fatalf("duplicate key accepted %+v %v", result, err)
	}
}

func TestFreeHarnessAgentOperationPropagatesBaselineBinding(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	repo := h.gateway.repo
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: h.policy(), Modes: sessionMode{mode: "auto"}}
	var err error
	h.executor, err = generationtool.New(generationtool.Config{Gateway: h.gateway, Harness: repo, References: &fakeReferences{}, Operations: repo, Grants: grants, Chat: h.app.Chat, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := resolve.Snapshot{Selection: harness.DefaultSelection("owner-a", "session-a"), Status: resolve.StatusFree}
	result, _ := h.runWith("owner-a", "session-a", "retexture_model", `{"source_task_id":"model-1","prompt":"wood"}`, nil, chat.TurnOptions{Harness: &snapshot})
	if result.NoSubmission || len(h.gateway.submitted) != 1 || h.gateway.submitted[0].Harness != snapshot.Selection.Binding {
		t.Fatalf("free baseline lost %+v", result)
	}
	op, err := repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "call-1")
	if err != nil || op.Harness != snapshot.Selection.Binding {
		t.Fatalf("operation binding %+v %v", op, err)
	}
}

func TestActiveHarnessPlanToolCannotCreateLegacyPlan(t *testing.T) {
	h, command := activeHarness(t)
	command.Tool = h.tool(h.app.Chat.Harness.PrerequisiteTool)
	command.Call.Name = command.Tool.Name
	command.Call.Arguments = []byte(`{"source_task_id":"model-1","steps":[{"capability":"rigging"}]}`)
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || len(h.gateway.frozen) != 0 || len(h.gateway.submitted) != 0 {
		t.Fatalf("active plan executed %+v %v", result, err)
	}
}

type normalizedHarnessGateway struct{ harnessGateway }

func (g normalizedHarnessGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	prepared, err := g.harnessGateway.PrepareInput(ctx, cmd)
	if err != nil {
		return prepared, err
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
		return prepared, err
	}
	input["prompt"] = json.RawMessage(`"silently changed"`)
	prepared.NormalizedInput, _ = json.Marshal(input)
	return prepared, nil
}
func TestActiveHarnessRejectsNormalizationChangingFixedValuesBeforeGrant(t *testing.T) {
	h, command := activeHarness(t)
	repo := h.gateway.repo
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: h.policy(), Modes: sessionMode{mode: "auto"}}
	executor, err := generationtool.New(generationtool.Config{Gateway: normalizedHarnessGateway{harnessGateway{h.gateway}}, Harness: repo, References: &fakeReferences{}, Operations: repo, Grants: grants, Chat: h.app.Chat, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || len(h.gateway.submitted) != 0 {
		t.Fatalf("fixed normalization accepted %+v %v", result, err)
	}
	if _, err := repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "active-call"); err == nil {
		t.Fatal("changed fixed value created operation/grant")
	}
}

func TestActiveHarnessPrerequisiteToolReturnsOnlyReadOnlyProposal(t *testing.T) {
	for _, arguments := range []string{`{"path":["rigging"]}`, `{"path":["rigging"],"path":["remesh"]}`, `{"path":["rigging"],"source_task":"other"}`, `{"path":"rigging"}`, `{"path":["rigging"]} {}`} {
		t.Run(arguments, func(t *testing.T) {
			h, command := activeHarness(t)
			repo := h.gateway.repo
			resolver := &resolve.Service{Store: repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
			offering, _ := h.gateway.product.Get("meshy/processing/animation")
			selection := command.Options.Harness.Selection
			selection.OperationID = "animation"
			selection.OfferingID = string(offering.ID)
			selection.OfferingRevision = offering.Revision
			selection.EnabledOperations = []string{"animation", "rigging"}
			selection.AllowPrerequisites = true
			selection.FixedParameters = nil
			selection.FillableParameters = []string{"action_id"}
			if _, err := resolver.Update(context.Background(), "owner-a", "session-a", selection.Revision, selection); err != nil {
				t.Fatal(err)
			}
			snapshot, err := resolver.Snapshot(context.Background(), "owner-a", "session-a")
			if err != nil {
				t.Fatal(err)
			}
			grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: h.policy(), Modes: sessionMode{mode: "auto"}}
			executor, err := generationtool.New(generationtool.Config{Gateway: h.gateway, Harness: repo, HarnessResolver: resolver, References: &fakeReferences{}, Operations: repo, Grants: grants, Chat: h.app.Chat, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
			if err != nil {
				t.Fatal(err)
			}
			command.Options.Harness = &snapshot
			command.Call.Name = h.app.Chat.Harness.PrerequisiteTool
			command.Tool = h.tool(command.Call.Name)
			command.Call.Arguments = []byte(arguments)
			result, err := executor.Execute(context.Background(), command, nil)
			if err != nil || !result.NoSubmission || len(h.gateway.frozen) != 0 || len(h.gateway.submitted) != 0 {
				t.Fatalf("proposal executed work %+v %v", result, err)
			}
			if arguments == `{"path":["rigging"]}` {
				if result.Error != "" || !strings.Contains(result.AgentContent, `"executed":false`) {
					t.Fatalf("valid proposal rejected %+v", result)
				}
			} else if result.Error == "" {
				t.Fatalf("invalid proposal arguments accepted %+v", result)
			}
		})
	}
}

func TestActiveHarnessKeepsExplicitAssetSourceRoute(t *testing.T) {
	h, command := activeHarness(t)
	resolver := resolve.Service{Store: h.gateway.repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
	selection := command.Options.Harness.Selection
	selection.Sources[0].Parameter = "source_asset"
	if _, err := resolver.Update(context.Background(), "owner-a", "session-a", selection.Revision, selection); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolver.Snapshot(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	command.Options.Harness = &snapshot
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || result.NoSubmission {
		t.Fatalf("explicit source rejected %+v %v", result, err)
	}
	var input map[string]any
	if err := json.Unmarshal(h.gateway.frozen[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if input["source_asset"] != "model-1-glb" || input["source_task"] != nil {
		t.Fatalf("explicit route changed %+v", input)
	}
}

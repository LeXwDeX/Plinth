package generationtool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type taskOnlyReferences struct{ calls int }

func (r *taskOnlyReferences) Resolve(context.Context, generation.OwnerID, []string) ([]generation.AssetID, error) {
	r.calls++
	return nil, errors.New("standalone tool must not resolve pictures")
}

func taskOnlyKernel(t *testing.T) (*uploadedHarness, catalog.ChatToolSchema, *taskOnlyReferences) {
	t.Helper()
	h := newUploadedHarness(t)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{Repository: h.repo, Catalog: product, Providers: h.registry, Assets: h.blobs, Inputs: h.sources})
	if err != nil {
		t.Fatal(err)
	}
	h.kernel = &uploadedKernel{Module: module}
	h.config.Gateway = h.kernel
	h.config.Chat = product.Application().Chat
	refs := &taskOnlyReferences{}
	h.config.References = refs
	h.executor, err = generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, v := range h.config.Chat.Tools {
		if v.Name == "create_motion_clip" {
			tool = v
		}
	}
	if tool.Name == "" {
		t.Fatal("motion tool missing")
	}
	current, err := h.repo.GetHarnessSelection(context.Background(), h.command.OwnerID, h.command.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.resolver.Update(context.Background(), h.command.OwnerID, h.command.SessionID, current.Revision, harness.DefaultSelection(h.command.OwnerID, h.command.SessionID)); err != nil {
		t.Fatal(err)
	}
	if err = h.chat.FinishTurn(context.Background(), h.command.AssistantMessageID, false); err != nil {
		t.Fatal(err)
	}
	if err = h.chat.FinishStream(context.Background(), h.command.SessionID, h.command.AssistantMessageID, false); err != nil {
		t.Fatal(err)
	}
	return h, tool, refs
}
func TestTaskOnlyMotionFreezesRealPreparedDurationWithoutReferenceSelection(t *testing.T) {
	for _, tc := range []struct{ name, duration, reference string }{
		{"plain nondefault", "4", ""},
		{"latest picture", "4.5", "latest"},
		{"invalid workspace indices", "4.5", "indices"},
		{"unrelated handle", "4", "handle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, tool, refs := taskOnlyKernel(t)
			ctx := context.Background()
			const prompt = "a character jumps and waves"
			assistant, err := h.chat.StartTurn(ctx, chat.StartTurn{OwnerID: h.command.OwnerID, SessionID: h.command.SessionID, Content: prompt})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := h.chat.GetTurn(ctx, h.command.OwnerID, h.command.SessionID, assistant.ID)
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"prompt": prompt, "duration": tc.duration}
			options := turn.Options
			options.AIModel = "unrelated-left-model"
			options.Parameters = map[string]json.RawMessage{"duration": json.RawMessage(`"2"`)}
			command := chat.ToolCommand{OwnerID: h.command.OwnerID, SessionID: h.command.SessionID, AssistantMessageID: assistant.ID, Tool: tool, Options: options, Call: agents.ToolCall{ID: "motion-call", Name: tool.Name}}
			if tc.reference != "" {
				command.History = []chat.Message{{Role: h.config.Chat.Roles.User, ImageURLs: []string{"/unrelated/picture"}}}
			}
			if tc.reference == "indices" {
				command.Options.ReferenceImageIndices = []int{999}
			}
			if tc.reference == "handle" {
				args[h.config.Chat.Context.ReferenceHandlesArgument] = []string{"@img999"}
			}
			command.Call.Arguments, err = json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			var observations []chat.ToolObservation
			result, err := h.executor.Execute(ctx, command, func(o chat.ToolObservation) error { observations = append(observations, o); return nil })
			if err != nil || result.NoSubmission || result.TaskID == "" {
				t.Fatalf("standalone execution result=%+v err=%v", result, err)
			}
			op, err := h.repo.LoadAgentOperation(ctx, command.OwnerID, command.SessionID, command.AssistantMessageID, command.Call.ID)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := h.kernel.GetPreparedInput(ctx, generation.OwnerID(command.OwnerID), op.PreparedInputID)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err = json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
				t.Fatal(err)
			}
			if input["duration"] != tc.duration || input["prompt"] != prompt || input["mode"] != "prime" || len(input) != 3 || prepared.OfferingID != "meshy/prime/text-to-motion" || len(prepared.Assets) != 0 || len(prepared.Dependencies) != 0 {
				t.Fatalf("wrong immutable standalone input: %+v", prepared)
			}
			grant, err := h.repo.GetExecutionGrant(ctx, command.OwnerID, op.GrantRequestID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := h.kernel.Get(ctx, generation.OwnerID(command.OwnerID), generation.TaskID(result.TaskID))
			if err != nil {
				t.Fatal(err)
			}
			if grant.Status != executiongrant.Bound || grant.TaskID != result.TaskID || grant.PreparedInputID != string(prepared.ID) || grant.PreparedInputHash != prepared.InputHash || grant.TriggerTurnID != command.AssistantMessageID || grant.Harness != op.Harness || grant.DecidedBy != "policy:auto" || task.Offering.EstimateTotalCredits(prepared.NormalizedInput) != 10 || prepared.ExtraCredits != 0 {
				t.Fatalf("wrong exact grant/cost: %+v", grant)
			}
			registered := false
			for _, o := range observations {
				if o.Registered {
					registered = true
					if o.EstimatedCredits != 10 || o.TaskID != result.TaskID {
						t.Fatalf("wrong accepted-cost evidence: %+v", o)
					}
				}
			}
			if !registered {
				t.Fatal("missing registered task")
			}
			if refs.calls != 0 || len(h.provider.requests) != 0 {
				t.Fatalf("unexpected reference/provider IO: refs=%d provider=%d", refs.calls, len(h.provider.requests))
			}
			if h.count(t, "generation_tasks") != 1 || h.count(t, "generation_prepared_inputs") != 1 || h.count(t, "execution_grants") != 1 || h.count(t, "processing_plans") != 0 {
				t.Fatal("unexpected paid-step writes")
			}
		})
	}
}
func TestTaskOnlyClipResultReturnsTaskHandleWithoutImages(t *testing.T) {
	h, tool, refs := taskOnlyKernel(t)
	task := generation.Task{ID: "finished-motion", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded, Assets: []generation.Asset{{ID: "clip", Role: "clip", Variant: "fbx", MediaType: "model/fbx"}, {ID: "unexpected-cover", Role: "thumbnail", MediaType: "image/png"}}}
	gateway := &fakeGateway{tasks: []generation.Task{task, task, task}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: refs, Chat: h.config.Chat, TaskStates: h.config.TaskStates})
	if err != nil {
		t.Fatal(err)
	}
	var observed []chat.ToolObservation
	result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", BoundTaskID: string(task.ID), Tool: tool, Call: agents.ToolCall{ID: "call", Name: tool.Name}}, func(o chat.ToolObservation) error { observed = append(observed, o); return nil })
	if err != nil || result.TaskID != string(task.ID) || len(result.ImageURLs) != 0 || result.AwaitTask || result.NoSubmission {
		t.Fatalf("wrong Task-only projection: %+v %v", result, err)
	}
	var content map[string]any
	if err = json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if content[tool.ResultFields.TaskID] != string(task.ID) || content[tool.ResultFields.Success] != true {
		t.Fatalf("lost real Task handle: %s", result.AgentContent)
	}
	for _, o := range observed {
		if len(o.ImageURLs) != 0 {
			t.Fatalf("clip became image event: %+v", o)
		}
	}
	if refs.calls != 0 || gateway.submitted.OfferingID != "" {
		t.Fatal("observing Task-only result performed IO/submission")
	}
}

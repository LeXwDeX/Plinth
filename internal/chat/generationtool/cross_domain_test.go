package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type crossDomainHarness struct {
	tool    catalog.ChatToolSchema
	chat    catalog.ChatSchema
	states  []catalog.TaskStatePresentation
	texts   *catalog.TextLocalizer
	gateway *fakeGateway
}

func newCrossDomainHarness(t *testing.T, offerings ...generation.Offering) *crossDomainHarness {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "create_model_3d" {
			tool = candidate
		}
	}
	if tool.Name == "" || !tool.CrossDomainModelFallback {
		t.Fatalf("default catalog must declare a cross-domain fallback 3D tool: %#v", tool)
	}
	for index := range offerings {
		offerings[index].Capability = tool.Capability
		offerings[index].Enabled = true
		offerings[index].Timeout = 5 * time.Second
		offerings[index].PollInterval = time.Second
		offerings[index].Parameters = []generation.Parameter{{
			Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true,
		}}
	}
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	return &crossDomainHarness{
		tool: tool, chat: application.Chat, states: application.TaskStates, texts: texts,
		gateway: &fakeGateway{
			offerings: offerings,
			tasks: []generation.Task{
				{ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskRunning},
				{
					ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a",
					State: generation.TaskSucceeded, Progress: 100,
					Assets: []generation.Asset{{ID: "thumb", Role: "thumbnail"}},
				},
				{
					ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a",
					State: generation.TaskSucceeded, Progress: 100,
					Assets: []generation.Asset{{ID: "thumb", Role: "thumbnail"}},
				},
			},
		},
	}
}

// expectOffering records which Offering the gateway reports on the Task it
// returns, the way the real gateway does for the Offering that was submitted.
func (h *crossDomainHarness) expectOffering(id generation.OfferingID) {
	for _, offering := range h.gateway.offerings {
		if offering.ID != id {
			continue
		}
		for index := range h.gateway.tasks {
			h.gateway.tasks[index].Offering = offering
		}
	}
}

func (h *crossDomainHarness) run(t *testing.T, options chat.TurnOptions) (chat.ToolResult, map[string]any) {
	t.Helper()
	result, payload, err := h.runErr(options)
	if err != nil {
		t.Fatal(err)
	}
	return result, payload
}

func (h *crossDomainHarness) runErr(options chat.TurnOptions) (chat.ToolResult, map[string]any, error) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway: h.gateway, References: &fakeReferences{}, Chat: h.chat, TaskStates: h.states, Texts: h.texts,
		Clock: func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error { now = now.Add(duration); return nil },
	})
	if err != nil {
		return chat.ToolResult{}, nil, err
	}
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: h.tool, Options: options,
		Call: agents.ToolCall{ID: "call-a", Name: h.tool.Name, Arguments: json.RawMessage(`{"prompt":"a stone fox"}`)},
	}
	if message := executor.SelectionError(command); message != "" {
		return chat.ToolResult{Error: message, NoSubmission: true}, nil, nil
	}
	result, err := executor.Execute(context.Background(), command, nil)
	if err != nil {
		return result, nil, err
	}
	if result.AwaitTask {
		command.BoundTaskID = result.TaskID
		command.TaskWaitDeadline = result.WaitUntil
		if result, err = executor.Execute(context.Background(), command, nil); err != nil {
			return result, nil, err
		}
	}
	var payload map[string]any
	if result.AgentContent != "" {
		_ = json.Unmarshal([]byte(result.AgentContent), &payload)
	}
	return result, payload, nil
}

func TestCrossDomainToolUsesThePerDomainModelTheUserPicked(t *testing.T) {
	h := newCrossDomainHarness(t,
		generation.Offering{ID: "image/default", ModelID: "default-3d", Default: true},
		generation.Offering{ID: "three/picked", ModelID: "picked-3d"},
	)
	h.expectOffering("three/picked")
	_, payload := h.run(t, chat.TurnOptions{
		AIModel: "image-model", CreativeDomain: "image",
		DomainModels: map[string]string{"model-3d": "picked-3d"},
	})
	if h.gateway.submitted.OfferingID != "three/picked" {
		t.Fatalf("submitted = %#v", h.gateway.submitted)
	}
	if payload[h.tool.ResultFields.Model] != "picked-3d" {
		t.Fatalf("result must name the model: %#v", payload)
	}
	if _, flagged := payload[h.tool.ResultFields.ModelFallback]; flagged {
		t.Fatalf("a user-picked model is not a fallback: %#v", payload)
	}
}

func TestCrossDomainToolFallsBackToDefaultThenRecommendedThenFirstAndSaysSo(t *testing.T) {
	cases := []struct {
		name      string
		offerings []generation.Offering
		want      string
	}{
		{"default wins", []generation.Offering{
			{ID: "o/first", ModelID: "first"},
			{ID: "o/recommended", ModelID: "recommended", Recommended: true},
			{ID: "o/default", ModelID: "default", Default: true},
		}, "o/default"},
		{"recommended next", []generation.Offering{
			{ID: "o/first", ModelID: "first"},
			{ID: "o/recommended", ModelID: "recommended", Recommended: true},
		}, "o/recommended"},
		{"first available last", []generation.Offering{
			{ID: "o/first", ModelID: "first"},
			{ID: "o/second", ModelID: "second"},
		}, "o/first"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCrossDomainHarness(t, tc.offerings...)
			h.expectOffering(generation.OfferingID(tc.want))
			// The image model selected in the image workspace must not leak
			// into the 3D tool.
			result, payload := h.run(t, chat.TurnOptions{AIModel: "image-model", CreativeDomain: "image"})
			if string(h.gateway.submitted.OfferingID) != tc.want {
				t.Fatalf("submitted = %#v result=%#v", h.gateway.submitted, result)
			}
			if payload[h.tool.ResultFields.ModelFallback] != true || payload[h.tool.ResultFields.Model] == nil {
				t.Fatalf("the Agent must be told a fallback model ran: %#v", payload)
			}
		})
	}
}

func TestFallbackNeverPicksADisabledOrUnavailableOffering(t *testing.T) {
	h := newCrossDomainHarness(t,
		generation.Offering{ID: "o/disabled-default", ModelID: "disabled-default", Default: true},
		generation.Offering{ID: "o/unconfigured-recommended", ModelID: "unconfigured-recommended", Recommended: true},
		generation.Offering{ID: "o/usable", ModelID: "usable"},
	)
	h.gateway.offerings[0].Enabled = false
	h.gateway.unavailable = map[generation.OfferingID]bool{"o/unconfigured-recommended": true}
	h.expectOffering("o/usable")
	_, payload := h.run(t, chat.TurnOptions{AIModel: "image-model", CreativeDomain: "image"})
	if h.gateway.submitted.OfferingID != "o/usable" {
		t.Fatalf("submitted = %#v; a disabled or unconfigured Offering must never run", h.gateway.submitted)
	}
	if payload[h.tool.ResultFields.ModelFallback] != true || payload[h.tool.ResultFields.Model] != "usable" {
		t.Fatalf("the Agent must be told which fallback model ran: %#v", payload)
	}

	// With nothing usable the tool fails before any paid submission.
	h = newCrossDomainHarness(t, generation.Offering{ID: "o/only", ModelID: "only", Default: true})
	h.gateway.unavailable = map[generation.OfferingID]bool{"o/only": true}
	result, _, err := h.runErr(chat.TurnOptions{AIModel: "image-model", CreativeDomain: "image"})
	if h.gateway.submitted.OfferingID != "" || !result.NoSubmission || (err == nil && result.Error == "") {
		t.Fatalf("no usable Offering must not submit: %#v %v", result, err)
	}
}

func TestSameDomainToolStillRequiresTheUsersModel(t *testing.T) {
	h := newCrossDomainHarness(t, generation.Offering{ID: "o/default", ModelID: "default", Default: true})
	result, _ := h.run(t, chat.TurnOptions{CreativeDomain: "model-3d"})
	if result.Error != h.tool.ModelRequiredMessage || h.gateway.submitted.OfferingID != "" {
		t.Fatalf("working in the tool's own domain without a model must still ask: %#v", result)
	}
	// The same call with no domain in view also asks: nothing proves the user
	// never had the chance to choose.
	result, _ = h.run(t, chat.TurnOptions{})
	if result.Error != h.tool.ModelRequiredMessage || h.gateway.submitted.OfferingID != "" {
		t.Fatalf("no domain must still ask: %#v", result)
	}
}

func TestFallbackNeedsTheSchemaFlag(t *testing.T) {
	h := newCrossDomainHarness(t, generation.Offering{ID: "o/default", ModelID: "default", Default: true})
	h.tool.CrossDomainModelFallback = false
	result, _ := h.run(t, chat.TurnOptions{AIModel: "image-model", CreativeDomain: "image"})
	if result.Error != h.tool.ModelRequiredMessage || h.gateway.submitted.OfferingID != "" {
		t.Fatalf("a tool without the Schema flag must not fall back: %#v", result)
	}
}

func TestUnknownPickedModelIsNotSilentlyReplaced(t *testing.T) {
	h := newCrossDomainHarness(t, generation.Offering{ID: "o/default", ModelID: "default", Default: true})
	result, _, err := h.runErr(chat.TurnOptions{
		CreativeDomain: "image", DomainModels: map[string]string{"model-3d": "removed-model"},
	})
	if h.gateway.submitted.OfferingID != "" || (err == nil && result.Error == "") {
		t.Fatalf("a model the user picked but that is gone must not be replaced silently: %#v %v", result, err)
	}
}

func TestTurnOptionsModelForPicksTheDomainModel(t *testing.T) {
	options := chat.TurnOptions{
		AIModel: "current", CreativeDomain: "image",
		DomainModels: map[string]string{"model-3d": "three", "image": "stale"},
	}
	for domain, want := range map[string]string{
		"": "current", "image": "current", "model-3d": "three", "audio": "",
	} {
		if got := options.ModelFor(domain); got != want {
			t.Fatalf("ModelFor(%q) = %q, want %q", domain, got, want)
		}
	}
	if got := (chat.TurnOptions{AIModel: "only"}).ModelFor("model-3d"); got != "only" {
		t.Fatalf("without a workspace domain the single model applies: %q", got)
	}
}

// #266: a canvas-sourced turn never runs a tool that reads no image; the call
// is turned down before anything is submitted and the Agent is told the tools
// that bind the snapshot.
func TestCanvasTurnRefusesTextOnlyToolBeforeSubmission(t *testing.T) {
	h := newCrossDomainHarness(t, generation.Offering{ID: "three/picked", ModelID: "picked-3d"})
	h.expectOffering("three/picked")
	result, _, err := h.runErr(chat.TurnOptions{
		AIModel: "image-model", CreativeDomain: "image",
		DomainModels: map[string]string{"model-3d": "picked-3d"},
		InputSource:  h.chat.Context.CanvasInputSource,
		Selection:    []chat.Attachment{{Kind: chat.AttachmentKindImage, URL: "canvas-snapshot"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || !strings.Contains(result.Error, "create_model_3d_from_image") {
		t.Fatalf("result = %#v", result)
	}
	if h.gateway.submitted.OfferingID != "" {
		t.Fatalf("submitted %#v", h.gateway.submitted)
	}
}

package generationtool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// creativeLabGateway answers source Task reads from a fixed table so a test
// can say whose Task, in which session, of which template a handle names.
type creativeLabGateway struct {
	*grantGateway
	sources  map[generation.TaskID]generation.Task
	prepared []generation.PrepareInputCommand
}

func (g *creativeLabGateway) Get(ctx context.Context, owner generation.OwnerID, id generation.TaskID) (generation.Task, error) {
	if task, exists := g.sources[id]; exists {
		if task.OwnerID != owner {
			return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
		}
		return task, nil
	}
	if strings.HasPrefix(string(id), "task-") {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
	}
	return g.grantGateway.Get(ctx, owner, id)
}

func (g *creativeLabGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.prepared = append(g.prepared, cmd)
	return g.grantGateway.PrepareInput(ctx, cmd)
}

func creativeLabOffering(id, model, capability string, source ...generation.OfferingID) generation.Offering {
	offering := generation.Offering{ID: generation.OfferingID(id), Revision: "rev-a", ModelID: model,
		Capability: generation.Capability(capability), Enabled: true, Timeout: time.Second, EstimatedCredits: 6,
		Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt}}}
	if len(source) > 0 {
		offering.EstimatedCredits = 30
		offering.Parameters = []generation.Parameter{{Name: "source_task", Type: "task", Required: true,
			Semantic: generation.SemanticSourceTask, TaskOfferings: source}}
	}
	return offering
}

type creativeLabCase struct {
	executor          *generationtool.Executor
	gateway           *creativeLabGateway
	command           chat.ToolCommand
	canvasInputSource string
	canvasMessage     string
}

func newCreativeLabCase(t *testing.T, mode, toolName, arguments string, unavailable ...generation.OfferingID) creativeLabCase {
	t.Helper()
	ctx := context.Background()
	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "creative-lab.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner-a",
		chat.Message{ID: "user-a", SessionID: "session-a", Role: "user", Content: "go", CreatedAt: now},
		chat.Message{ID: "turn-a", SessionID: "session-a", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session-a", Data: map[string]any{"type": "message.created"}, CreatedAt: now},
		chat.Stream{SessionID: "session-a", Status: "active"},
		chat.Turn{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
			State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == toolName {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatalf("tool %q missing", toolName)
	}
	figurePrototype := creativeLabOffering("meshy/creative-lab/figure/prototype", "creative-lab-figure-prototype", "creative-lab-prototype")
	vinylPrototype := creativeLabOffering("meshy/creative-lab/vinyl-figure/prototype", "creative-lab-vinyl-figure-prototype", "creative-lab-prototype")
	offerings := []generation.Offering{
		figurePrototype, vinylPrototype,
		creativeLabOffering("meshy/creative-lab/figure/build", "creative-lab-figure-build", "creative-lab-build", figurePrototype.ID),
		creativeLabOffering("meshy/creative-lab/vinyl-figure/build", "creative-lab-vinyl-figure-build", "creative-lab-build", vinylPrototype.ID),
	}
	gateway := &creativeLabGateway{
		grantGateway: &grantGateway{fakeGateway: &fakeGateway{offerings: offerings,
			unavailable: map[generation.OfferingID]bool{}}, repo: repo},
		sources: map[generation.TaskID]generation.Task{
			"task-figure": {ID: "task-figure", OwnerID: "owner-a", SessionID: "session-a",
				State: generation.TaskSucceeded, Offering: figurePrototype},
			"task-figure-running": {ID: "task-figure-running", OwnerID: "owner-a", SessionID: "session-a",
				State: generation.TaskRunning, Offering: figurePrototype},
			"task-figure-other-session": {ID: "task-figure-other-session", OwnerID: "owner-a", SessionID: "session-b",
				State: generation.TaskSucceeded, Offering: figurePrototype},
			"task-figure-other-owner": {ID: "task-figure-other-owner", OwnerID: "owner-b", SessionID: "session-a",
				State: generation.TaskSucceeded, Offering: figurePrototype},
		},
	}
	for _, id := range unavailable {
		gateway.unavailable[id] = true
	}
	var manual, known []string
	for _, capability := range application.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	for _, capability := range application.Capabilities {
		known = append(known, string(capability.ID))
	}
	policy := approvalpolicy.New(application.Chat.ApprovalPolicy.DefaultMode, application.Chat.ApprovalPolicy.Modes, manual, known)
	service := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: policy, Modes: sessionMode{mode: mode}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{},
		Operations: repo, Grants: service, Chat: application.Chat,
		StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	// The workspace model is deliberately some other 3D model: the template
	// argument, not the user's model choice, decides the Offering.
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: json.RawMessage(arguments)},
		Tool: tool, Options: chat.TurnOptions{AIModel: "meshy-6"}}
	return creativeLabCase{executor: executor, gateway: gateway, command: command,
		canvasInputSource: application.Chat.Context.CanvasInputSource,
		canvasMessage:     application.Chat.Presentation.Messages.SourceOfferingCanvas}
}

func (c creativeLabCase) run(t *testing.T) chat.ToolResult {
	t.Helper()
	result, err := c.executor.Execute(context.Background(), c.command, func(chat.ToolObservation) error { return nil })
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return result
}

func TestCreativeLabDesignToolPreparesTheNamedTemplate(t *testing.T) {
	c := newCreativeLabCase(t, "manual", "create_creative_lab_design",
		`{"template":"creative-lab-vinyl-figure-prototype"}`)
	result := c.run(t)
	if result.Error != "" || len(c.gateway.prepared) != 1 {
		t.Fatalf("result=%+v prepared=%d", result, len(c.gateway.prepared))
	}
	prepared := c.gateway.prepared[0]
	if prepared.OfferingID != "meshy/creative-lab/vinyl-figure/prototype" || prepared.Capability != "creative-lab-prototype" {
		t.Fatalf("prepared %s/%s", prepared.Capability, prepared.OfferingID)
	}
	// A 2D design step is approved by the manual policy without a card.
	if c.gateway.submitCount != 1 {
		t.Fatalf("design step submissions = %d", c.gateway.submitCount)
	}
}

func TestCreativeLabDesignToolRefusesAnUnavailableTemplateWithoutSubmitting(t *testing.T) {
	c := newCreativeLabCase(t, "auto", "create_creative_lab_design",
		`{"template":"creative-lab-figure-prototype"}`, "meshy/creative-lab/figure/prototype")
	result := c.run(t)
	if !result.NoSubmission || len(c.gateway.prepared) != 0 || c.gateway.submitCount != 0 {
		t.Fatalf("result=%+v prepared=%d submitted=%d", result, len(c.gateway.prepared), c.gateway.submitCount)
	}
	// The refusal names exactly the declared templates that can run: never the
	// unavailable one, and never a build stage, which is not a template choice.
	refusal := result.AgentContent + result.Error
	if !strings.Contains(refusal, "creative-lab-vinyl-figure-prototype") ||
		strings.Contains(refusal, "creative-lab-figure-prototype") || strings.Contains(refusal, "-build") {
		t.Fatalf("refusal must list only the available templates: %+v", result)
	}
}

func TestCreativeLabBuildToolPairsTheOwnPrototype(t *testing.T) {
	c := newCreativeLabCase(t, "manual", "build_creative_lab_item", `{"source_task":"task-figure"}`)
	c.run(t)
	if len(c.gateway.prepared) != 1 {
		t.Fatalf("prepared=%d", len(c.gateway.prepared))
	}
	prepared := c.gateway.prepared[0]
	if prepared.OfferingID != "meshy/creative-lab/figure/build" || prepared.Capability != "creative-lab-build" {
		t.Fatalf("prepared %s/%s", prepared.Capability, prepared.OfferingID)
	}
	var input map[string]any
	if err := json.Unmarshal(prepared.Input, &input); err != nil || input["source_task"] != "task-figure" {
		t.Fatalf("frozen input = %s (%v)", prepared.Input, err)
	}
}

// The build follows the session approval mode through the Schema policy: it
// waits for a human exactly when the policy lists its capability for the mode.
func TestCreativeLabBuildToolFollowsTheSessionApprovalPolicy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	manualListsBuild := slices.Contains(product.Application().Chat.ApprovalPolicy.ManualApprovalCapabilities, "creative-lab-build")
	for mode, waits := range map[string]bool{"auto": false, "manual": manualListsBuild} {
		t.Run(mode, func(t *testing.T) {
			c := newCreativeLabCase(t, mode, "build_creative_lab_item", `{"source_task":"task-figure"}`)
			result := c.run(t)
			submitted := c.gateway.submitCount == 1
			if len(c.gateway.prepared) != 1 || submitted == waits || (result.TaskID != "") == waits {
				t.Fatalf("mode=%s waits=%v result=%+v submitted=%d", mode, waits, result, c.gateway.submitCount)
			}
		})
	}
}

func TestCreativeLabBuildToolRefusesSourcesItCannotPair(t *testing.T) {
	for name, source := range map[string]string{
		"running prototype":       "task-figure-running",
		"another session":         "task-figure-other-session",
		"another owner":           "task-figure-other-owner",
		"unknown task":            "task-missing",
		"not a design (build id)": "",
	} {
		t.Run(name, func(t *testing.T) {
			c := newCreativeLabCase(t, "auto", "build_creative_lab_item", `{"source_task":"`+source+`"}`)
			result := c.run(t)
			if !result.NoSubmission || len(c.gateway.prepared) != 0 || c.gateway.submitCount != 0 {
				t.Fatalf("result=%+v prepared=%d submitted=%d", result, len(c.gateway.prepared), c.gateway.submitCount)
			}
		})
	}
	t.Run("paired build unavailable", func(t *testing.T) {
		c := newCreativeLabCase(t, "auto", "build_creative_lab_item", `{"source_task":"task-figure"}`,
			"meshy/creative-lab/figure/build")
		result := c.run(t)
		if !result.NoSubmission || len(c.gateway.prepared) != 0 {
			t.Fatalf("result=%+v prepared=%d", result, len(c.gateway.prepared))
		}
	})
}

// A build stage shares the design tool's capabilities but is not a template:
// naming its model_id must not run a build without its paired preview.
func TestCreativeLabDesignToolRefusesABuildStageAsTemplate(t *testing.T) {
	c := newCreativeLabCase(t, "auto", "create_creative_lab_design", `{"template":"creative-lab-figure-build"}`)
	result := c.run(t)
	if !result.NoSubmission || len(c.gateway.prepared) != 0 || c.gateway.submitCount != 0 {
		t.Fatalf("result=%+v prepared=%d submitted=%d", result, len(c.gateway.prepared), c.gateway.submitCount)
	}
}

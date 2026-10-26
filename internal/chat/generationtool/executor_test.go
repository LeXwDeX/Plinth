package generationtool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestExecutorSelectsSchemaOfferingReferencesAndProjectsTaskEvents(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:           "configured/offering",
			ModelID:      "configured-model",
			Capability:   tool.Capability,
			Enabled:      true,
			PollInterval: time.Second,
			Timeout:      5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "configured-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
					Required: true,
				},
				{
					Name:     "configured-references",
					Type:     "assets",
					Semantic: generation.SemanticReferenceImage,
					Required: true,
					MinItems: 1,
					MaxItems: 1,
				},
				{
					Name: "configured-aspect",
					Type: "string",
				},
			},
		}},
		tasks: []generation.Task{
			{
				ID:       "configured-task",
				State:    generation.TaskRunning,
				Progress: 35,
			},
			{
				ID:      "configured-task",
				OwnerID: "owner-a", SessionID: "session-a",
				State:    generation.TaskSucceeded,
				Progress: 100,
				Assets: []generation.Asset{{
					ID:      "configured-asset",
					Role:    "image",
					Ordinal: 0,
				}},
			},
			{
				ID: "configured-task", State: generation.TaskSucceeded, Progress: 100,
				OwnerID: "owner-a", SessionID: "session-a",
				Assets: []generation.Asset{{ID: "configured-asset", Role: "image"}},
			},
		},
	}
	resolver := &fakeReferences{}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: resolver,
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
		Clock:      func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.OverrideMappings = map[string]string{
		"aspect_ratio": "configured-aspect",
	}
	tool.PromptWithReferenceTemplate = "{prompt}"
	tool.PromptWithPreviousTemplate = "{previous_prompt}{prompt}"
	tool.IdempotencyKeyTemplate = "configured:{session_id}:{call_id}"
	tool.ResultFields = catalog.ChatToolResultFields{
		Success:   "configured-success",
		TaskID:    "configured-task-field",
		ImageURLs: "configured-images",
		Error:     "configured-error",
	}
	var observations []chat.ToolObservation
	result, err := executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-a",
				Name:      tool.Name,
				Arguments: []byte(`{"prompt":"paint","reference_image_indices":[2]}`),
			},
			Tool: tool,
			History: []chat.Message{
				{
					Role:      application.Chat.Roles.User,
					ImageURLs: []string{"/configured/first"},
				},
				{
					Role:      application.Chat.Roles.Assistant,
					ImageURLs: []string{"/configured/second"},
				},
			},
			Options: chat.TurnOptions{
				AIModel:     "configured-model",
				AspectRatio: "configured-ratio",
			},
		},
		func(observation chat.ToolObservation) error {
			observations = append(observations, observation)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolver.urls) != 1 ||
		resolver.urls[0] != "/configured/second" {
		t.Fatalf("resolved URLs = %#v", resolver.urls)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	references, _ := submitted["configured-references"].([]any)
	if submitted["configured-prompt"] != "paint" ||
		submitted["configured-aspect"] != "configured-ratio" ||
		len(references) != 1 ||
		references[0] != "configured-input-asset" {
		t.Fatalf("submitted = %#v", submitted)
	}
	if gateway.submitted.OfferingID != "configured/offering" ||
		gateway.submitted.IdempotencyKey !=
			"configured:session-a:call-a" {
		t.Fatalf("command = %#v", gateway.submitted)
	}
	if !result.AwaitTask || result.TaskID != "configured-task" || result.WaitUntil.IsZero() {
		t.Fatalf("first observation must release the worker: %#v", result)
	}
	result, err = executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", BoundTaskID: result.TaskID,
		TaskWaitDeadline: result.WaitUntil, Tool: tool,
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name},
	}, func(observation chat.ToolObservation) error {
		observations = append(observations, observation)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "configured-task" ||
		len(result.ImageURLs) != 1 ||
		result.ImageURLs[0] !=
			"/api/v1/tasks/configured-task/assets/configured-asset" {
		t.Fatalf("result = %#v", result)
	}
	var agentResult map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &agentResult); err != nil {
		t.Fatal(err)
	}
	if agentResult["configured-success"] != true ||
		agentResult["configured-task-field"] != "configured-task" {
		t.Fatalf("agent result = %#v", agentResult)
	}
	if len(observations) != 4 ||
		!observations[0].Registered ||
		observations[1].Progress != 35 ||
		!observations[2].Registered ||
		len(observations[3].ImageURLs) != 1 {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestExecutorAppliesCatalogSkillThroughSchemaConfiguredFeatureTemplateAndOverrides(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	application.Chat.Skills.Feature = "configured-skill-feature"
	application.Chat.Skills.Execution = catalog.ChatSkillExecutionSchema{
		PromptTemplateFields: catalog.ChatSkillPromptTemplateFields{
			UserInput:   "[[configured-user-input]]",
			SkillPrompt: "[[configured-skill-prompt]]",
		},
		PromptTemplates: map[generation.Capability]string{
			tool.FallbackCapability: "configured:[[configured-user-input]]::[[configured-skill-prompt]]",
		},
		ParameterOverrides: map[generation.ParameterSemantic]json.RawMessage{
			generation.SemanticQuality: json.RawMessage(`"configured-high"`),
		},
	}
	skills := catalog.SkillCatalogSchema{
		Revision: "configured-revision",
		Skills: []catalog.SkillSchema{{
			ID:              "configured-skill",
			Name:            "trusted catalog name",
			Category:        application.Chat.Skills.Categories[0].ID,
			RecommendedSize: "configured-size",
			ThumbnailPath:   "/configured-thumbnail",
			RawImageURL:     "https://configured.invalid/image.png",
			Prompt:          "trusted catalog prompt",
		}},
	}
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:         "configured/offering",
			ModelID:    "configured-model",
			Capability: tool.FallbackCapability,
			Enabled:    true,
			Features:   []string{"configured-skill-feature"},
			Timeout:    5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "configured-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
					Required: true,
				},
				{
					Name:     "configured-quality",
					Type:     "string",
					Semantic: generation.SemanticQuality,
				},
			},
		}},
		tasks: []generation.Task{{
			ID:       "configured-task",
			State:    generation.TaskSucceeded,
			Progress: 100,
		}},
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: &fakeReferences{},
		Chat:       application.Chat,
		Skills:     skills,
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]string{
		application.Chat.Context.ToolPromptArgument: "user intent",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-a",
				Name:      tool.Name,
				Arguments: arguments,
			},
			Tool: tool,
			Options: chat.TurnOptions{
				AIModel:   "configured-model",
				SkillID:   "configured-skill",
				SkillName: "untrusted client name",
			},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted["configured-prompt"] !=
		"configured:user intent::trusted catalog prompt" ||
		submitted["configured-quality"] != "configured-high" {
		t.Fatalf("submitted = %#v", submitted)
	}
}

func TestExecutorKeepsCatalogAppliedPromptWithinOfferingLimit(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	// This contract uses a short custom template so the 13-rune Offering limit
	// exercises truncation without depending on the longer default style policy.
	application.Chat.Skills.Execution.PromptTemplates[generation.TextToImage] = "{user_input}\n\n{skill_prompt}"
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:         "configured/offering",
			ModelID:    "configured-model",
			Capability: tool.FallbackCapability,
			Enabled:    true,
			Features:   []string{application.Chat.Skills.Feature},
			Timeout:    5 * time.Second,
			Parameters: []generation.Parameter{{
				Name:      "configured-prompt",
				Type:      "string",
				Semantic:  generation.SemanticPrompt,
				Required:  true,
				MaxLength: 13,
			}},
		}},
		tasks: []generation.Task{{
			ID:       "configured-task",
			State:    generation.TaskSucceeded,
			Progress: 100,
		}},
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: &fakeReferences{},
		Chat:       application.Chat,
		Skills: catalog.SkillCatalogSchema{
			Revision: "configured-revision",
			Skills: []catalog.SkillSchema{{
				ID:     "configured-skill",
				Prompt: "1234567890",
			}},
		},
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]string{
		application.Chat.Context.ToolPromptArgument: "intent",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-a",
				Name:      tool.Name,
				Arguments: arguments,
			},
			Tool: tool,
			Options: chat.TurnOptions{
				AIModel: "configured-model",
				SkillID: "configured-skill",
			},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted["configured-prompt"] != "intent\n\n12345" {
		t.Fatalf("submitted = %#v", submitted)
	}
}

func TestExecutorAppliesDeclaredTurnParametersWithoutAdapterBranches(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:         "configured/offering",
			ModelID:    "configured-model",
			Capability: tool.FallbackCapability,
			Enabled:    true,
			Timeout:    5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "configured-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
					Required: true,
				},
				{
					Name:     "configured-references",
					Type:     "asset-array",
					Semantic: generation.SemanticReferenceImage,
					MaxItems: 1,
				},
				{
					Name:     "configured-quality",
					Type:     "select",
					Semantic: generation.SemanticQuality,
				},
				{Name: "configured-format", Type: "select"},
				{Name: "configured-mask", Type: "asset", MaxBytes: 4},
			},
		}},
		tasks: []generation.Task{{
			ID:       "configured-task",
			State:    generation.TaskSucceeded,
			Progress: 100,
		}},
	}
	resolver := &fakeReferences{}
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: resolver,
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]string{
		application.Chat.Context.ToolPromptArgument: "paint",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-a",
				Name:      tool.Name,
				Arguments: arguments,
			},
			Tool: tool,
			History: []chat.Message{{
				Role:      application.Chat.Roles.User,
				ImageURLs: []string{"/configured/first"},
			}},
			Options: chat.TurnOptions{
				AIModel: "configured-model",
				Parameters: map[string]json.RawMessage{
					"configured-quality":    json.RawMessage(`"xhigh"`),
					"configured-format":     json.RawMessage(`"webp"`),
					"configured-mask":       json.RawMessage(`"data:image/png;base64,AAAA"`),
					"configured-prompt":     json.RawMessage(`"hijacked"`),
					"configured-references": json.RawMessage(`["data:image/png;base64,BBBB"]`),
					"undeclared":            json.RawMessage(`"drop"`),
				},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted["configured-quality"] != "xhigh" ||
		submitted["configured-format"] != "webp" {
		t.Fatalf("declared parameters dropped: %#v", submitted)
	}
	if submitted["configured-mask"] != "configured-input-asset" {
		t.Fatalf("mask data URI not resolved: %#v", submitted)
	}
	if submitted["configured-prompt"] !=
		"Edit the reference image: paint. Keep the original composition, style, and unchanged elements intact." ||
		strings.Contains(submitted["configured-prompt"].(string), "hijacked") {
		t.Fatalf("reserved prompt overridden: %#v", submitted)
	}
	references, _ := submitted["configured-references"].([]any)
	if len(references) != 1 || references[0] != "configured-input-asset" {
		t.Fatalf("reserved references overridden: %#v", submitted)
	}
	if _, exists := submitted["undeclared"]; exists {
		t.Fatalf("undeclared parameter accepted: %#v", submitted)
	}
	if len(resolver.urls) != 2 ||
		resolver.urls[0] != "data:image/png;base64,AAAA" ||
		resolver.urls[1] != "/configured/first" {
		t.Fatalf("resolved URLs = %#v", resolver.urls)
	}
}

func TestExecutorUsesHumanSelectedModelInsteadOfAgentOffering(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	configuredOffering := generation.Offering{
		ID: "configured/selected", ModelID: "selected-model",
		Capability: tool.FallbackCapability, Enabled: true,
		Timeout: time.Second,
		Parameters: []generation.Parameter{{
			Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt,
		}},
	}
	defaultOffering := configuredOffering
	defaultOffering.ID = "configured/default"
	defaultOffering.ModelID = "default-model"
	gateway := &fakeGateway{
		offerings: []generation.Offering{defaultOffering, configuredOffering},
		tasks:     []generation.Task{{ID: "configured-task", State: generation.TaskSucceeded}},
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
		Options: chat.TurnOptions{AIModel: "selected-model"},
		Call: agents.ToolCall{
			ID: "call-a", Name: tool.Name,
			Arguments: json.RawMessage(`{"prompt":"a cube","offering_id":"configured/default"}`),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.submitted.OfferingID != configuredOffering.ID {
		t.Fatalf("human selected model ignored: %q", gateway.submitted.OfferingID)
	}
}

func TestExecutorRequiresHumanModelBeforeGeneration(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		Tool: tool,
		Call: agents.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"paint"}`)},
	}, nil)
	if err != nil || result.Error != tool.ModelRequiredMessage || gateway.submitted.OfferingID != "" {
		t.Fatalf("model requirement not enforced: result=%#v error=%v submit=%#v", result, err, gateway.submitted)
	}
}

func TestExecutorPreservesGeminiUIParameters(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
		t.Run(string(capability), func(t *testing.T) {
			offering, ok := taskCatalog.Get(generation.OfferingID("gemini/gemini-3-pro-image/" + string(capability)))
			if !ok {
				t.Fatal("missing Gemini offering")
			}
			gateway := &fakeGateway{
				offerings: []generation.Offering{offering},
				tasks:     []generation.Task{{ID: "gemini-task", State: generation.TaskSucceeded, Progress: 100}},
			}
			executor, err := generationtool.New(generationtool.Config{
				Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat, TaskStates: application.TaskStates,
			})
			if err != nil {
				t.Fatal(err)
			}
			options := chat.TurnOptions{AIModel: offering.ModelID, AspectRatio: "16:9", ImageSize: "2K"}
			var history []chat.Message
			if capability == generation.ImageToImage {
				history = []chat.Message{{Role: application.Chat.Roles.User, ImageURLs: []string{"/owned-reference"}}}
			}
			_, err = executor.Execute(context.Background(), chat.ToolCommand{
				OwnerID: "owner", SessionID: "session", Tool: tool, Options: options, History: history,
				Call: agents.ToolCall{ID: "call", Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"paint a teapot"}`)},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if gateway.submitted.OfferingID != offering.ID {
				t.Fatalf("selected %s instead of %s", gateway.submitted.OfferingID, offering.ID)
			}
			_, normalized, err := taskCatalog.Normalize(capability, offering.ID, gateway.submitted.Input)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err := json.Unmarshal(normalized, &input); err != nil {
				t.Fatal(err)
			}
			// 合法 advised 值在 normalization 保留；真实 PrepareInput 另冻结理由。
			if input["aspect_ratio"] != "16:9" || input["image_size"] != "2K" {
				t.Fatalf("UI parameters lost: %s", normalized)
			}
		})
	}
}

func TestExecutorKeepsUpstreamDiagnosticsOutOfAgentVisibleFailure(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Chat.Presentation.Messages.GenerationError =
		"configured safe generation failure"
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:           "configured/offering",
			ModelID:      "configured-model",
			Capability:   tool.FallbackCapability,
			Enabled:      true,
			PollInterval: time.Second,
			Timeout:      5 * time.Second,
		}},
		submitErr: errors.New(
			"secret upstream diagnostic: bearer credential",
		),
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: &fakeReferences{},
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := json.Marshal(map[string]string{
		application.Chat.Context.ToolPromptArgument: "paint",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, executeErr := executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-a",
				Name:      tool.Name,
				Arguments: arguments,
			},
			Tool:    tool,
			Options: chat.TurnOptions{AIModel: "configured-model"},
		},
		nil,
	)
	if executeErr == nil ||
		!strings.Contains(executeErr.Error(), "secret upstream diagnostic") {
		t.Fatalf("diagnostic error = %v", executeErr)
	}
	if result.Error != "configured safe generation failure" ||
		strings.Contains(result.Error, "secret upstream diagnostic") {
		t.Fatalf("visible result error = %q", result.Error)
	}
	var agentResult map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &agentResult); err != nil {
		t.Fatal(err)
	}
	if agentResult[tool.ResultFields.Error] !=
		"configured safe generation failure" ||
		strings.Contains(
			result.AgentContent,
			"secret upstream diagnostic",
		) {
		t.Fatalf("agent result = %#v", agentResult)
	}
}

type fakeGateway struct {
	offerings []generation.Offering
	tasks     []generation.Task
	submitted generation.SubmitCommand
	submitErr error
	index     int
	// unavailable names Offerings whose runtime configuration is missing.
	unavailable map[generation.OfferingID]bool
}

func TestExecutorRejectsMissingExplicitReferenceBeforePaidSubmit(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name,
			Arguments: []byte(`{"prompt":"edit this image","reference_image_indices":[1]}`)},
		Options: chat.TurnOptions{AIModel: "configured-model"},
	}, nil)
	if err == nil || !result.NoSubmission {
		t.Fatalf("invalid reference must fail before submission: result=%#v err=%v", result, err)
	}
	if gateway.submitted.OfferingID != "" {
		t.Fatalf("invalid reference created paid task: %#v", gateway.submitted)
	}
}

func (gateway *fakeGateway) Offerings(
	generation.Capability,
) []generation.Offering {
	return append([]generation.Offering(nil), gateway.offerings...)
}

func (gateway *fakeGateway) OfferingAvailable(
	id generation.OfferingID,
) bool {
	return !gateway.unavailable[id]
}

func (gateway *fakeGateway) Submit(
	_ context.Context,
	command generation.SubmitCommand,
) (generation.Accepted, error) {
	gateway.submitted = command
	return generation.Accepted{TaskID: "configured-task"}, gateway.submitErr
}

func (gateway *fakeGateway) Get(
	_ context.Context,
	_ generation.OwnerID,
	_ generation.TaskID,
) (generation.Task, error) {
	task := gateway.tasks[gateway.index]
	gateway.index++
	return task, nil
}

type fakeReferences struct {
	urls []string
}

func (resolver *fakeReferences) Resolve(
	_ context.Context,
	_ generation.OwnerID,
	urls []string,
) ([]generation.AssetID, error) {
	resolver.urls = append(resolver.urls, urls...)
	return []generation.AssetID{"configured-input-asset"}, nil
}

func TestExecutorResolvesMaskArgumentToOwnedAsset(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:           "configured/offering",
			ModelID:      "configured-model",
			Capability:   tool.Capability,
			Enabled:      true,
			PollInterval: time.Second,
			Timeout:      5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "configured-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
					Required: true,
				},
				{
					Name: "configured-mask",
					Type: "asset",
				},
			},
		}},
		tasks: []generation.Task{
			{ID: "configured-task", State: generation.TaskRunning},
			{
				ID:     "configured-task",
				State:  generation.TaskSucceeded,
				Assets: []generation.Asset{{ID: "configured-asset", Role: "image"}},
			},
		},
	}
	resolver := &fakeReferences{}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: resolver,
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
		Clock:      func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.ArgumentMappings = map[string]string{
		"prompt": "prompt",
		"mask":   "mask",
	}
	tool.OverrideMappings = map[string]string{"mask": "configured-mask"}
	tool.PromptWithReferenceTemplate = "{prompt}"
	tool.PromptWithPreviousTemplate = "{previous_prompt}{prompt}"
	tool.IdempotencyKeyTemplate = "configured:{session_id}:{call_id}"
	tool.ResultFields = catalog.ChatToolResultFields{
		Success:   "configured-success",
		TaskID:    "configured-task-field",
		ImageURLs: "configured-images",
		Error:     "configured-error",
	}
	if _, err := executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-mask",
				Name:      tool.Name,
				Arguments: []byte(`{"prompt":"paint","mask":"/configured/mask.png"}`),
			},
			Tool:    tool,
			Options: chat.TurnOptions{AIModel: "configured-model"},
		},
		func(chat.ToolObservation) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted["configured-mask"] != "configured-input-asset" {
		t.Fatalf("submitted mask = %#v", submitted)
	}
	if len(resolver.urls) != 1 || resolver.urls[0] != "/configured/mask.png" {
		t.Fatalf("resolved urls = %#v", resolver.urls)
	}
}

// offering 不声明 mask 参数时，LLM 传来的 mask 必须被丢弃而不是让整次
// 生成失败（R1-7 回归锁定）。
func TestExecutorDropsUndeclaredMaskArgument(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:           "configured/offering",
			ModelID:      "configured-model",
			Capability:   tool.Capability,
			Enabled:      true,
			PollInterval: time.Second,
			Timeout:      5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "configured-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
					Required: true,
				},
			},
		}},
		tasks: []generation.Task{
			{ID: "configured-task", State: generation.TaskRunning},
			{
				ID:     "configured-task",
				State:  generation.TaskSucceeded,
				Assets: []generation.Asset{{ID: "configured-asset", Role: "image"}},
			},
		},
	}
	resolver := &fakeReferences{}
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: resolver,
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
		Clock:      func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.ArgumentMappings = map[string]string{
		"prompt": "prompt",
		"mask":   "mask",
	}
	tool.OverrideMappings = map[string]string{"mask": "configured-mask"}
	tool.PromptWithReferenceTemplate = "{prompt}"
	tool.PromptWithPreviousTemplate = "{previous_prompt}{prompt}"
	tool.IdempotencyKeyTemplate = "configured:{session_id}:{call_id}"
	tool.ResultFields = catalog.ChatToolResultFields{
		Success:   "configured-success",
		TaskID:    "configured-task-field",
		ImageURLs: "configured-images",
		Error:     "configured-error",
	}
	if _, err := executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-mask-undeclared",
				Name:      tool.Name,
				Arguments: []byte(`{"prompt":"paint","mask":"/configured/mask.png"}`),
			},
			Tool:    tool,
			Options: chat.TurnOptions{AIModel: "configured-model"},
		},
		func(chat.ToolObservation) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if _, exists := submitted["configured-mask"]; exists {
		t.Fatalf("undeclared mask leaked into input: %#v", submitted)
	}
	// 未声明参数不应触发资产解析。
	if len(resolver.urls) != 0 {
		t.Fatalf("resolved urls = %#v", resolver.urls)
	}
}

// remove_background 声明 ignore_user_model：用户所选的生图模型不能把
// 抠图工具过滤成"无可用 Offering"（R1-8 回归锁定）。
func TestExecutorRemoveBackgroundIgnoresUserModel(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "remove_background" {
			tool = candidate
			break
		}
	}
	if !tool.IgnoreUserModel {
		t.Fatal("remove_background must declare ignore_user_model in catalog")
	}
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:           "matting/offering",
			ModelID:      "matting-model",
			Capability:   tool.Capability,
			Enabled:      true,
			PollInterval: time.Second,
			Timeout:      5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "matting-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
				},
			},
		}},
		tasks: []generation.Task{
			{ID: "matting-task", State: generation.TaskRunning},
			{
				ID:     "matting-task",
				State:  generation.TaskSucceeded,
				Assets: []generation.Asset{{ID: "matting-asset", Role: "image"}},
			},
		},
	}
	resolver := &fakeReferences{}
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: resolver,
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
		Clock:      func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.IdempotencyKeyTemplate = "configured:{session_id}:{call_id}"
	tool.PromptWithReferenceTemplate = "{prompt}"
	tool.PromptWithPreviousTemplate = "{previous_prompt}{prompt}"
	if _, err := executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:   "owner-a",
			SessionID: "session-a",
			Call: agents.ToolCall{
				ID:        "call-matting",
				Name:      tool.Name,
				Arguments: []byte(`{"prompt":"cut out subject"}`),
			},
			Tool: tool,
			// 用户选的是生图模型，与抠图 offering 的 model_id 无关。
			Options: chat.TurnOptions{AIModel: "gpt-image-user-model"},
		},
		func(chat.ToolObservation) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	if gateway.submitted.OfferingID != "matting/offering" {
		t.Fatalf("submitted offering = %q", gateway.submitted.OfferingID)
	}
	if gateway.submitted.Capability != tool.Capability {
		t.Fatalf("submitted capability = %q", gateway.submitted.Capability)
	}
}

// B11 恢复重放：带 BoundTaskID 的调用直接观察等待既有任务，
// 不重建输入（避免引用重传改变请求哈希）、不重复提交。
func TestExecutorObservesBoundTaskWithoutResubmitting(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	tool := application.Chat.Tools[0]
	gateway := &fakeGateway{
		offerings: []generation.Offering{{
			ID:         "configured/offering",
			ModelID:    "configured-model",
			Capability: tool.Capability,
			Enabled:    true,
			Timeout:    5 * time.Second,
			Parameters: []generation.Parameter{
				{
					Name:     "configured-prompt",
					Type:     "string",
					Semantic: generation.SemanticPrompt,
				},
			},
		}},
		tasks: []generation.Task{
			{ID: "persisted-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskRunning,
				Offering: generation.Offering{ID: "historic/offering", ModelID: "historic-model", Timeout: 5 * time.Second, PollInterval: time.Second}},
			{
				ID: "persisted-task", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
				Offering: generation.Offering{ID: "historic/offering", ModelID: "historic-model", Timeout: 5 * time.Second, PollInterval: time.Second},
				Assets:   []generation.Asset{{ID: "persisted-asset", Role: "image"}},
			},
		},
	}
	resolver := &fakeReferences{}
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	executor, err := generationtool.New(generationtool.Config{
		Gateway:    gateway,
		References: resolver,
		Chat:       application.Chat,
		TaskStates: application.TaskStates,
		Clock:      func() time.Time { return now },
		Sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.IdempotencyKeyTemplate = "configured:{session_id}:{call_id}"
	tool.ResultFields = catalog.ChatToolResultFields{
		Success:   "configured-success",
		TaskID:    "configured-task-field",
		ImageURLs: "configured-images",
		Error:     "configured-error",
	}
	result, err := executor.Execute(
		context.Background(),
		chat.ToolCommand{
			OwnerID:     "owner-a",
			SessionID:   "session-a",
			BoundTaskID: "persisted-task",
			Call: agents.ToolCall{
				ID:        "call-resumed",
				Name:      tool.Name,
				Arguments: []byte(`{"prompt":"paint"}`),
			},
			Tool:    tool,
			Options: chat.TurnOptions{AIModel: "model-no-longer-listed"},
		},
		func(chat.ToolObservation) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.submitted.Input != nil {
		t.Fatalf("bound task must not resubmit: %#v", gateway.submitted)
	}
	if len(resolver.urls) != 0 {
		t.Fatalf("references must not re-resolve: %#v", resolver.urls)
	}
	if result.TaskID != "persisted-task" {
		t.Fatalf("result = %#v", result)
	}
}

func TestBoundTaskCannotCrossSession(t *testing.T) {
	catalogue, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := catalogue.Application()
	gateway := &fakeGateway{tasks: []generation.Task{{
		ID: "task-a", OwnerID: "owner-a", SessionID: "session-a", State: generation.TaskSucceeded,
		Offering: generation.Offering{Timeout: time.Second},
	}}}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat,
		TaskStates: application.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-b", BoundTaskID: "task-a",
		Tool: application.Chat.Tools[0],
	}, nil)
	if err == nil {
		t.Fatal("bound Task from another session was observed")
	}
	if gateway.submitted.Input != nil {
		t.Fatal("cross-session Task triggered Submit")
	}
}

func TestExecutorUsesFrozenSkillContentAfterPublishedConfigurationChanges(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	tool := app.Chat.Tools[0]
	gateway := &fakeGateway{offerings: []generation.Offering{{ID: "configured/offering", ModelID: "configured-model", Capability: tool.FallbackCapability, Enabled: true, Features: []string{app.Chat.Skills.Feature}, Timeout: time.Second, Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true}, {Name: "quality", Type: "string", Semantic: generation.SemanticQuality}}}}, tasks: []generation.Task{{ID: "configured-task", State: generation.TaskSucceeded}}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat, Skills: catalog.SkillCatalogSchema{Revision: "new", Skills: []catalog.SkillSchema{{ID: "style", Prompt: "newly published style"}}}, TaskStates: app.TaskStates})
	if err != nil {
		t.Fatal(err)
	}
	frozenExecution := app.Chat.Skills.Execution
	frozenExecution.PromptTemplates = map[generation.Capability]string{generation.TextToImage: "frozen:{user_input}::{skill_prompt}"}
	args, _ := json.Marshal(map[string]string{app.Chat.Context.ToolPromptArgument: "user intent"})
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", Call: agents.ToolCall{ID: "frozen-call", Name: tool.Name, Arguments: args}, Tool: tool, Options: chat.TurnOptions{AIModel: "configured-model", SkillID: "style"}, Configuration: &chat.AgentConfigurationSnapshot{Revision: "old", Skills: []catalog.SkillSchema{{ID: "style", Prompt: "frozen old style"}}, SkillExecution: frozenExecution}}
	if _, err := executor.Execute(context.Background(), command, nil); err != nil {
		t.Fatal(err)
	}
	var submitted map[string]any
	if err := json.Unmarshal(gateway.submitted.Input, &submitted); err != nil {
		t.Fatal(err)
	}
	if prompt, ok := submitted["prompt"].(string); !ok || prompt != "frozen:user intent::frozen old style" || strings.Contains(prompt, "newly published style") {
		t.Fatalf("executed changed Skill content: %#v", submitted)
	}
}

func TestExecutorRejectsSkillMissingFromOwnerFrozenConfiguration(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	tool := app.Chat.Tools[0]
	gateway := &fakeGateway{offerings: []generation.Offering{{ID: "configured/offering", ModelID: "configured-model", Capability: tool.FallbackCapability, Enabled: true, Features: []string{app.Chat.Skills.Feature}, Timeout: time.Second, Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true}}}}}
	executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat, Skills: catalog.SkillCatalogSchema{Revision: "other-owner", Skills: []catalog.SkillSchema{{ID: "personal-other", Prompt: "private style"}}}, TaskStates: app.TaskStates})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{app.Chat.Context.ToolPromptArgument: "draw a cat"})
	result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", Call: agents.ToolCall{ID: "call", Name: tool.Name, Arguments: args}, Tool: tool, Options: chat.TurnOptions{AIModel: "configured-model", SkillID: "personal-other"}, Configuration: &chat.AgentConfigurationSnapshot{Revision: "owner-a", Skills: []catalog.SkillSchema{}}}, nil)
	if err != nil || !result.NoSubmission || gateway.submitted.OfferingID != "" {
		t.Fatalf("cross-owner Skill selection submitted: %#v %v", result, err)
	}
}

// A user-created scene reference must follow the explicit subject and priority
// rules. This verifies the prepared prompt, not model behavior.
func TestCustomSkillCompositionKeepsExplicitSubjectAndPriorityBeforeSceneReference(t *testing.T) {
	for _, limit := range []int{0, 800} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := product.Application()
			skill := catalog.SkillSchema{
				ID:              "custom-scene-reference",
				Name:            "Custom scene reference",
				Category:        "anime",
				RecommendedSize: "1536x1024",
				ThumbnailPath:   "/gpt-skills/custom-reference.webp",
				RawImageURL:     "/gpt-skills/custom-reference.webp",
				Prompt:          strings.Repeat("A quiet courtyard in watercolor style. ", 40),
			}
			skills := catalog.SkillCatalogSchema{Revision: "custom-test", Skills: []catalog.SkillSchema{skill}}
			tool := app.Chat.Tools[0]
			gateway := &fakeGateway{offerings: []generation.Offering{{ID: "configured/offering", ModelID: "configured-model", Capability: tool.FallbackCapability, Enabled: true, Features: []string{app.Chat.Skills.Feature}, Timeout: time.Second, Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true, MaxLength: limit}, {Name: "quality", Type: "string", Semantic: generation.SemanticQuality}}}}, tasks: []generation.Task{{ID: "configured-task", State: generation.TaskSucceeded}}}
			executor, err := generationtool.New(generationtool.Config{Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat, Skills: skills, TaskStates: app.TaskStates})
			if err != nil {
				t.Fatal(err)
			}
			const intent = "An orange cat sleeping on a cushion."
			args, _ := json.Marshal(map[string]string{app.Chat.Context.ToolPromptArgument: intent})
			_, err = executor.Execute(context.Background(), chat.ToolCommand{OwnerID: "owner", SessionID: "session", Call: agents.ToolCall{ID: "call", Name: tool.Name, Arguments: args}, Tool: tool, Options: chat.TurnOptions{AIModel: "configured-model", SkillID: skill.ID}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err := json.Unmarshal(gateway.submitted.Input, &input); err != nil {
				t.Fatal(err)
			}
			prompt, ok := input["prompt"].(string)
			if !ok || !strings.HasPrefix(prompt, intent) {
				t.Fatal("explicit user subject was replaced")
			}
			rule := strings.Index(prompt, "Composition priority:")
			reference := strings.Index(prompt, "Selected Skill reference:")
			if rule < 0 || reference < 0 || rule >= reference || !strings.Contains(prompt, "only when the user explicitly requests that scene") {
				t.Fatal("priority policy was omitted or moved after preset scene")
			}
			if limit == 0 && !strings.Contains(prompt, skill.Prompt) {
				t.Fatal("selected Skill content was rewritten")
			}
			if limit > 0 && len([]rune(prompt)) != limit {
				t.Fatal("Offering prompt limit was bypassed")
			}
		})
	}
}

// A provider whose account has no balance left is told to the Agent and the
// user as such (Schema text), while any other failed task keeps the generic
// wording and never carries upstream text.
func TestExecutorTellsAnExhaustedProviderBalanceApartFromOtherFailures(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	tool := application.Chat.Tools[0]
	balance := application.Chat.Presentation.Messages.GenerationFailedByClass[string(generation.FailureInsufficientBalance)]
	generic := application.Chat.Presentation.Messages.GenerationFailed
	if balance == "" || balance == generic {
		t.Fatalf("Schema must declare a distinct balance text: %q vs %q", balance, generic)
	}
	for name, tc := range map[string]struct {
		failure generation.Failure
		want    string
	}{
		"balance":  {generation.Failure{Class: generation.FailureInsufficientBalance, SafeDetail: "upstream said: insufficient balance acct-123"}, balance},
		"rejected": {generation.Failure{Class: generation.FailureRejected, SafeDetail: "upstream said no"}, generic},
		// #326 N2: a source whose original task the provider no longer keeps
		// is named as such, with the way forward, never the upstream 404.
		"source expired": {generation.Failure{Class: generation.FailureSourceExpired, SafeDetail: "upstream said: Task not found acct-123"},
			application.Chat.Presentation.Messages.GenerationFailedByClass[string(generation.FailureSourceExpired)]},
	} {
		t.Run(name, func(t *testing.T) {
			failure := tc.failure
			gateway := &fakeGateway{
				offerings: []generation.Offering{{
					ID: "configured/offering", ModelID: "configured-model", Capability: tool.FallbackCapability,
					Enabled: true, PollInterval: time.Second, Timeout: 5 * time.Second,
				}},
				tasks: []generation.Task{{
					ID: "configured-task", OwnerID: "owner-a", SessionID: "session-a",
					State: generation.TaskFailed, Failure: &failure,
				}},
			}
			executor, err := generationtool.New(generationtool.Config{
				Gateway: gateway, References: &fakeReferences{}, Chat: application.Chat, TaskStates: application.TaskStates,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, execErr := executor.Execute(context.Background(), chat.ToolCommand{
				OwnerID: "owner-a", SessionID: "session-a", Tool: tool,
				Call:    agents.ToolCall{ID: "call-a", Name: tool.Name, Arguments: []byte(`{"prompt":"paint"}`)},
				Options: chat.TurnOptions{AIModel: "configured-model"},
			}, nil)
			if execErr == nil || result.Error != tc.want {
				t.Fatalf("error=%v result.Error=%q want %q", execErr, result.Error, tc.want)
			}
			if strings.Contains(result.AgentContent, "acct-123") || strings.Contains(result.AgentContent, "upstream said") {
				t.Fatalf("upstream text reached the Agent: %s", result.AgentContent)
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil || content[tool.ResultFields.Error] != tc.want {
				t.Fatalf("agent content = %s (%v)", result.AgentContent, err)
			}
		})
	}
}

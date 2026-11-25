package generationtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #612 / #613: the two 2D generation tools of the image workspace choose their
// model by one rule, for a text-to-image and an image-to-image call alike. A
// workspace without a picked model does not refuse the call, and a model the
// person must confirm is asked on an in-chat model card instead of a refusal
// the person has no control for.

// capabilityGateway lists only the Offerings of the asked capability, as the
// real gateway does.
type capabilityGateway struct{ *fakeGateway }

func (gateway capabilityGateway) Offerings(capability generation.Capability) []generation.Offering {
	var found []generation.Offering
	for _, offering := range gateway.offerings {
		if offering.Capability == capability {
			found = append(found, offering)
		}
	}
	return found
}

// modelCardStore keeps the cards the executor opens, with ListChoices, so a
// second call of the same reply can find the open card.
type modelCardStore struct {
	created []chat.Choice
}

func (store *modelCardStore) CreateChoice(_ context.Context, choice chat.Choice) (chat.Choice, error) {
	choice.ID = fmt.Sprintf("choice-%d", len(store.created)+1)
	store.created = append(store.created, choice)
	return choice, nil
}

func (store *modelCardStore) ListChoices(context.Context, string, string, int) ([]chat.Choice, error) {
	return append([]chat.Choice(nil), store.created...), nil
}

func (*modelCardStore) GetChoice(context.Context, string, string, string) (chat.Choice, error) {
	return chat.Choice{}, chat.ErrChoiceNotFound
}

func (*modelCardStore) AnswerChoice(context.Context, string, string, string, chat.ChoiceRecord) (chat.Choice, error) {
	return chat.Choice{}, nil
}

func (*modelCardStore) ReopenChoice(context.Context, string, string, string, string) error {
	return nil
}

type workspaceModelHarness struct {
	product     *catalog.Catalog
	application catalog.ApplicationSchema
	gateway     capabilityGateway
}

func newWorkspaceModelHarness(t *testing.T) *workspaceModelHarness {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	gateway := capabilityGateway{&fakeGateway{
		tasks: []generation.Task{{ID: "sketch-task", State: generation.TaskSucceeded, Progress: 100}},
	}}
	for _, model := range []string{"gemini/gemini-3-pro-image", "meshy/nano-banana-pro", "meshy/nano-banana-2"} {
		for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
			offering, ok := product.Get(generation.OfferingID(model + "/" + string(capability)))
			if !ok {
				t.Fatalf("default catalog lacks %s/%s", model, capability)
			}
			gateway.offerings = append(gateway.offerings, offering)
		}
	}
	return &workspaceModelHarness{product: product, application: product.Application(), gateway: gateway}
}

func (h *workspaceModelHarness) tool(t *testing.T, name string) catalog.ChatToolSchema {
	t.Helper()
	for _, tool := range h.application.Chat.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("default catalog lacks tool %s", name)
	return catalog.ChatToolSchema{}
}

func (h *workspaceModelHarness) executor(t *testing.T, store chat.ChoiceStore) *generationtool.Executor {
	t.Helper()
	executor, err := generationtool.New(generationtool.Config{
		Gateway: h.gateway, References: &fakeReferences{}, Chat: h.application.Chat,
		TaskStates: h.application.TaskStates, Choices: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func (h *workspaceModelHarness) command(tool catalog.ChatToolSchema, capability generation.Capability,
	workspace string, brief chat.ProductionBrief, callID string) chat.ToolCommand {
	command := chat.ToolCommand{
		OwnerID: "owner", SessionID: "session", AssistantMessageID: "reply-1", Tool: tool,
		Options: chat.TurnOptions{CreativeDomain: "image", AIModel: workspace, ProductionBrief: brief},
		Call:    agents.ToolCall{ID: callID, Name: tool.Name, Arguments: json.RawMessage(`{"prompt":"a bearded axe icon"}`)},
	}
	if capability == generation.ImageToImage {
		command.History = []chat.Message{{Role: h.application.Chat.Roles.User, ImageURLs: []string{"/owned-reference"}}}
	}
	return command
}

func conceptArtBrief() chat.ProductionBrief {
	return chat.ProductionBrief{"image_use": json.RawMessage(`"concept_art"`)}
}

// The shipped Schema gives both 2D generation tools the same model contract:
// the model argument with the derived choices, and no hard requirement of a
// workspace pick, which only the concept sketch tool used to declare.
func TestTheTwo2DGenerationToolsShareOneModelContract(t *testing.T) {
	h := newWorkspaceModelHarness(t)
	for _, name := range []string{"refine_image", "create_concept_sketch"} {
		tool := h.tool(t, name)
		if tool.ModelArgument != "model" || tool.ModelChoices == nil || tool.RequireUserModel || tool.IgnoreUserModel {
			t.Fatalf("%s model contract: argument=%q choices=%v require_user_model=%v ignore_user_model=%v",
				name, tool.ModelArgument, tool.ModelChoices, tool.RequireUserModel, tool.IgnoreUserModel)
		}
	}
	if h.application.Chat.Presentation.Messages.ModelChoiceCard == "" {
		t.Fatal("the Schema declares no text for the model choice card")
	}
}

// #612: four concept sketches in an image workspace with no picked model were
// all refused with "pick a model in the workspace first". The call now runs on
// the model the layers choose, like refine_image does, for both shapes.
func TestAConceptSketchWithoutAWorkspaceModelIsNotRefused(t *testing.T) {
	for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
		t.Run(string(capability), func(t *testing.T) {
			h := newWorkspaceModelHarness(t)
			tool := h.tool(t, "create_concept_sketch")
			executor := h.executor(t, &modelCardStore{})
			command := h.command(tool, capability, "", nil, "call-1")
			if message := executor.SelectionError(command); message != "" {
				t.Fatalf("preflight refused the sketch: %q", message)
			}
			result, err := executor.Execute(context.Background(), command, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.NoSubmission || h.gateway.submitted.OfferingID == "" {
				t.Fatalf("sketch was not submitted: result=%+v", result)
			}
			if h.gateway.submitted.Capability != capability {
				t.Fatalf("submitted %s, want %s", h.gateway.submitted.Capability, capability)
			}
		})
	}
}

// #613: a purpose the brief names recommends another model than the one the
// workspace picker shows. Both tools and both shapes ask the person on the same
// in-chat model card; nothing is submitted, and every option replays the call
// with its own model.
func TestAWorkspaceConflictOpensTheSameModelCardForEveryShapeAndTool(t *testing.T) {
	for _, name := range []string{"refine_image", "create_concept_sketch"} {
		for _, capability := range []generation.Capability{generation.TextToImage, generation.ImageToImage} {
			t.Run(name+"/"+string(capability), func(t *testing.T) {
				h := newWorkspaceModelHarness(t)
				tool := h.tool(t, name)
				store := &modelCardStore{}
				executor := h.executor(t, store)
				result, err := executor.Execute(context.Background(),
					h.command(tool, capability, "nano-banana-2", conceptArtBrief(), "call-1"), nil)
				if err != nil {
					t.Fatal(err)
				}
				if !result.NoSubmission || h.gateway.submitted.OfferingID != "" {
					t.Fatalf("conflict submitted a task: result=%+v submitted=%+v", result, h.gateway.submitted)
				}
				if len(store.created) != 1 {
					t.Fatalf("opened %d cards, want 1: %+v", len(store.created), result)
				}
				card := store.created[0]
				if card.ToolName != name || card.Capability != string(capability) || card.CallID != "call-1" ||
					card.Kind != "" || card.State != chat.ChoicePending {
					t.Fatalf("card = %+v", card)
				}
				wantFirst := "gemini/gemini-3-pro-image/" + string(capability)
				if len(card.Options) < 3 || card.Options[0].ID != wantFirst || !card.Options[0].Recommended {
					t.Fatalf("card options = %+v, want the purpose recommendation %s first", card.Options, wantFirst)
				}
				workspaceID := "meshy/nano-banana-2/" + string(capability)
				if _, ok := card.Option(workspaceID); !ok {
					t.Fatalf("card leaves out the workspace model %s: %+v", workspaceID, card.Options)
				}
				for _, option := range card.Options {
					var arguments map[string]any
					if err := json.Unmarshal(option.Arguments, &arguments); err != nil {
						t.Fatal(err)
					}
					if arguments["model"] != option.ID || arguments["prompt"] != "a bearded axe icon" {
						t.Fatalf("option %s replays %v", option.ID, arguments)
					}
				}
				var content map[string]any
				if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
					t.Fatalf("agent content %q: %v", result.AgentContent, err)
				}
				if content["choice_required"] != true || content["choice_id"] != "choice-1" {
					t.Fatalf("agent content = %v", content)
				}
				if _, plain := content["service_confirmation_required"]; plain {
					t.Fatalf("the conflict still answers with a plain refusal: %v", content)
				}
			})
		}
	}
}

// Several sketches of one reply that hit the same conflict share one card
// instead of each replacing the previous one.
func TestCallsOfOneReplyShareTheOpenModelCard(t *testing.T) {
	h := newWorkspaceModelHarness(t)
	tool := h.tool(t, "create_concept_sketch")
	store := &modelCardStore{}
	executor := h.executor(t, store)
	for _, callID := range []string{"call-1", "call-2", "call-3"} {
		result, err := executor.Execute(context.Background(),
			h.command(tool, generation.TextToImage, "nano-banana-2", conceptArtBrief(), callID), nil)
		if err != nil {
			t.Fatal(err)
		}
		var content map[string]any
		_ = json.Unmarshal([]byte(result.AgentContent), &content)
		if !result.NoSubmission || content["choice_id"] != "choice-1" {
			t.Fatalf("%s: result=%+v", callID, result)
		}
	}
	if len(store.created) != 1 {
		t.Fatalf("opened %d cards for one reply, want 1", len(store.created))
	}
}

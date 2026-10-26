package generationtool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

// hyper3DGLBTasks seed a finished model whose GLB the Hyper3D local-model
// Offerings accept as their source.
func hyper3DGLBTasks(h *processingHarness) []generation.Task {
	task := modelTask("model-1", "owner-a", "session-a", h.meshy, "text-to-3d", "glb")
	task.Assets[0].MediaType = "model/gltf-binary"
	task.Assets[0].SHA256 = "glb-hash"
	return []generation.Task{task}
}

// conversationHarness saves a Harness for a Hyper3D Offering whose required
// reference image comes from the conversation (#187 D).
func conversationHarness(t *testing.T, offeringID, operation string, fillable []string, localizers ...*catalog.TextLocalizer) (*processingHarness, *fakeReferences, chat.ToolCommand) {
	t.Helper()
	h := newProcessingHarness(t, "auto", hyper3DGLBTasks)
	texts := (*catalog.TextLocalizer)(nil)
	if len(localizers) > 0 {
		texts = localizers[0]
	} else {
		var err error
		texts, err = catalog.NewTextLocalizer(h.app)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	repo := h.gateway.repo
	resolver := resolve.Service{Store: repo, Schema: h.app.Chat.Harness, Tools: h.app.Chat.Tools, Catalog: h.gateway.product, Gateway: h.gateway}
	offering, ok := h.gateway.product.Get(generation.OfferingID(offeringID))
	if !ok {
		t.Fatalf("missing %s", offeringID)
	}
	selection := harness.DefaultSelection("owner-a", "session-a")
	selection.OperationID = operation
	selection.EnabledOperations = []string{operation}
	selection.OfferingID = string(offering.ID)
	selection.OfferingRevision = offering.Revision
	selection.Sources = []harness.Source{{Parameter: "source_asset", TaskID: "model-1", AssetID: "model-1-glb"}}
	selection.FixedParameters = map[string]json.RawMessage{}
	selection.FillableParameters = fillable
	if _, err := resolver.Update(ctx, "owner-a", "session-a", 0, selection); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolver.Snapshot(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	references := &fakeReferences{}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Policy: h.policy(), Modes: sessionMode{mode: "auto"}}
	h.executor, err = generationtool.New(generationtool.Config{Gateway: harnessGateway{h.gateway}, Harness: repo, References: references, Operations: repo, Grants: grants, Chat: h.app.Chat, Texts: texts, StopRequested: func(context.Context, string, string, string) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	history := []chat.Message{{Role: h.app.Chat.Roles.User, Content: "use my picture", ImageURLs: []string{"/api/v1/assets/asset-reference"},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 1}}}}
	tool := h.tool(snapshot.ToolName)
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "conversation-call", Name: tool.Name}, Tool: tool, History: history, Options: chat.TurnOptions{Harness: &snapshot}}
	return h, references, command
}

func TestHarnessBindsTheConversationPictureAsTheRequiredReferenceImage(t *testing.T) {
	for _, tc := range []struct{ offering, operation string }{
		{"hyper3d/texture/local-model", "retexture"},
		{"hyper3d/bang/local-model", "mesh-segmentation"},
	} {
		t.Run(tc.offering, func(t *testing.T) {
			h, references, command := conversationHarness(t, tc.offering, tc.operation, []string{"prompt"})
			command.Call.Arguments = []byte(`{"offering_id":"` + tc.offering + `","parameters":{"reference_image":"@img1","prompt":"warm carved wood"}}`)
			result, err := h.executor.Execute(context.Background(), command, nil)
			if err != nil || result.NoSubmission || len(h.gateway.frozen) != 1 || len(h.gateway.submitted) != 1 {
				t.Fatalf("conversation picture not submitted: %+v %v frozen=%d", result, err, len(h.gateway.frozen))
			}
			input := h.frozenInput(0)
			if input["reference_image"] != "configured-input-asset" || input["source_asset"] != "model-1-glb" || input["prompt"] != "warm carved wood" {
				t.Fatalf("frozen input %+v", input)
			}
			if len(references.urls) != 1 || references.urls[0] != "/api/v1/assets/asset-reference" {
				t.Fatalf("reference resolved from %v", references.urls)
			}
			// The step still goes through its operation and execution grant.
			op, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "conversation-call")
			if err != nil || op.GrantRequestID == "" || op.Harness != command.Options.Harness.Selection.Binding {
				t.Fatalf("operation %+v %v", op, err)
			}
		})
	}
}

func TestHarnessRefusesAMissingOrUnknownConversationPictureBeforePreparing(t *testing.T) {
	for name, arguments := range map[string]string{
		"missing":   `{"parameters":{"prompt":"wood"}}`,
		"unknown":   `{"parameters":{"reference_image":"@img9"}}`,
		"asset id":  `{"parameters":{"reference_image":"asset-of-someone"}}`,
		"two":       `{"parameters":{"reference_image":["@img1","@img1"]}}`,
		"not image": `{"parameters":{"reference_image":7}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, references, command := conversationHarness(t, "hyper3d/texture/local-model", "retexture", []string{"prompt"})
			command.Call.Arguments = []byte(arguments)
			result, err := h.executor.Execute(context.Background(), command, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || len(h.gateway.frozen) != 0 || len(h.gateway.submitted) != 0 || result.Error == "" {
				t.Fatalf("unsafe call prepared %+v", result)
			}
			if name == "missing" {
				if !strings.Contains(result.Error, "材质参考图") || len(references.urls) != 0 {
					t.Fatalf("missing picture not named: %q", result.Error)
				}
				if want := strings.Split(h.app.Chat.Harness.MissingConversationInputMessage, "{parameters}")[1]; !strings.Contains(result.Error, want) {
					t.Fatalf("missing picture refusal %q lacks %q", result.Error, want)
				}
			}
			if _, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "conversation-call"); err == nil {
				t.Fatal("refusal created an operation")
			}
		})
	}
}

// A turn whose only gap is a parameter the person must set keeps its tool; the
// call names the missing input instead of failing silently (#187 D).
func TestHarnessNamesMissingSettingsParameters(t *testing.T) {
	h, _, command := conversationHarness(t, "hyper3d/texture/local-model", "retexture", []string{"prompt"})
	snapshot := *command.Options.Harness
	snapshot.Status = resolve.StatusNeedsInput
	snapshot.CallableWithMissing = true
	snapshot.MissingParameters = []string{"reference_image", "prompt"}
	command.Options.Harness = &snapshot
	command.Call.Arguments = []byte(`{"parameters":{"reference_image":"@img1"}}`)
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || len(h.gateway.frozen) != 0 {
		t.Fatalf("needs_input call prepared %+v %v", result, err)
	}
	if !strings.Contains(result.Error, "材质参考图、提示词") || !strings.Contains(result.Error, strings.Split(h.app.Chat.Harness.MissingParametersMessage, "{parameters}")[1]) {
		t.Fatalf("missing parameter refusal %q", result.Error)
	}
}

// The turn's frozen UI locale names the missing conversation picture and the
// separator between several Schema labels; IDs and user input stay untouched.
func TestHarnessNamesAMissingConversationPictureInTheTurnLocale(t *testing.T) {
	h, references, command := conversationHarness(t, "hyper3d/texture/local-model", "retexture", []string{"prompt"})
	command.Options.Locale = "en"
	command.Call.Arguments = []byte(`{"parameters":{"prompt":"wood"}}`)
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || len(h.gateway.frozen) != 0 || len(h.gateway.submitted) != 0 || len(references.urls) != 0 {
		t.Fatalf("refusal prepared work: %+v frozen=%d submitted=%d urls=%v", result,
			len(h.gateway.frozen), len(h.gateway.submitted), references.urls)
	}
	if !strings.Contains(result.Error, `Missing "Material reference image"`) ||
		strings.Contains(result.Error, "材质参考图") || strings.Contains(result.Error, "、") {
		t.Fatalf("English refusal = %q", result.Error)
	}
	if _, err := h.gateway.repo.LoadAgentOperation(context.Background(), "owner-a", "session-a", "turn-a", "conversation-call"); err == nil {
		t.Fatal("refusal created an operation")
	}
}

func TestHarnessNamesAnInvalidConversationPictureInTheTurnLocale(t *testing.T) {
	for name, arguments := range map[string]string{
		"number": `{"parameters":{"reference_image":7}}`,
		"object": `{"parameters":{"reference_image":{"handle":"@img1"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, references, command := conversationHarness(t, "hyper3d/texture/local-model", "retexture", []string{"prompt"})
			command.Options.Locale = "en"
			command.Call.Arguments = []byte(arguments)
			result, err := h.executor.Execute(context.Background(), command, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || len(h.gateway.frozen) != 0 || len(h.gateway.submitted) != 0 || len(references.urls) != 0 {
				t.Fatalf("refusal prepared work: %+v frozen=%d submitted=%d urls=%v", result,
					len(h.gateway.frozen), len(h.gateway.submitted), references.urls)
			}
			if !strings.Contains(result.Error, `Missing "Material reference image"`) ||
				strings.Contains(result.Error, "材质参考图") || strings.Contains(result.Error, "、") {
				t.Fatalf("English refusal = %q", result.Error)
			}
		})
	}
}

func TestHarnessNamesMissingSettingsInTheTurnLocale(t *testing.T) {
	h, _, command := conversationHarness(t, "hyper3d/texture/local-model", "retexture", []string{"prompt"})
	snapshot := *command.Options.Harness
	snapshot.Status = resolve.StatusNeedsInput
	snapshot.CallableWithMissing = true
	snapshot.MissingParameters = []string{"reference_image", "prompt"}
	command.Options.Harness = &snapshot
	command.Options.Locale = "en"
	command.Call.Arguments = []byte(`{"parameters":{"reference_image":"@img1"}}`)
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || len(h.gateway.frozen) != 0 {
		t.Fatalf("needs_input call prepared %+v %v", result, err)
	}
	if !strings.Contains(result.Error, `Missing "Material reference image, Prompt"`) ||
		strings.Contains(result.Error, "材质参考图") || strings.Contains(result.Error, "、") {
		t.Fatalf("English missing parameter refusal %q", result.Error)
	}
}

// A legacy caller without a localizer keeps the Schema's default text and
// separator.
func TestHarnessWithoutALocalizerKeepsTheDefaultText(t *testing.T) {
	h, _, command := conversationHarness(t, "hyper3d/texture/local-model", "retexture", []string{"prompt"}, nil)
	snapshot := *command.Options.Harness
	snapshot.Status = resolve.StatusNeedsInput
	snapshot.CallableWithMissing = true
	snapshot.MissingParameters = []string{"reference_image", "prompt"}
	command.Options.Harness = &snapshot
	command.Call.Arguments = []byte(`{"parameters":{"reference_image":"@img1"}}`)
	result, err := h.executor.Execute(context.Background(), command, nil)
	if err != nil || !result.NoSubmission || len(h.gateway.frozen) != 0 {
		t.Fatalf("needs_input call prepared %+v %v", result, err)
	}
	if !strings.Contains(result.Error, "材质参考图、提示词") {
		t.Fatalf("default missing parameter refusal %q", result.Error)
	}
}

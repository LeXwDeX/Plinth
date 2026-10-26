package generationtool_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// purposeRig runs the shipped refine_image declaration against a fake
// gateway that records the frozen submission and the reference order.
type purposeRig struct {
	schema   catalog.ChatSchema
	tool     catalog.ChatToolSchema
	gateway  *fakeGateway
	resolver *fakeReferences
	executor *generationtool.Executor
}

func newPurposeRig(t *testing.T, maxReferences int) *purposeRig {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	rig := &purposeRig{schema: application.Chat, tool: application.Chat.Tools[0], resolver: &fakeReferences{}}
	if rig.tool.Name != "refine_image" || rig.tool.ReferencePurposesArgument == "" {
		t.Fatalf("the shipped refine tool must declare reference purposes: %+v", rig.tool.Name)
	}
	rig.gateway = &fakeGateway{offerings: []generation.Offering{{
		ID: "configured/edit", ModelID: "configured-model", Capability: rig.tool.Capability, Enabled: true,
		PollInterval: time.Second, Timeout: 5 * time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "images", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: maxReferences},
		},
	}}, tasks: []generation.Task{{ID: "configured-task", State: generation.TaskRunning}}}
	rig.executor, err = generationtool.New(generationtool.Config{
		Gateway: rig.gateway, References: rig.resolver, Chat: application.Chat, TaskStates: application.TaskStates,
		Clock: func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return rig
}

// poseAndOutfitHistory is the #279 scenario: an earlier generated picture of
// the white-clad gunman whose pose the user wants to keep (@img3), and an
// upload whose outfit should be used (@img7), followed by today's request.
func poseAndOutfitHistory(schema catalog.ChatSchema) []chat.Message {
	return []chat.Message{
		{Role: schema.Roles.User, Content: "白衣持枪的人，调整一下姿势"},
		{Role: schema.Roles.Assistant, ImageURLs: []string{"/api/v1/tasks/task-pose/assets/pose"},
			ToolCalls: []chat.ToolCall{{Name: "refine_image", TaskID: "task-pose", Arguments: []byte(`{"prompt":"white-clad gunman, new pose"}`)}},
			Attachments: []chat.Attachment{{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated,
				TaskID: "task-pose", AssetID: "pose", ImageSlot: 1}}},
		{Role: schema.Roles.User, Content: "这是衣服参考", ImageURLs: []string{"/api/v1/input-assets/outfit"},
			Attachments: []chat.Attachment{{Handle: "@img7", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload,
				AssetID: "outfit", ImageSlot: 1}}},
		{Role: schema.Roles.Assistant, Content: "收到"},
		{Role: schema.Roles.User, Content: "保留白衣持枪图调整后的姿势，参考另一张图换衣服"},
	}
}

func (rig *purposeRig) run(t *testing.T, command chat.ToolCommand, arguments string) chat.ToolResult {
	t.Helper()
	command.OwnerID, command.SessionID = "owner-a", "session-a"
	command.Tool = rig.tool
	command.Call = agents.ToolCall{ID: "call-1", Name: rig.tool.Name, Arguments: []byte(arguments)}
	command.Options.AIModel = "configured-model"
	if command.History == nil {
		command.History = poseAndOutfitHistory(rig.schema)
	}
	result, err := rig.executor.Execute(context.Background(), command, func(chat.ToolObservation) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (rig *purposeRig) submittedPrompt(t *testing.T) string {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal(rig.gateway.submitted.Input, &input); err != nil {
		t.Fatalf("nothing was submitted: %v", err)
	}
	prompt, _ := input["prompt"].(string)
	return prompt
}

func (rig *purposeRig) expectedPrompt(items, prompt string) string {
	return strings.NewReplacer("{purposes}", items, "{prompt}", prompt).Replace(rig.tool.PromptWithReferencePurposesTemplate)
}

func (rig *purposeRig) item(position, purpose string) string {
	return strings.NewReplacer("{position}", position, "{purpose}", purpose).Replace(rig.tool.ReferencePurposeItemTemplate)
}

// #279: each picture's purpose reaches the frozen generation input, numbered
// in the order the pictures are really sent, whatever order the Agent wrote
// them in; the prompt and the reference order cannot cross.
func TestReferencePurposesReachTheFrozenInputInSendOrder(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"the gunman wearing the new outfit",
		"reference_handles":["@img3","@img7"],
		"reference_purposes":{"@img7":"take the outfit from this image","@img3":"keep this character and its pose"}}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/tasks/task-pose/assets/pose", "/api/v1/input-assets/outfit"}) {
		t.Fatalf("frozen reference order = %v", got)
	}
	want := rig.expectedPrompt(rig.item("1", "keep this character and its pose")+rig.item("2", "take the outfit from this image"),
		"the gunman wearing the new outfit")
	if got := rig.submittedPrompt(t); got != want {
		t.Fatalf("prompt = %q\nwant     %q", got, want)
	}

	// The same purposes with the pictures named the other way round follow
	// the new order: the numbering is the order of the frozen references.
	rig = newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"p","reference_handles":["@img7","@img3"],
		"reference_purposes":{"@img7":"outfit","@img3":"pose"}}`)
	if got := rig.submittedPrompt(t); got != rig.expectedPrompt(rig.item("1", "outfit")+rig.item("2", "pose"), "p") {
		t.Fatalf("reordered prompt = %q", got)
	}
}

// On a canvas turn the artboard snapshot is always sent first (#266), so the
// purposes of the added history pictures are numbered after it.
func TestReferencePurposesCountTheBoundCanvasSnapshot(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{Options: chat.TurnOptions{
		InputSource: rig.schema.Context.CanvasInputSource,
		Selection: []chat.Attachment{{Handle: "@img9", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCanvas,
			AssetID: "board", URL: "/api/v1/input-assets/board", Canvas: &chat.CanvasLayout{Width: 1024, Height: 1024}}},
	}}, `{"prompt":"p","reference_handles":["@img7"],"reference_purposes":{"@img7":"outfit","@img9":"layout to keep"}}`)
	if got := rig.resolver.urls; !slices.Equal(got, []string{"/api/v1/input-assets/board", "/api/v1/input-assets/outfit"}) {
		t.Fatalf("frozen reference order = %v", got)
	}
	if got := rig.submittedPrompt(t); got != rig.expectedPrompt(rig.item("1", "layout to keep")+rig.item("2", "outfit"), "p") {
		t.Fatalf("prompt = %q", got)
	}
}

// A purpose for a picture the call does not send is refused before anything
// is submitted, with the Schema's wording, instead of silently dropping the
// purpose or the picture.
func TestReferencePurposesForUnsentPicturesAreRefusedBeforeSubmission(t *testing.T) {
	for name, test := range map[string]struct {
		maxReferences int
		arguments     string
		handle        string
	}{
		"not among the references": {4, `{"prompt":"p","reference_handles":["@img3"],"reference_purposes":{"@img3":"pose","@img7":"outfit"}}`, "@img7"},
		"not in this session":      {4, `{"prompt":"p","reference_handles":["@img3"],"reference_purposes":{"@img42":"outfit"}}`, "@img42"},
		"cut by the model's limit": {1, `{"prompt":"p","reference_handles":["@img3","@img7"],"reference_purposes":{"@img3":"pose","@img7":"outfit"}}`, "@img7"},
		"the whole selection":      {4, `{"prompt":"p","reference_handles":["@img3"],"reference_purposes":{"@selected":"pose"}}`, "@selected"},
		"no picture at all":        {4, `{"prompt":"p","reference_purposes":{"@img3":"pose"}}`, "@img3"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newPurposeRig(t, test.maxReferences)
			result := rig.run(t, chat.ToolCommand{History: []chat.Message{
				poseAndOutfitHistory(rig.schema)[1], poseAndOutfitHistory(rig.schema)[2],
				{Role: rig.schema.Roles.User, Content: "a fresh request"}}}, test.arguments)
			want := strings.ReplaceAll(rig.schema.Context.ReferencePurposeInvalidTemplate, "{handle}", test.handle)
			if !result.NoSubmission || result.Error != want {
				t.Fatalf("result = %+v; want refusal %q", result, want)
			}
			if rig.gateway.submitted.OfferingID != "" || len(rig.resolver.urls) != 0 {
				t.Fatalf("a refused call reached the gateway: %+v", rig.gateway.submitted)
			}
		})
	}
}

// Without purposes the existing prompt templates apply unchanged.
func TestReferencePurposesAreOptional(t *testing.T) {
	rig := newPurposeRig(t, 4)
	rig.run(t, chat.ToolCommand{}, `{"prompt":"p","reference_handles":["@img7"],"reference_purposes":{}}`)
	if got := rig.submittedPrompt(t); got != strings.ReplaceAll(rig.tool.PromptWithReferenceTemplate, "{prompt}", "p") {
		t.Fatalf("prompt = %q", got)
	}
}

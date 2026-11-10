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

type styleLockRig struct {
	app      catalog.ApplicationSchema
	tool     catalog.ChatToolSchema
	gateway  *fakeGateway
	resolver *fakeReferences
	executor *generationtool.Executor
}

type filteredStyleGateway struct{ *fakeGateway }

func (gateway filteredStyleGateway) Offerings(capability generation.Capability) []generation.Offering {
	var offerings []generation.Offering
	for _, offering := range gateway.fakeGateway.offerings {
		if offering.Capability == capability {
			offerings = append(offerings, offering)
		}
	}
	return offerings
}

func newStyleLockRig(t *testing.T, referenceLimit int) *styleLockRig {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	tool := app.Chat.Tools[0]
	tool.ResultFields.ReferenceNote = "reference_note"
	tool.PromptWithReferenceTemplate = "REF:{prompt}"
	tool.PromptWithPreviousTemplate = "PREV:{previous_prompt}|{prompt}"
	tool.PromptWithReferencePurposesTemplate = "PURPOSE:{purposes}|{prompt}"
	app.Chat.StyleLock.PromptTemplate = " STYLE[{style}]"
	app.Chat.StyleLock.EditScopeTemplate = " EDIT_SCOPE"
	text := generation.Offering{
		ID: "configured/text", ModelID: "configured-model", Capability: generation.TextToImage,
		Domain: generation.DomainImage, Enabled: true, Default: true,
		PollInterval: time.Second, Timeout: time.Second,
		Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true}},
	}
	edit := generation.Offering{
		ID: "configured/edit", ModelID: "configured-model", Capability: generation.ImageToImage,
		Domain: generation.DomainImage, Enabled: true, Default: true,
		PollInterval: time.Second, Timeout: time.Second,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true},
			{Name: "images", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: referenceLimit},
		},
	}
	rig := &styleLockRig{
		app: app, tool: tool, resolver: &fakeReferences{},
		gateway: &fakeGateway{offerings: []generation.Offering{text, edit}, tasks: []generation.Task{{State: generation.TaskSucceeded, Offering: edit}}},
	}
	rig.gateway.tasks[0].Offering = edit
	rig.executor, err = generationtool.New(generationtool.Config{
		Gateway: filteredStyleGateway{rig.gateway}, References: rig.resolver, Chat: app.Chat, TaskStates: app.TaskStates,
		InputAssetRoute: "/api/v1/input-assets/{assetID}",
		Clock:           func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) },
		Sleep:           func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return rig
}

func (rig *styleLockRig) run(t *testing.T, options chat.TurnOptions, history []chat.Message, args string) (chat.ToolResult, map[string]any) {
	t.Helper()
	if history == nil {
		history = []chat.Message{{Role: rig.app.Chat.Roles.User, Content: "draw"}}
	}
	result, err := rig.executor.Execute(context.Background(), chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", Tool: rig.tool, History: history,
		Options: options, Call: agents.ToolCall{ID: "style-call", Name: rig.tool.Name, Arguments: []byte(args)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if len(rig.gateway.submitted.Input) > 0 {
		if err := json.Unmarshal(rig.gateway.submitted.Input, &input); err != nil {
			t.Fatal(err)
		}
	}
	return result, input
}

func styleLock(enabled bool, text string, references ...string) chat.TurnOptions {
	return chat.TurnOptions{
		AIModel:   "configured-model",
		StyleLock: chat.StyleLock{Enabled: enabled, Text: text, References: references},
	}
}

func imageHistory() []chat.Message {
	return []chat.Message{{
		Role: "user", Content: "edit this", ImageURLs: []string{"/api/v1/input-assets/agent-image"},
		Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "agent-image", ImageSlot: 1}},
	}}
}

func TestStyleLockAppliesToTextReferencePurposeAndPreviousPromptPaths(t *testing.T) {
	t.Run("text-to-image without references", func(t *testing.T) {
		rig := newStyleLockRig(t, 3)
		result, input := rig.run(t, styleLock(true, "ink wash"), nil, `{"prompt":"a house"}`)
		if result.NoSubmission || input["prompt"] != "a house STYLE[ink wash]" {
			t.Fatalf("result=%+v input=%#v", result, input)
		}
	})

	t.Run("style references precede the agent image", func(t *testing.T) {
		rig := newStyleLockRig(t, 3)
		options := styleLock(true, "ink wash", "style-a")
		result, input := rig.run(t, options, imageHistory(), `{"prompt":"recolor","reference_handles":["@img1"]}`)
		if result.NoSubmission || input["prompt"] != "PURPOSE:image 1: 沿用用户风格锁的画风. |recolor STYLE[ink wash] EDIT_SCOPE" {
			t.Fatalf("result=%+v input=%#v", result, input)
		}
		if len(rig.resolver.urls) != 2 || rig.resolver.urls[0] != "/api/v1/input-assets/style-a" || rig.resolver.urls[1] != "/api/v1/input-assets/agent-image" {
			t.Fatalf("reference order=%v", rig.resolver.urls)
		}
	})

	t.Run("reference purposes stay scoped to agent handles", func(t *testing.T) {
		rig := newStyleLockRig(t, 3)
		options := styleLock(true, "ink wash", "style-a")
		options.AttachmentPurposes = map[string]string{"@img1": "input"}
		result, input := rig.run(t, options, imageHistory(), `{"prompt":"recolor","reference_handles":["@img1"],"reference_purposes":{"@img1":"keep the subject"}}`)
		if result.NoSubmission || !strings.Contains(input["prompt"].(string), "keep the subject") || !strings.HasSuffix(input["prompt"].(string), " STYLE[ink wash] EDIT_SCOPE") {
			t.Fatalf("result=%+v input=%#v", result, input)
		}
	})

	t.Run("previous prompt remains the source of the edit template", func(t *testing.T) {
		rig := newStyleLockRig(t, 3)
		history := []chat.Message{
			{Role: rig.app.Chat.Roles.User, ImageURLs: []string{"/api/v1/tasks/old/assets/image"}},
			{Role: rig.app.Chat.Roles.Assistant, ToolCalls: []chat.ToolCall{{Name: rig.tool.Name, TaskID: "old", Arguments: []byte(`{"prompt":"old prompt"}`)}}},
		}
		result, input := rig.run(t, styleLock(true, "ink wash"), history, `{"prompt":"new prompt","reference_image_indices":[1]}`)
		if result.NoSubmission || input["prompt"] != "PREV:old prompt|new prompt STYLE[ink wash] EDIT_SCOPE" {
			t.Fatalf("result=%+v input=%#v", result, input)
		}
	})
}

func TestStyleLockCanBeDisabledOrSkipped(t *testing.T) {
	for name, options := range map[string]chat.TurnOptions{
		"disabled": styleLock(false, "ink wash"),
		"skipped":  func() chat.TurnOptions { o := styleLock(true, "ink wash"); o.StyleLockSkipped = true; return o }(),
	} {
		t.Run(name, func(t *testing.T) {
			rig := newStyleLockRig(t, 3)
			result, input := rig.run(t, options, nil, `{"prompt":"a house"}`)
			if result.NoSubmission || input["prompt"] != "a house" {
				t.Fatalf("result=%+v input=%#v", result, input)
			}
		})
	}
}

func TestStyleReferencesOutrankAndSurviveTheReferenceLimit(t *testing.T) {
	rig := newStyleLockRig(t, 2)
	result, _ := rig.run(t, styleLock(true, "ink wash", "style-a", "style-b"), imageHistory(), `{"prompt":"recolor","reference_handles":["@img1"]}`)
	if result.NoSubmission || len(rig.resolver.urls) != 2 || rig.resolver.urls[0] != "/api/v1/input-assets/style-a" || rig.resolver.urls[1] != "/api/v1/input-assets/style-b" {
		t.Fatalf("result=%+v references=%v", result, rig.resolver.urls)
	}
	if !strings.Contains(result.AgentContent, "丢弃了普通参考 @img1") {
		t.Fatalf("trim note missing from result: %s", result.AgentContent)
	}
}

func TestStyleLockDoesNotApplyTo3DAndRejectsUnresolvableStyleURLs(t *testing.T) {
	t.Run("3D", func(t *testing.T) {
		rig := newStyleLockRig(t, 3)
		rig.tool = catalog.ChatToolSchema{Name: "create_3d", Capability: "text-to-3d", FallbackCapability: "text-to-3d"}
		rig.gateway.offerings = []generation.Offering{{ID: "configured/3d", ModelID: "3d-model", Capability: generation.TextTo3D, Domain: generation.DomainModel3D, Enabled: true, Default: true,
			Parameters: []generation.Parameter{{Name: "prompt", Type: "string", Semantic: generation.SemanticPrompt, Required: true}}}}
		options := styleLock(true, "ink wash", "style-a")
		options.AIModel = "3d-model"
		result, input := rig.run(t, options, nil, `{"prompt":"a robot"}`)
		if result.NoSubmission || input["prompt"] != "a robot" || len(rig.resolver.urls) != 0 {
			t.Fatalf("result=%+v input=%#v references=%v", result, input, rig.resolver.urls)
		}
	})

	t.Run("missing input-asset route", func(t *testing.T) {
		rig := newStyleLockRig(t, 3)
		rig.executor, _ = generationtool.New(generationtool.Config{
			Gateway: filteredStyleGateway{rig.gateway}, References: rig.resolver, Chat: rig.app.Chat, TaskStates: rig.app.TaskStates,
			Clock: func() time.Time { return time.Now() }, Sleep: func(context.Context, time.Duration) error { return nil },
		})
		result, _ := rig.run(t, styleLock(true, "ink wash", "style-a"), nil, `{"prompt":"a house"}`)
		if !result.NoSubmission || !strings.Contains(result.Error, "风格锁定参考图") || rig.gateway.submitted.OfferingID != "" {
			t.Fatalf("result=%+v submitted=%#v", result, rig.gateway.submitted)
		}
	})
}

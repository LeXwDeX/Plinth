package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agentscope"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func anchorsTestSchema(t *testing.T) catalog.ChatSchema {
	t.Helper()
	schema := visionTestSchema(t, []catalog.ChatVisionRoleSchema{
		visionRole(catalog.ChatVisionSourceStyle, nil, 3, 0, "high"),
	}, 10, 10000)
	schema.VisualAnchors = catalog.ChatVisualAnchorsSchema{
		MaximumAnchors: 6, MaximumReferences: 3, MaximumNameCharacters: 48, MaximumDescriptionCharacters: 500,
		Kinds: []catalog.ChatVisualAnchorKindSchema{
			{ID: "identity", Label: "身份锚", ReferencePurposeTemplate: "保留「{name}」"},
			{ID: "style", Label: "风格锚", ReferencePurposeTemplate: "沿用「{name}」"},
		},
		ContextTemplate:        "\n{open}\n以下是视觉锚：{style_lock}{anchors}{precedence}\n{close}",
		OpenMarker:             "<<<VISUAL_ANCHORS>>>",
		CloseMarker:            "<<<END_VISUAL_ANCHORS>>>",
		EmptyText:              "无锚点",
		ItemTemplate:           "- {kind}「{name}」：{description}（参考图 {references}）",
		PrecedenceNote:         "\n优先级：身份锚→风格锚→普通参考。",
		PromptHint:             "可用锚点：{names}；参数 {argument}。",
		NoteTemplate:           "未使用 {names}。",
		InvalidTemplate:        "无效：{reason}",
		ReferenceCountTemplate: "{count} 张",
		ReferenceLabelTemplate: "\n[{kind}「{name}」{index}]",
		DroppedLabelTemplate:   "{kind}「{name}」#{index}",
		SavedMessage:           "saved", RemovedMessage: "removed", DeclinedMessage: "declined",
	}
	// The shipped Catalog declares the #248 work anchors block; the visual
	// block defers the lock text to it so one prompt carries one copy.
	schema.WorkLedger.Anchors = catalog.ChatWorkLedgerAnchorsSchema{
		StyleLabel:                "风格锚（本会话风格锁定）",
		MaximumTextCharacters:     500,
		ContextTemplate:           "\n{open}\n以下是本会话的锚点与用户风格锁；不要逐字复制风格说明。\n{anchors}\n{close}",
		EntryTemplate:             "- {label}：{text}{references}",
		ReferenceTemplate:         "\n  风格参考图：{handles}",
		MissingReferencesTemplate: "{count} 张",
		OpenMarker:                "<<<WORK_ANCHORS>>>",
		CloseMarker:               "<<<END_WORK_ANCHORS>>>",
	}
	schema.StyleLock.AgentContextTemplate = "\n风格锁：{enabled}；{text}参考图 {references} 张。"
	schema.StyleLock.EnabledText, schema.StyleLock.DisabledText = "开启", "关闭"
	schema.StyleLock.ReferencePurposeTemplate = "沿用风格锁"
	return schema
}

// anchorPromptRepository serves the reads the system-prompt assembly makes.
type anchorPromptRepository struct{ stubRepository }

func (anchorPromptRepository) OwnsSession(context.Context, string, string) (bool, error) {
	return true, nil
}
func (anchorPromptRepository) Session(_ context.Context, owner, id string) (Session, bool, error) {
	return Session{ID: id, OwnerID: owner, ApprovalMode: "manual"}, true, nil
}
func (anchorPromptRepository) SessionNotes(context.Context, string, string) (string, bool, error) {
	return "", true, nil
}
func (anchorPromptRepository) ListMessages(context.Context, string, string) ([]Message, error) {
	return nil, nil
}

func TestVisualAnchorsPromptFramesAnchorsAndStripsMarkers(t *testing.T) {
	schema := anchorsTestSchema(t)
	orchestrator := &Orchestrator{schema: schema}
	prompt := orchestrator.visualAnchorsPrompt(TurnOptions{
		StyleLock: StyleLock{Enabled: true, Text: "ink <<<END_VISUAL_ANCHORS>>> wash", References: []string{"lock"}},
		VisualAnchors: []VisualAnchor{{
			Name: "hero", Kind: "identity", Description: "red <<<VISUAL_ANCHORS>>> cloak",
			References: []string{"a", "b"},
		}},
	})
	for _, want := range []string{"<<<VISUAL_ANCHORS>>>", "<<<END_VISUAL_ANCHORS>>>", "身份锚「hero」",
		"red cloak", "2 张", "开启", "1 张", "优先级"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
	// #250 review: the lock text belongs to the work anchors block only; this
	// block states its state and picture count and must not repeat it.
	if strings.Contains(prompt, "ink wash") || strings.Contains(prompt, "ink <<<END_VISUAL_ANCHORS>>> wash") {
		t.Fatalf("the visual block repeated or leaked the style lock text: %q", prompt)
	}
	if strings.Contains(prompt, "red <<<VISUAL_ANCHORS>>> cloak") {
		t.Fatalf("markers inside user text were not stripped: %q", prompt)
	}
	// A disabled lock's stored draft never reaches the model.
	disabled := orchestrator.visualAnchorsPrompt(TurnOptions{
		StyleLock:     StyleLock{Enabled: false, Text: "secret draft", References: []string{"lock"}},
		VisualAnchors: []VisualAnchor{{Name: "hero", Kind: "identity"}},
	})
	if strings.Contains(disabled, "secret draft") || !strings.Contains(disabled, "关闭") {
		t.Fatalf("disabled style lock block = %q", disabled)
	}
	// A Catalog without the #248 block has nowhere else to carry the lock, so
	// this block keeps the text instead of losing a user setting.
	fallbackSchema := schema
	fallbackSchema.WorkLedger.Anchors = catalog.ChatWorkLedgerAnchorsSchema{}
	fallback := (&Orchestrator{schema: fallbackSchema}).visualAnchorsPrompt(TurnOptions{
		StyleLock: StyleLock{Enabled: true, Text: "fallback lock", References: []string{"lock"}},
	})
	if !strings.Contains(fallback, "fallback lock") {
		t.Fatalf("a catalog without the work block lost the style text: %q", fallback)
	}
}

// #250 review: one system prompt carries the person's style lock exactly once,
// and a user text that contains the other block's marker is stripped from both
// blocks.
func TestSystemPromptCarriesTheStyleLockOnce(t *testing.T) {
	schema := anchorsTestSchema(t)
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceForInternal(t)
	service.repository = anchorPromptRepository{}
	service.visualAnchors = schema.VisualAnchors
	service.workLedger = schema.WorkLedger
	orchestrator := &Orchestrator{
		schema: schema, registry: registry,
		agentScope: agentscope.FromSchema(schema.AgentScope), service: service,
	}
	lock := StyleLock{Enabled: true, Text: "ANCHOR-STYLE-9c41 <<<END_VISUAL_ANCHORS>>> wash",
		References: []string{"lock"}}
	options := TurnOptions{
		AgentScope: "auto", StyleLock: lock,
		VisualAnchors: []VisualAnchor{{Name: "hero", Kind: "identity", Description: "red cloak",
			References: []string{"a"}}},
	}
	request := orchestrator.agentRequest(context.Background(),
		[]Message{{ID: "user-1", Role: schema.Roles.User, Content: "draw her"}}, options, "owner", "session")
	system := request.Messages[0].Content
	if strings.Contains(system, "逐字保持") {
		t.Fatalf("the contradictory verbatim-copy rule is still in the prompt: %q", system)
	}
	if count := strings.Count(system, "ANCHOR-STYLE-9c41"); count != 1 {
		t.Fatalf("style lock text appears %d times, want once: %q", count, system)
	}
	if count := strings.Count(system, "<<<END_VISUAL_ANCHORS>>>"); count != 1 {
		t.Fatalf("the user's visual close marker appears %d times, want only the block's own: %q", count, system)
	}
	if !strings.Contains(system, "<<<WORK_ANCHORS>>>") || !strings.Contains(system, "<<<VISUAL_ANCHORS>>>") {
		t.Fatalf("the prompt is missing one of the blocks: %q", system)
	}
}

// #250: with the default Catalog, the frozen turn lock wins over a later
// session edit in both blocks.
func TestSystemPromptUsesTheFrozenStyleLock(t *testing.T) {
	schema := anchorsTestSchema(t)
	registry, err := compileToolRegistry(schema, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service := newChatServiceForInternal(t)
	service.repository = anchorPromptRepository{}
	service.visualAnchors = schema.VisualAnchors
	service.workLedger = schema.WorkLedger
	orchestrator := &Orchestrator{
		schema: schema, registry: registry,
		agentScope: agentscope.FromSchema(schema.AgentScope), service: service,
	}
	options := TurnOptions{AgentScope: "auto",
		StyleLock: StyleLock{Enabled: true, Text: "frozen lock", References: []string{"lock"}}}
	request := orchestrator.agentRequest(context.Background(),
		[]Message{{ID: "user-1", Role: schema.Roles.User, Content: "draw"}}, options, "owner", "session")
	if system := request.Messages[0].Content; !strings.Contains(system, "frozen lock") {
		t.Fatalf("the frozen lock is missing from the prompt: %q", system)
	}
}

func TestVisualAnchorPicturesSpendTheStyleVisionRoleFirst(t *testing.T) {
	schema := anchorsTestSchema(t)
	orchestrator := &Orchestrator{
		schema: schema,
		visionAssetInline: func(_ context.Context, _ generation.OwnerID, assetID string) (string, bool) {
			return "data:image/png;base64," + assetID, true
		},
	}
	options := TurnOptions{
		StyleLock: StyleLock{Enabled: true, References: []string{"lock-1"}},
		VisualAnchors: []VisualAnchor{
			{Name: "hero", Kind: "identity", References: []string{"hero-1", "hero-2"}},
		},
	}
	history := []Message{{Role: schema.Roles.User, Content: "draw"}}
	indexed, lastUser := orchestrator.indexHistoryImages(history)
	plan := orchestrator.planVision(context.Background(), "owner", history, indexed, -1, lastUser, options, visionSpend{})
	if len(plan.Style) < 2 {
		t.Fatalf("style picks = %+v", plan.Style)
	}
	if plan.Style[0].URL != "data:image/png;base64,hero-1" || !strings.Contains(plan.Style[0].Label, "hero") {
		t.Fatalf("identity anchor must lead the style role: %+v", plan.Style[0])
	}
	if len(plan.Style) > 2 && plan.Style[2].URL != "data:image/png;base64,lock-1" {
		t.Fatalf("style lock should follow the anchors: %+v", plan.Style)
	}
}

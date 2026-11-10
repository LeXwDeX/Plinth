package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func briefNumber(value int) *int { return &value }

// testModel3DUsage is a small purpose table in the shape of the shipped one:
// two purposes, a bounded text field, a boolean and an integer.
func testModel3DUsage() catalog.ChatModel3DUsageSchema {
	return catalog.ChatModel3DUsageSchema{
		DefaultProfile: "render",
		Profiles: []catalog.ChatModel3DUsageProfile{
			{
				ID: "render", Label: "展示/渲染", Description: "展示、预览或宣传用模型。",
				Reason: "展示用途优先完整细节。",
				RecommendedOfferings: map[string][]string{
					"text-to-3d": {"tripo/tripo-v3.1-full/text-to-3d"},
				},
				Budget: catalog.ChatModel3DUsageBudget{MaxTriangles: 2000000},
			},
			{
				ID: "game_character", Label: "游戏/实时·角色", Description: "游戏中的角色。",
				Reason: "角色需要受控面数与 UV。",
				RecommendedOfferings: map[string][]string{
					"text-to-3d": {"tripo/tripo-p2/text-to-3d"},
				},
				Budget: catalog.ChatModel3DUsageBudget{
					MinTriangles: briefNumber(5000), MaxTriangles: 60000, RequireUV: true,
				},
			},
		},
		Brief: catalog.ChatProductionBriefSchema{
			Fields: []catalog.ChatProductionBriefField{
				{Name: "purpose", Type: "profile", Label: "用途"},
				{Name: "image_use", Type: "image_profile", Label: "2D 用途"},
				{Name: "style", Type: "text", Label: "风格", MaximumCharacters: 20},
				{Name: "needs_rig", Type: "boolean", Label: "需要绑骨"},
				{Name: "triangle_budget", Type: "integer", Label: "三角面预算", Minimum: 100, Maximum: 2000000},
			},
		},
	}
}

func usageProfileKind() catalog.ChatChoiceKindSchema {
	return catalog.ChatChoiceKindSchema{
		ID: "usage_profile", OptionSource: "usage_profiles",
		ToolName: "update_production_brief", Argument: "purpose",
		Question: "这次模型主要用来做什么？", Message: "请在下方卡片中选择用途。",
	}
}

// testImage2DUsage is a small 2D purpose table in the shipped shape (#239).
func testImage2DUsage() catalog.ChatImage2DUsageSchema {
	return catalog.ChatImage2DUsageSchema{
		DefaultProfile: "concept_art",
		Profiles: []catalog.ChatImage2DUsageProfile{
			{
				ID: "concept_art", Label: "概念设定图", Description: "多主体概念图。",
				Reason: "概念图优先构图与氛围。",
				RecommendedOfferings: map[string][]string{
					"text-to-image":  {"meshy/nano-banana-pro/text-to-image"},
					"image-to-image": {"meshy/nano-banana-pro/image-to-image"},
				},
				Requirements: catalog.ChatImage2DRequirements{
					AspectRatio: "16:9", MinimumShortEdge: 1024, Background: "any", Subjects: "multiple",
				},
			},
			{
				ID: "illustration", Label: "角色立绘", Description: "单角色立绘。",
				Reason: "立绘需要稳定的身份与竖画幅。",
				RecommendedOfferings: map[string][]string{
					"text-to-image": {"meshy/nano-banana-pro/text-to-image"},
				},
				Requirements: catalog.ChatImage2DRequirements{
					AspectRatio: "2:3", MinimumShortEdge: 1536, Background: "any", Subjects: "single",
				},
			},
		},
		RequirementsNote: catalog.ChatImage2DRequirementsNote{
			Template: "{items}", Separator: "；",
			AspectRatioTemplate: "画幅 {value}", MinimumShortEdgeTemplate: "短边至少 {value} 像素",
			BackgroundTemplate: "背景{value}", SubjectsTemplate: "主体{value}", PoseTemplate: "姿势 {value}",
			BackgroundTexts: map[string]string{"any": "不限", "solid": "纯色", "transparent": "透明", "solid_or_transparent": "纯色或透明"},
			SubjectsTexts:   map[string]string{"single": "单个", "multiple": "可多个"},
			PoseTexts:       map[string]string{"a-pose": "A-pose", "t-pose": "T-pose"},
		},
	}
}

func imageUsageProfileKind() catalog.ChatChoiceKindSchema {
	return catalog.ChatChoiceKindSchema{
		ID: "image_usage_profile", OptionSource: "image_usage_profiles",
		ToolName: "update_production_brief", Argument: "image_use",
		Question: "这张图主要用来做什么？", Message: "请选择 2D 用途。",
	}
}

func directionChoiceKind() catalog.ChatChoiceKindSchema {
	return catalog.ChatChoiceKindSchema{
		ID: "direction", OptionSource: "agent",
		ToolName: "update_production_brief", Argument: "style",
		Question: "想要哪个方向？", Message: "请选择方向。",
	}
}

func TestAskChoiceBuildsAUsageProfileCardAndTheBriefSettlesIt(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-b", OwnerName: "B", Title: "b", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = testModel3DUsage()
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{usageProfileKind()}
	})
	service.SetChoices(repository, 20)
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		Call: agents.ToolCall{ID: "call-1", Name: "ask_choice"},
	}
	result, err := service.AskChoice(ctx, command, usageProfileKind(), chat.AskChoiceArguments{
		Kind: "usage_profile", Recommended: "game_character",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.TaskID != "" {
		t.Fatalf("a choice card must not submit anything: %+v", result)
	}
	var opened struct {
		Success        bool   `json:"success"`
		ChoiceRequired bool   `json:"choice_required"`
		ChoiceID       string `json:"choice_id"`
		Kind           string `json:"kind"`
		Recommended    string `json:"recommended"`
		Question       string `json:"question"`
		Message        string `json:"message"`
		Options        []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Recommended bool   `json:"recommended"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Success || !opened.ChoiceRequired || opened.Kind != "usage_profile" ||
		opened.Recommended != "game_character" || opened.Question != usageProfileKind().Question ||
		opened.Message != usageProfileKind().Message || len(opened.Options) != 2 {
		t.Fatalf("opened card = %+v", opened)
	}
	if opened.Options[0].ID != "render" || opened.Options[0].Label != "展示/渲染" ||
		opened.Options[0].Description != "展示、预览或宣传用模型。" || opened.Options[0].Recommended {
		t.Fatalf("first option = %+v", opened.Options[0])
	}
	if opened.Options[1].ID != "game_character" || !opened.Options[1].Recommended {
		t.Fatalf("recommended option = %+v", opened.Options[1])
	}

	// The card is stored with the target tool and the exact argument each
	// option continues with.
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 {
		t.Fatalf("stored cards = %+v %v", choices, err)
	}
	card := choices[0]
	if card.ToolName != "update_production_brief" || card.Kind != "usage_profile" || card.State != chat.ChoicePending {
		t.Fatalf("stored card = %+v", card)
	}
	var arguments map[string]string
	if err := json.Unmarshal(card.Options[1].Arguments, &arguments); err != nil || arguments["purpose"] != "game_character" {
		t.Fatalf("option arguments = %s %v", card.Options[1].Arguments, err)
	}

	// Another owner cannot open a card on this session.
	foreign, err := service.AskChoice(ctx, chat.ToolCommand{
		OwnerID: "owner-b", SessionID: "session-a", AssistantMessageID: "turn-2",
		Call: agents.ToolCall{ID: "call-2", Name: "ask_choice"},
	}, usageProfileKind(), chat.AskChoiceArguments{Kind: "usage_profile"})
	if err != nil || !foreign.NoSubmission || foreign.Error != "session not found" {
		t.Fatalf("foreign card = %+v %v", foreign, err)
	}

	// Writing the purpose answers the open card as the Agent, not as a click.
	_, brief, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"game_character"`),
		"style":   json.RawMessage(`"写实"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if brief.Text("purpose") != "game_character" || brief.Text("style") != "写实" {
		t.Fatalf("stored brief = %+v", brief)
	}
	choices, err = service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 || choices[0].State != chat.ChoiceAnswered ||
		choices[0].AnswerOptionID != "game_character" || choices[0].AnswerSource != chat.ChoiceAnsweredByAgent {
		t.Fatalf("settled card = %+v %v", choices, err)
	}
	stored, err := service.SessionBrief(ctx, "owner-a", "session-a")
	if err != nil || stored.Text("purpose") != "game_character" {
		t.Fatalf("session brief = %+v %v", stored, err)
	}

	if _, err := service.SessionBrief(ctx, "owner-b", "session-a"); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("another owner's brief read = %v", err)
	}
	if _, _, err := service.SetProductionBrief(ctx, "owner-b", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"render"`),
	}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("another owner's brief write = %v", err)
	}
	var invalid *chat.BriefError
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"missing"`),
	}); !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "未声明") {
		t.Fatalf("unknown purpose = %v", err)
	}
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"style": json.RawMessage(`"` + strings.Repeat("长", 21) + `"`),
	}); !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "不能超过") {
		t.Fatalf("over-long style = %v", err)
	}
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"unknown": json.RawMessage(`1`),
	}); !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "未知字段") {
		t.Fatalf("unknown field = %v", err)
	}
}

// The brief write settles the usage-profile kind by the id and argument the
// Schema declares, so renaming either needs no code change (#103).
func TestBriefSettlesTheUsageProfileKindDeclaredByTheSchema(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	kind := catalog.ChatChoiceKindSchema{
		ID: "purpose_card", OptionSource: "usage_profiles",
		ToolName: "update_production_brief", Argument: "profile",
		Question: "用途？", Message: "请选择用途。",
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = testModel3DUsage()
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{kind}
	})
	service.SetChoices(repository, 20)
	if _, err := service.AskChoice(ctx, chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		Call: agents.ToolCall{ID: "call-1", Name: "ask_choice"},
	}, kind, chat.AskChoiceArguments{Kind: kind.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"game_character"`),
	}); err != nil {
		t.Fatal(err)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 || choices[0].Kind != kind.ID ||
		choices[0].State != chat.ChoiceAnswered || choices[0].AnswerOptionID != "game_character" {
		t.Fatalf("settled card = %+v %v", choices, err)
	}
}

// #103 review C2: the choice-card continuation replays
// update_production_brief with only the purpose, so the tool must merge that
// field instead of replacing the whole brief.
func TestUpdateProductionBriefKeepsFieldsTheChoiceCardReplayOmits(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = testModel3DUsage()
	})
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose":         json.RawMessage(`"game_character"`),
		"style":           json.RawMessage(`"写实"`),
		"needs_rig":       json.RawMessage(`true`),
		"triangle_budget": json.RawMessage(`40000`),
	}); err != nil {
		t.Fatal(err)
	}
	previous, stored, err := service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"render"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if previous.Text("purpose") != "game_character" || previous.Text("style") != "写实" {
		t.Fatalf("previous brief = %+v", previous)
	}
	if stored.Text("purpose") != "render" || stored.Text("style") != "写实" ||
		!stored.Flag("needs_rig") {
		t.Fatalf("the choice replay cleared the rest of the brief: %+v", stored)
	}
	if value, ok := stored.Integer("triangle_budget"); !ok || value != 40000 {
		t.Fatalf("triangle budget after the replay = %d %v", value, ok)
	}
	// A present empty value clears only that field.
	_, stored, err = service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"style": json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Text("style") != "" || stored.Text("purpose") != "render" || !stored.Flag("needs_rig") {
		t.Fatalf("clearing style changed another field: %+v", stored)
	}
}

// #103 review C5: a writer that lands between the read and the conditional
// write makes the service retry on the new state instead of overwriting it,
// and the returned previous value is the one the audit may report.
func TestUpdateProductionBriefRetriesAConcurrentChangeWithoutLosingIt(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = testModel3DUsage()
	})
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose":   json.RawMessage(`"game_character"`),
		"style":     json.RawMessage(`"写实"`),
		"needs_rig": json.RawMessage(`true`),
	}); err != nil {
		t.Fatal(err)
	}
	interposed := 0
	repository.interposeBrief = func(current chat.ProductionBrief) (chat.ProductionBrief, bool) {
		if interposed > 0 {
			return nil, false
		}
		interposed++
		next := chat.ProductionBrief{}
		for name, value := range current {
			next[name] = value
		}
		next["style"] = json.RawMessage(`"并发"`)
		return next, true
	}
	previous, stored, err := service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"render"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if interposed != 1 {
		t.Fatalf("interposed %d times, want one retry", interposed)
	}
	if stored.Text("purpose") != "render" || stored.Text("style") != "并发" || !stored.Flag("needs_rig") {
		t.Fatalf("the retry lost the concurrent change: %+v", stored)
	}
	if previous.Text("style") != "并发" || previous.Text("purpose") != "game_character" {
		t.Fatalf("previous brief is not the state the retry wrote from: %+v", previous)
	}
}

// A persistent conflict stops after the bounded retries instead of writing
// over the other writer.
func TestUpdateProductionBriefStopsAfterPersistentConflicts(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = testModel3DUsage()
	})
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"game_character"`),
	}); err != nil {
		t.Fatal(err)
	}
	interposed := 0
	repository.interposeBrief = func(current chat.ProductionBrief) (chat.ProductionBrief, bool) {
		interposed++
		next := chat.ProductionBrief{}
		for name, value := range current {
			next[name] = value
		}
		next["style"] = json.RawMessage(`"并发` + strconv.Itoa(interposed) + `"`)
		return next, true
	}
	if _, _, err := service.UpdateProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"render"`),
	}); !errors.Is(err, chat.ErrProductionBriefConflict) {
		t.Fatalf("persistent conflict = %v", err)
	}
	brief, err := service.SessionBrief(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if brief.Text("purpose") != "game_character" {
		t.Fatalf("a refused write changed the purpose: %+v", brief)
	}
}

// #103 review C3: the refusal text comes from the Schema's brief messages, so
// the Agent receives the deployment's wording rather than a hardcoded one.
func TestValidateProductionBriefUsesTheSchemaMessages(t *testing.T) {
	usage := testModel3DUsage()
	usage.Brief.Messages = catalog.ChatProductionBriefMessages{
		UnknownField:      `unknown field "{field}"`,
		NotString:         `"{field}" must be text`,
		ProfileUndeclared: `purpose "{value}" is not declared`,
		TextTooLong:       `"{field}" exceeds {maximum}`,
		OutOfRange:        `"{field}" must be {minimum}-{maximum}`,
		NotInteger:        `"{field}" must be an integer`,
		NotBoolean:        `"{field}" must be a boolean`,
	}
	for _, test := range []struct {
		name string
		raw  map[string]json.RawMessage
		want string
	}{
		{name: "unknown field", raw: map[string]json.RawMessage{"colour": json.RawMessage(`"red"`)},
			want: `unknown field "colour"`},
		{name: "undeclared purpose", raw: map[string]json.RawMessage{"purpose": json.RawMessage(`"missing"`)},
			want: `purpose "missing" is not declared`},
		{name: "text too long", raw: map[string]json.RawMessage{"style": json.RawMessage(`"` + strings.Repeat("长", 21) + `"`)},
			want: `"style" exceeds 20`},
		{name: "out of range", raw: map[string]json.RawMessage{"triangle_budget": json.RawMessage(`99`)},
			want: `"triangle_budget" must be 100-2000000`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, test.raw)
			var invalid *chat.BriefError
			if !errors.As(err, &invalid) || invalid.Reason != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateProductionBriefNormalizesAndBounds(t *testing.T) {
	usage := testModel3DUsage()
	tests := []struct {
		name    string
		raw     map[string]json.RawMessage
		want    string
		success bool
	}{
		{name: "a whole brief", raw: map[string]json.RawMessage{
			"purpose":         json.RawMessage(`" game_character "`),
			"style":           json.RawMessage(`"  写实  "`),
			"needs_rig":       json.RawMessage(`true`),
			"triangle_budget": json.RawMessage(`40000`),
		}, success: true},
		{name: "null clears a field", raw: map[string]json.RawMessage{
			"purpose": json.RawMessage(`"render"`),
			"style":   json.RawMessage(`null`),
		}, success: true},
		{name: "an unknown field", raw: map[string]json.RawMessage{"colour": json.RawMessage(`"red"`)}, want: "未知字段"},
		{name: "an unknown purpose", raw: map[string]json.RawMessage{"purpose": json.RawMessage(`"missing"`)}, want: "未声明"},
		{name: "text over the bound", raw: map[string]json.RawMessage{
			"style": json.RawMessage(`"` + strings.Repeat("长", 21) + `"`),
		}, want: "不能超过"},
		{name: "an integer below the bound", raw: map[string]json.RawMessage{
			"triangle_budget": json.RawMessage(`99`),
		}, want: "必须在"},
		{name: "an integer above the bound", raw: map[string]json.RawMessage{
			"triangle_budget": json.RawMessage(`2000001`),
		}, want: "必须在"},
		{name: "a fractional integer", raw: map[string]json.RawMessage{
			"triangle_budget": json.RawMessage(`100.5`),
		}, want: "必须是整数"},
		{name: "a boolean written as text", raw: map[string]json.RawMessage{
			"needs_rig": json.RawMessage(`"yes"`),
		}, want: "必须是布尔值"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, test.raw)
			if test.success {
				if err != nil {
					t.Fatalf("valid brief refused: %v", err)
				}
				return
			}
			var invalid *chat.BriefError
			if !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, test.want) {
				t.Fatalf("error = %v, want mention of %q", err, test.want)
			}
		})
	}
	brief, err := chat.ValidateProductionBrief(usage.Brief, usage.Profiles, nil, map[string]json.RawMessage{
		"purpose":         json.RawMessage(`" game_character "`),
		"style":           json.RawMessage(`" 写实 "`),
		"needs_rig":       json.RawMessage(`true`),
		"triangle_budget": json.RawMessage(`40000`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if brief.Text("purpose") != "game_character" || brief.Text("style") != "写实" || !brief.Flag("needs_rig") {
		t.Fatalf("normalized brief = %+v", brief)
	}
	if value, ok := brief.Integer("triangle_budget"); !ok || value != 40000 {
		t.Fatalf("triangle budget = %d %v", value, ok)
	}
	encoded, err := chat.EncodeProductionBrief(brief)
	if err != nil || encoded == "" {
		t.Fatalf("encoded brief = %q %v", encoded, err)
	}
	if decoded := chat.DecodeProductionBrief(encoded); decoded.Text("purpose") != "game_character" {
		t.Fatalf("decoded brief = %+v", decoded)
	}
	if encoded, err := chat.EncodeProductionBrief(chat.ProductionBrief{}); err != nil || encoded != "" {
		t.Fatalf("empty brief = %q %v", encoded, err)
	}
	if decoded := chat.DecodeProductionBrief("not json"); !decoded.Empty() {
		t.Fatalf("malformed storage = %+v", decoded)
	}
}

// #103 review C4: the explicit 3D model lock is refused at write time when it
// names no enabled 3D Offering, so no later turn can get stuck on it.
func TestSetExplicitModel3DRefusesAModelTheValidatorRejects(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ExplicitModel3DValid = func(model string) bool { return model == "meshy-7.1" }
	})
	if _, err := service.SetExplicitModel3D(ctx, "owner-a", "session-a", "gpt-image-2.6-sunburst"); !errors.Is(err, chat.ErrInvalidExplicitModel3D) {
		t.Fatalf("a 2D model was accepted: %v", err)
	}
	stored, err := service.SessionExplicitModel3D(ctx, "owner-a", "session-a")
	if err != nil || stored != "" {
		t.Fatalf("refused pick was stored: %q %v", stored, err)
	}
	accepted, err := service.SetExplicitModel3D(ctx, "owner-a", "session-a", "meshy-7.1")
	if err != nil || accepted != "meshy-7.1" {
		t.Fatalf("a valid 3D model was refused: %q %v", accepted, err)
	}
	if stored, err := service.SessionExplicitModel3D(ctx, "owner-a", "session-a"); err != nil || stored != "meshy-7.1" {
		t.Fatalf("stored lock = %q %v", stored, err)
	}
}

// #239: the 2D purpose card is built from image2d_usage.profiles and its
// answer writes image_use; a brief write settles only the card whose argument
// matches the written field.
func TestImageUsageProfileCardAndBriefSettlement(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	for _, session := range []chat.Session{
		{ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now},
		{ID: "session-b", OwnerID: "owner-a", OwnerName: "A", Title: "b", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Model3DUsage = testModel3DUsage()
		config.Image2DUsage = testImage2DUsage()
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{usageProfileKind(), imageUsageProfileKind()}
		config.ChoiceMaxOptions = 8
	})
	service.SetChoices(repository, 20)
	commandFor := func(session, call string) chat.ToolCommand {
		return chat.ToolCommand{
			OwnerID: "owner-a", SessionID: session, AssistantMessageID: "turn-1",
			Call: agents.ToolCall{ID: call, Name: "ask_choice"},
		}
	}
	// A 3D card on one session and a 2D card on another are open at once.
	if _, err := service.AskChoice(ctx, commandFor("session-a", "call-3d"), usageProfileKind(),
		chat.AskChoiceArguments{Kind: "usage_profile"}); err != nil {
		t.Fatal(err)
	}
	result, err := service.AskChoice(ctx, commandFor("session-b", "call-2d"), imageUsageProfileKind(),
		chat.AskChoiceArguments{Kind: "image_usage_profile", Recommended: "illustration"})
	if err != nil {
		t.Fatal(err)
	}
	var opened struct {
		Kind        string `json:"kind"`
		Recommended string `json:"recommended"`
		Options     []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Kind != "image_usage_profile" || opened.Recommended != "illustration" || len(opened.Options) != 2 ||
		opened.Options[1].ID != "illustration" || opened.Options[1].Label != "角色立绘" {
		t.Fatalf("2D card = %+v", opened)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-b")
	if err != nil || len(choices) != 1 {
		t.Fatalf("stored 2D cards = %+v %v", choices, err)
	}
	var arguments map[string]string
	if err := json.Unmarshal(choices[0].Options[1].Arguments, &arguments); err != nil || arguments["image_use"] != "illustration" {
		t.Fatalf("2D option arguments = %s %v", choices[0].Options[1].Arguments, err)
	}
	// Writing the 2D purpose answers the 2D card; the 3D card is untouched.
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-b", map[string]json.RawMessage{
		"image_use": json.RawMessage(`"illustration"`),
	}); err != nil {
		t.Fatal(err)
	}
	choices, err = service.ListChoices(ctx, "owner-a", "session-b")
	if err != nil || len(choices) != 1 || choices[0].State != chat.ChoiceAnswered ||
		choices[0].AnswerOptionID != "illustration" || choices[0].AnswerSource != chat.ChoiceAnsweredByAgent {
		t.Fatalf("settled 2D card = %+v %v", choices, err)
	}
	choices, err = service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 || choices[0].State != chat.ChoicePending {
		t.Fatalf("3D card was settled by a 2D write: %+v %v", choices, err)
	}
	// Writing the 3D purpose settles the 3D card.
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-a", map[string]json.RawMessage{
		"purpose": json.RawMessage(`"game_character"`),
	}); err != nil {
		t.Fatal(err)
	}
	choices, err = service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 || choices[0].State != chat.ChoiceAnswered ||
		choices[0].AnswerOptionID != "game_character" {
		t.Fatalf("settled 3D card = %+v %v", choices, err)
	}
	// An undeclared 2D purpose is refused.
	var invalid *chat.BriefError
	if _, _, err := service.SetProductionBrief(ctx, "owner-a", "session-b", map[string]json.RawMessage{
		"image_use": json.RawMessage(`"missing"`),
	}); !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "未声明") {
		t.Fatalf("unknown 2D purpose = %v", err)
	}
}

// #239: a direction card carries Agent-supplied options; only bounded tokens
// reach the answer, a recommended id outside the list is ignored, and the
// option count is bounded.
func TestDirectionCardTakesBoundedAgentOptions(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlitetest.Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	now := time.Now().UTC()
	if _, err := repository.CreateSession(ctx, chat.Session{
		ID: "session-a", OwnerID: "owner-a", OwnerName: "A", Title: "a", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{directionChoiceKind()}
		config.ChoiceMaxOptions = 3
	})
	service.SetChoices(repository, 20)
	command := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		Call: agents.ToolCall{ID: "call-1", Name: "ask_choice"},
	}
	result, err := service.AskChoice(ctx, command, directionChoiceKind(), chat.AskChoiceArguments{
		Kind: "direction", Recommended: "missing-id",
		Options: []chat.ChoiceOptionInput{
			{ID: "watercolor", Label: "水彩"},
			{ID: "ink-2", Label: "水墨", Description: "淡墨与留白"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var opened struct {
		Recommended string `json:"recommended"`
		Options     []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Recommended != "" || len(opened.Options) != 2 || opened.Options[0].ID != "watercolor" {
		t.Fatalf("direction card = %+v", opened)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 {
		t.Fatalf("stored cards = %+v %v", choices, err)
	}
	var arguments map[string]string
	if err := json.Unmarshal(choices[0].Options[0].Arguments, &arguments); err != nil || arguments["style"] != "watercolor" {
		t.Fatalf("direction answer = %s %v", choices[0].Options[0].Arguments, err)
	}
	for _, bad := range [][]chat.ChoiceOptionInput{
		{{ID: "only-one", Label: "一个"}},
		{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}, {ID: "c", Label: "C"}, {ID: "d", Label: "D"}},
		{{ID: "free text", Label: "带空格"}},
		{{ID: "ok", Label: "好的"}, {ID: "x", Label: ""}},
	} {
		refused, err := service.AskChoice(ctx, command, directionChoiceKind(), chat.AskChoiceArguments{
			Kind: "direction", Options: bad,
		})
		if err != nil || !refused.NoSubmission || refused.Error == "" {
			t.Fatalf("bad direction options accepted: %+v %v", refused, err)
		}
	}
}

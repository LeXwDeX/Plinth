package chat_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #360: the quantity card asks how many low-cost sketches to draw and stores
// the answer with the choice card; the next reply draws and converges exactly
// that many, and one chosen sketch opens no direction card.
func TestConceptQuantityCardDrivesTheDirectionCard(t *testing.T) {
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
	quantityKind := catalog.ChatChoiceKindSchema{ID: "concept_quantity", OptionSource: "concept_quantities",
		ToolName: "create_concept_sketch", Question: "这次想先看几张概念图？", Message: "请选择数量。"}
	conceptKind := catalog.ChatChoiceKindSchema{ID: "concept_direction", OptionSource: "agent",
		ToolName: "refine_image", Question: "想沿着哪个方向深入？", Message: "请选择概念方向。"}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{directionChoiceKind(), conceptKind, quantityKind}
		config.ChoiceMaxOptions = 8
		config.ConceptDirections = catalog.ChatConceptDirectionsSchema{
			Enabled: true, MinCount: 2, MaxCount: 4, SketchTool: "create_concept_sketch", Kind: "concept_direction",
			Quantity: catalog.ChatConceptQuantitySchema{
				Kind: "concept_quantity", MinCount: 1, MaxCount: 4,
				Question: "这次想先看几张概念图？", Message: "请选择数量。",
				OptionLabel: "{count} 张", OptionDescription: "低成本付费生成 {count} 张概念图",
				PendingTemplate: "open ({continuation})", AnswerTemplate: "画 {count} 张",
				SketchRefusal: "先让用户选择数量。",
			},
			MaxLabelCharacters: 40, MaxDescriptionCharacters: 120, MaxArgumentsBytes: 4096,
		}
		config.ChoiceTargetArguments = map[string][]string{"refine_image": {"prompt", "reference_handles"}}
	})
	service.SetChoices(repository, 20)

	// The asking turn opens the quantity card and submits nothing.
	ask := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		Call: agents.ToolCall{ID: "call-q", Name: "ask_choice"},
	}
	result, err := service.AskChoice(ctx, ask, quantityKind, chat.AskChoiceArguments{})
	if err != nil || result.Error != "" || !result.NoSubmission {
		t.Fatalf("quantity card = %+v err=%v", result, err)
	}
	choices, err := service.ListChoices(ctx, "owner-a", "session-a")
	if err != nil || len(choices) != 1 || len(choices[0].Options) != 4 {
		t.Fatalf("quantity card stored = %+v %v", choices, err)
	}
	if choices[0].Kind != "concept_quantity" || choices[0].Options[0].ID != "1" ||
		choices[0].Options[1].Label != "2 张" ||
		choices[0].Options[2].Description != "低成本付费生成 3 张概念图" {
		t.Fatalf("quantity options = %+v", choices[0])
	}
	// A sketch in the reply that only asks the count is refused.
	askWithSketch := ask
	askWithSketch.Call.ID = "call-q2"
	askWithSketch.EarlierCalls = []chat.ToolCall{{Name: "create_concept_sketch"}}
	if refused, err := service.AskChoice(ctx, askWithSketch, quantityKind, chat.AskChoiceArguments{}); err != nil ||
		!refused.NoSubmission || refused.Error != "先让用户选择数量。" {
		t.Fatalf("quantity card after a sketch = %+v %v", refused, err)
	}

	// The person chooses 2 on the card. The answer is bound to the user
	// message that starts the next reply.
	if _, err := repository.AnswerChoice(ctx, "owner-a", "session-a", choices[0].ID, chat.ChoiceRecord{
		OptionID: "2", Source: chat.ChoiceAnsweredOnCard, MessageID: "user-2", AnsweredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	direction := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-2",
		Call:    agents.ToolCall{ID: "call-d", Name: "ask_choice"},
		History: []chat.Message{{ID: "user-2", Role: "configured-user", Content: "选 2 张"}},
		EarlierCalls: []chat.ToolCall{
			{Name: "create_concept_sketch", TaskID: "task-1"},
			{Name: "create_concept_sketch", TaskID: "task-2"},
		},
	}
	arguments := chat.AskChoiceArguments{Kind: "concept_direction", Options: []chat.ChoiceOptionInput{
		{ID: "realistic", Label: "写实", Arguments: json.RawMessage(`{"prompt":"a realistic forest guardian"}`)},
		{ID: "cartoon", Label: "卡通", Arguments: json.RawMessage(`{"prompt":"a cartoon forest guardian"}`)},
	}}
	if accepted, err := service.AskChoice(ctx, direction, conceptKind, arguments); err != nil || accepted.Error != "" {
		t.Fatalf("two sketches for two chosen = %+v %v", accepted, err)
	}
	for _, earlier := range [][]chat.ToolCall{
		{{Name: "create_concept_sketch", TaskID: "task-1"}},
		{{Name: "create_concept_sketch", TaskID: "task-1"}, {Name: "create_concept_sketch", TaskID: "task-2"},
			{Name: "create_concept_sketch", TaskID: "task-3"}},
	} {
		bad := direction
		bad.Call.ID = "call-d3"
		bad.EarlierCalls = earlier
		if refused, err := service.AskChoice(ctx, bad, conceptKind, arguments); err != nil ||
			!refused.NoSubmission || refused.Error == "" {
			t.Fatalf("direction card with the wrong sketch count = %+v %v", refused, err)
		}
	}

	// A stale answer of another message never caps this reply.
	stale := direction
	stale.Call.ID = "call-d4"
	stale.History = []chat.Message{{ID: "user-3", Role: "configured-user", Content: "继续"}}
	stale.EarlierCalls = []chat.ToolCall{
		{Name: "create_concept_sketch", TaskID: "task-1"},
		{Name: "create_concept_sketch", TaskID: "task-2"},
	}
	if accepted, err := service.AskChoice(ctx, stale, conceptKind, arguments); err != nil || accepted.Error != "" {
		t.Fatalf("stale quantity answer applied = %+v %v", accepted, err)
	}
}

// A chosen count of one draws one sketch and opens no direction card.
func TestConceptQuantityOneRefusesTheDirectionCard(t *testing.T) {
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
	quantityKind := catalog.ChatChoiceKindSchema{ID: "concept_quantity", OptionSource: "concept_quantities",
		ToolName: "create_concept_sketch", Question: "这次想先看几张概念图？", Message: "请选择数量。"}
	conceptKind := catalog.ChatChoiceKindSchema{ID: "concept_direction", OptionSource: "agent",
		ToolName: "refine_image", Question: "想沿着哪个方向深入？", Message: "请选择概念方向。"}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.ChoiceKinds = []catalog.ChatChoiceKindSchema{directionChoiceKind(), conceptKind, quantityKind}
		config.ChoiceMaxOptions = 8
		config.ConceptDirections = catalog.ChatConceptDirectionsSchema{
			Enabled: true, MinCount: 2, MaxCount: 4, SketchTool: "create_concept_sketch", Kind: "concept_direction",
			Quantity: catalog.ChatConceptQuantitySchema{
				Kind: "concept_quantity", MinCount: 1, MaxCount: 4,
				Question: "这次想先看几张概念图？", Message: "请选择数量。",
				OptionLabel: "{count} 张", OptionDescription: "低成本付费生成 {count} 张概念图",
				PendingTemplate: "open ({continuation})", AnswerTemplate: "画 {count} 张",
				SketchRefusal: "先让用户选择数量。",
			},
			MaxLabelCharacters: 40, MaxDescriptionCharacters: 120, MaxArgumentsBytes: 4096,
		}
		config.ChoiceTargetArguments = map[string][]string{"refine_image": {"prompt"}}
	})
	service.SetChoices(repository, 20)

	stored, err := repository.CreateChoice(ctx, chat.Choice{
		ID: "choice-1", OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-1",
		CallID: "call-q", ToolName: "create_concept_sketch", Kind: "concept_quantity", Capability: "concept_quantity",
		StepIndex: -1, State: chat.ChoicePending,
		Options:   []chat.ChoiceOption{{ID: "1", Label: "1 张", Arguments: json.RawMessage(`{"count":1}`)}},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AnswerChoice(ctx, "owner-a", "session-a", stored.ID, chat.ChoiceRecord{
		OptionID: "1", Source: chat.ChoiceAnsweredOnCard, MessageID: "user-2", AnsweredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	direction := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-2",
		Call:    agents.ToolCall{ID: "call-d", Name: "ask_choice"},
		History: []chat.Message{{ID: "user-2", Role: "configured-user", Content: "先看 1 张"}},
		EarlierCalls: []chat.ToolCall{
			{Name: "create_concept_sketch", TaskID: "task-1"},
		},
	}
	if refused, err := service.AskChoice(ctx, direction, conceptKind, chat.AskChoiceArguments{
		Kind: "concept_direction", Options: []chat.ChoiceOptionInput{
			{ID: "a", Label: "A", Arguments: json.RawMessage(`{"prompt":"a"}`)},
			{ID: "b", Label: "B", Arguments: json.RawMessage(`{"prompt":"b"}`)},
		},
	}); err != nil || !refused.NoSubmission || refused.Error == "" {
		t.Fatalf("direction card after choosing one = %+v %v", refused, err)
	}
}

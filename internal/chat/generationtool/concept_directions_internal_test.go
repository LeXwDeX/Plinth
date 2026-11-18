package generationtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func conceptExecutor() *Executor {
	return &Executor{chat: catalog.ChatSchema{ConceptDirections: catalog.ChatConceptDirectionsSchema{
		Enabled: true, MinCount: 3, MaxCount: 3, SketchTool: "create_concept_sketch",
		LimitReached: "最多 {maximum} 个方向",
		ParameterOverrides: map[string]map[string]json.RawMessage{
			"image-cheap": {"size": json.RawMessage(`"1024x1024"`), "fixed_quality": json.RawMessage(`"low"`)},
		},
	}}}
}

func conceptCommand() chat.ToolCommand {
	return chat.ToolCommand{
		Call: agents.ToolCall{ID: "call-1", Name: "create_concept_sketch"},
		Tool: catalog.ChatToolSchema{Name: "create_concept_sketch"},
	}
}

func TestConceptSketchLimitCountsTheReplysEarlierCalls(t *testing.T) {
	executor := conceptExecutor()
	command := conceptCommand()
	if refusal := executor.conceptSketchLimit(context.Background(), command); refusal != "" {
		t.Fatalf("first sketch refused: %q", refusal)
	}
	command.EarlierCalls = []chat.ToolCall{{Name: "create_concept_sketch"}, {Name: "create_concept_sketch"}}
	if refusal := executor.conceptSketchLimit(context.Background(), command); refusal != "" {
		t.Fatalf("third sketch refused: %q", refusal)
	}
	command.EarlierCalls = append(command.EarlierCalls, chat.ToolCall{Name: "create_concept_sketch"})
	if refusal := executor.conceptSketchLimit(context.Background(), command); refusal != "最多 3 个方向" {
		t.Fatalf("fourth sketch refusal = %q", refusal)
	}
	other := conceptCommand()
	other.Tool.Name = "refine_image"
	other.EarlierCalls = command.EarlierCalls
	if refusal := executor.conceptSketchLimit(context.Background(), other); refusal != "" {
		t.Fatalf("another tool refused: %q", refusal)
	}
}

type conceptQuantityChoices struct {
	chat.ChoiceStore
	choices []chat.Choice
}

func (store conceptQuantityChoices) ListChoices(context.Context, string, string, int) ([]chat.Choice, error) {
	return store.choices, nil
}

// #360: a chosen count caps the sketches of the reply. One chosen sketch
// allows exactly one; the reply that only opened the quantity card draws
// nothing.
func TestConceptSketchLimitFollowsTheChosenQuantity(t *testing.T) {
	executor := conceptExecutor()
	executor.chat.Roles.User = "user"
	executor.chat.ConceptDirections.Quantity = catalog.ChatConceptQuantitySchema{
		Kind: "concept_quantity", MinCount: 1, MaxCount: 4, SketchRefusal: "先让用户选择数量",
	}
	executor.chat.ConceptDirections.MaxCount = 4
	command := conceptCommand()
	command.OwnerID, command.SessionID = "owner", "session"
	command.AssistantMessageID = "turn-2"
	command.History = []chat.Message{{ID: "user-2", Role: "user", Content: "选 2 张"}}
	answered := chat.Choice{ID: "choice_1", Kind: "concept_quantity", State: chat.ChoiceAnswered,
		AnswerOptionID: "2", AnswerSource: chat.ChoiceAnsweredOnCard, AnswerMessageID: "user-2"}
	executor.choices = conceptQuantityChoices{choices: []chat.Choice{answered}}

	one := command
	one.EarlierCalls = []chat.ToolCall{{Name: "create_concept_sketch"}}
	if refusal := executor.conceptSketchLimit(context.Background(), one); refusal != "" {
		t.Fatalf("sketch below the chosen count refused: %q", refusal)
	}
	two := command
	two.EarlierCalls = []chat.ToolCall{{Name: "create_concept_sketch"}, {Name: "create_concept_sketch"}}
	if refusal := executor.conceptSketchLimit(context.Background(), two); !strings.Contains(refusal, "2") {
		t.Fatalf("sketch above the chosen count = %q", refusal)
	}

	// A chosen count of one allows a single sketch.
	single := conceptCommand()
	single.OwnerID, single.SessionID = "owner", "session"
	single.AssistantMessageID = "turn-2"
	single.History = command.History
	executor.choices = conceptQuantityChoices{choices: []chat.Choice{{
		ID: "choice_1", Kind: "concept_quantity", State: chat.ChoiceAnswered,
		AnswerOptionID: "1", AnswerSource: chat.ChoiceAnsweredOnCard, AnswerMessageID: "user-2"}}}
	if refusal := executor.conceptSketchLimit(context.Background(), single); refusal != "" {
		t.Fatalf("the single chosen sketch refused: %q", refusal)
	}
	one = single
	one.EarlierCalls = []chat.ToolCall{{Name: "create_concept_sketch"}}
	if refusal := executor.conceptSketchLimit(context.Background(), one); !strings.Contains(refusal, "1") {
		t.Fatalf("second sketch for a chosen count of one = %q", refusal)
	}

	// The quantity card is still open in this reply: no sketch may run.
	pending := conceptCommand()
	pending.OwnerID, pending.SessionID = "owner", "session"
	pending.AssistantMessageID = "turn-2"
	executor.choices = conceptQuantityChoices{choices: []chat.Choice{{
		ID: "choice_2", Kind: "concept_quantity", State: chat.ChoicePending, AssistantMessageID: "turn-2"}}}
	if refusal := executor.conceptSketchLimit(context.Background(), pending); refusal != "先让用户选择数量" {
		t.Fatalf("sketch before the quantity answer = %q", refusal)
	}
}

func TestConceptSketchParameterValuesOnlyApplyDeclaredPublicParameters(t *testing.T) {
	executor := conceptExecutor()
	offering := generation.Offering{ID: "image-cheap", Parameters: []schemacontract.Parameter{
		{Name: "size", Type: "select"},
		{Name: "fixed_quality", Type: "select", Fixed: true},
	}}
	values := executor.conceptSketchParameterValues(conceptCommand(), offering)
	if string(values["size"]) != `"1024x1024"` {
		t.Fatalf("values = %v", values)
	}
	if _, present := values["fixed_quality"]; present {
		t.Fatalf("a fixed parameter was applied: %v", values)
	}
	// An Offering the table does not name is used as is.
	if values := executor.conceptSketchParameterValues(conceptCommand(), generation.Offering{ID: "other"}); len(values) != 0 {
		t.Fatalf("unlisted offering values = %v", values)
	}
}

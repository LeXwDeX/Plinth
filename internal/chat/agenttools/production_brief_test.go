package agenttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

// #103: update_production_brief replaces the whole brief through the service
// write path; a refused field comes back through invalid_template, and the
// tool never submits a Task.
func TestUpdateProductionBriefToolWritesTheWholeBriefAndReportsRefusals(t *testing.T) {
	var stored map[string]json.RawMessage
	refusal := error(nil)
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		UpdateBrief: func(_ context.Context, ownerID, sessionID string, brief map[string]json.RawMessage) error {
			if ownerID != testOwner || sessionID != "s1" {
				t.Errorf("wrote brief for %q/%q", ownerID, sessionID)
			}
			if refusal != nil {
				return refusal
			}
			stored = brief
			return nil
		},
		BriefSavedMessage:    "制作简报已更新。",
		BriefInvalidTemplate: "制作简报无效：{reason}",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := func(arguments string) chat.ToolCommand {
		return chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
			Call: agents.ToolCall{ID: "call-brief", Name: ToolUpdateProductionBrief,
				Arguments: json.RawMessage(arguments)}}
	}
	result, err := executor.Execute(context.Background(), command(`{"purpose":"game_character","needs_rig":true}`), nil)
	if err != nil || result.AgentContent != "制作简报已更新。" || !result.NoSubmission || result.TaskID != "" {
		t.Fatalf("update brief = %+v, %v", result, err)
	}
	if string(stored["purpose"]) != `"game_character"` || string(stored["needs_rig"]) != "true" {
		t.Fatalf("stored brief = %v", stored)
	}

	refusal = &chat.BriefError{Reason: `字段 "style" 不能超过 200 个字符`}
	result, _ = executor.Execute(context.Background(), command(`{"style":"`+strings.Repeat("设", 201)+`"}`), nil)
	if !result.NoSubmission || result.Error != `制作简报无效：字段 "style" 不能超过 200 个字符` {
		t.Fatalf("refused brief = %+v", result)
	}

	refusal = nil
	result, _ = executor.Execute(context.Background(), command(`["not","an","object"]`), nil)
	if !result.NoSubmission || result.Error != "制作简报无效：参数必须是对象" {
		t.Fatalf("non-object brief = %+v", result)
	}
}

func TestUpdateProductionBriefToolRefusesWhenUnavailable(t *testing.T) {
	executor, err := New(Config{Params: agentparams.NewStore(), Generate: &fakeGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
		Call: agents.ToolCall{ID: "call-brief", Name: ToolUpdateProductionBrief,
			Arguments: json.RawMessage(`{"purpose":"render"}`)}}, nil)
	if err != nil || !result.NoSubmission || result.Error != "production brief unavailable" {
		t.Fatalf("unavailable brief = %+v, %v", result, err)
	}
}

// #103: ask_choice forwards only a Schema-declared kind, strips nothing of the
// service's card, and refuses a card whose answer would call a tool this turn
// cannot run.
func TestAskChoiceToolBuildsOnlyDeclaredKindsWithAnAvailableTarget(t *testing.T) {
	kind := catalog.ChatChoiceKindSchema{ID: "usage_profile", ToolName: ToolUpdateProductionBrief}
	var receivedKind catalog.ChatChoiceKindSchema
	var received chat.AskChoiceArguments
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		ChoiceKinds: []catalog.ChatChoiceKindSchema{kind},
		AskChoice: func(_ context.Context, command chat.ToolCommand, chosen catalog.ChatChoiceKindSchema,
			arguments chat.AskChoiceArguments,
		) (chat.ToolResult, error) {
			receivedKind, received = chosen, arguments
			if command.OwnerID != testOwner || command.SessionID != "s1" {
				t.Errorf("card opened for %q/%q", command.OwnerID, command.SessionID)
			}
			return chat.ToolResult{AgentContent: `{"choice_required":true}`, NoSubmission: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(arguments string) chat.ToolCommand {
		return chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
			Call: agents.ToolCall{ID: "call-choice", Name: ToolAskChoice, Arguments: json.RawMessage(arguments)},
			ToolAvailable: func(name string) bool {
				return name == ToolUpdateProductionBrief
			}}
	}
	result, err := executor.Execute(context.Background(),
		call(`{"kind":"usage_profile","question":"主要用途？","recommended":"game_character"}`), nil)
	if err != nil || result.AgentContent != `{"choice_required":true}` || !result.NoSubmission {
		t.Fatalf("ask choice = %+v, %v", result, err)
	}
	if receivedKind.ID != "usage_profile" || received.Kind != "usage_profile" ||
		received.Question != "主要用途？" || received.Recommended != "game_character" {
		t.Fatalf("forwarded kind=%+v arguments=%+v", receivedKind, received)
	}

	if result, _ := executor.Execute(context.Background(), call(`{"kind":"unknown_kind"}`), nil); result.Error != "choice kind unavailable" {
		t.Fatalf("undeclared kind = %+v", result)
	}
	blocked := call(`{"kind":"usage_profile"}`)
	blocked.ToolAvailable = func(string) bool { return false }
	if result, _ := executor.Execute(context.Background(), blocked, nil); result.Error != "choice target tool unavailable" {
		t.Fatalf("blocked target = %+v", result)
	}
	if result, _ := executor.Execute(context.Background(), call(`{"question":"no kind"}`), nil); result.Error != "invalid choice arguments" {
		t.Fatalf("missing kind = %+v", result)
	}
}

// #239: a direction card forwards Agent-supplied options after checking that
// each id is a bounded token and each label is present.
func TestAskChoiceToolForwardsBoundedDirectionOptions(t *testing.T) {
	kind := catalog.ChatChoiceKindSchema{ID: "direction", OptionSource: "agent", ToolName: ToolUpdateProductionBrief}
	var received chat.AskChoiceArguments
	executor, err := New(Config{
		Params: agentparams.NewStore(), Generate: &fakeGenerate{},
		ChoiceKinds:         []catalog.ChatChoiceKindSchema{kind},
		ChoiceOptionInvalid: "方向卡选项无效。",
		AskChoice: func(_ context.Context, _ chat.ToolCommand, _ catalog.ChatChoiceKindSchema,
			arguments chat.AskChoiceArguments,
		) (chat.ToolResult, error) {
			received = arguments
			return chat.ToolResult{AgentContent: `{"choice_required":true}`, NoSubmission: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := func(arguments string) chat.ToolCommand {
		return chat.ToolCommand{OwnerID: testOwner, SessionID: "s1",
			Call:          agents.ToolCall{ID: "call-choice", Name: ToolAskChoice, Arguments: json.RawMessage(arguments)},
			ToolAvailable: func(name string) bool { return name == ToolUpdateProductionBrief }}
	}
	result, err := executor.Execute(context.Background(), command(
		`{"kind":"direction","options":[{"id":"watercolor","label":"水彩"},{"id":"ink-2","label":"水墨","description":"淡墨"}]}`), nil)
	if err != nil || !result.NoSubmission || len(received.Options) != 2 ||
		received.Options[0].ID != "watercolor" || received.Options[1].Description != "淡墨" {
		t.Fatalf("direction options = %+v %+v %v", result, received, err)
	}
	for _, arguments := range []string{
		`{"kind":"direction","options":[{"id":"free text","label":"带空格"},{"id":"b","label":"B"}]}`,
		`{"kind":"direction","options":[{"id":"a","label":""},{"id":"b","label":"B"}]}`,
	} {
		if result, _ := executor.Execute(context.Background(), command(arguments), nil); result.Error != "方向卡选项无效。" || !result.NoSubmission {
			t.Fatalf("invalid options = %+v", result)
		}
	}
}

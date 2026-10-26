package mock_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/agents/mock"
)

func TestAdapterPlansConfiguredToolAndStreamsConfiguredSummary(t *testing.T) {
	adapter, err := mock.New(mock.Config{
		ToolName:       "configured-tool",
		PromptArgument: "configured-prompt",
		Summary:        "configured summary",
		UserRole:       "configured-user",
		ToolRole:       "configured-tool-role",
	})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := adapter.Stream(
		context.Background(),
		agents.Request{
			Messages: []agents.Message{{
				Role:    "configured-user",
				Content: "configured request",
			}},
			Tools: []agents.Tool{{Name: "configured-tool"}},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Content != "" || len(planned.ToolCalls) != 1 ||
		planned.ToolCalls[0].Name != "configured-tool" {
		t.Fatalf("planned = %#v", planned)
	}
	var arguments map[string]string
	if err := json.Unmarshal(
		planned.ToolCalls[0].Arguments,
		&arguments,
	); err != nil {
		t.Fatal(err)
	}
	if arguments["configured-prompt"] != "configured request" {
		t.Fatalf("arguments = %#v", arguments)
	}

	var streamed string
	summary, err := adapter.Stream(
		context.Background(),
		agents.Request{
			Messages: []agents.Message{{
				Role:    "configured-tool-role",
				Content: `{"result":"ok"}`,
			}},
		},
		func(delta agents.Delta) error {
			streamed += delta.Content
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Content != "configured summary" ||
		streamed != "configured summary" {
		t.Fatalf("summary = %#v, streamed = %q", summary, streamed)
	}
	// The orchestrator retains tool definitions in its follow-up request.
	followUp, err := adapter.Stream(context.Background(), agents.Request{
		Messages: []agents.Message{{Role: "configured-tool-role", Content: `{"result":"ok"}`}},
		Tools:    []agents.Tool{{Name: "configured-tool"}},
	}, nil)
	if err != nil || len(followUp.ToolCalls) != 0 || followUp.Content != "configured summary" {
		t.Fatalf("follow-up planned another paid tool: %+v %v", followUp, err)
	}
}

func TestAdapterRejectsIncompleteSchemaConfig(t *testing.T) {
	if _, err := mock.New(mock.Config{}); err == nil {
		t.Fatal("expected incomplete schema config to fail")
	}
}

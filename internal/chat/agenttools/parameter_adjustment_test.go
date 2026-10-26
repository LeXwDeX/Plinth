package agenttools

import (
	"context"
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"path/filepath"
	"testing"
)

func TestSetParamsRequiresExactOfferingAndExplainedLegalAdvice(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	params := agentparams.NewPersistentParameterStore(nil, nil)
	generate := &fakeGenerate{}
	e, err := New(Config{Params: params, Generate: generate, Offerings: func() []generation.Offering { return product.List("") }})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, args string
		valid      bool
	}{
		{"unscoped", `{"params":{"quality":"low"}}`, false},
		{"unknown offering", `{"offering_id":"missing","params":{"quality":"low"}}`, false},
		{"missing reason", `{"offering_id":"openai/gpt-image-2/text-to-image","params":{"quality":"low"}}`, false},
		{"illegal quality", `{"offering_id":"openai/gpt-image-2/text-to-image","params":{"quality":"invented"},"reasons":{"quality":"draft"}}`, false},
		{"hidden", `{"offering_id":"openai/gpt-image-2/text-to-image","params":{"moderation":"low"}}`, false},
		{"explained legal", `{"offering_id":"openai/gpt-image-2/text-to-image","params":{"quality":"low"},"reasons":{"quality":"draft"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := e.Execute(context.Background(), chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(tc.args)}}, nil)
			if err != nil || (result.Error == "") != tc.valid {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
	state := params.OfferingStates(testScope, "openai/gpt-image-2/text-to-image")["quality"]
	if state.Source != "agent" || state.Reason != "draft" || string(state.Value) != `"low"` || generate.callCount != 0 {
		t.Fatalf("state=%+v calls=%d", state, generate.callCount)
	}
	if len(params.GetOffering(testScope, "openai/gpt-image-2/image-to-image")) != 0 {
		t.Fatal("cross-offering suggestion leaked")
	}
}

func TestSetParamsReturnsPersistenceFailureWithoutClaimingUpdate(t *testing.T) {
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "params.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := agentparams.NewPersistentParameterStore(repo, nil)
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	e, err := New(Config{Params: store, Generate: &fakeGenerate{}})
	if err != nil {
		t.Fatal(err)
	}
	result := e.setParams(chat.ToolCommand{OwnerID: testOwner, SessionID: "s1", Call: agents.ToolCall{Name: ToolSetParams, Arguments: json.RawMessage(`{"offering_id":"o","params":{"quality":"low"},"reasons":{"quality":"draft"}}`)}})
	if result.Error == "" || result.AgentContent != "" || !result.NoSubmission {
		t.Fatalf("failed write acknowledged: %+v", result)
	}
}

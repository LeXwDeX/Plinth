package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// #509: a chat model call that failed transiently after its retries (the
// adapter layer's Retrying), or broke off after it had started streaming, ends
// the turn as a recoverable failure: the person reads the Schema's text that
// says to send the message again, with the error ID, never the upstream's
// wording and never "unknown error".
func TestOrchestratorSaysWhenTheChatModelCallBrokeOff(t *testing.T) {
	schema := configuredChatSchema(t)
	interrupted := schema.Presentation.Messages.AgentUpstreamInterrupted
	if interrupted == "" {
		t.Fatal("the default Schema declares no agent_upstream_interrupted text")
	}
	transient := agents.Transient(errors.Join(&agents.StatusError{Status: 503}, errors.New("secret upstream diagnostic")), 0)
	content := runFailingTurn(t, failingAgent{err: transient})
	if !strings.HasPrefix(content, interrupted) || strings.Contains(content, "secret") {
		t.Fatalf("assistant = %q, want it to start with %q", content, interrupted)
	}
	if !strings.Contains(content, strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]) {
		t.Fatalf("assistant = %q lacks the error reference", content)
	}
	// A quota stays the quota text: it is not transient.
	quota := errors.Join(&agents.QuotaExhaustion{Class: "credential_quota"}, &agents.StatusError{Status: 429})
	if content := runFailingTurn(t, failingAgent{err: quota}); !strings.HasPrefix(content, schema.Presentation.Messages.AgentQuotaExhausted) {
		t.Fatalf("quota: assistant = %q", content)
	}
}

// The follow-up round after a tool broke off: the reply the person already
// read stays, and the turn ends with the text that says it was cut, instead of
// stopping mid-sentence without a word. With no reply text yet, the text alone
// replaces "no result".
func TestOrchestratorEndsABrokenFollowUpWithTheInterruptedText(t *testing.T) {
	schema := configuredChatSchema(t)
	interrupted := schema.Presentation.Messages.AgentUpstreamInterrupted
	reference := strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]
	for _, opening := range []string{"Working on it.", ""} {
		agent := &breaksAfterToolAgent{tool: schema.Tools[0].Name, opening: opening,
			err: agents.Transient(errors.New("agent stream ended before its last chunk"), 0)}
		content := runFailingTurnWith(t, schema, agent)
		if agent.streams() < 2 {
			t.Fatalf("streams = %d, want the follow-up to have run", agent.streams())
		}
		if !strings.HasPrefix(content, opening) || !strings.Contains(content, interrupted) || !strings.Contains(content, reference) {
			t.Fatalf("opening %q: assistant = %q", opening, content)
		}
		if opening == "" && !strings.HasPrefix(content, interrupted) {
			t.Fatalf("no opening: assistant = %q", content)
		}
	}
}

// breaksAfterToolAgent says opening and calls one tool, then every later
// stream emits half a sentence and fails with err.
type breaksAfterToolAgent struct {
	mu      sync.Mutex
	tool    string
	opening string
	err     error
	count   int
}

func (agent *breaksAfterToolAgent) Stream(_ context.Context, _ agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	agent.mu.Lock()
	agent.count++
	count := agent.count
	agent.mu.Unlock()
	if count == 1 {
		if agent.opening != "" {
			if err := emit(agents.Delta{Content: agent.opening}); err != nil {
				return agents.Completion{}, err
			}
		}
		return agents.Completion{Content: agent.opening,
			ToolCalls: []agents.ToolCall{{ID: "call-1", Name: agent.tool, Arguments: []byte(`{"prompt":"paint"}`)}}}, nil
	}
	if err := emit(agents.Delta{Content: " Half a sen"}); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{}, agent.err
}

func (agent *breaksAfterToolAgent) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{}, agent.err
}

func (agent *breaksAfterToolAgent) streams() int {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.count
}

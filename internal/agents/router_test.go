package agents_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

type namedAdapter string

func (adapter namedAdapter) Stream(_ context.Context, _ agents.Request, emit func(agents.Delta) error) (agents.Completion, error) {
	if err := emit(agents.Delta{Content: string(adapter)}); err != nil {
		return agents.Completion{}, err
	}
	return agents.Completion{Content: string(adapter)}, nil
}

func (adapter namedAdapter) Complete(context.Context, agents.Request) (agents.Completion, error) {
	return agents.Completion{Content: string(adapter)}, nil
}

// #322 Scope 5: each request goes to the connection it names; an empty name
// is the deployment's own connection; a connection the environment does not
// declare (or declares without settings) answers "not configured".
func TestConnectionRouterDispatchesOnTheRequestConnection(t *testing.T) {
	router, err := agents.NewConnectionRouter("LLM", map[string]agents.Adapter{
		"LLM": namedAdapter("deepseek-connection"),
		"GLM": namedAdapter("glm-connection"),
		"OFF": agents.NotConfigured{Missing: []string{"OFF_API_KEY"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for connection, want := range map[string]string{"": "deepseek-connection", "LLM": "deepseek-connection", "GLM": "glm-connection"} {
		completion, err := router.Complete(ctx, agents.Request{Connection: connection})
		if err != nil || completion.Content != want {
			t.Fatalf("complete %q = %q %v", connection, completion.Content, err)
		}
		streamed, err := router.Stream(ctx, agents.Request{Connection: connection}, func(agents.Delta) error { return nil })
		if err != nil || streamed.Content != want {
			t.Fatalf("stream %q = %q %v", connection, streamed.Content, err)
		}
	}
	for _, connection := range []string{"OFF", "NOPE"} {
		if _, err := router.Complete(ctx, agents.Request{Connection: connection}); !errors.Is(err, agents.ErrNotConfigured) {
			t.Fatalf("connection %q = %v", connection, err)
		}
	}
	if agents.IsNotConfigured(router) {
		t.Fatal("a router with a configured connection reported itself not configured")
	}
	if _, err := agents.NewConnectionRouter("NOPE", map[string]agents.Adapter{"LLM": namedAdapter("x")}); err == nil {
		t.Fatal("router without its default connection")
	}
}

func TestStatusErrorKeepsTheStatusOnly(t *testing.T) {
	err := errors.Join(errors.New("context"), &agents.StatusError{Status: 401})
	var status *agents.StatusError
	if !errors.As(err, &status) || status.Status != 401 || status.Error() != "agent returned status 401" {
		t.Fatalf("status error = %v", err)
	}
}

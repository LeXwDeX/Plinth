package app

import (
	"errors"
	"testing"
)

type wiring bool

func (w wiring) UsesExecutionGrants() bool { return bool(w) }

// #665: an application whose generation tools are not wired to execution
// grants must not start, instead of submitting through the legacy path.
func TestApplicationRefusesGenerationToolsWithoutExecutionGrants(t *testing.T) {
	if err := requireExecutionGrants(wiring(false)); !errors.Is(err, errExecutionGrantsUnwired) {
		t.Fatalf("unwired tools = %v", err)
	}
	if err := requireExecutionGrants(nil); !errors.Is(err, errExecutionGrantsUnwired) {
		t.Fatalf("missing tools = %v", err)
	}
	if err := requireExecutionGrants(wiring(true)); err != nil {
		t.Fatalf("wired tools = %v", err)
	}
}

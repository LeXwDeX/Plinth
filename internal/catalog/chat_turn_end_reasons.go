package catalog

import (
	"fmt"
	"strings"
)

// ChatTurnEndReasonsSchema names why a chat turn ended (#328), one string per
// reason the orchestrator tells apart. The server stores the string with the
// finished turn, so a reader can tell a turn that was stopped from one that
// waits for an approval without a literal of its own. Turns stored before
// #553 may still carry the retired tool_limit reason. The whole
// object is optional: a Catalog that leaves it out records no reason and keeps
// the empty string.
type ChatTurnEndReasonsSchema struct {
	// Completed is a normal end.
	Completed string `json:"completed"`
	// AwaitingApproval is a turn that left at least one approval card (a paid
	// step or a production plan) for the person to decide.
	AwaitingApproval string `json:"awaiting_approval"`
	// Stopped is a turn the person stopped.
	Stopped string `json:"stopped"`
	// Background is a turn that ended while its Tasks keep running (#137).
	Background string `json:"background"`
	// Error is a turn that finished as failed.
	Error string `json:"error"`
	// EmptyResponse is a turn whose model round answered with no text and no
	// tool call, also after its retry (#638): not a normal end, and not a
	// failed one either, since what the turn did before it stays.
	EmptyResponse string `json:"empty_response"`
}

// Declared reports whether the Catalog declares the turn end reasons.
func (reasons ChatTurnEndReasonsSchema) Declared() bool {
	return reasons != ChatTurnEndReasonsSchema{}
}

// Validate refuses a declaration that is only partly there: once any reason is
// declared, every reason needs a value and no two may share one, or a reader
// could not tell the reasons apart. An undeclared object is valid.
func (reasons ChatTurnEndReasonsSchema) Validate() error {
	if !reasons.Declared() {
		return nil
	}
	seen := make(map[string]string, 6)
	for _, entry := range []struct{ name, value string }{
		{"completed", reasons.Completed},
		{"awaiting_approval", reasons.AwaitingApproval},
		{"stopped", reasons.Stopped},
		{"background", reasons.Background},
		{"error", reasons.Error},
		{"empty_response", reasons.EmptyResponse},
	} {
		if strings.TrimSpace(entry.value) == "" {
			return fmt.Errorf("state.turn_end_reasons.%s 不能为空", entry.name)
		}
		if other, duplicate := seen[entry.value]; duplicate {
			return fmt.Errorf("state.turn_end_reasons.%s 与 state.turn_end_reasons.%s 的值 %q 重复", entry.name, other, entry.value)
		}
		seen[entry.value] = entry.name
	}
	return nil
}

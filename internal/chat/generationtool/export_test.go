package generationtool

import (
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// InputGapsForTest exposes inputGaps to the package's external tests.
func (executor *Executor) InputGapsForTest(command chat.ToolCommand, offering generation.Offering, input map[string]any) []string {
	return executor.inputGaps(command, offering, input)
}

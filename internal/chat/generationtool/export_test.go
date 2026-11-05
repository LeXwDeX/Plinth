package generationtool

import (
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// InputGapsForTest exposes inputGaps to the package's external tests.
func (executor *Executor) InputGapsForTest(command chat.ToolCommand, offering generation.Offering, input map[string]any) []string {
	return executor.inputGaps(command, offering, input)
}

// ModelReviewCacheForTest reports the parsed-model cache's charged bytes and
// entry count to the package's external tests.
func (executor *Executor) ModelReviewCacheForTest() (bytes int64, entries int) {
	cache := executor.modelReview.cache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.bytes, len(cache.entries)
}

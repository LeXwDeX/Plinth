package chat

import (
	"strconv"
	"strings"
)

// imageCountPrompt adds the person's own 2D image count to the system prompt
// (#486). The count is one value for the whole session, read from storage on
// every request like the approval mode, so a person who switches Offering is
// still working to the number they set and the Agent never has to guess one or
// ask for it again.
//
// A session with no count on record renders nothing: the Offering's own
// declared default is what the turn will use, and naming a number nobody chose
// would be the guess this block exists to prevent. The words come from
// application.chat.image_count.context_template.
func (orchestrator *Orchestrator) imageCountPrompt(ownerID, sessionID string) string {
	prompt := orchestrator.schema.ImageCount
	if orchestrator.imageCount == nil || sessionID == "" ||
		strings.TrimSpace(prompt.ContextTemplate) == "" {
		return ""
	}
	count, ok := orchestrator.imageCount(ownerID, sessionID)
	if !ok {
		return ""
	}
	return strings.ReplaceAll(prompt.ContextTemplate, "{count}", strconv.Itoa(count))
}
package chat

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #564: the system prompt promised that parameters the session locked are
// followed, but nothing showed the Agent which values the panel holds before
// it submits. The turn now carries one line per Offering with the session's
// values and who set each.
func TestSessionParametersPromptListsThePanelValuesAndTheirSource(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := loaded.Application().Chat
	if strings.TrimSpace(schema.SessionParameters.ContextTemplate) == "" {
		t.Fatal("默认 Catalog 未声明 chat.session_parameters.context_template")
	}
	orchestrator := &Orchestrator{schema: schema,
		sessionParameters: func(ownerID, sessionID string) []SessionParameterGroup {
			if ownerID != "owner" || sessionID != "session" {
				return nil
			}
			return []SessionParameterGroup{{
				Model: "GPT Image 2", Capability: "text-to-image",
				Parameters: []SessionParameterValue{
					{Label: "输出尺寸", Value: "1536x1024", Source: "user"},
					{Label: "背景", Value: "透明", Source: "agent"},
				},
			}}
		}}
	prompt := orchestrator.sessionParametersPrompt("owner", "session")
	for _, want := range []string{"GPT Image 2", "text-to-image", "输出尺寸", "1536x1024", "背景", "透明",
		schema.SessionParameters.Sources["user"], schema.SessionParameters.Sources["agent"]} {
		if want == "" || !strings.Contains(prompt, want) {
			t.Errorf("the block lacks %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "{") {
		t.Errorf("the block kept a placeholder: %q", prompt)
	}
	if prompt := orchestrator.sessionParametersPrompt("owner", "other"); prompt != "" {
		t.Errorf("a session without values rendered a block: %q", prompt)
	}
	if prompt := (&Orchestrator{schema: schema}).sessionParametersPrompt("owner", "session"); prompt != "" {
		t.Errorf("a prompt without the reader rendered a block: %q", prompt)
	}
}

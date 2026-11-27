package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// The handle refusals are shared by read-only tools (history lookups, image
// inspection, crops, reviews) and submitting tools, so they do not claim that
// nothing was charged: a read-only tool never charges anything.
func TestHandleRefusalsDoNotTalkAboutCharges(t *testing.T) {
	var document struct {
		Application struct {
			Chat struct {
				Context ChatContextSchema `json:"context"`
			} `json:"chat"`
		} `json:"application"`
	}
	if err := json.Unmarshal(DefaultDocument(), &document); err != nil {
		t.Fatal(err)
	}
	context := document.Application.Chat.Context
	for key, text := range map[string]string{"unknown_handle_template": context.UnknownHandleTemplate,
		"empty_selection_template": context.EmptySelectionTemplate} {
		if text == "" || strings.Contains(text, "扣费") || strings.Contains(text, "提交") {
			t.Errorf("chat.context.%s = %q, want a wording that fits read-only tools", key, text)
		}
	}
}

// @selected resolves only when the system prompt lists a selection (#684), so
// every tool and parameter description that offers it says so. Mentions that
// warn against it or explain what it resolves to are not offers.
func TestToolDescriptionsOfferSelectedOnlyWhenListed(t *testing.T) {
	var document struct {
		Application struct {
			Chat struct {
				Tools      json.RawMessage `json:"tools"`
				AgentTools json.RawMessage `json:"agent_tools"`
			} `json:"chat"`
		} `json:"application"`
	}
	if err := json.Unmarshal(DefaultDocument(), &document); err != nil {
		t.Fatal(err)
	}
	notOffers := []string{"or is @selected (it names", "and @selected resolves to",
		"with @selected or any guessed handle", "or @selected for one of them when it is the selected model"}
	const qualifier = "only when the system prompt lists one"
	var walk func(path string, value any)
	walk = func(path string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				walk(path+"."+key, child)
			}
		case []any:
			for _, child := range typed {
				walk(path+"[]", child)
			}
		case string:
			rest := typed
			for {
				index := strings.Index(rest, "@selected")
				if index < 0 {
					return
				}
				window := rest[max(0, index-60):min(len(rest), index+100)]
				offer := true
				for _, phrase := range notOffers {
					if strings.Contains(window, phrase) {
						offer = false
					}
				}
				if offer && !strings.Contains(window, qualifier) {
					t.Errorf("%s offers @selected without %q: ...%s...", path, qualifier, window)
				}
				rest = rest[index+len("@selected"):]
			}
		}
	}
	for name, raw := range map[string]json.RawMessage{"tools": document.Application.Chat.Tools,
		"agent_tools": document.Application.Chat.AgentTools} {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		walk(name, value)
	}
}

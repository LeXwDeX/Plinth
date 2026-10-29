package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestPromptSectionsAreValidated(t *testing.T) {
	section := func(mutate func(*catalog.ChatAgentPromptSection)) func(*catalog.ApplicationSchema) {
		return func(a *catalog.ApplicationSchema) {
			sections := append([]catalog.ChatAgentPromptSection(nil), a.Chat.Agent.PromptSections...)
			sections[0].Capabilities = append([]schemacontract.Capability(nil), sections[0].Capabilities...)
			mutate(&sections[0])
			a.Chat.Agent.PromptSections = sections
		}
	}
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"an empty id", section(func(s *catalog.ChatAgentPromptSection) { s.ID = "" }), "prompt_sections"},
		{"no capabilities", section(func(s *catalog.ChatAgentPromptSection) { s.Capabilities = nil }), "capabilities"},
		{"an unknown capability", section(func(s *catalog.ChatAgentPromptSection) {
			s.Capabilities = []schemacontract.Capability{"no-such-capability"}
		}), "no-such-capability"},
		{"text that is nowhere in the prompt", section(func(s *catalog.ChatAgentPromptSection) { s.Text = "not in any prompt" }), "text"},
		{"a duplicate id", func(a *catalog.ApplicationSchema) {
			sections := append([]catalog.ChatAgentPromptSection(nil), a.Chat.Agent.PromptSections...)
			sections[1].ID = sections[0].ID
			a.Chat.Agent.PromptSections = sections
		}, "重复"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid prompt section")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestStripInactivePromptSections(t *testing.T) {
	sections := []catalog.ChatAgentPromptSection{
		{ID: "line", Capabilities: []schemacontract.Capability{"a", "b"}, Text: "- about a"},
		{ID: "clause", Capabilities: []schemacontract.Capability{"a"}, Text: "A (x), "},
	}
	text := "intro\n- about a\n- other\nuses A (x), B"
	none := func(schemacontract.Capability) bool { return false }
	if got, want := catalog.StripInactivePromptSections(text, sections, none), "intro\n- other\nuses B"; got != want {
		t.Fatalf("inactive: %q, want %q", got, want)
	}
	// One active capability of the section keeps it.
	onlyB := func(c schemacontract.Capability) bool { return c == "b" }
	if got, want := catalog.StripInactivePromptSections(text, sections, onlyB), "intro\n- about a\n- other\nuses B"; got != want {
		t.Fatalf("partly active: %q, want %q", got, want)
	}
	all := func(schemacontract.Capability) bool { return true }
	if got := catalog.StripInactivePromptSections(text, sections, all); got != text {
		t.Fatalf("active: %q", got)
	}
}

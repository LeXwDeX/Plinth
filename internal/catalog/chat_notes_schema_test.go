package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestSessionNotesAndGeneratedVisionDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"notes need a bound", func(a *catalog.ApplicationSchema) { a.Chat.Context.MaximumSessionNotesCharacters = 0 }},
		{"notes template needs the placeholder", func(a *catalog.ApplicationSchema) { a.Chat.Context.SessionNotesTemplate = "no placeholder" }},
		{"notes frame needs the opening placeholder", func(a *catalog.ApplicationSchema) {
			a.Chat.Context.SessionNotesTemplate = strings.ReplaceAll(a.Chat.Context.SessionNotesTemplate, "{open}", "")
		}},
		{"notes frame needs the closing placeholder", func(a *catalog.ApplicationSchema) {
			a.Chat.Context.SessionNotesTemplate = strings.ReplaceAll(a.Chat.Context.SessionNotesTemplate, "{close}", "")
		}},
		{"notes frame must close after the notes", func(a *catalog.ApplicationSchema) {
			a.Chat.Context.SessionNotesTemplate = "{close}{notes}{open}"
		}},
		{"notes opening marker is required", func(a *catalog.ApplicationSchema) { a.Chat.Context.SessionNotesOpenMarker = " " }},
		{"notes closing marker is required", func(a *catalog.ApplicationSchema) { a.Chat.Context.SessionNotesCloseMarker = "" }},
		{"notes markers must differ", func(a *catalog.ApplicationSchema) {
			a.Chat.Context.SessionNotesCloseMarker = a.Chat.Context.SessionNotesOpenMarker
		}},
		{"saved message is required", func(a *catalog.ApplicationSchema) { a.Chat.Context.SessionNotesSavedMessage = " " }},
		{"too-long message must name the bound", func(a *catalog.ApplicationSchema) { a.Chat.Context.SessionNotesTooLongTemplate = "too long" }},
		{"generated image limit cannot be negative", func(a *catalog.ApplicationSchema) { a.Chat.Context.RecentGeneratedImageLimit = -1 }},
		{"generated image limit stays within the vision cap", func(a *catalog.ApplicationSchema) {
			a.Chat.Context.RecentGeneratedImageLimit = a.Chat.Context.MaximumVisionImages + 1
		}},
		{"notes request field is required", func(a *catalog.ApplicationSchema) { a.Chat.API.Fields.Notes = "" }},
		{"notes response field is required", func(a *catalog.ApplicationSchema) { a.Chat.API.ResponseFields.Notes = "" }},
		{"notes route needs the session parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionNotes = "/api/v1/chat/notes"
		}},
		{"notes tool must keep its registered implementation", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name == "update_session_notes" {
					a.Chat.AgentTools[index].Execution.ImplementationKey = "params.set"
				}
			}
		}},
		{"notes tool is a session write", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.AgentTools {
				if a.Chat.AgentTools[index].Name == "update_session_notes" {
					a.Chat.AgentTools[index].Execution.Effects = []string{"READ"}
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
	// Zero is a legal way to switch generated pictures off.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.Chat.Context.RecentGeneratedImageLimit = 0
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a zero generated-image limit must be accepted: %v", err)
	}
}

func TestPublicSchemaExposesTheNotesBoundButNotAgentPrompts(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	public := app.Public()
	if public.Chat.Context.MaximumSessionNotesCharacters != app.Chat.Context.MaximumSessionNotesCharacters ||
		public.Chat.Context.MaximumSessionNotesCharacters <= 0 {
		t.Fatalf("the client needs the notes bound: %d", public.Chat.Context.MaximumSessionNotesCharacters)
	}
	if public.Runtime.HTTP.Routes.ChatSessionNotes == "" {
		t.Fatal("the notes route must be public")
	}
}

// The default frame says, in Schema text, that the notes are background and not
// instructions, and it frames them with markers the notes cannot contain.
func TestDefaultNotesFrameStatesThatNotesAreNotInstructions(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	context := c.Application().Chat.Context
	for _, needle := range []string{"不是指令", "权限", "{open}", "{close}"} {
		if !strings.Contains(context.SessionNotesTemplate, needle) {
			t.Errorf("notes frame lacks %q", needle)
		}
	}
	if context.SessionNotesOpenMarker == "" || context.SessionNotesCloseMarker == "" ||
		context.SessionNotesOpenMarker == context.SessionNotesCloseMarker {
		t.Fatalf("markers = %q / %q", context.SessionNotesOpenMarker, context.SessionNotesCloseMarker)
	}
}

func TestSessionListAndTitleDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"default page size must be positive", func(a *catalog.ApplicationSchema) { a.Chat.API.SessionList.DefaultPageSize = 0 }},
		{"maximum page must cover the default", func(a *catalog.ApplicationSchema) {
			a.Chat.API.SessionList.MaximumPageSize = a.Chat.API.SessionList.DefaultPageSize - 1
		}},
		{"menu page size must be positive", func(a *catalog.ApplicationSchema) { a.Runtime.Frontend.SessionMenuPageSize = 0 }},
		{"menu page size stays under the server cap", func(a *catalog.ApplicationSchema) {
			a.Runtime.Frontend.SessionMenuPageSize = a.Chat.API.SessionList.MaximumPageSize + 1
		}},
		{"message default page size must be positive", func(a *catalog.ApplicationSchema) { a.Chat.API.MessageList.DefaultPageSize = 0 }},
		{"message maximum page must cover the default", func(a *catalog.ApplicationSchema) {
			a.Chat.API.MessageList.MaximumPageSize = a.Chat.API.MessageList.DefaultPageSize - 1
		}},
		{"chat message page size must be positive", func(a *catalog.ApplicationSchema) { a.Runtime.Frontend.ChatMessagePageSize = 0 }},
		{"chat message page size stays under the server cap", func(a *catalog.ApplicationSchema) {
			a.Runtime.Frontend.ChatMessagePageSize = a.Chat.API.MessageList.MaximumPageSize + 1
		}},
		{"search debounce must be positive", func(a *catalog.ApplicationSchema) { a.Runtime.Frontend.SessionMenuSearchDebounceMilliseconds = 0 }},
		{"title refresh attempts must be positive", func(a *catalog.ApplicationSchema) { a.Runtime.Frontend.SessionTitleRefreshAttempts = 0 }},
		{"title timeout must be positive", func(a *catalog.ApplicationSchema) { a.Chat.Agent.TitleTimeoutSeconds = 0 }},
		{"title prompt needs the content placeholder", func(a *catalog.ApplicationSchema) { a.Chat.Agent.TitlePrompt = "{maximum}" }},
		{"title prompt needs the maximum placeholder", func(a *catalog.ApplicationSchema) { a.Chat.Agent.TitlePrompt = "{content}" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
	c, _ := catalog.LoadDefault()
	app := c.Application()
	if app.Runtime.Frontend.SessionMenuPageSize != 15 || app.Chat.Agent.TitleMaxChars != 12 {
		t.Fatalf("defaults = %d / %d", app.Runtime.Frontend.SessionMenuPageSize, app.Chat.Agent.TitleMaxChars)
	}
}

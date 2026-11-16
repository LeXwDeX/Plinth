package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #306: the 加工 panel reads the values a session's purpose gives the parameters
// an Offering declares as following it (#292) from a Schema-declared session
// route, and words each one with a Schema message that carries the value.

func TestPurposeParametersRouteIsDeclaredForTheSession(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := c.Application()
	const want = "/api/v1/chat/sessions/{sessionID}/purpose-parameters"
	if got := application.Runtime.HTTP.Routes.ChatSessionPurposeParameters; got != want {
		t.Fatalf("chat_session_purpose_parameters = %q, want %q", got, want)
	}
	// The client reads the route from the public Schema; nothing else of it is private.
	if got := application.Public().Runtime.HTTP.Routes.ChatSessionPurposeParameters; got != want {
		t.Fatalf("the public Schema carries %q, want %q", got, want)
	}
}

func TestPurposeParametersRouteDeclarationIsValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"it needs the session parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionPurposeParameters = "/api/v1/chat/purpose-parameters"
		}},
		{"it takes no other parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionPurposeParameters = "/api/v1/chat/sessions/{sessionID}/purpose-parameters/{offeringID}"
		}},
		{"it cannot repeat another route", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionPurposeParameters = a.Runtime.HTTP.Routes.ChatSessionBrief
		}},
		{"it is a path", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionPurposeParameters = "purpose-parameters/{sessionID}"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := c.Application()
			tt.mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
	// A historical Catalog without the route stays loadable; the read is then off.
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := c.Application()
	application.Runtime.HTTP.Routes.ChatSessionPurposeParameters = ""
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("a Catalog without the route was refused: %v", err)
	}
}

// The panel words a resolved value with one Schema message in each language; the
// Catalog already refuses a language whose placeholders drift from the other's.
func TestFollowsPurposeResolvedMessageCarriesTheValueInBothLanguages(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := c.Application()
	const key = "ui.studio.params.follows_purpose_resolved"
	for language, text := range map[string]string{
		"zh-CN": application.Content.Messages[key],
		"en":    application.Localization.Bundles["en"].Messages[key],
	} {
		if !strings.Contains(text, "{value}") {
			t.Fatalf("%s %s = %q, want it to carry {value}", language, key, text)
		}
	}
	// The unresolved wording stays the fallback while the value is not known.
	if application.Content.Messages["ui.studio.params.follows_purpose"] == "" {
		t.Fatal("the unresolved wording ui.studio.params.follows_purpose is missing")
	}
}

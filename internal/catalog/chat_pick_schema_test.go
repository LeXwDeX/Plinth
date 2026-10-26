package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestCandidatePickDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"pick response member is required", func(a *catalog.ApplicationSchema) { a.Chat.API.ResponseFields.Pick = "" }},
		{"picked-selection note is required", func(a *catalog.ApplicationSchema) { a.Chat.Context.SelectionPickedTemplate = " " }},
		{"pick route needs the session parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionPick = "/api/v1/chat/pick"
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
}

func TestPublicSchemaExposesThePickRoute(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	public := c.Application().Public()
	if public.Runtime.HTTP.Routes.ChatSessionPick == "" || public.Chat.API.ResponseFields.Pick == "" {
		t.Fatal("the client needs the pick route and member")
	}
}

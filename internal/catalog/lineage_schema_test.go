package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestVersionChainDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"task bound is required", func(a *catalog.ApplicationSchema) { a.Runtime.HTTP.MaximumLineageTasks = 0 }},
		{"page size is required", func(a *catalog.ApplicationSchema) { a.Runtime.HTTP.LineagePageSize = 0 }},
		{"page size is bounded", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.LineagePageSize = a.Runtime.HTTP.MaximumLineageTasks + 1
		}},
		{"lineage route needs the session parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionLineage = "/api/v1/chat/lineage"
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

func TestPublicSchemaCarriesTheVersionChainRoute(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	public := c.Application().Public()
	if public.Runtime.HTTP.Routes.ChatSessionLineage == "" {
		t.Fatal("the client needs the lineage route")
	}
}

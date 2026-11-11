package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func defaultApplication(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return source.Application()
}

func TestDefaultProjectSchemaLoadsAndProjectsSafeMembers(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	if application.Project == nil {
		t.Fatal("default Catalog has no application.project")
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default project schema invalid: %v", err)
	}
	public := application.Public().Project
	if public == nil {
		t.Fatal("public projection lost application.project")
	}
	if len(public.Assets.Kinds) != 4 || public.Assets.Kinds[0].ID == "" || public.Assets.Kinds[0].LabelKey == "" {
		t.Fatalf("public kinds = %+v", public.Assets.Kinds)
	}
	if strings.Contains(strings.Join(public.Conventions.Fields, ","), "agent") ||
		len(public.Conventions.Texts) == 0 {
		t.Fatalf("public conventions = %+v", public.Conventions)
	}
	if !strings.Contains(application.Public().Chat.StyleLock.Sources[2], "off") {
		t.Fatalf("public style-lock sources = %v", application.Public().Chat.StyleLock.Sources)
	}
}

func TestProjectValidationRejectsIncompleteOrUnknownDeclarations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"zero limit": func(app *catalog.ApplicationSchema) {
			app.Project.Limits.MaximumProjectsPerOwner = 0
		},
		"empty kinds": func(app *catalog.ApplicationSchema) {
			app.Project.Assets.Kinds = nil
		},
		"uppercase kind": func(app *catalog.ApplicationSchema) {
			app.Project.Assets.Kinds[0].ID = "Character"
		},
		"kind label missing": func(app *catalog.ApplicationSchema) {
			app.Project.Assets.Kinds[0].LabelKey = "ui.project.missing"
		},
		"conventions field not a brief subset": func(app *catalog.ApplicationSchema) {
			app.Project.Conventions.Fields[0].Type = "text"
		},
		"conventions text collides with field": func(app *catalog.ApplicationSchema) {
			app.Project.Conventions.Texts[0].Name = app.Project.Conventions.Fields[0].Name
		},
		"invalid palette pattern": func(app *catalog.ApplicationSchema) {
			app.Project.StyleGuide.PaletteColorPattern = "["
		},
		"agent tool not declared": func(app *catalog.ApplicationSchema) {
			app.Project.Agent = &catalog.ProjectAgentSchema{
				ToolName: "list_project_assets", HandlePrefix: "@project", ListLimit: 20,
				ContextTemplate: "{assets}", StylePromptTemplate: "{style}",
				AssetLineTemplate: "line", CitationTemplate: "cite",
				UnknownAssetTemplate: "unknown", ListLimitTemplate: "limit",
			}
		},
		"missing audit action": func(app *catalog.ApplicationSchema) {
			app.Project.Audit.PromoteAction = ""
		},
		"missing message": func(app *catalog.ApplicationSchema) {
			app.Project.Messages.AssetNotFound = ""
		},
		"missing route": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.Project = ""
		},
		"wrong route parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetItem = "/api/v1/projects/{projectID}/assets"
		},
		"bad style lock sources": func(app *catalog.ApplicationSchema) {
			app.Chat.StyleLock.Sources = []string{"project", "session"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			application := defaultApplication(t)
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("invalid project declaration was accepted")
			}
		})
	}
}

func TestProjectCloneDoesNotShareSlices(t *testing.T) {
	t.Parallel()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := source.Application()
	first.Project.Assets.Kinds[0].ID = "mutated"
	first.Project.Conventions.Fields[0].Name = "mutated"
	first.Project.Conventions.Texts[0].Name = "mutated"
	first.Chat.StyleLock.Sources[0] = "mutated"
	second := source.Application()
	if second.Project.Assets.Kinds[0].ID == "mutated" ||
		second.Project.Conventions.Fields[0].Name == "mutated" ||
		second.Project.Conventions.Texts[0].Name == "mutated" ||
		second.Chat.StyleLock.Sources[0] == "mutated" {
		t.Fatal("Catalog Application() shares project slices with a previous copy")
	}
}

func TestHistoricalCatalogWithoutProjectStaysValid(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.Project = nil
	application.Runtime.HTTP.Routes.Project = ""
	application.Runtime.HTTP.Routes.ProjectItem = ""
	application.Runtime.HTTP.Routes.ProjectAssets = ""
	application.Runtime.HTTP.Routes.ProjectAssetItem = ""
	application.Runtime.HTTP.Routes.ProjectStyleGuide = ""
	application.Runtime.HTTP.Routes.ProjectConventions = ""
	application.Runtime.HTTP.Routes.ProjectPromote = ""
	application.Runtime.HTTP.Routes.ChatSessionProject = ""
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("historical Catalog without project rejected: %v", err)
	}
	if application.Public().Project != nil {
		t.Fatal("historical Catalog projected a project section")
	}
}

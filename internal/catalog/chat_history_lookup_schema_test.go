package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #279: the history lookup bound and texts, and the reference-purpose
// argument and templates, are Product Schema declarations the server checks.
func TestHistoryLookupAndReferencePurposesAreValidatedSchemaDeclarations(t *testing.T) {
	load := func() catalog.ApplicationSchema {
		t.Helper()
		c, err := catalog.LoadDefault()
		if err != nil {
			t.Fatal(err)
		}
		return c.Application()
	}
	app := load()
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("shipped schema: %v", err)
	}
	if app.Chat.Context.HistoryImageLookupLimit != 10 {
		t.Fatalf("history_image_lookup_limit = %d; want the default of 10", app.Chat.Context.HistoryImageLookupLimit)
	}
	refine := app.Chat.Tools[0]
	if refine.Name != "refine_image" || refine.ReferencePurposesArgument == "" {
		t.Fatalf("refine_image must declare reference purposes: %+v", refine.Name)
	}
	for name, breakIt := range map[string]func(*catalog.ApplicationSchema){
		"no bound":                   func(app *catalog.ApplicationSchema) { app.Chat.Context.HistoryImageLookupLimit = 0 },
		"budget text without limit":  func(app *catalog.ApplicationSchema) { app.Chat.Context.HistoryImageBudgetTemplate = "full" },
		"label without handle":       func(app *catalog.ApplicationSchema) { app.Chat.Context.HistoryImageViewLabelTemplate = "picture" },
		"unreadable without handle":  func(app *catalog.ApplicationSchema) { app.Chat.Context.HistoryImageUnreadableTemplate = "gone" },
		"no intro":                   func(app *catalog.ApplicationSchema) { app.Chat.Context.HistoryImageViewIntro = " " },
		"no vision-off text":         func(app *catalog.ApplicationSchema) { app.Chat.Context.HistoryImageVisionOffMessage = "" },
		"purpose refusal w/o handle": func(app *catalog.ApplicationSchema) { app.Chat.Context.ReferencePurposeInvalidTemplate = "no" },
		"purpose not a parameter":    func(app *catalog.ApplicationSchema) { app.Chat.Tools[0].ReferencePurposesArgument = "roles" },
		"item without position":      func(app *catalog.ApplicationSchema) { app.Chat.Tools[0].ReferencePurposeItemTemplate = "{purpose}" },
		"prompt without purposes": func(app *catalog.ApplicationSchema) {
			app.Chat.Tools[0].PromptWithReferencePurposesTemplate = "{prompt}"
		},
		"templates without argument": func(app *catalog.ApplicationSchema) { app.Chat.Tools[0].ReferencePurposesArgument = "" },
		"purposes on a text-only tool": func(app *catalog.ApplicationSchema) {
			for index := range app.Chat.Tools {
				if app.Chat.Tools[index].AcceptReferenceImages != nil && !*app.Chat.Tools[index].AcceptReferenceImages {
					tool := &app.Chat.Tools[index]
					tool.ReferencePurposesArgument = "prompt"
					tool.PromptWithReferencePurposesTemplate = "{purposes}{prompt}"
					tool.ReferencePurposeItemTemplate = "{position}{purpose}"
					return
				}
			}
			t.Skip("no reference-disabled tool in the shipped schema")
		},
	} {
		t.Run(name, func(t *testing.T) {
			broken := load()
			breakIt(&broken)
			if err := catalog.ValidateApplicationSchema(broken); err == nil {
				t.Fatal("an incomplete declaration was accepted")
			}
		})
	}
	// Without the lookup tool its texts are not required.
	withoutTool := load()
	tools := withoutTool.Chat.AgentTools[:0]
	for _, tool := range withoutTool.Chat.AgentTools {
		if tool.Name != "find_history_images" {
			tools = append(tools, tool)
		}
	}
	withoutTool.Chat.AgentTools = tools
	withoutTool.Chat.Context.HistoryImageLookupLimit = 0
	withoutTool.Chat.Context.HistoryImageBudgetTemplate = ""
	if err := catalog.ValidateApplicationSchema(withoutTool); err != nil {
		t.Fatalf("a Schema without the lookup tool must not need its texts: %v", err)
	}
	// The purposes parameter is an object of strings the model fills.
	var parameters struct {
		Properties map[string]struct {
			Type                 string          `json:"type"`
			AdditionalProperties json.RawMessage `json:"additionalProperties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(refine.Parameters, &parameters); err != nil {
		t.Fatal(err)
	}
	if property := parameters.Properties[refine.ReferencePurposesArgument]; property.Type != "object" || len(property.AdditionalProperties) == 0 {
		t.Fatalf("reference purposes parameter = %+v", property)
	}
}

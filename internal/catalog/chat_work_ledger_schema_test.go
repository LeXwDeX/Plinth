package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestDefaultWorkLedgerDeclarationIsValid(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatal(err)
	}
	ledger := app.Chat.WorkLedger
	if ledger.MaximumSteps <= 0 || ledger.SummaryMaximumCharacters <= 0 ||
		ledger.StepPrefix == "" || ledger.OpenMarker == "" || ledger.CloseMarker == "" {
		t.Fatalf("work_ledger = %+v", ledger)
	}
	declared := false
	for _, tool := range app.Chat.AgentTools {
		if tool.Name == "get_production_step" {
			declared = true
			if tool.Execution.ImplementationKey != "ledger.read" ||
				len(tool.Execution.Effects) != 1 || tool.Execution.Effects[0] != "READ" ||
				tool.Execution.Consent != "none" || tool.Execution.AgentScope != "utility" {
				t.Fatalf("tool execution = %+v", tool.Execution)
			}
		}
	}
	if !declared {
		t.Fatal("get_production_step is not declared")
	}
}

func TestWorkLedgerDeclarationIsValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
		want   string
	}{
		{"no steps bound", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.MaximumSteps = 0
		}, "maximum_steps"},
		{"no preview bound", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.PromptPreviewCharacters = 0
		}, "prompt_preview_characters"},
		{"a summary without steps", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.SummaryTemplate = "{open}{rejected}{close}"
		}, "{steps}"},
		{"a step line without self-check", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.StepLineTemplate = "{step} {task_id} {capability} {model} {status} {prompt} {decision}"
		}, "{self_check}"},
		{"an empty none text", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.NoneText = ""
		}, "none_text"},
		{"a self-check line without unknown", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.SelfCheckLineTemplate = "{tool} 通过 {passed} 失败 {failed}{leftovers}"
		}, "{unknown}"},
		{"failed views without a placeholder", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.FailedViewsTemplate = "failed"
		}, "{views}"},
		{"the same frame markers", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.CloseMarker = app.Chat.WorkLedger.OpenMarker
		}, "open_marker"},
		{"a step prefix that collides with image handles", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.StepPrefix = "@img"
		}, "handle_prefixes"},
		{"a step prefix that collides with the selection alias", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.StepPrefix = "@selected"
		}, "selection_alias"},
		{"a rejected line without a reason", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.Rejected.LineTemplate = "- {step}"
		}, "{reason}"},
		{"anchor markers that match", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.Anchors.CloseMarker = app.Chat.WorkLedger.Anchors.OpenMarker
		}, "anchors"},
		{"an anchor entry without references", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.Anchors.EntryTemplate = "{label}: {text}"
		}, "{references}"},
		{"missing references without a count", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.Anchors.MissingReferencesTemplate = "no handles"
		}, "{count}"},
		{"an anchor bound below the style lock", func(app *catalog.ApplicationSchema) {
			app.Chat.WorkLedger.Anchors.MaximumTextCharacters = app.Chat.StyleLock.MaximumTextCharacters - 1
		}, "style_lock.maximum_text_characters"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := loaded.Application()
			test.mutate(&app)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid work ledger")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestWorkLedgerIsOptionalWithoutItsTool(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	tools := app.Chat.AgentTools[:0]
	for _, tool := range app.Chat.AgentTools {
		if tool.Name != "get_production_step" {
			tools = append(tools, tool)
		}
	}
	app.Chat.AgentTools = tools
	app.Chat.WorkLedger = catalog.ChatWorkLedgerSchema{}
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a Catalog without the ledger tool must stay valid: %v", err)
	}
}

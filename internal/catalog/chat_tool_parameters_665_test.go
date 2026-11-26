package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// mutateAgentToolParameters rewrites one Agent tool's parameter document.
func mutateAgentToolParameters(t *testing.T, app *catalog.ApplicationSchema, name string, edit func(map[string]any)) {
	t.Helper()
	for index := range app.Chat.AgentTools {
		if app.Chat.AgentTools[index].Name != name {
			continue
		}
		var document map[string]any
		if err := json.Unmarshal(app.Chat.AgentTools[index].Parameters, &document); err != nil {
			t.Fatal(err)
		}
		edit(document)
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		app.Chat.AgentTools[index].Parameters = encoded
		return
	}
	t.Fatalf("no agent tool %q", name)
}

// #665: every chat tool's parameter declaration is parsed at load, not only
// checked for valid JSON, and create_three_view's arguments follow the
// three-view declaration.
func TestChatToolParametersAreValidatedAtLoad(t *testing.T) {
	property := func(document map[string]any, name string) map[string]any {
		return document["properties"].(map[string]any)[name].(map[string]any)
	}
	for name, test := range map[string]struct {
		tool string
		edit func(map[string]any)
		want string
	}{
		"a required name must be a property": {"get_task", func(d map[string]any) {
			d["required"] = []any{"task"}
		}, `"task" is not a declared property`},
		"an Agent tool object must be closed": {"get_task", func(d map[string]any) {
			delete(d, "additionalProperties")
		}, "additionalProperties: false"},
		"an enum must not be empty": {"propose_production_plan", func(d map[string]any) {
			step := property(d, "steps")["items"].(map[string]any)
			step["properties"].(map[string]any)["check"].(map[string]any)["enum"] = []any{}
		}, "enum: must be a non-empty list"},
		"bounds must be ordered": {"crop_image_regions", func(d map[string]any) {
			property(d, "regions")["minItems"] = 99
			property(d, "regions")["maxItems"] = 1
		}, "minItems is above maxItems"},
		"the three-view purpose follows three_view.purposes": {"create_three_view", func(d map[string]any) {
			property(d, "purpose")["enum"] = []any{"model_input"}
		}, "three_view.purposes"},
		"the four-view switch must be declared": {"create_three_view", func(d map[string]any) {
			delete(d["properties"].(map[string]any), "include_right")
		}, "include_right"},
		"the notes limit is the Schema's": {"update_session_notes", func(d map[string]any) {
			property(d, "notes")["maxLength"] = 5000
		}, "maximum_session_notes_characters"},
		"the three-view source is one picture": {"create_three_view", func(d map[string]any) {
			delete(property(d, "reference_handles"), "maxItems")
		}, "maxItems: 1"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutateAgentToolParameters(t, &app, test.tool, test.edit)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateApplicationSchema = %v, want an error containing %q", err, test.want)
			}
		})
	}
}

package catalog

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #292: a parameter whose presentation says follows_purpose has the value the
// session's production brief gives it until the person picks one. The 加工 panel
// leaves it out of a request while it is untouched and the server fills it from
// the brief, so the declaration is a promise about the parameter: the person edits
// it themselves, it is an optional select, and the Offering's default stands when
// no purpose gives a value. Only the export texture size of the Tripo processing
// Offerings carries it.

func TestFollowsPurposeIsDeclaredOnTheProcessingExportSizeOnly(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := c.Application().Chat.Model3DUsage
	limit, bounded := usage.BudgetLimit(schema.SemanticTextureSize)
	if !bounded {
		t.Fatal("the texture size is not a bounded semantic")
	}
	var flagged []string
	for _, offering := range c.byID {
		for _, parameter := range offering.Parameters {
			if parameter.Presentation == nil || !parameter.Presentation.FollowsPurpose {
				continue
			}
			flagged = append(flagged, string(offering.ID)+":"+parameter.Name)
			// A person edits it, it is optional, and its default stands without a purpose.
			if !offering.UserParameterAllowed(parameter) || parameter.Type != "select" || parameter.Required ||
				len(bytes.TrimSpace(parameter.Default)) == 0 || parameter.Presentation.Surface != schema.SurfaceProvider {
				t.Errorf("%s:%s follows the purpose but is not an optional select the person edits: %+v", offering.ID, parameter.Name, parameter)
			}
			// The purpose can give it a value: it is the bounded texture size, and every
			// option has a measure for the budget to fit.
			if parameter.Semantic != schema.SemanticTextureSize {
				t.Errorf("%s:%s follows the purpose but no purpose budget bounds it", offering.ID, parameter.Name)
			}
			for _, option := range parameter.Options {
				if _, measured := limit.OptionValues[option.Value]; !measured {
					t.Errorf("%s:%s option %s has no measure for the purpose budget", offering.ID, parameter.Name, option.Value)
				}
			}
		}
	}
	sort.Strings(flagged)
	want := make([]string, 0, len(processingExportOfferings))
	for _, test := range processingExportOfferings {
		want = append(want, test.id+":export_texture_size")
	}
	sort.Strings(want)
	if strings.Join(flagged, ",") != strings.Join(want, ",") {
		t.Fatalf("parameters that follow the purpose = %v, want exactly %v", flagged, want)
	}
}

// follows_purpose fails closed: a parameter that is not an optional select the
// person edits, or that has no default to stand on, does not load with it.
func TestFollowsPurposeDeclarationFailsClosed(t *testing.T) {
	find := func(document map[string]any, offering, parameter string) map[string]any {
		t.Helper()
		for _, rawOffering := range document["offerings"].([]any) {
			candidate := rawOffering.(map[string]any)
			if candidate["id"] != offering {
				continue
			}
			for _, rawParameter := range candidate["parameters"].([]any) {
				if entry := rawParameter.(map[string]any); entry["name"] == parameter {
					return entry
				}
			}
		}
		t.Fatalf("%s:%s is missing", offering, parameter)
		return nil
	}
	flag := func(entry map[string]any) {
		presentation, _ := entry["presentation"].(map[string]any)
		if presentation == nil {
			presentation = map[string]any{"surface": "provider", "control": "text"}
			entry["presentation"] = presentation
		}
		presentation["follows_purpose"] = true
	}
	cases := map[string]func(document map[string]any){
		"a number":                    func(d map[string]any) { flag(find(d, "tripo/geometry/decimate-triangle", "face_limit")) },
		"a hidden consent":            func(d map[string]any) { flag(find(d, "tripo/processing/rigging-v3", "authorize_extra_credits")) },
		"a fixed readonly value":      func(d map[string]any) { flag(find(d, "tripo/processing/rigging-v3", "out_format")) },
		"an advised value (a reason)": func(d map[string]any) { flag(find(d, "tripo/tripo-v3.1-full/text-to-3d", "texture_quality")) },
		"a required select": func(d map[string]any) {
			find(d, "tripo/processing/rigging-v3", "export_texture_size")["required"] = true
		},
		"a select with no default": func(d map[string]any) {
			delete(find(d, "tripo/processing/rigging-v3", "export_texture_size"), "default")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(defaultCatalog, &document); err != nil {
				t.Fatal(err)
			}
			mutate(document)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(bytes.NewReader(raw)); err == nil {
				t.Fatal("a follows_purpose declaration that breaks the promise was accepted")
			} else if !strings.Contains(err.Error(), "follows_purpose") {
				t.Fatalf("error = %v, want it to name the follows_purpose rule", err)
			}
		})
	}
}

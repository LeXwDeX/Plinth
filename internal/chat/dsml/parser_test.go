package dsml_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/dsml"
)

func TestParserUsesConfiguredDSMLProtocol(t *testing.T) {
	schema := configuredDSML()
	content := `visible text
<configured-marker-calls>
<configured-marker-run key="configured-tool">
<configured-marker-arg key="prompt" text="yes">paint it</configured-marker-arg>
<configured-marker-arg key="indices" text="no">[1,2]</configured-marker-arg>
</configured-marker-run>
</configured-marker-calls>`
	result := dsml.Parse(schema, content)
	if result.Content != "visible text" || len(result.ToolCalls) != 1 {
		t.Fatalf("result = %#v", result)
	}
	call := result.ToolCalls[0]
	if call.ID != "configured-0" || call.Name != "configured-tool" {
		t.Fatalf("call = %#v", call)
	}
	var arguments map[string]any
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	if arguments["prompt"] != "paint it" ||
		len(arguments["indices"].([]any)) != 2 {
		t.Fatalf("arguments = %#v", arguments)
	}
}

// #275: the default Schema reads the official DeepSeek spelling and the
// variant seen in production (doubled marker, a space before every element
// name, outer element "calls") with one grammar.
func TestParserReadsDefaultSchemaSpellingVariants(t *testing.T) {
	schema := defaultDSML(t)
	for name, content := range map[string]string{
		"official": "Let me refine it.\n" +
			"<｜DSML｜function_calls>\n" +
			`<｜DSML｜invoke name="refine_image">` + "\n" +
			`<｜DSML｜parameter name="prompt" string="true">sharper edges</｜DSML｜parameter>` + "\n" +
			`<｜DSML｜parameter name="reference_image_indices" string="false">[1]</｜DSML｜parameter>` + "\n" +
			"</｜DSML｜invoke>\n" +
			"</｜DSML｜function_calls>",
		"doubled marker": "Let me refine it.\n" +
			"<｜｜DSML｜｜tool_calls>\n" +
			`<｜｜DSML｜｜invoke name="refine_image">` + "\n" +
			`<｜｜DSML｜｜parameter name="prompt" string="true">sharper edges</｜｜DSML｜｜parameter>` + "\n" +
			`<｜｜DSML｜｜parameter name="reference_image_indices" string="false">[1]</｜｜DSML｜｜parameter>` + "\n" +
			"</｜｜DSML｜｜invoke>\n" +
			"</｜｜DSML｜｜tool_calls>",
		"spaced calls variant": "Let me refine it.\n" +
			"<｜｜DSML｜｜ calls>\n" +
			`<｜｜DSML｜｜ invoke name="refine_image">` + "\n" +
			`<｜｜DSML｜｜ parameter name="prompt" string="true">sharper edges</｜｜DSML｜｜ parameter>` + "\n" +
			`<｜｜DSML｜｜ parameter name="reference_image_indices" string="false">[1]</｜｜DSML｜｜ parameter>` + "\n" +
			"</｜｜DSML｜｜ invoke>\n" +
			"</｜｜DSML｜｜ calls>",
		"ascii marker": "Let me refine it.\n" +
			"<|DSML|function_calls>\n" +
			`<|DSML|invoke name="refine_image">` + "\n" +
			`<|DSML|parameter name="prompt" string="true">sharper edges</|DSML|parameter>` + "\n" +
			`<|DSML|parameter name="reference_image_indices" string="false">[1]</|DSML|parameter>` + "\n" +
			"</|DSML|invoke>\n" +
			"</|DSML|function_calls>",
	} {
		t.Run(name, func(t *testing.T) {
			result := dsml.Parse(schema, content)
			if result.Content != "Let me refine it." || len(result.ToolCalls) != 1 {
				t.Fatalf("result = %#v", result)
			}
			call := result.ToolCalls[0]
			if call.Name != "refine_image" || call.ID != "dsml_call_0" {
				t.Fatalf("call = %#v", call)
			}
			var arguments map[string]any
			if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
				t.Fatal(err)
			}
			indices, _ := arguments["reference_image_indices"].([]any)
			if arguments["prompt"] != "sharper edges" || len(indices) != 1 {
				t.Fatalf("arguments = %#v", arguments)
			}
		})
	}
}

func TestParserKeepsTextThatIsNotADeclaredSpelling(t *testing.T) {
	schema := defaultDSML(t)
	for _, content := range []string{
		"use <b>bold</b> text",
		"<｜DSML｜callsx>not a block",
		"<｜DSML｜unknown>not a block",
	} {
		result := dsml.Parse(schema, content)
		if result.Content != content || len(result.ToolCalls) != 0 {
			t.Fatalf("Parse(%q) = %#v", content, result)
		}
	}
	strict := schema
	strict.MarkerWhitespace = false
	spaced := "text <｜｜DSML｜｜ calls>"
	if result := dsml.Parse(strict, spaced); result.Content != spaced {
		t.Fatalf("whitespace accepted while marker_whitespace is off: %#v", result)
	}
}

func TestStreamingFilterSuppressesFragmentedDSMLWithoutBufferingText(
	t *testing.T,
) {
	schema := configuredDSML()
	visible := pushAll(t, dsml.NewFilter(schema), []string{
		"visible ",
		"text<configured-",
		"marker-ca",
		"lls><configured-marker-run",
	}, nil)
	if visible != "visible text" {
		t.Fatalf("visible = %q", visible)
	}
}

// #275: every declared spelling is held back while it may still become a
// tool-call tag, even when the stream splits it inside the marker, the
// whitespace or the element name; no half tag ever reaches the person.
func TestStreamingFilterHoldsEveryDeclaredSpellingAcrossChunks(t *testing.T) {
	schema := defaultDSML(t)
	for name, chunks := range map[string][]string{
		"official": {"Let me", " refine.<", "｜DS", "ML｜func", "tion_calls", ">\n<｜DSML｜invoke"},
		"spaced calls variant": {
			"Let me refine.", "<｜", "｜DSML｜", "｜", " ", " ca", "lls", ">", "\n<｜｜DSML｜｜ invoke name=",
		},
		"marker split mid-rune": {"Let me refine.<\xef\xbd", "\x9c\xef\xbd\x9cDSML\xef\xbd\x9c\xef\xbd\x9c calls>"},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			visible := pushAll(t, dsml.NewFilter(schema), chunks, &seen)
			if visible != "Let me refine." {
				t.Fatalf("visible = %q, emitted %q", visible, seen)
			}
			for _, part := range seen {
				if strings.ContainsAny(part, "<｜|") {
					t.Fatalf("part of a tag leaked: %q", seen)
				}
			}
		})
	}
}

func TestStreamingFilterReleasesHeldTextThatIsNotATag(t *testing.T) {
	schema := defaultDSML(t)
	var seen []string
	filter := dsml.NewFilter(schema)
	visible := pushAll(t, filter, []string{"a <", "b> and <｜DSML｜", "note> done"}, &seen)
	if visible != "a <b> and <｜DSML｜note> done" {
		t.Fatalf("visible = %q", visible)
	}
	// Ordinary text is never held: each chunk without a possible tag start
	// goes out in the same Push.
	if len(seen) != 3 || seen[0] != "a " || seen[1] != "<b> and " {
		t.Fatalf("emitted = %q", seen)
	}
}

func pushAll(
	t *testing.T,
	filter *dsml.Filter,
	chunks []string,
	seen *[]string,
) string {
	t.Helper()
	var visible strings.Builder
	emit := func(value string) error {
		visible.WriteString(value)
		if seen != nil {
			*seen = append(*seen, value)
		}
		return nil
	}
	for _, chunk := range chunks {
		if err := filter.Push(chunk, emit); err != nil {
			t.Fatal(err)
		}
	}
	if err := filter.Flush(emit); err != nil {
		t.Fatal(err)
	}
	return visible.String()
}

func defaultDSML(t *testing.T) catalog.ChatDSMLSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application().Chat.DSML
	// The default Schema keeps chat.dsml off; a model profile turns it on.
	schema.Enabled = true
	return schema
}

func configuredDSML() catalog.ChatDSMLSchema {
	return catalog.ChatDSMLSchema{
		Enabled:           true,
		Markers:           []string{"configured-marker-"},
		ToolCallsElements: []string{"calls"},
		InvokeElement:     "run",
		ParameterElement:  "arg",
		NameAttribute:     "key",
		StringAttribute:   "text",
		TrueValue:         "yes",
		FalseValue:        "no",
		CallIDTemplate:    "configured-{index}",
	}
}

// #189: a DeepSeek-style call to the Creative Lab design tool keeps the
// template as a string and the handle list as JSON, so the executor can pick
// the template's Offering; a build call carries only the preview handle.
func TestParserReadsCreativeLabCalls(t *testing.T) {
	schema := defaultDSML(t)
	content := "我先做钥匙扣设计。\n" +
		"<｜DSML｜function_calls>\n" +
		`<｜DSML｜invoke name="create_creative_lab_design">` + "\n" +
		`<｜DSML｜parameter name="template" string="true">creative-lab-keychain-prototype</｜DSML｜parameter>` + "\n" +
		`<｜DSML｜parameter name="reference_handles" string="false">["@img2"]</｜DSML｜parameter>` + "\n" +
		`<｜DSML｜parameter name="engraving_text" string="true">PLINTH</｜DSML｜parameter>` + "\n" +
		"</｜DSML｜invoke>\n" +
		`<｜DSML｜invoke name="build_creative_lab_item">` + "\n" +
		`<｜DSML｜parameter name="source_task" string="true">@img3</｜DSML｜parameter>` + "\n" +
		"</｜DSML｜invoke>\n" +
		"</｜DSML｜function_calls>"
	result := dsml.Parse(schema, content)
	if result.Content != "我先做钥匙扣设计。" || len(result.ToolCalls) != 2 {
		t.Fatalf("result = %#v", result)
	}
	var design map[string]any
	if err := json.Unmarshal(result.ToolCalls[0].Arguments, &design); err != nil {
		t.Fatal(err)
	}
	handles, _ := design["reference_handles"].([]any)
	if result.ToolCalls[0].Name != "create_creative_lab_design" || design["template"] != "creative-lab-keychain-prototype" ||
		len(handles) != 1 || handles[0] != "@img2" || design["engraving_text"] != "PLINTH" {
		t.Fatalf("design call = %s %#v", result.ToolCalls[0].Name, design)
	}
	var build map[string]any
	if err := json.Unmarshal(result.ToolCalls[1].Arguments, &build); err != nil {
		t.Fatal(err)
	}
	if result.ToolCalls[1].Name != "build_creative_lab_item" || len(build) != 1 || build["source_task"] != "@img3" {
		t.Fatalf("build call = %s %#v", result.ToolCalls[1].Name, build)
	}
}

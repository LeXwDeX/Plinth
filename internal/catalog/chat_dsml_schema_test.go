package catalog_test

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #275: the Schema declares every accepted DSML spelling — the official
// "<｜DSML｜function_calls>", the doubled marker some relays emit, the "calls"
// alias and whitespace between marker and element name.
func TestDefaultCatalogDeclaresDSMLSpellingVariants(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat
	for _, marker := range []string{"｜DSML｜", "｜｜DSML｜｜"} {
		if !slices.Contains(chat.DSML.Markers, marker) {
			t.Fatalf("markers = %q, missing %q", chat.DSML.Markers, marker)
		}
	}
	for _, element := range []string{"function_calls", "tool_calls", "calls"} {
		if !slices.Contains(chat.DSML.ToolCallsElements, element) {
			t.Fatalf("tool_calls_elements = %q, missing %q", chat.DSML.ToolCallsElements, element)
		}
	}
	if !chat.DSML.MarkerWhitespace {
		t.Fatal("marker_whitespace must accept the spaced variant")
	}
}

func TestDSMLSpellingsAndToolTextsAreValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	markers := `"markers": [
          "｜DSML｜",
          "｜｜DSML｜｜",
          "|DSML|",
          "||DSML||"
        ]`
	elements := `"tool_calls_elements": [
          "function_calls",
          "tool_calls",
          "calls"
        ]`
	for name, test := range map[string]struct{ old, new, detail string }{
		"no markers":        {markers, `"markers": []`, "dsml.markers"},
		"blank marker":      {markers, `"markers": [""]`, "dsml.markers"},
		"spaced marker":     {markers, `"markers": ["｜DSML｜ "]`, "dsml.markers"},
		"duplicate marker":  {markers, `"markers": ["｜DSML｜", "｜DSML｜"]`, "重复"},
		"no elements":       {elements, `"tool_calls_elements": []`, "dsml.tool_calls_elements"},
		"tag in element":    {elements, `"tool_calls_elements": ["calls>"]`, "dsml.tool_calls_elements"},
		"duplicate element": {elements, `"tool_calls_elements": ["calls", "calls"]`, "重复"},
		"no invalid-arguments text": {`"tool_arguments_invalid": "工具参数不是合法的 JSON，该工具没有执行。请按工具定义重新生成参数后再调用。"`,
			`"tool_arguments_invalid": ""`, "tool_arguments_invalid"},
		"unavailable text without the tool name": {`"tool_unavailable": "工具 {tool} 本回合不可用，没有执行。请改用当前可用的工具，或不用工具直接回答。"`,
			`"tool_unavailable": "本回合不可用"`, "tool_unavailable"},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
}

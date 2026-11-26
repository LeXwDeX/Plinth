package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func testToolResultNote(cut toolResultCut) string {
	return map[toolResultCut]string{
		toolResultCutStrings: "【截断：长文本】", toolResultCutItems: "【截断：列表】", toolResultCutText: "【截断：文本】",
	}[cut]
}

// #665: an object result over the bound keeps valid JSON, cuts long strings
// before list items, carries the note and fits the bound.
func TestShortenToolResultKeepsJSONAndCutsStringsFirst665(t *testing.T) {
	offerings := make([]map[string]any, 0, 30)
	for range 30 {
		offerings = append(offerings, map[string]any{"offering_id": "meshy/image-to-3d",
			"knowledge": strings.Repeat("适合写实角色的模型说明。", 40)})
	}
	raw, _ := json.Marshal(map[string]any{"capabilities": []any{map[string]any{"offerings": offerings}}})
	content := string(raw)
	limit := utf8.RuneCountInString(content) / 3
	shortened, cut := shortenToolResult(content, limit, testToolResultNote)
	if cut != toolResultCutStrings || utf8.RuneCountInString(shortened) > limit || !utf8.ValidString(shortened) {
		t.Fatalf("cut=%d size=%d limit=%d", cut, utf8.RuneCountInString(shortened), limit)
	}
	var decoded struct {
		Note         string `json:"truncation_note"`
		Capabilities []struct {
			Offerings []struct {
				ID        string `json:"offering_id"`
				Knowledge string `json:"knowledge"`
			} `json:"offerings"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(shortened), &decoded); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if decoded.Note != "【截断：长文本】" || len(decoded.Capabilities[0].Offerings) != 30 ||
		decoded.Capabilities[0].Offerings[29].ID != "meshy/image-to-3d" ||
		!strings.HasSuffix(decoded.Capabilities[0].Offerings[0].Knowledge, toolResultEllipsis) {
		t.Fatalf("decoded = %+v", decoded)
	}
}

// #665: when cutting strings is not enough, trailing list items go; an array
// result is wrapped to carry the note.
func TestShortenToolResultCutsListItemsAndWrapsArrays665(t *testing.T) {
	items := make([]string, 400)
	for index := range items {
		items[index] = "task_0123456789"
	}
	raw, _ := json.Marshal(items)
	shortened, cut := shortenToolResult(string(raw), 600, testToolResultNote)
	var decoded struct {
		Items []string `json:"items"`
		Note  string   `json:"truncation_note"`
	}
	if cut != toolResultCutItems || utf8.RuneCountInString(shortened) > 600 ||
		json.Unmarshal([]byte(shortened), &decoded) != nil || len(decoded.Items) == 0 ||
		len(decoded.Items) >= 400 || decoded.Note != "【截断：列表】" {
		t.Fatalf("cut=%d shortened=%q", cut, shortened)
	}
}

// #665: plain text keeps its beginning without splitting a rune and ends with
// the note; a result under the bound is untouched by the orchestrator hook.
func TestShortenToolResultTextIsRuneSafe665(t *testing.T) {
	content := strings.Repeat("图", 500) + "😀" + strings.Repeat("a", 500)
	for limit := 30; limit < 1100; limit += 37 {
		shortened, cut := shortenToolResult(content, limit, testToolResultNote)
		if cut != toolResultCutText || !utf8.ValidString(shortened) ||
			utf8.RuneCountInString(shortened) > limit || !strings.HasSuffix(shortened, "【截断：文本】") {
			t.Fatalf("limit %d: cut=%d %q", limit, cut, shortened)
		}
	}
	if got := runePrefix("ab😀cd", 3); got != "ab😀" {
		t.Fatalf("runePrefix = %q", got)
	}
}

func TestCapToolResultUnderTheBoundOrUnlimitedIsUntouched665(t *testing.T) {
	orchestrator := &Orchestrator{}
	orchestrator.schema.Agent.MaxToolResultCharacters = 10
	orchestrator.schema.Presentation.Messages.ToolResultTruncated = "{cut}"
	if got := orchestrator.capToolResult(t.Context(), "get_task", "短结果"); got != "短结果" {
		t.Fatalf("under the bound = %q", got)
	}
	orchestrator.schema.Agent.MaxToolResultCharacters = 0
	long := strings.Repeat("x", 100)
	if got := orchestrator.capToolResult(t.Context(), "get_task", long); got != long {
		t.Fatalf("0 = unlimited, got %q", got)
	}
}

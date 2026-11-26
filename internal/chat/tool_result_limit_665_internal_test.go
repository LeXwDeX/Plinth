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

// #665: IDs, handles and URLs (no whitespace, shorter than the token maximum)
// stay whole while free text is cut; when even that does not fit, list items
// go with their tokens whole, so an entry's handle leaves with its entry and the
// handle list keeps the same first entries.
func TestShortenToolResultKeepsTokensWhole665(t *testing.T) {
	url := "https://assets.example.test/v1/" + strings.Repeat("a1b2c3d4", 40) + ".png?signature=" + strings.Repeat("x", 60)
	views := make([]map[string]any, 0, 12)
	handles := make([]any, 0, 12)
	for index := range 12 {
		handle := "@img" + strings.Repeat("9", index+1)
		views = append(views, map[string]any{"handle": handle, "url": url,
			"notes": strings.Repeat("一段很长的描述文字，可以截短。 ", 30)})
		handles = append(handles, handle)
	}
	raw, _ := json.Marshal(map[string]any{"views": views, "result_handles": handles})
	content := string(raw)
	type decodedResult struct {
		Note  string `json:"truncation_note"`
		Views []struct {
			Handle, URL, Notes string
		} `json:"views"`
		Handles []string `json:"result_handles"`
	}
	// Room for every view once the notes are short: strings are cut, tokens kept.
	limit := utf8.RuneCountInString(content) * 2 / 3
	shortened, cut := shortenToolResult(content, limit, testToolResultNote)
	var decoded decodedResult
	if cut != toolResultCutStrings || utf8.RuneCountInString(shortened) > limit ||
		json.Unmarshal([]byte(shortened), &decoded) != nil || len(decoded.Views) != 12 {
		t.Fatalf("cut=%d size=%d limit=%d", cut, utf8.RuneCountInString(shortened), limit)
	}
	for _, view := range decoded.Views {
		if view.URL != url || !strings.HasSuffix(view.Notes, toolResultEllipsis) {
			t.Fatalf("a token was cut or free text kept: %+v", view)
		}
	}
	// No room for every view: entries go, tokens stay whole, and the handle
	// list keeps the same first entries.
	limit = utf8.RuneCountInString(url) * 5
	shortened, cut = shortenToolResult(content, limit, testToolResultNote)
	decoded = decodedResult{}
	if cut != toolResultCutItems || json.Unmarshal([]byte(shortened), &decoded) != nil || len(decoded.Views) == 0 ||
		len(decoded.Views) >= 12 || len(decoded.Handles) != len(decoded.Views) {
		t.Fatalf("cut=%d shortened=%q", cut, shortened)
	}
	for index, view := range decoded.Views {
		if view.URL != url || view.Handle != decoded.Handles[index] {
			t.Fatalf("entry %d: %+v handles %v", index, view, decoded.Handles)
		}
	}
}

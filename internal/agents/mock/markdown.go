package mock

import (
	"context"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
)

// mockMarkdownMarker in the person's message makes the mock Agent answer in
// Markdown instead of calling a tool (#333), so the chat's Markdown subset and
// its XSS cases run end to end on the mock stack. Only the mock reads it.
const mockMarkdownMarker = "[mock:markdown]"

// mockMarkdownPieces is the reply as it streams. The cuts fall inside a bold
// run, an inline code span and a link, the places where a half-received reply
// could flash raw Markdown. The dangerous lines must come out as plain text.
var mockMarkdownPieces = []string{
	"**Markdown 检查 / check**\n\n这是 **粗",
	"体**、*斜体* 和 `inline <co",
	"de>`；**注意：**中文标点后的粗体。\n\n1. 第一步 first\n2. 第二步 with `code`\n   - 嵌套 nested\n\n- 无序 item\n- __下划线粗体__\n\n",
	"```go\nfmt.Println(\"<b>not bold</b>\")\n```\n\n[安全链接 safe link](https://exa",
	"mple.com/docs?a=1&b=2) [bad js](javascript:alert(1)) [bad data](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)\n\n",
	"<script>window.__xss333=1</script> <img src=x onerror=\"window.__xss333=2\"> &lt;b&gt;entity&lt;/b&gt; &amp; ![image](https://example.com/x.png)",
}

// mockMarkdownPause spaces the pieces so a browser sees the reply mid-stream.
const mockMarkdownPause = 150 * time.Millisecond

func markdownTurn(text string) bool {
	return strings.Contains(text, mockMarkdownMarker)
}

// streamMarkdown answers with the Markdown reply; the handles the person
// wrote close it, the first one in bold.
func streamMarkdown(ctx context.Context, prompt string, emit func(agents.Delta) error) (agents.Completion, error) {
	pieces := append([]string(nil), mockMarkdownPieces...)
	if handles := summaryHandleText.FindAllString(prompt, -1); len(handles) > 0 {
		line := "\n\n句柄 / handles: **" + handles[0] + "**"
		if len(handles) > 1 {
			line += " " + strings.Join(handles[1:], " ")
		}
		pieces = append(pieces, line)
	}
	var content strings.Builder
	for index, piece := range pieces {
		if index > 0 && emit != nil {
			select {
			case <-ctx.Done():
				return agents.Completion{}, ctx.Err()
			case <-time.After(mockMarkdownPause):
			}
		}
		if emit != nil {
			if err := emit(agents.Delta{Content: piece}); err != nil {
				return agents.Completion{}, err
			}
		}
		content.WriteString(piece)
	}
	return agents.Completion{Content: content.String()}, nil
}

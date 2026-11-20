package chat

import (
	"strings"
	"unicode"
)

// A reply can take several model rounds: the Agent narrates, calls a tool,
// then narrates the result. The rounds' visible text is one public message, and
// without a break between them the sentences run together (#402).

// narrationBreak is the paragraph break that goes right before the first visible
// character of a later round, given the reply's text up to that point. Nothing
// is added when that text has no visible character yet (the rounds before only
// called tools) or already ends with a line break.
func narrationBreak(before string) string {
	if strings.TrimSpace(before) == "" || strings.HasSuffix(before, "\n") {
		return ""
	}
	return "\n\n"
}

// joinNarration appends the text of a later round to the reply's text so far.
// The break goes before the round's first visible character, after whatever
// whitespace the round opens with: the stream has already sent that whitespace
// when the first visible character arrives, so the stream (thinkingRound.
// emitText) and the stored text place the break the same way and carry the same
// text. A round without a visible character is appended as it is.
func joinNarration(before, next string) string {
	visible := strings.TrimLeftFunc(next, unicode.IsSpace)
	if visible == "" {
		return before + next
	}
	head := before + next[:len(next)-len(visible)]
	return head + narrationBreak(head) + visible
}

package catalog

import (
	"strconv"
	"strings"
)

// ComposeTaskNotice is the sentence of a succeeded Task's notice (#284),
// built from Schema messages only: task.notice.<code>, or, when the notice
// lists positions and the Schema has the sentence that names them,
// task.notice.<code>_named with {inputs} replaced by the positions. Each
// position is the message task.notice.input with {ordinal} filled, joined by
// task.notice.input_separator. message looks a key up in the caller's locale
// (and may return "" for an unknown key). The result is "" when the Schema
// has no text for the code, so a caller drops a notice it cannot say rather
// than inventing the words.
func ComposeTaskNotice(message func(key string) string, code string, inputs []int) string {
	if code == "" || message == nil {
		return ""
	}
	base := "task.notice." + code
	if len(inputs) > 0 {
		if sentence := message(base + "_named"); sentence != "" {
			item := message("task.notice.input")
			names := make([]string, len(inputs))
			for index, position := range inputs {
				names[index] = strings.ReplaceAll(item, "{ordinal}", strconv.Itoa(position))
			}
			return strings.ReplaceAll(sentence, "{inputs}", strings.Join(names, message("task.notice.input_separator")))
		}
	}
	return message(base)
}

// TaskNoticeText is ComposeTaskNotice in a locale: the Schema's default text
// where a bundle does not translate a message.
func (l *TextLocalizer) TaskNoticeText(locale, code string, inputs []int) string {
	if l == nil {
		return ""
	}
	return ComposeTaskNotice(func(key string) string { return l.Message(locale, key) }, code, inputs)
}

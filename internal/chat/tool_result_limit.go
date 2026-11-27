package chat

// Agent tool result size bound (#665). The text of one executed tool's result
// that goes back to the model is bounded by chat.agent.max_tool_result_characters
// (Unicode code points). A JSON result stays valid JSON: long string values
// are cut first, then trailing list items, and the note goes into the object
// (an array result is wrapped). Anything else, or JSON that still does not
// fit, keeps its beginning as plain text. The cut never splits a rune. The
// note is the Schema's presentation.messages.tool_result_truncated.
//
// A printable ASCII string without whitespace and shorter than
// toolResultTokenMaximum (an ID, a handle, a URL) is a token: a cut one is useless to the model, so free text is
// cut first, then list items with the tokens whole; only when that still does
// not fit are tokens cut like free text. Lists are cut the same way at every
// level (the first n entries of each), so the entries of a result's
// PresentedEntries array and its handle list keep the same first n: a handle
// is written into its entry before the bound applies and leaves with it. The
// pictures attached to the tool message are not cut; the note says entries
// were cut.

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// toolResultNoteField carries the truncation note inside a JSON result;
	// toolResultItemsField holds an array result wrapped to carry it.
	toolResultNoteField  = "truncation_note"
	toolResultItemsField = "items"
	// toolResultStringFloor is the shortest a string value is cut to before
	// lists are cut instead.
	toolResultStringFloor = 80
	toolResultEllipsis    = "…"
	// toolResultTokenMaximum is the length (characters) below which a string
	// without whitespace is kept whole while anything else can be cut.
	toolResultTokenMaximum = 2000
)

// toolResultCut names what was cut, so the note can say it.
type toolResultCut int

const (
	toolResultCutStrings toolResultCut = iota
	toolResultCutItems
	toolResultCutText
)

// capToolResult bounds one tool result for the model. Without a bound or
// without the Schema note the content is returned as is.
func (orchestrator *Orchestrator) capToolResult(ctx context.Context, tool, content string) string {
	limit := orchestrator.schema.Agent.MaxToolResultCharacters
	messages := orchestrator.schema.Presentation.Messages
	if limit <= 0 || strings.TrimSpace(messages.ToolResultTruncated) == "" {
		return content
	}
	size := utf8.RuneCountInString(content)
	if size <= limit {
		return content
	}
	template := orchestrator.text(ctx, messages.ToolResultTruncated)
	phrases := map[toolResultCut]string{
		toolResultCutStrings: orchestrator.text(ctx, messages.ToolResultCutStrings),
		toolResultCutItems:   orchestrator.text(ctx, messages.ToolResultCutItems),
		toolResultCutText:    orchestrator.text(ctx, messages.ToolResultCutText),
	}
	note := func(cut toolResultCut) string {
		return strings.NewReplacer("{tool}", tool, "{size}", strconv.Itoa(size),
			"{limit}", strconv.Itoa(limit), "{cut}", phrases[cut]).Replace(template)
	}
	shortened, cut := shortenToolResult(content, limit, note)
	if orchestrator.logger != nil {
		orchestrator.logger.Info("Agent 工具结果超过 Schema 上限，已截断后交给模型",
			"tool", tool, "characters", size, "limit", limit, "cut", int(cut),
			"result_characters", utf8.RuneCountInString(shortened))
	}
	return shortened
}

// shortenToolResult returns content shortened to at most limit characters
// (when the note itself fits) and what was cut.
func shortenToolResult(content string, limit int, note func(toolResultCut) string) (string, toolResultCut) {
	if shortened, cut, ok := shortenJSONToolResult(content, limit, note); ok {
		return shortened, cut
	}
	text := note(toolResultCutText)
	keep := limit - utf8.RuneCountInString(text) - 1
	return runePrefix(content, keep) + "\n" + text, toolResultCutText
}

// shortenJSONToolResult cuts a JSON object or array result. ok is false when
// the content is not such JSON or cannot be made to fit.
func shortenJSONToolResult(content string, limit int, note func(toolResultCut) string) (string, toolResultCut, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') || !json.Valid([]byte(trimmed)) {
		return "", 0, false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", 0, false
	}
	// Tokens whole first (keepTokens), then everything as free text.
	for _, keepTokens := range []bool{true, false} {
		maxString, maxItems := jsonExtent(value, keepTokens)
		encode := func(stringLimit, itemLimit int, cut toolResultCut) (string, bool) {
			encoded, err := encodeToolResult(withTruncationNote(cutJSON(value, stringLimit, itemLimit, keepTokens), note(cut)))
			if err != nil {
				return "", false
			}
			return encoded, utf8.RuneCountInString(encoded) <= limit
		}
		// Long strings first: every entry of every list stays visible.
		if maxString > toolResultStringFloor {
			if best, found := largestFitting(toolResultStringFloor, maxString, func(n int) bool {
				_, fits := encode(n, maxItems, toolResultCutStrings)
				return fits
			}); found {
				encoded, _ := encode(best, maxItems, toolResultCutStrings)
				return encoded, toolResultCutStrings, true
			}
		}
		if best, found := largestFitting(0, maxItems, func(n int) bool {
			_, fits := encode(toolResultStringFloor, n, toolResultCutItems)
			return fits
		}); found {
			encoded, _ := encode(toolResultStringFloor, best, toolResultCutItems)
			return encoded, toolResultCutItems, true
		}
	}
	return "", 0, false
}

// toolResultToken reports whether a string value is a token (an ID, a handle,
// a URL): printable ASCII without whitespace, shorter than
// toolResultTokenMaximum characters. Text in a script written without spaces
// (Chinese) is free text, not a token.
func toolResultToken(text string) bool {
	if text == "" || len(text) >= toolResultTokenMaximum {
		return false
	}
	for index := 0; index < len(text); index++ {
		if text[index] <= ' ' || text[index] >= 0x7f {
			return false
		}
	}
	return true
}

// largestFitting finds the largest n in [low, high] with fits(n), assuming fits
// is monotonic (true up to some n, false above it).
func largestFitting(low, high int, fits func(int) bool) (int, bool) {
	if high < low || !fits(low) {
		return 0, false
	}
	for low < high {
		middle := low + (high-low+1)/2
		if fits(middle) {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return low, true
}

// jsonExtent reports the longest string value that may be cut (in
// characters; tokens are left out when keepTokens) and the longest list of a
// decoded JSON value.
func jsonExtent(value any, keepTokens bool) (longestString, longestList int) {
	switch typed := value.(type) {
	case string:
		if keepTokens && toolResultToken(typed) {
			return 0, 0
		}
		return utf8.RuneCountInString(typed), 0
	case []any:
		longestList = len(typed)
		for _, child := range typed {
			s, l := jsonExtent(child, keepTokens)
			longestString, longestList = max(longestString, s), max(longestList, l)
		}
	case map[string]any:
		for _, child := range typed {
			s, l := jsonExtent(child, keepTokens)
			longestString, longestList = max(longestString, s), max(longestList, l)
		}
	}
	return longestString, longestList
}

// cutJSON copies a decoded JSON value with every string value cut to at most
// maxString characters (plus an ellipsis) and every list to maxItems entries.
// Object keys are never cut, nor tokens when keepTokens.
func cutJSON(value any, maxString, maxItems int, keepTokens bool) any {
	switch typed := value.(type) {
	case string:
		if keepTokens && toolResultToken(typed) {
			return typed
		}
		if utf8.RuneCountInString(typed) > maxString {
			return runePrefix(typed, maxString) + toolResultEllipsis
		}
		return typed
	case []any:
		count := min(len(typed), maxItems)
		result := make([]any, 0, count)
		for _, child := range typed[:count] {
			result = append(result, cutJSON(child, maxString, maxItems, keepTokens))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = cutJSON(child, maxString, maxItems, keepTokens)
		}
		return result
	}
	return value
}

// withTruncationNote puts the note into an object result, or wraps an array
// result in an object that carries it.
func withTruncationNote(value any, note string) any {
	if object, ok := value.(map[string]any); ok {
		object[toolResultNoteField] = note
		return object
	}
	return map[string]any{toolResultItemsField: value, toolResultNoteField: note}
}

func encodeToolResult(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// runePrefix keeps the first n characters of text, never splitting a rune.
func runePrefix(text string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for index := range text {
		if count == n {
			return text[:index]
		}
		count++
	}
	return text
}

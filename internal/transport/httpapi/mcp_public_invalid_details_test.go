package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// #517: why an argument was refused is the Schema's text in the request's
// language, after the JSON path it names; the problem code stays the same.
func TestMCPInvalidArgumentDetailFollowsTheRequestLanguage(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	details := f.application.MCPPublic.Messages.InvalidDetails
	arguments := func(idempotency string) map[string]any {
		return map[string]any{"prompt": "x", "service": "openai/gpt-image-2/text-to-image",
			"idempotency_key": idempotency, "parameters": map[string]any{"no_such_parameter": 1}}
	}
	chinese := f.call(key, "generate_image", arguments("invalid-detail-zh"))
	if chinese.problemCode() != "invalid_arguments" ||
		!strings.Contains(chinese.text[0], "$.parameters.no_such_parameter: "+details.ParameterNotSettable) {
		t.Fatalf("default language refusal = %s", chinese.raw)
	}
	english := decodeToolResult(t, f.post(key, "tools/call", map[string]any{"name": "generate_image", "arguments": arguments("invalid-detail-en")},
		func(r *http.Request) { r.Header.Set("Accept-Language", "en") }))
	if english.problemCode() != "invalid_arguments" ||
		!strings.Contains(english.text[0], "$.parameters.no_such_parameter: is not a parameter this service lets a caller set") ||
		strings.Contains(english.text[0], details.ParameterNotSettable) {
		t.Fatalf("english refusal = %s", english.raw)
	}
	cursor := f.call(key, "list_tasks", map[string]any{"cursor": "not-a-cursor"})
	if cursor.problemCode() != "invalid_arguments" || !strings.Contains(cursor.text[0], "$.cursor: "+details.NotACursor) {
		t.Fatalf("a bad cursor = %s", cursor.raw)
	}
}

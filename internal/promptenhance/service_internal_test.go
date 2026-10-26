package promptenhance

import "testing"

func TestStripCodeFence(t *testing.T) {
	cases := map[string]string{
		"```json\n{\"prompt\":\"a\"}\n```": "{\"prompt\":\"a\"}",
		"```\n{\"prompt\":\"a\"}\n```":     "{\"prompt\":\"a\"}",
		"```JSON\n{\"prompt\":\"a\"}\n```": "{\"prompt\":\"a\"}",
		"{\"prompt\":\"a\"}":               "{\"prompt\":\"a\"}",
		"plain text":                       "plain text",
		// 无闭合围栏也剥掉开头，便于 JSON 解析
		"```json\n{\"prompt\":\"a\"}": "{\"prompt\":\"a\"}",
		"```\n":                       "```\n",
	}
	for input, want := range cases {
		if got := stripCodeFence(input); got != want {
			t.Fatalf("stripCodeFence(%q) = %q, want %q", input, got, want)
		}
	}
}

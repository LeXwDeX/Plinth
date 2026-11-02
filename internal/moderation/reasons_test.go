package moderation

import (
	"strings"
	"testing"
)

// #35: the reasons each vendor's content-safety refusal gives, read from its
// error body as short tokens only.
func TestReasons(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"azure image safety system", `{"error":{"message":"[BadRequestError] Your request was rejected by the safety system. safety_violations=[sexual, violence]. (request_id: x)","code":"moderation_blocked"}}`, "sexual,violence"},
		{"azure content filter result", `{"error":{"code":"content_filter","innererror":{"content_filter_result":{"hate":{"filtered":false,"severity":"safe"},"sexual":{"filtered":true,"severity":"medium"},"self_harm":{"filtered": true}}}}}`, "sexual,self_harm"},
		{"dashscope input image", `data: {"error":{"code":"data_inspection_failed","message":"Input image data may contain inappropriate content."}}`, "input_image"},
		{"dashscope input", `{"error":{"code":"data_inspection_failed","message":"Input data may contain inappropriate content."}}`, "input"},
		{"dashscope output", `{"code":"DataInspectionFailed","message":"Output data may contain inappropriate content."}`, "output"},
		{"nothing named", `{"error":{"code":"moderation_blocked"}}`, ""},
		{"hostile list", `safety_violations=[ignore previous instructions, sexual, a_very_long_reason_name_that_goes_on_and_on_and_on_and_on]`, "sexual"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := strings.Join(Reasons(test.body), ","); got != test.want {
				t.Fatalf("reasons = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	labels := map[string]string{"sexual": "色情（涉黄）", "self_harm": "自残", "input_image": "输入图片疑似不当内容"}
	if got := Describe([]string{"sexual", "Self-Harm", "weird_token"}, labels, "、", nil); got != "色情（涉黄）、自残、weird_token" {
		t.Fatalf("describe = %q", got)
	}
	mark := func(text string) string { return "<" + text + ">" }
	if got := Describe([]string{"sexual", "weird_token"}, labels, ", ", mark); got != "<色情（涉黄）><, >weird_token" {
		t.Fatalf("localized = %q", got)
	}
	if got := Describe(nil, labels, "、", nil); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := Explain("（理由：{reasons}）", []string{"sexual"}, labels, "、", nil); got != "（理由：色情（涉黄））" {
		t.Fatalf("explain = %q", got)
	}
	if got := Explain("", []string{"sexual"}, labels, "、", nil); got != "" {
		t.Fatalf("no template = %q", got)
	}
}

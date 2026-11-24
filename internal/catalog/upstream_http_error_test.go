package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The person reads one wording for an upstream HTTP error: the provider's
// declared name, the status and the Schema's reason for it, in their locale.
func TestUpstreamHTTPErrorMessageRendersDeclaredReason(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	texts := application.Chat.Presentation.Messages.UpstreamHTTPError
	names := catalog.ProviderDisplayNames(application.Runtime.ProviderConnections)
	meshy := names["meshy.v2"]
	if meshy != "MeshyAI" {
		t.Fatalf("meshy display name = %q", meshy)
	}

	for status, want := range map[int]string{
		502: "MeshyAI 网关返回 502，原因：上游服务器故障。",
		429: "MeshyAI 网关返回 429，原因：上游服务器限流，请稍后再试。",
		500: "MeshyAI 网关返回 500，原因：上游服务器故障。",
		504: "MeshyAI 网关返回 504，原因：上游服务器故障。",
		408: "MeshyAI 网关返回 408，原因：上游服务器响应超时。",
		401: "MeshyAI 网关返回 401，原因：上游凭据无效或无权限，请联系管理员。",
		403: "MeshyAI 网关返回 403，原因：上游凭据无效或无权限，请联系管理员。",
		402: "MeshyAI 网关返回 402，原因：上游账户余额不足。",
		404: "MeshyAI 网关返回 404，原因：上游拒绝了请求，请检查输入。",
		422: "MeshyAI 网关返回 422，原因：上游拒绝了请求，请检查输入。",
	} {
		if got := texts.Message(meshy, status, nil); got != want {
			t.Errorf("status %d = %q, want %q", status, got, want)
		}
	}
	// No error status, or no provider name: the caller keeps its own text.
	for _, status := range []int{0, 200, 302, 600} {
		if got := texts.Message(meshy, status, nil); got != "" {
			t.Errorf("status %d = %q, want empty", status, got)
		}
	}
	if got := texts.Message("", 502, nil); got != "" {
		t.Errorf("unnamed provider = %q, want empty", got)
	}
	if got, want := texts.UnreachableMessage(meshy, nil), "MeshyAI 网关连接失败，原因：无法连接上游服务器。"; got != want {
		t.Errorf("unreachable = %q, want %q", got, want)
	}

	localizer, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	english := func(text string) string { return localizer.Text("en", text) }
	if got, want := texts.Message(meshy, 429, english),
		"MeshyAI gateway returned 429: The upstream server is rate limiting requests; please try again later."; got != want {
		t.Errorf("en 429 = %q, want %q", got, want)
	}
	if got, want := texts.Message(names["openai-compatible-chat"], 502, english),
		"Chat model gateway returned 502: The upstream server failed."; got != want {
		t.Errorf("en chat 502 = %q, want %q", got, want)
	}
	if got, want := texts.Message(names["openai-compatible-chat"], 502, nil),
		"对话模型 网关返回 502，原因：上游服务器故障。"; got != want {
		t.Errorf("zh chat 502 = %q, want %q", got, want)
	}
	if got, want := texts.Combined("A。", "B", english), "A。 B"; got != want {
		t.Errorf("en combined = %q, want %q", got, want)
	}
	if got, want := texts.Combined("A。", "B", nil), "A。B"; got != want {
		t.Errorf("zh combined = %q, want %q", got, want)
	}
	if !texts.Covers("rate_limited") || texts.Covers("content_rejected") || texts.Covers("submission_unknown") {
		t.Fatalf("failure classes = %v", texts.FailureClasses)
	}
}

// Every default connection reached over the network names its upstream for
// the person; a local one needs no name. (A connection without one keeps the
// failure class texts, so a Schema may add a connection before naming it.)
func TestProviderConnectionsDeclareDisplayNames(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range loaded.Application().Runtime.ProviderConnections {
		if connection.BaseURLSetting != "" && connection.DisplayName == "" {
			t.Errorf("connection %q has no display_name", connection.ID)
		}
	}
}

func TestUpstreamHTTPErrorValidation(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*catalog.UpstreamHTTPErrorTexts){
		"template without status": func(texts *catalog.UpstreamHTTPErrorTexts) { texts.Template = "{provider} {reason}" },
		"reason for 2xx":          func(texts *catalog.UpstreamHTTPErrorTexts) { texts.Reasons = map[string]string{"200": "x"} },
		"reason key family 3xx":   func(texts *catalog.UpstreamHTTPErrorTexts) { texts.Reasons = map[string]string{"3xx": "x"} },
		"unknown failure class":   func(texts *catalog.UpstreamHTTPErrorTexts) { texts.FailureClasses = []string{"nope"} },
		"combined without detail": func(texts *catalog.UpstreamHTTPErrorTexts) { texts.CombinedTemplate = "{upstream}" },
	} {
		t.Run(name, func(t *testing.T) {
			schema := loaded.Application()
			mutate(&schema.Chat.Presentation.Messages.UpstreamHTTPError)
			if err := catalog.ValidateApplicationSchema(schema); err == nil {
				t.Fatal("invalid upstream_http_error was accepted")
			}
		})
	}
	schema := loaded.Application()
	schema.Runtime.ProviderConnections[0].DisplayName = " MeshyAI"
	if err := catalog.ValidateApplicationSchema(schema); err == nil {
		t.Fatal("display_name with surrounding blanks was accepted")
	}
}

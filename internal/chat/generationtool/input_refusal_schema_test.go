package generationtool

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #358: a synchronous input refusal that reached the kernel with a Schema
// reason is written in the turn's language. The Provider diagnostic stays
// out of the Agent's text, and the size/limit values are preserved.
func TestInputRefusalWritesSchemaReasonInBothLocales(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	tool := application.Chat.Tools[0]
	refusal := application.Runtime.GenerationMessages.InputRefusal(schema.InputRefusalTooLarge, schema.InputRefusalValues{
		Size: "10.03 MB", Limit: "10 MB",
	})
	if refusal == nil {
		t.Fatal("Schema declares no input refusal")
	}
	const diagnostic = "provider diagnostic 10.03 MB over 10 MB"
	provider := &generation.ProviderError{
		Failure: &generation.Failure{Class: generation.FailureInvalidInput, SafeDetail: diagnostic},
		Cause:   refusal,
	}
	wrapper := &generation.Error{Code: generation.ErrInvalidInput, Operation: "validate_provider_input",
		Detail: diagnostic, Cause: provider}
	executor := Executor{chat: application.Chat, texts: texts}
	for _, test := range []struct {
		locale string
		want   string
	}{
		{"en", "The source file is 10.03 MB, over the 10 MB limit."},
		{"zh-CN", "来源文件为 10.03 MB，超过上限 10 MB"},
	} {
		t.Run(test.locale, func(t *testing.T) {
			got := executor.refusalDetail(chat.ToolCommand{
				Tool: tool, Options: chat.TurnOptions{Locale: test.locale},
			}, wrapper, wrapper.Detail)
			if !strings.Contains(got, test.want) {
				t.Fatalf("refusal = %q, want %q", got, test.want)
			}
			if strings.Contains(got, "provider diagnostic") {
				t.Fatalf("SafeDetail reached the Agent: %q", got)
			}
			if test.locale == "en" && strings.Contains(got, "来源") {
				t.Fatalf("English refusal still has Chinese: %q", got)
			}
		})
	}
}

// A kernel diagnostic is never returned; only the Provider's explicitly safe
// text is.
func TestUnmarkedRefusalReturnsOnlyProviderSafeDetail(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{chat: application.Chat, texts: texts}
	const detail = "kernel detail"
	provider := &generation.ProviderError{Failure: &generation.Failure{
		Class: generation.FailureInvalidInput, SafeDetail: "provider detail"}}
	err = &generation.Error{Code: generation.ErrInvalidInput, Detail: detail, Cause: provider}
	got := executor.refusalDetail(chat.ToolCommand{
		Tool: application.Chat.Tools[0], Options: chat.TurnOptions{Locale: "en"},
	}, err, detail)
	if got != "provider detail" {
		t.Fatalf("refusal = %q", got)
	}
}

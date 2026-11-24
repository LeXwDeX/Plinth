package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// The Agent's tool result for a Task the upstream answered with an HTTP error
// opens with the upstream text naming the Task's connection, in the turn's
// locale; a class text that guides the Agent follows it, and a metered
// failure keeps its own text.
func TestToolResultNamesTheUpstreamHTTPError(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	executor := Executor{chat: application.Chat, texts: texts,
		providerNames: catalog.ProviderDisplayNames(application.Runtime.ProviderConnections)}
	meshy := generation.Offering{AdapterKey: "meshy.v2", CreditsMetered: true}
	chinese := chat.ToolCommand{}
	english := chat.ToolCommand{Options: chat.TurnOptions{Locale: "en"}}

	limited := &generation.Failure{Class: generation.FailureRateLimited, Retryable: true, UpstreamStatus: 429}
	if got, want := executor.failureMessage(chinese, meshy, limited), "MeshyAI 网关返回 429，原因：上游服务器限流，请稍后再试。"; got != want {
		t.Fatalf("zh = %q, want %q", got, want)
	}
	if got, want := executor.failureMessage(english, meshy, limited),
		"MeshyAI gateway returned 429: The upstream server is rate limiting requests; please try again later."; got != want {
		t.Fatalf("en = %q, want %q", got, want)
	}
	denied := &generation.Failure{Class: generation.FailureUpstreamDenied, UpstreamStatus: 403}
	if got, want := executor.failureMessage(chinese, meshy, denied),
		"MeshyAI 网关返回 403，原因：上游凭据无效或无权限，请联系管理员。"+application.Chat.Presentation.Messages.GenerationFailedByClass["upstream_denied"]; got != want {
		t.Fatalf("guided = %q, want %q", got, want)
	}
	integrity := &generation.Failure{Class: generation.FailureIntegrity, UpstreamStatus: 500}
	if got := executor.failureMessage(chinese, meshy, integrity); got != MeteredFailureMessage(application.Chat, true, integrity) {
		t.Fatalf("metered = %q", got)
	}
	// An Offering whose connection is unnamed keeps the class text.
	if got := executor.failureMessage(chinese, generation.Offering{AdapterKey: "unknown"}, limited); got != application.Chat.Presentation.Messages.GenerationFailed {
		t.Fatalf("unnamed = %q", got)
	}
}

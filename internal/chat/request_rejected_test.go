package chat_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #401: when the upstream refused a request parameter (an HTTP 400
// invalid_request_error that names the field), the person reads the Schema's
// text with the field's name and the error ID, not "unknown error" and never
// the upstream's own wording, which stays in the error the log reads. A 400
// that names no field is unchanged.
func TestOrchestratorNamesTheRequestParameterTheUpstreamRefused(t *testing.T) {
	const upstreamWording = "secret upstream diagnostic: Unsupported value: 'temperature' does not support 0"
	for _, test := range []struct {
		name string
		err  error
		want func(rejected, unknown string) string
	}{
		{"refused temperature",
			errors.Join(&agents.RequestRejection{Param: "temperature"}, &agents.StatusError{Status: 400}, errors.New(upstreamWording)),
			func(rejected, _ string) string { return strings.ReplaceAll(rejected, "{param}", "temperature") }},
		{"refused thinking level",
			errors.Join(&agents.RequestRejection{Param: "reasoning_effort"}, &agents.StatusError{Status: 400}, errors.New(upstreamWording)),
			func(rejected, _ string) string { return strings.ReplaceAll(rejected, "{param}", "reasoning_effort") }},
		{"a 400 that names no field",
			errors.Join(&agents.StatusError{Status: 400}, errors.New(upstreamWording)),
			func(_, unknown string) string { return unknown }},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := configuredChatSchema(t)
			rejectedText := schema.Presentation.Messages.AgentRequestRejected
			if !strings.Contains(rejectedText, "{param}") {
				t.Fatalf("the default Schema's agent_request_rejected = %q, want a text with {param}", rejectedText)
			}
			content := runFailingTurn(t, failingAgent{err: test.err})
			want := test.want(rejectedText, schema.Presentation.Messages.UnknownError)
			if !strings.HasPrefix(content, want) || strings.Contains(content, "secret") || strings.Contains(content, "Unsupported value") {
				t.Fatalf("assistant = %q, want it to start with %q", content, want)
			}
			if !strings.Contains(content, strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]) {
				t.Fatalf("assistant = %q lacks the error reference", content)
			}
		})
	}
}

// The text exists in both default languages and keeps its placeholder, so the
// English reader gets the same sentence with the field named.
func TestRequestRejectedTextIsTranslated(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	chinese := application.Chat.Presentation.Messages.AgentRequestRejected
	english := texts.Text("en", chinese)
	if english == chinese || !strings.Contains(english, "{param}") || strings.ContainsAny(english, "，。「」") {
		t.Fatalf("english = %q", english)
	}
}

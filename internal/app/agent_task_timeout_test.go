package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #136: a timed-out accepted job may still run and charge upstream. get_task
// tells the Agent not to retry on its own (user decision) and relays the
// Schema text that has the result checked first: the queue timeout's own
// text, else the shared ambiguous-failure text. A certain failure is as before.
func TestAgentTaskViewTreatsATimedOutAcceptedJobAsPossiblyCharged(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	messages := application.Chat.Presentation.Messages
	queueText := messages.GenerationFailedByClass[string(generation.FailureUpstreamQueueTimeout)]
	ambiguousText := messages.GenerationFailedAmbiguous
	if !strings.Contains(queueText, "排队") || !strings.Contains(queueText, "get_task") ||
		!strings.Contains(ambiguousText, "get_task") || texts.Text("en", queueText) == queueText ||
		texts.Text("en", ambiguousText) == ambiguousText {
		t.Fatalf("the Schema needs both texts in both languages: %q / %q", queueText, ambiguousText)
	}
	for _, test := range []struct {
		name    string
		failure generation.Failure
		reason  string
	}{
		{"queue timeout", generation.Failure{Class: generation.FailureUpstreamQueueTimeout, Ambiguous: true}, queueText},
		{"job timeout", generation.Failure{Class: generation.FailureTimedOut, Ambiguous: true}, ambiguousText},
		{"unknown submission", generation.Failure{Class: generation.FailureSubmissionUnknown, Ambiguous: true}, ambiguousText},
		{"certain timeout", generation.Failure{Class: generation.FailureTimedOut, Retryable: true}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := test.failure
			task := generation.Task{ID: "task_1", OwnerID: "owner_a", SessionID: "session_1",
				State: generation.TaskFailed, Failure: &failure}
			for _, locale := range []string{"", "en"} {
				ctx := context.Background()
				want := test.reason
				if locale != "" {
					ctx = catalog.WithLocale(ctx, locale)
					want = texts.Text(locale, want)
				}
				raw, err := newAgentTaskReader(fakeAgentTaskReader{task: task}, application.Chat,
					application.Content.Messages, texts)(ctx, "owner_a", "task_1")
				if err != nil {
					t.Fatal(err)
				}
				var view agentTaskView
				if err := json.Unmarshal(raw, &view); err != nil {
					t.Fatal(err)
				}
				if view.Failure == nil || view.Failure.Reason != want ||
					view.Failure.UserDecisionRequired != (want != "") || view.Failure.Retryable != failure.Retryable && want == "" {
					t.Fatalf("locale %q: failure view = %+v", locale, view.Failure)
				}
				if want != "" && view.Failure.Retryable {
					t.Fatalf("a possibly charged failure is never retryable for the Agent: %+v", view.Failure)
				}
			}
			// The generation tool's failure result reads the same text.
			if want := test.reason; want != "" {
				if got := generationtool.FailureMessage(application.Chat, false, &failure); got != want {
					t.Fatalf("tool failure text = %q, want %q", got, want)
				}
			}
		})
	}
}

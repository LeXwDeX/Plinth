package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #131: get_task and observe_task tell the Agent that the Provider still holds
// the accepted job in its own queue, with the count ahead when reported and
// the Schema note (in the turn's locale) on what to tell the waiting person.
func TestAgentTaskViewCarriesTheUpstreamQueue(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	note := application.Chat.Agent.UpstreamQueueContent
	english := texts.Text("en", note)
	if note == "" || english == note {
		t.Fatalf("the upstream queue note needs distinct default and English copy: %q / %q", note, english)
	}
	ahead := 1313
	task := generation.Task{
		ID: "task_1", OwnerID: "owner_a", SessionID: "session_1",
		State: generation.TaskRunning, Progress: 0,
		UpstreamQueue: &generation.UpstreamQueue{Ahead: &ahead},
	}
	read := newAgentTaskReader(fakeAgentTaskReader{task: task}, application.Chat, application.Content.Messages, texts)
	for _, test := range []struct{ locale, want string }{{"", note}, {"en", english}} {
		ctx := context.Background()
		if test.locale != "" {
			ctx = catalog.WithLocale(ctx, test.locale)
		}
		raw, err := read(ctx, "owner_a", "task_1")
		if err != nil {
			t.Fatal(err)
		}
		var view agentTaskView
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		if view.UpstreamQueue == nil || view.UpstreamQueue.Ahead == nil || *view.UpstreamQueue.Ahead != 1313 ||
			view.UpstreamQueue.Note != test.want {
			t.Fatalf("locale %q: upstream_queue = %+v (raw %s)", test.locale, view.UpstreamQueue, raw)
		}
	}

	task.UpstreamQueue = &generation.UpstreamQueue{}
	raw, err := newAgentTaskReader(fakeAgentTaskReader{task: task}, application.Chat, nil, nil)(context.Background(), "owner_a", "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"upstream_queue":{"note":`) || strings.Contains(string(raw), `"ahead"`) {
		t.Fatalf("a queue without a count = %s", raw)
	}

	for _, done := range []generation.Task{
		{ID: "task_1", OwnerID: "owner_a", State: generation.TaskRunning},
		{ID: "task_1", OwnerID: "owner_a", State: generation.TaskSucceeded, UpstreamQueue: &generation.UpstreamQueue{Ahead: &ahead}},
	} {
		raw, err := newAgentTaskReader(fakeAgentTaskReader{task: done}, application.Chat, nil, nil)(context.Background(), "owner_a", "task_1")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "upstream_queue") {
			t.Fatalf("state %s without a running queue says more: %s", done.State, raw)
		}
	}
}

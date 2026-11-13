package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #284: get_task and observe_task tell the Agent that a succeeded Task did not
// deliver everything it was asked for, with the Schema sentence in the turn's
// locale and the positions it names.
func TestAgentTaskViewCarriesTheNoticesOfASucceededTask(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	task := generation.Task{
		ID: "task_1", OwnerID: "owner_a", SessionID: "session_1", State: generation.TaskSucceeded, Progress: 100,
		Notices: []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 4}}},
	}
	read := newAgentTaskReader(fakeAgentTaskReader{task: task}, application.Chat, application.Content.Messages, texts)
	for _, test := range []struct{ locale, want string }{
		{"", "第 2 张、第 4 张没有处理成功，其余已完成。"},
		{"en", "Not processed: picture 2, picture 4. The rest are done."},
	} {
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
		if len(view.Notices) != 1 || view.Notices[0].Code != "partial_inputs" || view.Notices[0].Message != test.want ||
			len(view.Notices[0].Inputs) != 2 || view.Notices[0].Inputs[0] != 2 || view.Notices[0].Inputs[1] != 4 {
			t.Fatalf("locale %q: notices = %+v (raw %s)", test.locale, view.Notices, raw)
		}
		if view.State != "SUCCEEDED" || view.Failure != nil {
			t.Fatalf("a notice changed the Task's state or failure: %s", raw)
		}
	}

	// Without a localizer the code and positions still reach the Agent.
	raw, err := newAgentTaskReader(fakeAgentTaskReader{task: task}, application.Chat, nil, nil)(context.Background(), "owner_a", "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"notices":[{"code":"partial_inputs","inputs":[2,4]}]`) {
		t.Fatalf("without a localizer = %s", raw)
	}

	// A Task that delivered everything, or is not a success, says no notice.
	for _, other := range []generation.Task{
		{ID: "task_1", OwnerID: "owner_a", State: generation.TaskSucceeded},
		{ID: "task_1", OwnerID: "owner_a", State: generation.TaskFailed, Notices: task.Notices},
		{ID: "task_1", OwnerID: "owner_a", State: generation.TaskMaterializing, Notices: task.Notices},
	} {
		raw, err := newAgentTaskReader(fakeAgentTaskReader{task: other}, application.Chat, nil, nil)(context.Background(), "owner_a", "task_1")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "notices") {
			t.Fatalf("state %s says more: %s", other.State, raw)
		}
	}
}

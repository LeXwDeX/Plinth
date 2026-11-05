package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #131: a running Task whose accepted job waits in the Provider's own queue
// carries queue_notice {reason upstream_queued, ahead, detail}. The detail is
// the Schema text in the request locale; it names the count only when the
// upstream reports jobs ahead.
func TestTaskResponseCarriesTheUpstreamQueue(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{generation: &generation.Module{}, application: application, texts: texts}
	count := func(value int) *int { return &value }
	type notice struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
		Ahead  *int   `json:"ahead"`
	}
	for _, test := range []struct {
		name   string
		locale string
		task   generation.Task
		want   *notice
	}{
		{"count ahead", "", generation.Task{State: generation.TaskRunning, UpstreamQueue: &generation.UpstreamQueue{Ahead: count(1313)}},
			&notice{Reason: "upstream_queued", Detail: "上游排队中（前方约 1313 个任务）", Ahead: count(1313)}},
		{"count ahead in English", "en", generation.Task{State: generation.TaskRunning, UpstreamQueue: &generation.UpstreamQueue{Ahead: count(1313)}},
			&notice{Reason: "upstream_queued", Detail: "Queued at the provider (about 1313 tasks ahead)", Ahead: count(1313)}},
		{"no count", "", generation.Task{State: generation.TaskRunning, UpstreamQueue: &generation.UpstreamQueue{}},
			&notice{Reason: "upstream_queued", Detail: "上游排队中"}},
		{"none ahead", "", generation.Task{State: generation.TaskRunning, UpstreamQueue: &generation.UpstreamQueue{Ahead: count(0)}},
			&notice{Reason: "upstream_queued", Detail: "上游排队中", Ahead: count(0)}},
		{"running without a queue", "", generation.Task{State: generation.TaskRunning}, nil},
		{"finished", "", generation.Task{State: generation.TaskSucceeded, UpstreamQueue: &generation.UpstreamQueue{Ahead: count(3)}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.task.ID = "task"
			encoded, err := json.Marshal(s.newTaskResponse(test.locale, test.task))
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				QueueNotice *notice `json:"queue_notice"`
			}
			if err := json.Unmarshal(encoded, &response); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(response.QueueNotice)
			want, _ := json.Marshal(test.want)
			if string(got) != string(want) {
				t.Fatalf("queue_notice = %s, want %s (response %s)", got, want, encoded)
			}
		})
	}
}

// #136: a queue timeout has its own public text, distinct from an ordinary
// timeout, and both timeouts of an accepted job say the result is ambiguous.
func TestTaskResponseNamesTheUpstreamQueueTimeout(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{generation: &generation.Module{}, application: application, texts: texts}
	for _, test := range []struct {
		locale, want string
		failure      generation.Failure
	}{
		{"", "上游排队过久，已停止等待；服务商可能仍会执行并扣费", generation.Failure{Class: generation.FailureUpstreamQueueTimeout, Ambiguous: true}},
		{"en", "Waited too long in the provider's queue and stopped waiting; the provider may still run and bill the task", generation.Failure{Class: generation.FailureUpstreamQueueTimeout, Ambiguous: true}},
		{"", "上游请求超时", generation.Failure{Class: generation.FailureTimedOut, Ambiguous: true}},
	} {
		failure := test.failure
		response := s.newTaskResponse(test.locale, generation.Task{ID: "task", State: generation.TaskFailed, Failure: &failure})
		if response.Failure == nil || response.Failure.Detail != test.want || !response.Failure.Ambiguous || response.Failure.Retryable {
			t.Fatalf("%s %q: failure = %+v", failure.Class, test.locale, response.Failure)
		}
	}
}

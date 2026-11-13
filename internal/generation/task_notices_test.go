package generation_test

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// noticeAdapter reports the same notices on every observation of whatever its
// next adapter does. The kernel must keep them only from a completion.
type noticeAdapter struct {
	next    generation.ProviderPort
	notices []generation.TaskNotice
}

func (a noticeAdapter) with(observation generation.Observation, err error) (generation.Observation, error) {
	observation.Notices = a.notices
	return observation, err
}

func (a noticeAdapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	return a.with(a.next.Start(ctx, request))
}

func (a noticeAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	return a.with(a.next.Observe(ctx, request))
}

func (a noticeAdapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.next.OpenAsset(ctx, request)
}

func submitImage(t *testing.T, h *harness) generation.Accepted {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"a brass robot"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

// #284: a completion that did not deliver everything it was asked for says so
// through the Provider's notices; the kernel writes them with the completion,
// bounded and normalized, and the succeeded Task carries them in every read
// that reads a Task: the single read and the list.
func TestACompletionsNoticesAreKeptOnTheSucceededTask(t *testing.T) {
	h := newHarness(t, noticeAdapter{next: mock.New(), notices: []generation.TaskNotice{
		{Code: generation.NoticePartialInputs, Inputs: []int{4, 2, 2, 0, -1, 3}},
		{Code: "Not A Code!", Inputs: []int{1}},
		{Code: generation.NoticePartialInputs, Inputs: []int{9}},
		{Code: "another_notice"},
	}}, nil)
	accepted := submitImage(t, h)
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state = %s: %+v", task.State, task.Failure)
	}
	want := []generation.TaskNotice{
		{Code: generation.NoticePartialInputs, Inputs: []int{2, 3, 4}},
		{Code: "another_notice"},
	}
	if !reflect.DeepEqual(task.Notices, want) {
		t.Fatalf("notices = %+v, want %+v", task.Notices, want)
	}
	// The notices are the Attempt's fact, written by the completion.
	aggregate, err := h.repository.Load(context.Background(), accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil || !reflect.DeepEqual(aggregate.CurrentAttempt.Notices, want) {
		t.Fatalf("attempt notices = %+v", aggregate.CurrentAttempt)
	}
	// The list reads the same Task.
	page, err := h.module.List(context.Background(), generation.OwnTasks("owner-a"), generation.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 || !reflect.DeepEqual(page.Tasks[0].Notices, want) {
		t.Fatalf("listed task = %+v", page.Tasks)
	}
}

// Nothing is reported, nothing is carried: an ordinary success has no notices.
func TestASucceededTaskWithoutNoticesCarriesNone(t *testing.T) {
	h := newHarness(t, mock.New(), nil)
	accepted := submitImage(t, h)
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Notices) != 0 {
		t.Fatalf("state = %s notices = %+v", task.State, task.Notices)
	}
}

// A notice describes a success. A Task whose completion was reported with
// notices but then failed its own integrity check is a failure and carries
// none, so the public view never says "done, except..." about a failed Task.
func TestAFailedTaskCarriesNoNotices(t *testing.T) {
	h := newHarness(t, noticeAdapter{next: parameterizedInvalidImageAdapter{}, notices: []generation.TaskNotice{
		{Code: generation.NoticePartialInputs, Inputs: []int{2}},
	}}, nil)
	accepted := submitImage(t, h)
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil {
		t.Fatalf("task = %+v", task)
	}
	if len(task.Notices) != 0 {
		t.Fatalf("a failed Task carries notices: %+v", task.Notices)
	}
	// The Attempt did record what the completion said; the Task's projection is
	// what keeps it from a Task that is not a success.
	aggregate, err := h.repository.Load(context.Background(), accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil || len(aggregate.CurrentAttempt.Notices) != 1 {
		t.Fatalf("attempt = %+v", aggregate.CurrentAttempt)
	}
}

// The notice a provider reports without a code the projection can name is
// dropped, never refused: a malformed notice must not cost a paid result its
// delivery.
func TestAMalformedNoticeNeverCostsTheDelivery(t *testing.T) {
	h := newHarness(t, noticeAdapter{next: mock.New(), notices: []generation.TaskNotice{
		{Code: ""}, {Code: strings.Repeat("a", 49)}, {Code: "9starts_with_digit"}, {Code: "has-dash"},
	}}, nil)
	accepted := submitImage(t, h)
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Assets) != 1 || len(task.Notices) != 0 {
		t.Fatalf("task = %+v", task)
	}
}

// Normalization is bounded: at most four notices, each naming at most 200 positions.
func TestNoticesAreBounded(t *testing.T) {
	var many []generation.TaskNotice
	for index := range 8 {
		many = append(many, generation.TaskNotice{Code: "notice_" + string(rune('a'+index))})
	}
	positions := make([]int, 300)
	for index := range positions {
		positions[index] = index + 1
	}
	many[0].Inputs = positions
	h := newHarness(t, noticeAdapter{next: mock.New(), notices: many}, nil)
	accepted := submitImage(t, h)
	task := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if len(task.Notices) != 4 || len(task.Notices[0].Inputs) != 200 || task.Notices[0].Inputs[199] != 200 {
		t.Fatalf("notices = %+v", task.Notices)
	}
}

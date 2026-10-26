package generation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #326 N1: on dev-test a Meshy multi-image result recovered on 10-20 (#295
// refetch) was 35 hours old, but its upstream job was its failed source's,
// made on 10-18. Retexture still named the job and Meshy answered 404. The
// age of a task's upstream job counts from the Task that holds the job, and
// a job the Provider answered 404 for is gone for every later step.

// expiringPort answers a remesh create as Meshy answers for a gone job.
type expiringPort struct {
	generation.ProviderPort
	mu     sync.Mutex
	expire bool
}

func (p *expiringPort) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	p.mu.Lock()
	expire := p.expire
	p.mu.Unlock()
	if expire && request.Offering.Capability == generation.Remesh {
		return generation.Observation{}, &generation.ProviderError{Failure: &generation.Failure{
			Class: generation.FailureSourceExpired, SafeDetail: "Meshy 已不保留来源模型的原任务（上游返回 404 Task not found）", UpstreamStatus: 404}}
	}
	return p.ProviderPort.Start(ctx, request)
}

func remeshOffering(t *testing.T) generation.Offering {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("meshy/processing/remesh")
	if !ok {
		t.Fatal("meshy remesh missing")
	}
	return offering
}

func TestARecoveredResultCountsItsUpstreamJobAgeFromTheJob(t *testing.T) {
	h, store, _ := newRefetchHarness(t)
	ctx := context.Background()
	source := failAfterUpstream(t, h, store)
	h.clock.Advance(36 * time.Hour)
	result, err := h.module.RefetchUpstreamResult(ctx, "owner-a", source.ID, generation.RefetchByOwner)
	if err != nil {
		t.Fatal(err)
	}
	if task := h.runUntilTerminal(t, "owner-a", result.TaskID); task.State != generation.TaskSucceeded {
		t.Fatalf("refetch = %s %#v", task.State, task.Failure)
	}
	h.clock.Advance(24 * time.Hour)
	recovered, err := h.module.Get(ctx, "owner-a", result.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.UpstreamJobCreatedAt.Equal(source.CreatedAt) {
		t.Fatalf("upstream job created at %v; want the source's %v", recovered.UpstreamJobCreatedAt, source.CreatedAt)
	}
	// The task page reads the summary projection; it carries the same fact.
	summary, err := h.module.GetSummaryScoped(ctx, generation.OwnTasks("owner-a"), result.TaskID)
	if err != nil || !summary.UpstreamJobCreatedAt.Equal(source.CreatedAt) {
		t.Fatalf("summary upstream job created at %v, %v; want %v", summary.UpstreamJobCreatedAt, err, source.CreatedAt)
	}
	// The recovered Task is a day old, its job sixty hours: the saved file goes.
	binding, err := generation.BindModelSourceAt(remeshOffering(t), recovered, "", nil, h.clock.Now())
	if err != nil || binding.Parameter != "source_asset" {
		t.Fatalf("binding = %+v, %v; want the saved file", binding, err)
	}
	page, err := h.module.List(ctx, generation.OwnTasks("owner-a"), generation.ListQuery{SessionID: source.SessionID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range page.Tasks {
		if listed.ID == recovered.ID && !listed.UpstreamJobCreatedAt.Equal(source.CreatedAt) {
			t.Fatalf("listed task job age = %v", listed.UpstreamJobCreatedAt)
		}
	}
	_, err = h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: source.SessionID,
		Capability: generation.Remesh, OfferingID: "meshy/processing/remesh",
		Input: []byte(`{"source_task":"` + string(recovered.ID) + `","target_formats":["glb"]}`)})
	if !errors.Is(err, generation.ErrSourceExpired) {
		t.Fatalf("a step naming the expired job = %v; want ErrSourceExpired", err)
	}
}

func TestAJobTheProviderNoLongerKeepsIsGoneForLaterSteps(t *testing.T) {
	port := &expiringPort{ProviderPort: mock.New()}
	h := newHarness(t, port, nil)
	ctx := context.Background()
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"stool"}`)})
	if err != nil {
		t.Fatal(err)
	}
	model := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if model.State != generation.TaskSucceeded {
		t.Fatalf("model = %s %#v", model.State, model.Failure)
	}
	if fresh, _ := h.module.Get(ctx, "owner-a", model.ID); fresh.UpstreamJobGone {
		t.Fatal("a fresh model's job is gone before anything failed")
	}
	port.mu.Lock()
	port.expire = true
	port.mu.Unlock()
	failedAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.Remesh, OfferingID: "meshy/processing/remesh",
		Input: []byte(`{"source_task":"` + string(model.ID) + `","target_formats":["glb"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	failed := h.runUntilTerminal(t, "owner-a", failedAccepted.TaskID)
	if failed.State != generation.TaskFailed || failed.Failure == nil || failed.Failure.Class != generation.FailureSourceExpired {
		t.Fatalf("remesh = %s %#v", failed.State, failed.Failure)
	}
	gone, err := h.module.Get(ctx, "owner-a", model.ID)
	if err != nil || !gone.UpstreamJobGone {
		t.Fatalf("model after the 404 = %+v, %v; want its job marked gone", gone.UpstreamJobGone, err)
	}
	if summary, err := h.module.GetSummaryScoped(ctx, generation.OwnTasks("owner-a"), model.ID); err != nil || !summary.UpstreamJobGone {
		t.Fatalf("summary after the 404 = %v, %v; want its job marked gone", summary.UpstreamJobGone, err)
	}
	binding, err := generation.BindModelSourceAt(remeshOffering(t), gone, "", nil, h.clock.Now())
	if err != nil || binding.Parameter != "source_asset" {
		t.Fatalf("binding = %+v, %v; want the saved file", binding, err)
	}
	// Another owner's tasks are not marked by it.
	if _, err := h.module.Get(ctx, "owner-b", model.ID); err == nil {
		t.Fatal("another owner reads the task")
	}
}

// #326: a chat card whose step failed as source_expired offers a paid
// regenerate only when running that step again can succeed. The kernel judges
// the failed Task with the rule plans use (JudgeSourceRetry): Meshy Remesh
// takes the saved model file, so running it again uses that file.
func TestAFailedSourceExpiredTaskIsJudgedForRunningAgain(t *testing.T) {
	port := &expiringPort{ProviderPort: mock.New()}
	h := newHarness(t, port, nil)
	ctx := context.Background()
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: []byte(`{"prompt":"stool"}`)})
	if err != nil {
		t.Fatal(err)
	}
	model := h.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if model.State != generation.TaskSucceeded {
		t.Fatalf("model = %s %#v", model.State, model.Failure)
	}
	if judgment := h.module.TaskSourceRetry(ctx, model); judgment != generation.SourceRetryAsBefore {
		t.Fatalf("a succeeded task judged %q", judgment)
	}
	port.mu.Lock()
	port.expire = true
	port.mu.Unlock()
	failedAccepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.Remesh, OfferingID: "meshy/processing/remesh",
		Input: []byte(`{"source_task":"` + string(model.ID) + `","target_formats":["glb"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	failed := h.runUntilTerminal(t, "owner-a", failedAccepted.TaskID)
	if failed.State != generation.TaskFailed || failed.Failure == nil || failed.Failure.Class != generation.FailureSourceExpired {
		t.Fatalf("remesh = %s %#v", failed.State, failed.Failure)
	}
	if judgment := h.module.TaskSourceRetry(ctx, failed); judgment != generation.SourceRetrySavedFile {
		t.Fatalf("failed remesh judged %q; want the saved file", judgment)
	}
	// Another owner's view of the task is judged on nothing it cannot read.
	foreign := failed
	foreign.OwnerID = "owner-b"
	if judgment := h.module.TaskSourceRetry(ctx, foreign); judgment != generation.SourceRetryAsBefore {
		t.Fatalf("another owner's judgment = %q", judgment)
	}
}

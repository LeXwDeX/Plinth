package processing

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 在途准入已满时内核确定没有提交：计划不暂停、不要求再次确认，记下重试
// 时间；到点前后台不再提交，到点后按同一幂等键提交并清掉排队标记。
func TestAdmissionFullQueuesStepAndResubmitsWithSameKey(t *testing.T) {
	ctx := context.Background()
	service, store, gateway, _, plan := singleRemeshFixture(t)
	gateway.submitError = &generation.Error{Code: generation.ErrTooManyRequests, Operation: "submit",
		RetryAfter: 30 * time.Second, Cause: generation.RepositoryAdmissionFull}
	if err := advanceApproved(ctx, service); err != nil {
		t.Fatal(err)
	}
	queued, _ := service.Get(ctx, "owner", plan.ID)
	step := queued.Steps[0]
	if queued.State == Paused || queued.State == Failed || queued.Failure != "" || step.State == Paused || step.Failure != "" {
		t.Fatalf("准入已满不应暂停或失败: %#v", queued)
	}
	if step.QueuedUntil == nil || !step.QueuedUntil.After(time.Now()) || step.TaskID != "" || step.PreparedInputID == "" || step.AuthorizationRef == "" {
		t.Fatalf("应排队并保留冻结输入与授权: %#v", step)
	}
	key := step.IdempotencyKey
	// 未到重试时间：即使准入已恢复也不提交。
	gateway.submitError = nil
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gateway.commands) != 0 {
		t.Fatalf("重试时间未到就提交了: %#v", gateway.commands)
	}
	// 推过重试时间。
	stored := store.plans[plan.ID]
	past := time.Now().Add(-time.Second)
	stored.Steps[0].QueuedUntil = &past
	store.plans[plan.ID] = stored
	if err := service.advanceAll(ctx); err != nil {
		t.Fatal(err)
	}
	running, _ := service.Get(ctx, "owner", plan.ID)
	if len(gateway.commands) != 1 || gateway.commands[0].IdempotencyKey != key {
		t.Fatalf("应按同一幂等键提交一次: %#v", gateway.commands)
	}
	if running.State != Running || running.Steps[0].TaskID == "" || running.Steps[0].QueuedUntil != nil {
		t.Fatalf("running = %#v", running)
	}
}

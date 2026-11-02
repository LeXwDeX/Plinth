package agenttools

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// 在途准入已满时三视图步骤确定没有提交：工具回 NoSubmission + RetryAfter，
// 交给编排器排队；唤醒后同一调用重放，沿用已批准的授权和同一幂等键提交。
func TestThreeViewAdmissionFullAsksToQueueAndReplaySubmitsOnce(t *testing.T) {
	ctx := context.Background()
	executor, gateway, _, command, _ := threeViewHarnessFixture(t)
	if result, err := executor.Execute(ctx, command, nil); err != nil || !result.Suspend {
		t.Fatalf("prepare %+v %v", result, err)
	}
	_, execution, grant, err := executor.existingThreeView(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.repo.DecideExecutionGrant(ctx, "owner", grant.RequestID, "human", executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	gateway.submitErr = &generation.Error{Code: generation.ErrTooManyRequests, Operation: "submit",
		RetryAfter: 30 * time.Second, Cause: generation.RepositoryAdmissionFull}
	result, err := executor.Execute(ctx, command, nil)
	if err == nil || !result.NoSubmission || result.RetryAfter != 30*time.Second || result.TaskID != "" || result.AwaitTask {
		t.Fatalf("准入已满应请求排队: %+v %v", result, err)
	}
	if result.AgentContent != "admission full, not submitted" {
		t.Fatalf("排队超时时交给模型的回执应是 Schema 的 admission_full: %q", result.AgentContent)
	}
	_, _, grant, err = executor.existingThreeView(ctx, command)
	if err != nil || grant.Status != executiongrant.Approved {
		t.Fatalf("授权应保持已批准、未绑定任务: %+v %v", grant, err)
	}
	gateway.submitErr = nil
	result, err = executor.Execute(ctx, command, nil)
	if err != nil || result.TaskID != "accepted-three-view" || gateway.submits != 2 {
		t.Fatalf("重放应提交成功: %+v %v submits=%d", result, err, gateway.submits)
	}
	task, err := gateway.repo.Get(ctx, "owner", "accepted-three-view")
	if err != nil || task.IdempotencyKey != execution.IdempotencyKey {
		t.Fatalf("应沿用同一幂等键: %+v %v", task, err)
	}
}

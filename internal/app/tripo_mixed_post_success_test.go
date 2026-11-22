package app

// tripo_mixed_post_success_test.go 是 #462 的真实链路验收：真实 Tripo 适配器
//（默认目录声明接线）+ httptest 假上游 + 真实 SQLite + 真实本地资产存储 + 内核
// 读模型。全部本地 fixture、零付费调用、零真实上游。
//
// 覆盖：GLTF 转换就绪即 SUCCEEDED（主模型可用）、五个已请求格式进入「转换中」
// 的资源槽位与 n/5 进度；单个格式失败只影响它自己（按官方规则重提后按失败收尾，
// 其余格式照常交付）；成功后观察进行中重启，状态一致。

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// submitFullFormatTask 提交一个 Tripo 全格式生成任务（与 lifecycleHarness 同一
// 审批与冻结路径，只换 Offering 与输入）。
func submitFullFormatTask(t *testing.T, h *lifecycleHarness, formats []string, key string) generation.TaskID {
	t.Helper()
	ctx := context.Background()
	input, err := json.Marshal(map[string]any{
		"prompt": "a lantern", "output_formats": formats, "authorize_extra_credits": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: lifecycleOwnerID, SessionID: lifecycleSessionID,
		Capability: generation.TextTo3D, OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	key = fmt.Sprintf("mixed-%d-%s", lifecycleSubmissionSequence.Add(1), key)
	operationID := "mixed:" + string(prepared.ID)
	grants := executiongrant.Service{Grants: h.repository.(*sqlite.Repository), Prepared: h.repository.(*sqlite.Repository)}
	principal := executiongrant.Principal{OwnerID: string(lifecycleOwnerID), ActorID: "lifecycle-test-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: lifecycleSessionID, OfferingID: string(prepared.OfferingID),
		OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
		PreparedInputHash: prepared.InputHash, IdempotencyKey: key, OperationID: operationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: lifecycleOwnerID, SessionID: lifecycleSessionID,
		PreparedInputID: prepared.ID, IdempotencyKey: key,
		OperationID: operationID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

func TestTripoMixedTaskSucceedsWithThePrimaryModelAndConvertsTheRestAfterwards(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	harness := newLifecycleHarness(t, upstream, false, nil)
	taskID := submitFullFormatTask(t, harness, []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}, "succeeds")

	task := harness.runToSucceeded(t, taskID)
	if task.Failure != nil {
		t.Fatalf("task failed: %+v", task.Failure)
	}
	// The stage shows the primary model: one model asset, and it is the GLTF
	// conversion's self-contained GLB.
	var model, gltf, extra int
	for _, asset := range task.Assets {
		switch {
		case asset.Role == "model":
			model++
			if asset.Variant != "" {
				t.Fatalf("model asset variant = %q, want the primary model", asset.Variant)
			}
		case asset.Variant == "converted.gltf":
			gltf++
		case asset.Role == "converted_model":
			extra++
		}
	}
	if model != 1 || gltf != 1 || extra != 0 {
		t.Fatalf("assets at success = model %d, gltf %d, other converted %d: %+v", model, gltf, extra, task.Assets)
	}
	// The five requested formats are in flight: pending slots with the kernel's
	// deadline, and no format may be reported as delivered yet.
	pending := []string{}
	for _, state := range task.Resources {
		if state.State == generation.ResourcePending {
			pending = append(pending, state.Reason)
		}
	}
	if len(pending) != 5 {
		t.Fatalf("pending slots = %d, want the five requested formats: %+v", len(pending), task.Resources)
	}
	if !generation.ResourcesPending(task.Resources) || task.NextActionAt.IsZero() {
		t.Fatalf("the post-success run is not scheduled: %+v", task)
	}
	// The formats land one round at a time, and the task stays SUCCEEDED.
	task = mixedDrivePostSuccess(t, harness, taskID)
	if task.State != generation.TaskSucceeded || generation.ResourcesPending(task.Resources) {
		t.Fatalf("task = %s, pending %v", task.State, task.Resources)
	}
	for _, state := range task.Resources {
		if state.State != generation.ResourceReady {
			t.Fatalf("slot %+v did not land", state)
		}
	}
	converts, _ := upstream.snapshot()
	if len(converts) != 6 {
		t.Fatalf("convert requests = %v, want the GLTF before success and the five after", converts)
	}
}

// A format whose child keeps failing only affects that format: the official
// rules resubmit it while its own budget lasts, then it ends failed with its own
// slot; the other four formats are still delivered and the task stays SUCCEEDED.
func TestTripoMixedFailedFormatOnlyFailsItsOwnSlot(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	upstream.failChild("FBX")
	harness := newLifecycleHarness(t, upstream, false, nil)
	taskID := submitFullFormatTask(t, harness, []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}, "fbx-fails")

	task := harness.runToSucceeded(t, taskID)
	task = mixedDrivePostSuccess(t, harness, taskID)
	if task.State != generation.TaskSucceeded || task.Failure != nil {
		t.Fatalf("task = %s %+v, want the format failure to stay inside its slot", task.State, task.Failure)
	}
	fbx := lifecycleSlot(t, task, "converted.fbx")
	if fbx.State != generation.ResourceFailed || fbx.Reason != "post_success.conversion_failed" {
		t.Fatalf("FBX slot = %+v, want failed/post_success.conversion_failed", fbx)
	}
	// The other requested formats landed.
	for _, variant := range []string{"converted.usdz", "converted.obj", "converted.stl", "converted.3mf"} {
		if state := lifecycleSlot(t, task, variant); state.State != generation.ResourceReady {
			t.Fatalf("%s = %+v, want ready", variant, state)
		}
	}
	converts, _ := upstream.snapshot()
	fbxPosts := 0
	for _, format := range converts {
		if format == "FBX" {
			fbxPosts++
		}
	}
	if fbxPosts != 6 {
		t.Fatalf("FBX posts = %d, want the first send and five resubmissions of its own", fbxPosts)
	}
	for _, format := range []string{"USDZ", "OBJ", "STL", "3MF"} {
		posts := 0
		for _, sent := range converts {
			if sent == format {
				posts++
			}
		}
		if posts != 1 {
			t.Fatalf("%s posts = %d, want exactly one", format, posts)
		}
	}
}

// A running post-success run survives a restart: the checkpoint and the resource
// states come back unchanged, and nothing is charged twice.
func TestTripoMixedPostSuccessSurvivesARestart(t *testing.T) {
	upstream := newLifecycleUpstream(t)
	harness := newLifecycleHarness(t, upstream, false, nil)
	taskID := submitFullFormatTask(t, harness, []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}, "restart")

	harness.runToSucceeded(t, taskID)
	mixedStep(t, harness)
	mixedStep(t, harness)
	before := harness.task(t, taskID)
	postsBefore, _ := upstream.snapshot()

	harness.module = harness.rebuild(t, harness.repository, harness.assets)
	after := harness.task(t, taskID)
	if after.State != generation.TaskSucceeded {
		t.Fatalf("state after restart = %s, want SUCCEEDED", after.State)
	}
	if len(after.Resources) != len(before.Resources) {
		t.Fatalf("slots after restart = %d, want %d", len(after.Resources), len(before.Resources))
	}
	mixedDrivePostSuccess(t, harness, taskID)
	postsAfter, _ := upstream.snapshot()
	// Nothing is charged twice: every format was sent exactly once, before or
	// after the restart.
	// The upstream records every request it ever saw, so the pre-restart list is
	// a prefix of the post-restart one.
	sent := map[string]int{}
	for _, format := range postsAfter {
		sent[format]++
	}
	for _, format := range []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"} {
		if sent[format] != 1 {
			t.Fatalf("%s sent %d times in total (%v / %v)", format, sent[format], postsBefore, postsAfter)
		}
	}
	if len(postsAfter) != 6 {
		t.Fatalf("convert requests after restart = %v, want all six", postsAfter)
	}
}

// mixedStep is one post-success round of the mixed Offering: its declared
// interval is 5 seconds, so the clock moves less than the lifecycle harness's
// 61-second step and the 7200-second deadline is not spent by the loop.
func mixedStep(t *testing.T, h *lifecycleHarness) {
	t.Helper()
	if _, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID:  "mixed-worker",
		LeaseFor:  time.Minute,
		BatchSize: 10,
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(6 * time.Second)
}

// mixedDrivePostSuccess drives the post-success run until every requested format
// reached a terminal state.
func mixedDrivePostSuccess(t *testing.T, h *lifecycleHarness, id generation.TaskID) generation.Task {
	t.Helper()
	for range 800 {
		mixedStep(t, h)
		task := h.task(t, id)
		if !generation.ResourcesPending(task.Resources) {
			return task
		}
	}
	t.Fatalf("post-success formats did not finish: %+v", h.task(t, id).Resources)
	return generation.Task{}
}

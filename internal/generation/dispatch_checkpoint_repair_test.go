package generation_test

// W4 安全修复（W5 前置）的敏感回归测试：
//   - F1：finishFailed（含内联落盘失败路径）必须持久化已验证回执结算；
//   - S1：CommitDispatchCheckpoint 的 attempts UPDATE 命中 0 行时必须
//     整体回滚、拒绝发放发送许可；
//   - S2：被去重/陈旧判定丢弃的 observation 不得吞掉已验证回执；
//   - STD1：tryRefreshClaims 物化刷新路径显式拒绝私有付费派发，
//     不得退回 nil-checkpointer 兼容直连。
// 复用 dispatch_checkpoint_test.go 的 harness 与持久态构造纪律。

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ── F1(a)：非重试失败路径的回执结算 ──

// failingAfterDispatchAdapter 在 Start 内检查点+发送+回执，随后以不可
// 重试失败结束尝试。
type failingAfterDispatchAdapter struct {
	mu           sync.Mutex
	sends        int
	checkpoints  int
	attemptID    generation.AttemptID
	failureShown bool
}

func (a *failingAfterDispatchAdapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	ack, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
		Bytes: json.RawMessage(`{"stage":"fixture.create"}`),
	})
	a.mu.Lock()
	a.checkpoints++
	a.attemptID = request.AttemptID
	a.mu.Unlock()
	if err != nil {
		return generation.Observation{}, err
	}
	a.mu.Lock()
	a.sends++
	a.mu.Unlock()
	_ = ack
	return generation.Observation{
		Phase: generation.UpstreamFailed,
		Failure: &generation.Failure{
			Class:      generation.FailureRejected,
			Retryable:  false,
			SafeDetail: "fixture upstream rejected",
		},
		Dispatch: receiptOf(ack),
	}, nil
}

func (a *failingAfterDispatchAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("not expected in this fixture")
}

func (a *failingAfterDispatchAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("not expected")
}

func receiptOf(receipt *generation.DispatchReceipt) *generation.DispatchReceipt {
	return receipt
}

func TestFinishFailedSettlesValidReceipt(t *testing.T) {
	adapter := &failingAfterDispatchAdapter{}
	h := newCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "f1-finish.db"),
	)
	taskID := h.submit(t)
	h.reconcile(t)
	task := h.task(t, taskID)
	if task.State != generation.TaskFailed {
		t.Fatalf("task state = %s; want FAILED", task.State)
	}
	if adapter.checkpoints != 1 || adapter.sends != 1 {
		t.Fatalf("checkpoints = %d, sends = %d; want 1/1", adapter.checkpoints, adapter.sends)
	}
	state := readCheckpointColumn(t, h.dbPath, string(adapter.attemptID))
	if !json.Valid([]byte(state)) {
		t.Fatalf("checkpoint column not settled: %q", state)
	}
	var settled struct {
		Sequence int64  `json:"sequence"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal([]byte(state), &settled); err != nil {
		t.Fatalf("parse checkpoint state: %v", err)
	}
	if settled.State != "received" || settled.Sequence != 1 {
		t.Fatalf("checkpoint state = %+v; want {received 1}", settled)
	}
}

// ── F1(b)：内联落盘失败路径的回执结算 ──

// inlineFailureAdapter 在 Observe 完成相位携带无法落盘的内联产物
// （声明 image/* 但内容不是图片），触发 storeInlineImages 失败；回执
// 来自 Observe 内的检查点发送。
type inlineFailureAdapter struct {
	mu          sync.Mutex
	rounds      int
	sends       int
	checkpoints int
	attemptID   generation.AttemptID
}

func (a *inlineFailureAdapter) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "gemini", RemoteID: "inline-1"},
		PollAfter: time.Millisecond,
		DedupeKey: "inline-1:accepted",
	}, nil
}

func (a *inlineFailureAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	a.rounds++
	round := a.rounds
	a.attemptID = request.AttemptID
	a.mu.Unlock()
	if round == 1 {
		return generation.Observation{
			Phase:     generation.UpstreamRunning,
			Job:       &request.Job,
			Progress:  50,
			PollAfter: time.Millisecond,
			DedupeKey: "inline-1:running",
		}, nil
	}
	ack, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
		Bytes: json.RawMessage(`{"stage":"fixture.observe-dispatch"}`),
	})
	a.mu.Lock()
	a.checkpoints++
	a.mu.Unlock()
	if err != nil {
		return generation.Observation{}, err
	}
	a.mu.Lock()
	a.sends++
	a.mu.Unlock()
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &request.Job,
		Progress:  100,
		DedupeKey: "inline-1:completed",
		Artifacts: []generation.ArtifactClaim{{
			ID:        "inline-1:image",
			Role:      "image",
			Inline:    []byte("definitely not an image payload"),
			MediaType: "image/png",
		}},
		Dispatch: ack,
	}, nil
}

func (a *inlineFailureAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("not expected")
}

func TestInlineStoreFailureSettlesValidReceipt(t *testing.T) {
	adapter := &inlineFailureAdapter{}
	h := newCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "f1-inline.db"),
	)
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.TextToImage,
		OfferingID: "gemini/gemini-3-pro-image/text-to-image",
		Input:      []byte(`{"prompt":"an inline fixture"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		h.reconcile(t)
		h.clock.Advance(2 * time.Second)
		if task := h.task(t, accepted.TaskID); task.State.Terminal() {
			if task.State != generation.TaskFailed {
				t.Fatalf("task state = %s; want FAILED (inline store failure)", task.State)
			}
			if adapter.checkpoints != 1 || adapter.sends != 1 {
				t.Fatalf("checkpoints = %d, sends = %d; want 1/1", adapter.checkpoints, adapter.sends)
			}
			state := readCheckpointColumn(t, h.dbPath, string(adapter.attemptID))
			var settled struct {
				Sequence int64  `json:"sequence"`
				State    string `json:"state"`
			}
			if err := json.Unmarshal([]byte(state), &settled); err != nil {
				t.Fatalf("parse checkpoint state %q: %v", state, err)
			}
			if settled.State != "received" {
				t.Fatalf("checkpoint state = %+v; want received（内联失败也必须结算回执）", settled)
			}
			return
		}
	}
	t.Fatal("task did not reach terminal state")
}

// ── S1：attempts UPDATE 0 行 → 回滚 + 拒绝许可 ──

func TestCheckpointAttemptRowMissingRefusesPermit(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "s1-missing-attempt.db"),
	)
	taskID := h.submit(t)
	lease := h.stageSubmittingAttempt(t, taskID)

	// 模拟并发删除尝试行：许可的持久化目标不复存在。
	db, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`DELETE FROM generation_attempts WHERE id = ?`,
		lease.CurrentAttempt.ID,
	); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = h.repository.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          lease.Task.ID,
		AttemptID:       lease.CurrentAttempt.ID,
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Now:             h.clock.Now(),
		Intent:          json.RawMessage(`{"stage":"fixture.create"}`),
		AckToken:        "s1-ack-token",
	})
	if err == nil {
		t.Fatal("attempts 行缺失时必须拒绝发放发送许可")
	}
	// 行缺失的拒绝可能来自事务内早期 NotFound 探测或 UPDATE 0 行守卫
	//（同一事务同谓词下后者是纵深防御）；契约只要求：无许可 + 整体回滚。
	if !errors.Is(err, generation.RepositoryConflict) &&
		!errors.Is(err, generation.RepositoryNotFound) {
		t.Fatalf("err = %v; want RepositoryConflict 或 RepositoryNotFound", err)
	}
	// 关键断言：任务版本未被推进（事务整体回滚，许可未持久化）。
	// 直接读列：删除尝试行后聚合 Load 不再可用。
	db2, err := sql.Open("sqlite", "file:"+h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var version int64
	if err := db2.QueryRow(
		`SELECT version FROM generation_tasks WHERE id = ?`,
		lease.Task.ID,
	).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != lease.Task.Version {
		t.Fatalf("task version = %d; want %d（回滚不得推进版本）", version, lease.Task.Version)
	}
}

// ── S2：去重 observation 不得吞掉已验证回执 ──

// dedupeReceiptAdapter 的 Observe 恒定返回同一 DedupeKey；第二轮在
// 观察期内发起私有派发并携带回执——该 observation 会被去重，但回执
// 必须被结算。
type dedupeReceiptAdapter struct {
	mu          sync.Mutex
	rounds      int
	sends       int
	checkpoints int
	attemptID   generation.AttemptID
}

func (a *dedupeReceiptAdapter) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "dedupe-1"},
		PollAfter: time.Millisecond,
		DedupeKey: "dedupe-1:accepted",
	}, nil
}

func (a *dedupeReceiptAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	a.rounds++
	round := a.rounds
	a.attemptID = request.AttemptID
	a.mu.Unlock()
	base := generation.Observation{
		Job:       &request.Job,
		Phase:     generation.UpstreamRunning,
		Progress:  50,
		PollAfter: time.Millisecond,
		DedupeKey: "dedupe-1:running",
	}
	if round == 2 {
		// 观察期私有派发：上游父任务相位未变（同 DedupeKey），回执仍须结算。
		ack, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
			Bytes: json.RawMessage(`{"stage":"fixture.observe-dispatch"}`),
		})
		a.mu.Lock()
		a.checkpoints++
		a.mu.Unlock()
		if err != nil {
			return generation.Observation{}, err
		}
		a.mu.Lock()
		a.sends++
		a.mu.Unlock()
		base.Dispatch = ack
		return base, nil
	}
	if round >= 3 {
		base.Phase = generation.UpstreamCompleted
		base.Progress = 100
		base.DedupeKey = fmt.Sprintf("dedupe-1:completed:%d", round)
		base.Artifacts = []generation.ArtifactClaim{{
			ID:        "dedupe-1:model",
			Role:      "model",
			Inline:    validGLB("dedupe receipt"),
			MediaType: "model/gltf-binary",
		}}
	}
	return base, nil
}

func (a *dedupeReceiptAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func TestDedupedObservationSettlesValidReceipt(t *testing.T) {
	adapter := &dedupeReceiptAdapter{}
	h := newCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "s2-dedupe.db"),
	)
	taskID := h.submit(t)
	task := h.runUntilTerminal(t, taskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task state = %s; want SUCCEEDED", task.State)
	}
	if adapter.checkpoints != 1 || adapter.sends != 1 {
		t.Fatalf("checkpoints = %d, sends = %d; want 1/1", adapter.checkpoints, adapter.sends)
	}
	state := readCheckpointColumn(t, h.dbPath, string(adapter.attemptID))
	var settled struct {
		Sequence int64  `json:"sequence"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal([]byte(state), &settled); err != nil {
		t.Fatalf("parse checkpoint state %q: %v", state, err)
	}
	if settled.State != "received" || settled.Sequence != 1 {
		t.Fatalf("checkpoint state = %+v; want {received 1}（去重不得吞回执）", settled)
	}
}

// ── STD1：物化刷新路径显式拒绝付费派发 ──

// refreshDenialAdapter 完成相位返回无法下载的产物，触发物化失败重试；
// 刷新 Observe 时尝试取得私有派发许可——必须被显式拒绝且零发送。
type refreshDenialAdapter struct {
	mu             sync.Mutex
	rounds         int
	paidSends      int
	permitAttempts int
	permitDenied   bool
}

func (a *refreshDenialAdapter) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "refresh-1"},
		PollAfter: time.Millisecond,
		DedupeKey: "refresh-1:accepted",
	}, nil
}

func (a *refreshDenialAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	a.rounds++
	round := a.rounds
	a.mu.Unlock()
	if round <= 1 {
		return generation.Observation{
			Phase:     generation.UpstreamRunning,
			Job:       &request.Job,
			Progress:  50,
			PollAfter: time.Millisecond,
			DedupeKey: "refresh-1:running",
		}, nil
	}
	// 完成相位（DedupeKey 稳定不变，模拟上游已完成、产物就绪）。
	observation := generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &request.Job,
		Progress:  100,
		PollAfter: time.Millisecond,
		DedupeKey: "refresh-1:completed",
		Artifacts: []generation.ArtifactClaim{{
			ID:        "refresh-1:model",
			Role:      "model",
			Locator:   "https://fixtures.invalid/model.glb",
			MediaType: "model/gltf-binary",
		}},
	}
	// 物化刷新会再次调用 Observe（round>=3；round 2 是正常观察期的
	// 完成上报）：刷新路径上尝试付费私有派发必须被显式拒绝。
	if round >= 3 {
		receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
			Bytes: json.RawMessage(`{"stage":"fixture.refresh-dispatch"}`),
		})
		a.mu.Lock()
		a.permitAttempts++
		a.mu.Unlock()
		if err != nil {
			a.mu.Lock()
			a.permitDenied = true
			a.mu.Unlock()
			return generation.Observation{}, err
		}
		if receipt != nil {
			a.mu.Lock()
			a.paidSends++
			a.mu.Unlock()
		}
	}
	return observation, nil
}

func (a *refreshDenialAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, &generation.ProviderError{
		Failure: &generation.Failure{
			Class:      generation.FailureUpstreamUnavailable,
			Retryable:  true,
			SafeDetail: "fixture download unavailable",
		},
	}
}

func TestTryRefreshClaimsDeniesPaidDispatch(t *testing.T) {
	adapter := &refreshDenialAdapter{}
	h := newCheckpointHarness(
		t,
		adapter,
		filepath.Join(t.TempDir(), "std1-refresh.db"),
	)
	taskID := h.submit(t)
	// 推进足够多轮：进入 MATERIALIZING、下载失败、触发带刷新的重试。
	sawMaterializing := false
	for range 24 {
		h.reconcile(t)
		h.clock.Advance(2 * time.Second)
		if task := h.task(t, taskID); task.State == generation.TaskMaterializing {
			sawMaterializing = true
		}
		if adapter.permitDenied {
			break
		}
	}
	if !sawMaterializing {
		t.Fatal("fixture did not reach MATERIALIZING")
	}
	if !adapter.permitDenied {
		t.Fatal("刷新路径的私有派发许可必须被显式拒绝，而不是放行")
	}
	if adapter.paidSends != 0 {
		t.Fatalf("paid sends = %d; want 0（拒绝后必须零付费发送）", adapter.paidSends)
	}
	// 任务可能仍在重试，也可能耗尽物化次数后 FAILED——两者都不违反本
	// 用例的契约；契约是刷新路径零付费发送 + 显式拒绝。
	if adapter.permitAttempts == 0 {
		t.Fatal("刷新路径未尝试私有派发（用例未覆盖目标路径）")
	}
}

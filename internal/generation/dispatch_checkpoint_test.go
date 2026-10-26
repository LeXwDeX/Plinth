package generation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

// 本文件实现计划 §4.4 崩溃矩阵与 §4.2/§4.3 协议行为的模块级用例（W4）。
// 崩溃用持久态构造模拟：进程崩溃的唯一持久效果是已提交的 SQLite 事务，
// 直接经仓储 API 构造"崩溃遗留状态"后重启协调器，比进程内 panic 更忠实
// 且可复现。重启场景关闭并重开真实文件库。

type checkpointHarness struct {
	module                *generation.Module
	repository            *sqlite.Repository
	clock                 *fakeClock
	dbPath                string
	historicalPostSuccess bool
	submitFixtureState
}

func newCheckpointHarness(
	t *testing.T,
	adapter generation.ProviderPort,
	dbPath string,
) *checkpointHarness {
	return newCheckpointHarnessWithClock(t, adapter, dbPath, nil)
}

func newCheckpointHarnessWithClock(
	t *testing.T,
	adapter generation.ProviderPort,
	dbPath string,
	clock *fakeClock,
	historicalPostSuccess ...bool,
) *checkpointHarness {
	t.Helper()
	return newCheckpointHarnessWithCatalog(t, adapter, dbPath, clock, nil, historicalPostSuccess...)
}

// newConversionCheckpointHarness runs the harness with Tripo format conversion
// enabled. The default Schema ships it disabled (the 3D stage reads GLB only);
// the kernel's conversion path (consent, checkpoint, no replay) stays covered.
func newConversionCheckpointHarness(t *testing.T, adapter generation.ProviderPort, dbPath string) *checkpointHarness {
	t.Helper()
	return newCheckpointHarnessWithCatalog(t, adapter, dbPath, nil, catalogtest.LoadDefaultWithTripoConversion(t))
}

func newCheckpointHarnessWithCatalog(
	t *testing.T,
	adapter generation.ProviderPort,
	dbPath string,
	clock *fakeClock,
	override *catalog.Catalog,
	historicalPostSuccess ...bool,
) *checkpointHarness {
	t.Helper()
	repository, err := sqlitetest.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var taskCatalog *catalog.Catalog
	if override != nil {
		taskCatalog = override
	} else {
		taskCatalog, err = catalog.LoadDefault()
	}
	if len(historicalPostSuccess) > 0 && historicalPostSuccess[0] {
		taskCatalog, err = historicalPostSuccessCatalog(t)
	}
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	for _, key := range []string{
		"openai-images.v1",
		"gemini-images.v1",
		"meshy.v2",
		"tripo.v3",
		"matting.v1",
	} {
		if key == "tripo.v3" {
			adapter = sixFormatFixtureAdapter{next: adapter}
		}
		if err := registry.Register(key, adapter); err != nil {
			t.Fatal(err)
		}
	}
	localStore, err := local.New(filepath.Join(filepath.Dir(dbPath), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repository, localStore)
	if err != nil {
		t.Fatal(err)
	}
	if clock == nil {
		clock = &fakeClock{now: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)}
	}
	repository.SetClock(clock.Now)
	module, err := generation.New(generation.Dependencies{
		Repository: repository,
		Catalog:    taskCatalog,
		Providers:  registry,
		Assets:     localStore,
		Inputs:     inputs,
		Clock:      clock,
		Cache:      &recordingCache{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return &checkpointHarness{
		module:                module,
		repository:            repository,
		clock:                 clock,
		dbPath:                dbPath,
		historicalPostSuccess: len(historicalPostSuccess) > 0 && historicalPostSuccess[0],
	}
}

// restart 关闭当前模块并按同一数据库文件重建协调器（真实重启）。
func (h *checkpointHarness) restart(
	t *testing.T,
	adapter generation.ProviderPort,
) *checkpointHarness {
	t.Helper()
	if err := h.module.Close(); err != nil {
		t.Fatal(err)
	}
	// 时钟跨重启延续（模拟真实墙钟）。
	return newCheckpointHarnessWithClock(t, adapter, h.dbPath, h.clock, h.historicalPostSuccess)
}

func (h *checkpointHarness) submit(t *testing.T) generation.TaskID {
	t.Helper()
	accepted, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"a checkpoint lantern","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted.TaskID
}

func (h *checkpointHarness) reconcile(t *testing.T) {
	t.Helper()
	_, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID:  "checkpoint-worker",
		LeaseFor:  time.Minute,
		BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (h *checkpointHarness) task(
	t *testing.T,
	id generation.TaskID,
) generation.Task {
	t.Helper()
	task, err := h.module.Get(context.Background(), "owner-dispatch", id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// stageSubmittingAttempt 直接经仓储构造"startAttempt 已提交新尝试、进程在
// port.Start 内崩溃"的持久状态（镜像 coordinator.startAttempt 的新尝试
// 提交），返回任务与持有租约的 lease。
func (h *checkpointHarness) stageSubmittingAttempt(
	t *testing.T,
	taskID generation.TaskID,
) generation.Lease {
	t.Helper()
	now := h.clock.Now()
	leases, err := h.repository.ClaimReady(
		context.Background(),
		"staging-worker",
		now,
		time.Minute,
		5,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range leases {
		if lease.Task.ID != taskID {
			continue
		}
		attempt := &generation.Attempt{
			ID:            "attp-staged-1",
			TaskID:        taskID,
			Ordinal:       1,
			Offering:      lease.Task.Offering,
			State:         generation.AttemptSubmitting,
			SubmissionKey: string(taskID) + ":1:attp-staged-1",
			StartedAt:     now,
			UpdatedAt:     now,
		}
		task := lease.Task
		task.CurrentAttemptID = attempt.ID
		task.UpdatedAt = now
		task.Version++
		if err := h.repository.Commit(context.Background(), generation.Commit{
			LeaseToken:      lease.Token,
			ExpectedVersion: lease.Task.Version,
			Task:            task,
			Attempt:         attempt,
			Events: []generation.Event{{
				TaskID:    taskID,
				Type:      "attempt.started",
				Data:      generation.Event{}.Data,
				CreatedAt: now,
			}},
		}); err != nil {
			t.Fatal(err)
		}
		lease.Task = task
		lease.CurrentAttempt = attempt
		return lease
	}
	t.Fatal("staging: task not claimed")
	return generation.Lease{}
}

// readCheckpointColumn 直读持久化的内核检查点状态（测试取证用）。
func readCheckpointColumn(
	t *testing.T,
	dbPath string,
	attemptID string,
) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value sql.NullString
	if err := db.QueryRow(
		`SELECT dispatch_checkpoint_json FROM generation_attempts WHERE id = ?`,
		attemptID,
	).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if !value.Valid {
		return ""
	}
	return value.String
}

// checkpointingAdapter 是真实消费者形状的协议测试替身：在 Start 内先
// Checkpoint 再"发送"（计数），回执随观察返回；Observe 轮可选地在观察
// 期发起私有派发（W6 形状）。
type checkpointingAdapter struct {
	mu sync.Mutex

	httpSends       int
	checkpointCalls int
	observeRounds   int

	startCheckpointerSeen   bool
	observeCheckpointerSeen bool

	forgedReceiptOnStart  bool
	abortBeforeSend       bool // 有证明未派发：not_dispatched 回执 + 可重试失败
	checkpointOnObserve   bool
	observeDispatchResult string // 非空时 Observe 携带回执
}

func (a *checkpointingAdapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	checkpointer, ok := generation.DispatchCheckpointerFromContext(ctx)
	a.mu.Lock()
	a.startCheckpointerSeen = ok
	a.mu.Unlock()
	if !ok {
		return generation.Observation{}, errors.New("测试替身要求 Start 注入检查点")
	}
	ack, err := checkpointer.Checkpoint(ctx, generation.DispatchIntent{
		Bytes: json.RawMessage(
			`{"stage":"fixture.create","submission_key":"` + request.SubmissionKey + `"}`,
		),
	})
	a.mu.Lock()
	a.checkpointCalls++
	a.mu.Unlock()
	if err != nil {
		return generation.Observation{}, err
	}
	if a.abortBeforeSend {
		a.mu.Lock()
		a.abortBeforeSend = false // 只在首轮生效
		a.mu.Unlock()
		return generation.Observation{
			Phase: generation.UpstreamFailed,
			Job:   nil,
			Failure: &generation.Failure{
				Class:     generation.FailureRateLimited,
				Retryable: true,
			},
			Dispatch: &generation.DispatchReceipt{
				Sequence: ack.Sequence,
				AckToken: ack.AckToken,
				Result:   generation.DispatchResultNotDispatched,
			},
		}, nil
	}
	a.mu.Lock()
	a.httpSends++
	a.mu.Unlock()
	receipt := &generation.DispatchReceipt{
		Sequence: ack.Sequence,
		AckToken: ack.AckToken,
		Result:   generation.DispatchResultDispatched,
	}
	if a.forgedReceiptOnStart {
		receipt.AckToken = "forged-token"
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "dispatch-1"},
		PollAfter: time.Millisecond,
		DedupeKey: "dispatch-1:accepted",
		Dispatch:  receipt,
	}, nil
}

func (a *checkpointingAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	checkpointer, ok := generation.DispatchCheckpointerFromContext(ctx)
	a.mu.Lock()
	a.observeCheckpointerSeen = ok
	a.observeRounds++
	round := a.observeRounds
	checkpointOnObserve := a.checkpointOnObserve
	observeDispatchResult := a.observeDispatchResult
	a.mu.Unlock()
	var receipt *generation.DispatchReceipt
	if checkpointOnObserve {
		ack, err := checkpointer.Checkpoint(ctx, generation.DispatchIntent{
			Bytes: json.RawMessage(`{"stage":"fixture.observe-dispatch"}`),
		})
		a.mu.Lock()
		a.checkpointCalls++
		a.mu.Unlock()
		if err != nil {
			return generation.Observation{}, err
		}
		a.mu.Lock()
		a.httpSends++
		a.mu.Unlock()
		receipt = &generation.DispatchReceipt{
			Sequence: ack.Sequence,
			AckToken: ack.AckToken,
			Result:   generation.DispatchResultDispatched,
		}
	}
	if observeDispatchResult != "" && receipt == nil && round == 1 {
		receipt = &generation.DispatchReceipt{
			Sequence: 99,
			AckToken: "stale-or-forged",
			Result:   observeDispatchResult,
		}
	}
	base := generation.Observation{
		Job:       &request.Job,
		PollAfter: time.Millisecond,
		Dispatch:  receipt,
	}
	if round == 1 {
		base.Phase = generation.UpstreamRunning
		base.Progress = 50
		base.DedupeKey = "dispatch-1:running"
		return base, nil
	}
	base.Phase = generation.UpstreamCompleted
	base.Progress = 100
	base.DedupeKey = "dispatch-1:completed"
	base.Artifacts = []generation.ArtifactClaim{{
		ID:        "dispatch-1:model",
		Role:      "model",
		Inline:    validGLB("dispatch checkpoint"),
		MediaType: "model/gltf-binary",
	}}
	return base, nil
}

func (a *checkpointingAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(
		bytes.NewReader(request.Claim.Inline),
	), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func (a *checkpointingAdapter) snapshot() (sends int, checkpoints int, rounds int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.httpSends, a.checkpointCalls, a.observeRounds
}

// privateConversionProgressAdapter reproduces a provider that reaches the
// public 98% cap before dispatching a paid, provider-private conversion.
type privateConversionProgressAdapter struct {
	checkpointingAdapter
	rounds int
}

func (a *privateConversionProgressAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	a.rounds++
	base := generation.Observation{
		Phase: generation.UpstreamRunning, Job: &request.Job, Progress: 98,
		PollAfter: time.Millisecond,
	}
	switch a.rounds {
	case 1:
		base.DedupeKey = "source:98"
	case 2:
		receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
			Bytes: json.RawMessage(`{"stage":"models/convert","details":{"conversions":{"GLTF":{"status":"dispatching"}}}}`),
		})
		if err != nil {
			return generation.Observation{}, err
		}
		base.Dispatch = receipt
		base.ObservedAt = time.Now().UTC()
		base.Opaque = json.RawMessage(`{"conversions":{"GLTF":{"status":"submitted"}}}`)
		base.DedupeKey = "conversion:submitted"
	case 3:
		base.ObservedAt = time.Now().Add(time.Second).UTC()
		base.Opaque = json.RawMessage(`{"conversions":{"GLTF":{"status":"done"}}}`)
		base.DedupeKey = "conversion:done"
	}
	return base, nil
}

func TestPrivateConversionAfterRunning98PersistsReceiptAndOpaque(t *testing.T) {
	adapter := &privateConversionProgressAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "conversion.db"))
	taskID := h.submit(t)
	for range 3 { // Start, source reaches 98%, then conversion dispatches.
		h.reconcile(t)
		h.clock.Advance(6 * time.Second)
	}
	aggregate, err := h.repository.Load(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.State != generation.TaskRunning || aggregate.Task.Progress != 98 ||
		!bytes.Contains(aggregate.CurrentAttempt.Opaque, []byte(`"status":"submitted"`)) {
		t.Fatalf("conversion receipt lost: state=%s progress=%d opaque=%s",
			aggregate.Task.State, aggregate.Task.Progress, aggregate.CurrentAttempt.Opaque)
	}
	if state := readCheckpointColumn(t, h.dbPath, string(aggregate.CurrentAttempt.ID)); !jsonQueryEquals(state, "state", "received") {
		t.Fatalf("checkpoint state = %s, want received", state)
	}
	h.reconcile(t)
	aggregate, err = h.repository.Load(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(aggregate.CurrentAttempt.Opaque, []byte(`"status":"done"`)) {
		t.Fatalf("same-progress private observation was discarded: %s", aggregate.CurrentAttempt.Opaque)
	}
}

// runUntilState 轮询协调直至任务达到终态。
func (h *checkpointHarness) runUntilTerminal(
	t *testing.T,
	id generation.TaskID,
) generation.Task {
	t.Helper()
	for range 30 {
		h.reconcile(t)
		h.clock.Advance(2 * time.Second)
		if task := h.task(t, id); task.State.Terminal() {
			return task
		}
	}
	t.Fatal("task did not reach terminal state")
	return generation.Task{}
}

// ── §4.3 唯一合法路径 + §4.2 值语义版本连续 ──

// TestDispatchCheckpointHappyPath 验证：检查点在 Start 与 Observe 两个入口
// 均可用；一次 Start 恰好一次 Checkpoint、至多一次 HTTP；回执被接受并
// 结算为 received；整个流程无版本 CAS 冲突（值语义版本连续的端到端证明）。
func TestDispatchCheckpointHappyPath(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	task := h.runUntilTerminal(t, taskID)

	if task.State != generation.TaskSucceeded || task.Failure != nil {
		t.Fatalf("task = %s, failure = %#v", task.State, task.Failure)
	}
	sends, checkpoints, _ := adapter.snapshot()
	if checkpoints != 1 || sends != 1 {
		t.Fatalf("checkpoints = %d, sends = %d; want 1/1（一次 ack 至多一次 HTTP）", checkpoints, sends)
	}
	if !adapter.startCheckpointerSeen || !adapter.observeCheckpointerSeen {
		t.Fatal("检查点回调必须在 Start 与 Observe 两个入口均可用")
	}
	aggregate, err := h.repository.Load(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	state := readCheckpointColumn(t, h.dbPath, string(aggregate.CurrentAttempt.ID))
	if !jsonQueryEquals(state, "state", "received") {
		t.Fatalf("dispatch_checkpoint_json = %s; want state=received", state)
	}
}

// ── §4.4 崩溃矩阵 ──

// 窗口 1：检查点调用前崩溃——无意图持久化；恢复保守失败（SubmissionUnknown），
// 其后新任务重新检查点并发送恰好一次（计数=1）。
func TestDispatchCrashBeforeCheckpoint(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	h.stageSubmittingAttempt(t, taskID)

	// 模拟进程死亡后重启：同库新协调器，租约过期后重新领取。
	h.clock.Advance(61 * time.Second)
	h2 := h.restart(t, adapter)
	task := h2.runUntilTerminal(t, taskID)
	if task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureSubmissionUnknown ||
		!task.Failure.Ambiguous {
		t.Fatalf("task = %s, failure = %#v; want ambiguous submission_unknown", task.State, task.Failure)
	}
	sends, checkpoints, _ := adapter.snapshot()
	if sends != 0 || checkpoints != 0 {
		t.Fatalf("恢复轮 sends = %d, checkpoints = %d; want 0/0（本轮未触达检查点/发送）", sends, checkpoints)
	}

	// 重新检查点并发送：新任务恰好一次。
	fresh := h2.submit(t)
	if task := h2.runUntilTerminal(t, fresh); task.State != generation.TaskSucceeded {
		t.Fatalf("fresh task = %s, failure = %#v", task.State, task.Failure)
	}
	sends, checkpoints, _ = adapter.snapshot()
	if sends != 1 || checkpoints != 1 {
		t.Fatalf("cumulative sends = %d, checkpoints = %d; want 1/1", sends, checkpoints)
	}
}

// 窗口 2：检查点后、HTTP 前崩溃——dispatching 无回执；恢复禁止再次发送
// （计数=0），无第二次 Checkpoint 成功；歧义可见（SubmissionUnknown）。
func TestDispatchCrashAfterCheckpointBeforeHTTP(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	lease := h.stageSubmittingAttempt(t, taskID)

	// 检查点已持久化（进程在 HTTP 前死亡）。
	write := generation.DispatchCheckpointWrite{
		TaskID:          taskID,
		AttemptID:       "attp-staged-1",
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Now:             h.clock.Now(),
		Intent:          json.RawMessage(`{"stage":"fixture.create"}`),
		AckToken:        repeatToken("a"),
	}
	if _, err := h.repository.CommitDispatchCheckpoint(context.Background(), write); err != nil {
		t.Fatal(err)
	}
	// 已存在 pending 检查点：任何新的 Checkpoint 都不得授予发送许可。
	if _, err := h.repository.CommitDispatchCheckpoint(context.Background(), write); !errors.Is(err, generation.ErrDispatchPending) {
		t.Fatalf("second checkpoint err = %v; want ErrDispatchPending", err)
	}

	h.clock.Advance(61 * time.Second)
	h2 := h.restart(t, adapter)
	task := h2.runUntilTerminal(t, taskID)
	if task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureSubmissionUnknown ||
		!task.Failure.Ambiguous {
		t.Fatalf("task = %s, failure = %#v; want ambiguous submission_unknown", task.State, task.Failure)
	}
	sends, checkpoints, _ := adapter.snapshot()
	if sends != 0 || checkpoints != 0 {
		t.Fatalf("sends = %d, checkpoints = %d; want 0/0（pending 栅栏禁止恢复期发送）", sends, checkpoints)
	}
	if state := readCheckpointColumn(t, h.dbPath, "attp-staged-1"); !jsonQueryEquals(state, "state", "dispatching") {
		t.Fatalf("checkpoint state = %s; want dispatching", state)
	}
}

// 窗口 3：HTTP 后、返回响应前崩溃——持久态与窗口 2 相同（这正是歧义根源）；
// 非幂等操作上游调用计数保持 ≤1，恢复零新增发送。
func TestDispatchCrashAfterHTTPBeforeResponse(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	lease := h.stageSubmittingAttempt(t, taskID)

	if _, err := h.repository.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          taskID,
		AttemptID:       "attp-staged-1",
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Now:             h.clock.Now(),
		Intent:          json.RawMessage(`{"stage":"fixture.create"}`),
		AckToken:        repeatToken("b"),
	}); err != nil {
		t.Fatal(err)
	}
	// 已发出的那次 HTTP（崩溃前完成）在计数器上不可区分于未发送——
	// 用预置计数模拟"确实已发送一次"。
	adapter.mu.Lock()
	adapter.httpSends = 1
	adapter.mu.Unlock()

	h.clock.Advance(61 * time.Second)
	h2 := h.restart(t, adapter)
	task := h2.runUntilTerminal(t, taskID)
	if task.State != generation.TaskFailed || task.Failure == nil || !task.Failure.Ambiguous {
		t.Fatalf("task = %s, failure = %#v; want ambiguous", task.State, task.Failure)
	}
	sends, _, _ := adapter.snapshot()
	if sends > 1 {
		t.Fatalf("upstream sends = %d; want ≤1（非幂等操作禁止重复发送）", sends)
	}
}

// 窗口 4：返回响应后、最终 Commit 前崩溃——回执丢失；恢复不新增
// Attempt/Start，版本 CAS 正确（收尾提交成功本身即证明版本连续），
// 检查点保持 dispatching（无回执结算）。
func TestDispatchCrashAfterResponseBeforeCommit(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	lease := h.stageSubmittingAttempt(t, taskID)

	if _, err := h.repository.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          taskID,
		AttemptID:       "attp-staged-1",
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Now:             h.clock.Now(),
		Intent:          json.RawMessage(`{"stage":"fixture.create"}`),
		AckToken:        repeatToken("c"),
	}); err != nil {
		t.Fatal(err)
	}

	h.clock.Advance(61 * time.Second)
	h2 := h.restart(t, adapter)
	task := h2.runUntilTerminal(t, taskID)
	if task.State != generation.TaskFailed || task.Failure == nil || !task.Failure.Ambiguous {
		t.Fatalf("task = %s, failure = %#v; want ambiguous", task.State, task.Failure)
	}
	events, err := h2.module.Events(context.Background(), "owner-dispatch", taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if countEvents(events, "attempt.started") != 1 {
		t.Fatalf("attempt.started = %d; want 1（恢复不新增 Attempt/Start）", countEvents(events, "attempt.started"))
	}
	if state := readCheckpointColumn(t, h.dbPath, "attp-staged-1"); !jsonQueryEquals(state, "state", "dispatching") {
		t.Fatalf("checkpoint state = %s; want dispatching（回执丢失不得结算）", state)
	}
	sends, _, _ := adapter.snapshot()
	if sends != 0 {
		t.Fatalf("sends = %d; want 0", sends)
	}
}

// 窗口 5：最终 Commit 后崩溃——检查点已 received；经真实 SQLite 重启后
// 正常继续观察直至成功，无重复提交。
func TestDispatchCrashAfterFinalCommit(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)

	// 第一轮：Start 检查点 + 发送 + 回执被提交（received）。此后"崩溃"。
	h.reconcile(t)
	h.clock.Advance(2 * time.Second)
	task := h.task(t, taskID)
	if task.State != generation.TaskRunning {
		t.Fatalf("state after start round = %s, failure = %#v", task.State, task.Failure)
	}
	aggregate, err := h.repository.Load(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if state := readCheckpointColumn(t, h.dbPath, string(aggregate.CurrentAttempt.ID)); !jsonQueryEquals(state, "state", "received") {
		t.Fatalf("checkpoint state = %s; want received", state)
	}

	h.clock.Advance(61 * time.Second)
	h2 := h.restart(t, adapter)
	final := h2.runUntilTerminal(t, taskID)
	if final.State != generation.TaskSucceeded {
		t.Fatalf("task = %s, failure = %#v", final.State, final.Failure)
	}
	sends, checkpoints, _ := adapter.snapshot()
	if sends != 1 || checkpoints != 1 {
		t.Fatalf("sends = %d, checkpoints = %d; want 1/1（received 后无重复提交）", sends, checkpoints)
	}
}

// ── not_dispatched 回执：有证明未派发才可清除 pending 并重发 ──

// TestDispatchNotDispatchedReceiptAllowsSingleResend 验证：not_dispatched
// 回执把检查点清回 idle，随后重试轮重新检查点并发送，累计恰好一次 HTTP。
func TestDispatchNotDispatchedReceiptAllowsSingleResend(t *testing.T) {
	adapter := &checkpointingAdapter{abortBeforeSend: true}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	task := h.runUntilTerminal(t, taskID)

	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %s, failure = %#v", task.State, task.Failure)
	}
	sends, checkpoints, _ := adapter.snapshot()
	if sends != 1 {
		t.Fatalf("sends = %d; want 1（未派发证明后才允许的一次发送）", sends)
	}
	if checkpoints != 2 {
		t.Fatalf("checkpoints = %d; want 2（首轮 not_dispatched 清 pending，次轮新许可）", checkpoints)
	}
}

// ── 回执拒绝：伪造/陈旧回执零生命周期突变、零重发 ──

// TestDispatchForgedReceiptRejected 伪造回执（错误 token）被保守丢弃：
// 任务不因该 observation 推进（后续合法观察继续），无重发。
func TestDispatchForgedReceiptRejected(t *testing.T) {
	adapter := &checkpointingAdapter{forgedReceiptOnStart: true}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)

	h.reconcile(t)
	h.clock.Advance(2 * time.Second)
	task := h.task(t, taskID)
	// 领取租约即 RUNNING（begin_attempt 合法转换）；伪造回执本身不得再推进。
	if task.State != generation.TaskRunning {
		t.Fatalf("state = %s; want RUNNING（仅 begin_attempt 推进，伪造回执不得推进生命周期）", task.State)
	}
	events, err := h.module.Events(context.Background(), "owner-dispatch", taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if countEvents(events, "provider.dispatch_receipt_rejected") != 0 {
		t.Fatalf("rejection events = %d; want 0（B17：陈旧回执静默重排不写事件）", countEvents(events, "provider.dispatch_receipt_rejected"))
	}
	// 伪造轮之后：pending 检查点使恢复保守失败（歧义可见）。
	task = h.runUntilTerminal(t, taskID)
	if task.State != generation.TaskFailed || task.Failure == nil || !task.Failure.Ambiguous {
		t.Fatalf("task = %s, failure = %#v; want ambiguous failure", task.State, task.Failure)
	}
	sends, _, _ := adapter.snapshot()
	if sends != 1 {
		t.Fatalf("sends = %d; want 1（拒绝回执不触发重发）", sends)
	}
}

// TestDispatchStaleReceiptOnObserveRejected 已结算 ack 在后续观察轮重放：
// 回执不可验证（新租约内无有效 ack）→ 保守丢弃，任务不受影响地继续完成。
func TestDispatchStaleReceiptOnObserveRejected(t *testing.T) {
	adapter := &checkpointingAdapter{observeDispatchResult: generation.DispatchResultDispatched}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	task := h.runUntilTerminal(t, taskID)

	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %s, failure = %#v", task.State, task.Failure)
	}
	events, err := h.module.Events(context.Background(), "owner-dispatch", taskID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if countEvents(events, "provider.dispatch_receipt_rejected") != 0 {
		t.Fatalf("rejection events = %d; want 0（B17：陈旧回执静默重排不写事件）", countEvents(events, "provider.dispatch_receipt_rejected"))
	}
	sends, checkpoints, _ := adapter.snapshot()
	if sends != 1 || checkpoints != 1 {
		t.Fatalf("sends = %d, checkpoints = %d; want 1/1", sends, checkpoints)
	}
}

// TestDispatchPendingFenceBlocksObserveDispatch 观察期的私有派发同样受
// pending 栅栏约束：存在未回执 dispatching 时，Observe 内的 Checkpoint
// 返回 ErrDispatchPending，Provider 放弃发送，任务保守失败。
func TestDispatchPendingFenceBlocksObserveDispatch(t *testing.T) {
	adapter := &checkpointingAdapter{checkpointOnObserve: true}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	lease := h.stageSubmittingAttempt(t, taskID)

	// 构造：Start 已成功（attempt RUNNING + job），上一轮观察期检查点
	// 持久化后进程死亡（dispatching 无回执）。
	now := h.clock.Now()
	attempt := *lease.CurrentAttempt
	attempt.State = generation.AttemptRunning
	attempt.UpstreamJob = &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "dispatch-9"}
	task := lease.Task
	task.State = generation.TaskRunning
	task.NextActionAt = now
	task.Version++
	if err := h.repository.Commit(context.Background(), generation.Commit{
		LeaseToken:      lease.Token,
		ExpectedVersion: lease.Task.Version,
		Task:            task,
		Attempt:         &attempt,
		ClearLease:      true,
	}); err != nil {
		t.Fatal(err)
	}
	// 下一轮观察被重新领取（新租约），Provider 在 Observe 内检查点后崩溃。
	h.clock.Advance(2 * time.Second)
	observeNow := h.clock.Now()
	observeLeases, err := h.repository.ClaimReady(
		context.Background(),
		"observe-worker",
		observeNow,
		time.Minute,
		5,
	)
	if err != nil {
		t.Fatal(err)
	}
	var observeLease generation.Lease
	for _, candidate := range observeLeases {
		if candidate.Task.ID == taskID {
			observeLease = candidate
		}
	}
	if observeLease.Token == "" {
		t.Fatal("observe round not claimed")
	}
	if _, err := h.repository.CommitDispatchCheckpoint(context.Background(), generation.DispatchCheckpointWrite{
		TaskID:          taskID,
		AttemptID:       attempt.ID,
		LeaseToken:      observeLease.Token,
		ExpectedVersion: observeLease.Task.Version,
		Now:             observeNow,
		Intent:          json.RawMessage(`{"stage":"fixture.observe-dispatch"}`),
		AckToken:        repeatToken("d"),
	}); err != nil {
		t.Fatal(err)
	}

	h.clock.Advance(61 * time.Second)
	h2 := h.restart(t, adapter)
	final := h2.runUntilTerminal(t, taskID)
	if final.State != generation.TaskFailed || final.Failure == nil || !final.Failure.Ambiguous {
		t.Fatalf("task = %s, failure = %#v; want ambiguous", final.State, final.Failure)
	}
	sends, checkpoints, rounds := adapter.snapshot()
	if sends != 0 || checkpoints != 1 {
		t.Fatalf("sends = %d, checkpoints = %d; want 0/1（pending 期检查点尝试被拒，零发送）", sends, checkpoints)
	}
	if rounds == 0 {
		t.Fatal("Observe 必须被调用一次以暴露 ErrDispatchPending")
	}
}

// TestDispatchOversizedIntentRejectedBeforeSend 意图超过调度器声明上限
// 时在发送前失败：0 次上游调用，任务失败且非歧义（协议违约）。
func TestDispatchOversizedIntentRejectedBeforeSend(t *testing.T) {
	adapter := &oversizedIntentAdapter{}
	root := t.TempDir()
	dbPath := filepath.Join(root, "dispatch.db")
	repository, err := sqlitetest.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	if err := registry.Register("tripo.v3", adapter); err != nil {
		t.Fatal(err)
	}
	localStore, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)}
	module, err := generation.New(generation.Dependencies{
		Repository:                 repository,
		Catalog:                    taskCatalog,
		Providers:                  registry,
		Assets:                     localStore,
		Clock:                      clock,
		Cache:                      &recordingCache{},
		DispatchCheckpointMaxBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	h := &harness{module: module, repository: repository, clock: clock}
	accepted, err := h.submitApprovedWithModule(t, context.Background(), module, generation.SubmitCommand{
		OwnerID:    "owner-dispatch",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"an oversized lantern","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 30 {
		if _, err := module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID:  "checkpoint-worker",
			LeaseFor:  time.Minute,
			BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(2 * time.Second)
		task, err := module.Get(context.Background(), "owner-dispatch", accepted.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			if task.State != generation.TaskFailed ||
				task.Failure == nil ||
				task.Failure.Class != generation.FailureProtocol {
				t.Fatalf("task = %s, failure = %#v; want protocol failure", task.State, task.Failure)
			}
			if sends := adapter.sendCount(); sends != 0 {
				t.Fatalf("sends = %d; want 0（超界在发送前失败）", sends)
			}
			return
		}
	}
	t.Fatal("task did not reach terminal state")
}

type oversizedIntentAdapter struct {
	httpSends atomic.Int32
}

func (a *oversizedIntentAdapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	checkpointer, ok := generation.DispatchCheckpointerFromContext(ctx)
	if !ok {
		return generation.Observation{}, errors.New("测试替身要求 Start 注入检查点")
	}
	intent := make([]byte, 128)
	for i := range intent {
		intent[i] = 'x'
	}
	if _, err := checkpointer.Checkpoint(ctx, generation.DispatchIntent{Bytes: intent}); err != nil {
		return generation.Observation{}, err
	}
	a.httpSends.Add(1)
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "oversized-1"},
		PollAfter: time.Millisecond,
	}, nil
}

func (a *oversizedIntentAdapter) Observe(
	_ context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:    generation.UpstreamCompleted,
		Job:      &request.Job,
		Progress: 100,
		Artifacts: []generation.ArtifactClaim{{
			ID:        "oversized-1:model",
			Role:      "model",
			Inline:    []byte("glTF"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (a *oversizedIntentAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func (a *oversizedIntentAdapter) sendCount() int {
	return int(a.httpSends.Load())
}

// ── 私密性：检查点 ack/sequence 不进入事件流（§8.1）──

// TestDispatchReceiptNeverEntersEventStream 断言全量事件数据中不出现
// 回执 token/序列字段；intent 字节也不进入事件。
func TestDispatchReceiptNeverEntersEventStream(t *testing.T) {
	adapter := &checkpointingAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "dispatch.db"))
	taskID := h.submit(t)
	if task := h.runUntilTerminal(t, taskID); task.State != generation.TaskSucceeded {
		t.Fatalf("task = %s", task.State)
	}
	events, err := h.module.Events(context.Background(), "owner-dispatch", taskID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		payload := string(event.Data)
		if strings.Contains(payload, `"ack_token"`) ||
			strings.Contains(payload, `"dispatch"`) ||
			strings.Contains(payload, `"sequence"`) {
			t.Fatalf("event %s 泄漏检查点回执/序列: %s", event.Type, payload)
		}
	}
}

// repeatToken 生成测试用 32 位十六进制形状的 ack token。
func repeatToken(character string) string {
	return strings.Repeat(character, 32)
}

// jsonQueryEquals 判断持久化检查点 JSON 的顶层字符串字段是否等于期望值。
func jsonQueryEquals(raw string, key string, want string) bool {
	if raw == "" {
		return false
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return false
	}
	value, ok := decoded[key].(string)
	return ok && value == want
}

// rateLimitedChildAdapter reproduces a provider whose paid private child is
// refused with an explicit HTTP 429: the permit is settled as not dispatched
// at a higher private stage, the provider asks to be observed again after
// Retry-After, and the next observation sends the same child under a new
// permit.
type rateLimitedChildAdapter struct {
	checkpointingAdapter
	rounds  int
	permits int
}

func (a *rateLimitedChildAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	a.rounds++
	base := generation.Observation{Phase: generation.UpstreamRunning, Job: &request.Job, PollAfter: time.Millisecond}
	switch a.rounds {
	case 1:
		base.Progress = 80
		base.DedupeKey = "source:80"
	case 2, 3:
		if a.rounds == 3 && !bytes.Contains(request.Opaque, []byte(`"rate_limited"`)) {
			return generation.Observation{}, errors.New("refused child state was not kept")
		}
		receipt, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{
			Bytes: json.RawMessage(`{"conversions":{"FBX":{"status":"dispatching"}}}`),
		})
		if err != nil {
			return generation.Observation{}, err
		}
		a.permits++
		base.Dispatch = receipt
		if a.rounds == 2 {
			receipt.Result = generation.DispatchResultNotDispatched
			base.Progress = 82
			base.PollAfter = 30 * time.Second
			base.Opaque = json.RawMessage(`{"conversions":{"FBX":{"status":"rate_limited","rate_limit_retries":1}}}`)
			base.DedupeKey = "fbx:refused:1"
		} else {
			base.Progress = 85
			base.Opaque = json.RawMessage(`{"conversions":{"FBX":{"status":"submitted","remote_id":"child","rate_limit_retries":1}}}`)
			base.DedupeKey = "fbx:submitted"
		}
	}
	return base, nil
}

func TestRefusedPrivateChildKeepsLedgerAndWaitsForRetryAfter(t *testing.T) {
	adapter := &rateLimitedChildAdapter{}
	h := newCheckpointHarness(t, adapter, filepath.Join(t.TempDir(), "refusal.db"))
	taskID := h.submit(t)
	for range 3 { // Start, source at 80%, then the refused child.
		h.reconcile(t)
		h.clock.Advance(6 * time.Second)
	}
	aggregate, err := h.repository.Load(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Task.State != generation.TaskRunning || aggregate.Task.Progress != 82 ||
		!bytes.Contains(aggregate.CurrentAttempt.Opaque, []byte(`"rate_limited"`)) {
		t.Fatalf("refused child lost: state=%s progress=%d opaque=%s", aggregate.Task.State, aggregate.Task.Progress, aggregate.CurrentAttempt.Opaque)
	}
	if state := readCheckpointColumn(t, h.dbPath, string(aggregate.CurrentAttempt.ID)); !jsonQueryEquals(state, "state", "idle") {
		t.Fatalf("a refused child must leave the checkpoint idle: %s", state)
	}
	if wait := aggregate.Task.NextActionAt.Sub(h.clock.Now().Add(-6 * time.Second)); wait != 30*time.Second {
		t.Fatalf("Retry-After not scheduled: next action in %v", wait)
	}
	h.reconcile(t) // 6 s after the refusal: still waiting.
	if adapter.rounds != 2 {
		t.Fatalf("observed before Retry-After: rounds %d", adapter.rounds)
	}
	h.clock.Advance(30 * time.Second)
	h.reconcile(t)
	aggregate, err = h.repository.Load(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.rounds != 3 || adapter.permits != 2 || aggregate.Task.Progress != 85 ||
		!bytes.Contains(aggregate.CurrentAttempt.Opaque, []byte(`"submitted"`)) {
		t.Fatalf("same child not resent under a new permit: rounds=%d permits=%d progress=%d opaque=%s", adapter.rounds, adapter.permits, aggregate.Task.Progress, aggregate.CurrentAttempt.Opaque)
	}
	if state := readCheckpointColumn(t, h.dbPath, string(aggregate.CurrentAttempt.ID)); !jsonQueryEquals(state, "state", "received") {
		t.Fatalf("checkpoint state = %s, want received", state)
	}
}

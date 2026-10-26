package tripo_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// recordingCheckpointer 记录调用，模拟内核检查点回调。
type recordingCheckpointer struct {
	mu     sync.Mutex
	calls  int
	intent []byte
	pended bool
}

func (c *recordingCheckpointer) Checkpoint(
	_ context.Context,
	intent generation.DispatchIntent,
) (generation.DispatchAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.intent = append([]byte(nil), intent.Bytes...)
	if c.pended {
		return generation.DispatchAck{}, errors.Join(
			errors.New("attempt 存在未回执的私有派发检查点"),
			generation.ErrDispatchPending,
		)
	}
	return generation.DispatchAck{
		Sequence: 3,
		AckToken: "ack-tripo-000000000000000000000000001",
		Opaque:   intent.Bytes,
	}, nil
}

func (c *recordingCheckpointer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// countingCreateUpstream 统计付费创建端点命中数。
type countingCreateUpstream struct {
	server *httptest.Server
	hits   atomic.Int32
}

func newCountingCreateUpstream(t *testing.T) *countingCreateUpstream {
	t.Helper()
	upstream := &countingCreateUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/generation/text-to-model" {
				upstream.hits.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    0,
					"message": "success",
					"data":    map[string]string{"task_id": "dispatch-task-1"},
				})
				return
			}
			http.NotFound(w, r)
		},
	))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func dispatchCheckpointTestOffering() generation.Offering {
	return generation.Offering{
		Capability: generation.TextTo3D,
		ProviderOptions: []byte(
			`{"model":"v3.1","endpoint":"generation/text-to-model","observe_endpoint":"tasks/{task_id}","upload_endpoint":"files"}`,
		),
	}
}

// TestStartCheckpointsBeforePaidSubmit 锁定真实消费者契约：付费创建 POST
// 前必须先取得检查点许可（顺序由计数器证明：HTTP 命中时检查点已调用），
// 回执随 Observation 返回且与 ack 匹配。
func TestStartCheckpointsBeforePaidSubmit(t *testing.T) {
	checkpointer := &recordingCheckpointer{}
	upstream := newCountingCreateUpstream(t)
	adapter := newAdapter(t, upstream.server)

	observation, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		generation.StartRequest{
			AttemptID:     "att-dispatch",
			SubmissionKey: "sub-dispatch",
			Offering:      dispatchCheckpointTestOffering(),
			Input:         []byte(`{"prompt":"a fenced lighthouse"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls := checkpointer.count(); calls != 1 {
		t.Fatalf("checkpoint calls = %d; want 1", calls)
	}
	if hits := upstream.hits.Load(); hits != 1 {
		t.Fatalf("create endpoint hits = %d; want 1（恰好一次付费提交）", hits)
	}
	if observation.Dispatch == nil ||
		observation.Dispatch.Sequence != 3 ||
		observation.Dispatch.AckToken != "ack-tripo-000000000000000000000000001" ||
		observation.Dispatch.Result != generation.DispatchResultDispatched {
		t.Fatalf("dispatch receipt = %#v", observation.Dispatch)
	}
	// intent 必须自含协调标识与阶段标记（§4.5.2），不依赖进程内存。
	var intent struct {
		Stage         string `json:"stage"`
		SubmissionKey string `json:"submission_key"`
	}
	if err := json.Unmarshal(checkpointer.intent, &intent); err != nil {
		t.Fatal(err)
	}
	if intent.Stage != "task.create" || intent.SubmissionKey != "sub-dispatch" {
		t.Fatalf("intent = %s（必须自含 stage 与 submission_key）", checkpointer.intent)
	}
}

// TestStartPendingCheckpointYieldsZeroSubmits 存在未回执检查点时：
// 0 次上游付费调用，错误可经 errors.Is 判定为 ErrDispatchPending。
func TestStartPendingCheckpointYieldsZeroSubmits(t *testing.T) {
	checkpointer := &recordingCheckpointer{pended: true}
	upstream := newCountingCreateUpstream(t)
	adapter := newAdapter(t, upstream.server)

	_, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		generation.StartRequest{
			SubmissionKey: "sub-pending",
			Offering:      dispatchCheckpointTestOffering(),
			Input:         []byte(`{"prompt":"pending fence"}`),
		},
	)
	if err == nil {
		t.Fatal("ErrDispatchPending 必须使 Start 失败")
	}
	if !errors.Is(err, generation.ErrDispatchPending) {
		t.Fatalf("err = %v; want ErrDispatchPending 可判定", err)
	}
	if hits := upstream.hits.Load(); hits != 0 {
		t.Fatalf("create endpoint hits = %d; want 0（禁止发送）", hits)
	}
}

// TestStartWithoutCheckpointerKeepsDirectPath 无检查点回调（旧调用面/
// 单测直连）时保持既有行为：正常提交、无回执。
func TestStartWithoutCheckpointerKeepsDirectPath(t *testing.T) {
	upstream := newCountingCreateUpstream(t)
	adapter := newAdapter(t, upstream.server)

	observation, err := adapter.Start(
		context.Background(),
		generation.StartRequest{
			SubmissionKey: "sub-direct",
			Offering:      dispatchCheckpointTestOffering(),
			Input:         []byte(`{"prompt":"direct compatibility"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Dispatch != nil {
		t.Fatalf("直连路径不得携带回执: %#v", observation.Dispatch)
	}
	if hits := upstream.hits.Load(); hits != 1 {
		t.Fatalf("create endpoint hits = %d; want 1", hits)
	}
}

package matting_test

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

type mattingRecordingCheckpointer struct {
	mu      sync.Mutex
	calls   int
	pending bool
}

func (c *mattingRecordingCheckpointer) Checkpoint(
	_ context.Context,
	intent generation.DispatchIntent,
) (generation.DispatchAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.pending {
		return generation.DispatchAck{}, errors.Join(
			errors.New("attempt 存在未回执的私有派发检查点"),
			generation.ErrDispatchPending,
		)
	}
	return generation.DispatchAck{
		Sequence: 2,
		AckToken: "ack-matting-0000000000000000000000001",
		Opaque:   intent.Bytes,
	}, nil
}

func (c *mattingRecordingCheckpointer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newMattingCountingUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/jobs" && r.Method == http.MethodPost {
				hits.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]string{"batch_id": "batch-dispatch-1"})
				return
			}
			http.NotFound(w, r)
		},
	))
	t.Cleanup(server.Close)
	t.Cleanup(func() { mattingCreateHits = &hits })
	mattingCreateHits = &hits
	return server
}

var mattingCreateHits *atomic.Int32

func mattingDispatchStartRequest(submissionKey string) generation.StartRequest {
	return generation.StartRequest{
		AttemptID:     "att-matting-dispatch",
		SubmissionKey: submissionKey,
		Offering:      defaultOffering(),
		Input: []byte(
			`{"source_assets":["asset-1"]}`,
		),
		InputAssets: map[string][]generation.InputAsset{
			"source_assets": {{
				ID:        "asset-1",
				MediaType: "image/png",
			}},
		},
	}
}

// TestStartCheckpointsBeforePaidSubmit 付费批处理创建前取得许可，回执随
// Observation 返回；恰好一次付费 POST。
func TestStartCheckpointsBeforePaidSubmit(t *testing.T) {
	checkpointer := &mattingRecordingCheckpointer{}
	upstream := newMattingCountingUpstream(t)
	adapter := newAdapter(t, upstream, memoryInputs{content: []byte("input-image")})

	observation, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		mattingDispatchStartRequest("sub-matting-dispatch"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls := checkpointer.count(); calls != 1 {
		t.Fatalf("checkpoint calls = %d; want 1", calls)
	}
	if hits := mattingCreateHits.Load(); hits != 1 {
		t.Fatalf("create endpoint hits = %d; want 1（multipart 本地完成后恰好一次提交）", hits)
	}
	if observation.Dispatch == nil ||
		observation.Dispatch.Sequence != 2 ||
		observation.Dispatch.AckToken != "ack-matting-0000000000000000000000001" ||
		observation.Dispatch.Result != generation.DispatchResultDispatched {
		t.Fatalf("dispatch receipt = %#v", observation.Dispatch)
	}
}

// TestStartPendingCheckpointYieldsZeroSubmits 未回执检查点：0 次付费调用。
func TestStartPendingCheckpointYieldsZeroSubmits(t *testing.T) {
	checkpointer := &mattingRecordingCheckpointer{pending: true}
	upstream := newMattingCountingUpstream(t)
	adapter := newAdapter(t, upstream, memoryInputs{content: []byte("input-image")})

	_, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		mattingDispatchStartRequest("sub-matting-pending"),
	)
	if !errors.Is(err, generation.ErrDispatchPending) {
		t.Fatalf("err = %v; want ErrDispatchPending", err)
	}
	if hits := mattingCreateHits.Load(); hits != 0 {
		t.Fatalf("create endpoint hits = %d; want 0（禁止发送）", hits)
	}
}

// TestStartWithoutCheckpointerKeepsDirectPath 直连兼容：无回执、正常提交。
func TestStartWithoutCheckpointerKeepsDirectPath(t *testing.T) {
	upstream := newMattingCountingUpstream(t)
	adapter := newAdapter(t, upstream, memoryInputs{content: []byte("input-image")})

	observation, err := adapter.Start(
		context.Background(),
		mattingDispatchStartRequest("sub-matting-direct"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Dispatch != nil {
		t.Fatalf("直连路径不得携带回执: %#v", observation.Dispatch)
	}
	if hits := mattingCreateHits.Load(); hits != 1 {
		t.Fatalf("create endpoint hits = %d; want 1", hits)
	}
}

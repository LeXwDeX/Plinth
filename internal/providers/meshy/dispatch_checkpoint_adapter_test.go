package meshy_test

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
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

type meshyRecordingCheckpointer struct {
	mu      sync.Mutex
	calls   int
	pending bool
}

func (c *meshyRecordingCheckpointer) Checkpoint(
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
		Sequence: 1,
		AckToken: "ack-meshy-000000000000000000000000001",
		Opaque:   intent.Bytes,
	}, nil
}

func (c *meshyRecordingCheckpointer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newMeshyCountingUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/openapi/v2/text-to-3d" && r.Method == http.MethodPost {
				hits.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]string{"result": "meshy-dispatch-1"})
				return
			}
			http.NotFound(w, r)
		},
	))
	t.Cleanup(server.Close)
	t.Cleanup(func() { meshyCreateHits = &hits })
	meshyCreateHits = &hits
	return server
}

var meshyCreateHits *atomic.Int32

func meshyDispatchOffering() generation.Offering {
	return generation.Offering{
		ProviderID:   "meshy",
		ModelID:      "meshy-test",
		Capability:   generation.TextTo3D,
		PollInterval: 0,
		ProviderOptions: json.RawMessage(
			`{"endpoint":"text-to-3d","api_version":"v2","model_parameter":"ai_model"}`,
		),
	}
}

// TestStartCheckpointsBeforePaidSubmit 付费创建前取得许可，回执随
// Observation 返回；恰好一次付费 POST。
func TestStartCheckpointsBeforePaidSubmit(t *testing.T) {
	checkpointer := &meshyRecordingCheckpointer{}
	upstream := newMeshyCountingUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})

	observation, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		generation.StartRequest{
			AttemptID:     "att-meshy-dispatch",
			SubmissionKey: "sub-meshy-dispatch",
			Offering:      meshyDispatchOffering(),
			Input:         []byte(`{"prompt":"a fenced greenhouse"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls := checkpointer.count(); calls != 1 {
		t.Fatalf("checkpoint calls = %d; want 1", calls)
	}
	if hits := meshyCreateHits.Load(); hits != 1 {
		t.Fatalf("create endpoint hits = %d; want 1", hits)
	}
	if observation.Dispatch == nil ||
		observation.Dispatch.Sequence != 1 ||
		observation.Dispatch.AckToken != "ack-meshy-000000000000000000000000001" ||
		observation.Dispatch.Result != generation.DispatchResultDispatched {
		t.Fatalf("dispatch receipt = %#v", observation.Dispatch)
	}
}

// TestStartPendingCheckpointYieldsZeroSubmits 未回执检查点：0 次付费调用。
func TestStartPendingCheckpointYieldsZeroSubmits(t *testing.T) {
	checkpointer := &meshyRecordingCheckpointer{pending: true}
	upstream := newMeshyCountingUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})

	_, err := adapter.Start(
		generation.WithDispatchCheckpointer(context.Background(), checkpointer),
		generation.StartRequest{
			SubmissionKey: "sub-meshy-pending",
			Offering:      meshyDispatchOffering(),
			Input:         []byte(`{"prompt":"pending fence"}`),
		},
	)
	if !errors.Is(err, generation.ErrDispatchPending) {
		t.Fatalf("err = %v; want ErrDispatchPending", err)
	}
	if hits := meshyCreateHits.Load(); hits != 0 {
		t.Fatalf("create endpoint hits = %d; want 0（禁止发送）", hits)
	}
}

// TestStartWithoutCheckpointerKeepsDirectPath 直连兼容：无回执、正常提交。
func TestStartWithoutCheckpointerKeepsDirectPath(t *testing.T) {
	upstream := newMeshyCountingUpstream(t)
	adapter := newMeshyAdapter(t, meshy.Config{
		BaseURL:    upstream.URL,
		APIKey:     "meshy-key",
		HTTPClient: upstream.Client(),
	})

	observation, err := adapter.Start(
		context.Background(),
		generation.StartRequest{
			SubmissionKey: "sub-meshy-direct",
			Offering:      meshyDispatchOffering(),
			Input:         []byte(`{"prompt":"direct compatibility"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Dispatch != nil {
		t.Fatalf("直连路径不得携带回执: %#v", observation.Dispatch)
	}
	if hits := meshyCreateHits.Load(); hits != 1 {
		t.Fatalf("create endpoint hits = %d; want 1", hits)
	}
}

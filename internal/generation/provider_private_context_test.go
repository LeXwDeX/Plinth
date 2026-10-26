package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

var (
	childCheckpointsRunning = json.RawMessage(
		`[{"identifier":"subtask-1","status":"RUNNING"},{"identifier":"subtask-2","status":"RUNNING"}]`,
	)
	childCheckpointsPartial = json.RawMessage(
		`[{"identifier":"subtask-1","status":"SUCCEEDED"},{"identifier":"subtask-2","status":"RUNNING"}]`,
	)
	childCheckpointsCompleted = json.RawMessage(
		`[{"identifier":"subtask-1","status":"SUCCEEDED"},{"identifier":"subtask-2","status":"FAILED"}]`,
	)
)

// 特征化当前内核边界：Provider 私有子任务的记录只作为不透明检查点（Opaque）
// 在同一个公开 Task/Attempt 内往返。内核不解析其内容、不为子任务创建新的
// Task/Attempt，子任务记录中的失败也不会改变已满足必需产物的父任务结果。
func TestProviderPrivateChildRecordsStayOpaqueInOnePublicTask(t *testing.T) {
	adapter := &childContextAdapter{}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"a small observatory","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded || task.Failure != nil {
		t.Fatalf("task = %#v", task)
	}
	if len(task.Assets) != 7 {
		t.Fatalf("assets = %#v", task.Assets)
	}
	variants := map[string]bool{}
	for _, asset := range task.Assets {
		if !asset.Required {
			t.Fatalf("optional asset in full-format task: %#v", asset)
		}
		variants[asset.Variant] = true
	}
	for _, variant := range []string{"converted.gltf", "converted.usdz", "converted.fbx", "converted.obj", "converted.stl", "converted.3mf"} {
		if !variants[variant] {
			t.Fatalf("missing %s in %#v", variant, task.Assets)
		}
	}

	page, err := harness.module.List(
		context.Background(),
		generation.OwnTasks("owner-a"),
		generation.ListQuery{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("public task count = %d, want 1", page.Total)
	}
	events, err := harness.module.Events(
		context.Background(),
		"owner-a",
		accepted.TaskID,
		0,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if countEvents(events, "attempt.started") != 1 {
		t.Fatalf("events = %#v", eventTypes(events))
	}

	received := adapter.ObserveOpaques()
	if len(received) != 2 {
		t.Fatalf("observe calls = %d, want 2", len(received))
	}
	if !bytes.Equal(received[0], childCheckpointsRunning) {
		t.Fatalf("first observe checkpoint = %s", received[0])
	}
	if !bytes.Equal(received[1], childCheckpointsPartial) {
		t.Fatalf("second observe checkpoint = %s", received[1])
	}

	stored, err := harness.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.ProviderData, childCheckpointsCompleted) {
		t.Fatalf("public provider data = %s", stored.ProviderData)
	}
	aggregate, err := harness.repository.Load(context.Background(), accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.CurrentAttempt == nil ||
		!bytes.Equal(aggregate.CurrentAttempt.Opaque, childCheckpointsCompleted) {
		t.Fatalf("stored attempt = %#v", aggregate.CurrentAttempt)
	}
}

type childContextAdapter struct {
	mu       sync.Mutex
	received []json.RawMessage
}

func (a *childContextAdapter) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       &generation.UpstreamJob{ProviderID: "tripo", RemoteID: "batch-1"},
		Progress:  1,
		PollAfter: time.Millisecond,
		DedupeKey: "batch-1:accepted",
		Opaque:    childCheckpointsRunning,
	}, nil
}

func (a *childContextAdapter) Observe(
	_ context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	a.received = append(a.received, append(json.RawMessage(nil), request.Opaque...))
	call := len(a.received)
	a.mu.Unlock()
	switch call {
	case 1:
		return generation.Observation{
			Phase:     generation.UpstreamRunning,
			Job:       &request.Job,
			Progress:  50,
			PollAfter: time.Millisecond,
			DedupeKey: "batch-1:running",
			Opaque:    childCheckpointsPartial,
		}, nil
	case 2:
		return generation.Observation{
			Phase:     generation.UpstreamCompleted,
			Job:       &request.Job,
			Progress:  100,
			DedupeKey: "batch-1:completed",
			Artifacts: []generation.ArtifactClaim{{
				ID:        "batch-1:model",
				Role:      "model",
				Variant:   "converted.gltf",
				Inline:    validGLB("private child fixture"),
				MediaType: "model/gltf-binary",
			}},
			Opaque: childCheckpointsCompleted,
		}, nil
	default:
		return generation.Observation{}, errors.New("unexpected Observe call")
	}
}

func (a *childContextAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	body := request.Claim.Inline
	return io.NopCloser(bytes.NewReader(body)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(body)),
	}, nil
}

func (a *childContextAdapter) ObserveOpaques() []json.RawMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]json.RawMessage(nil), a.received...)
}

package generation_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func TestTaskLifecycleKeepsStableIdentityAndMaterializesAsset(t *testing.T) {
	harness := newHarness(t, mock.New(), nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:        "owner-a",
		Capability:     generation.TextToImage,
		OfferingID:     "openai/gpt-image-2/text-to-image",
		Input:          []byte(`{"prompt":"a brass robot"}`),
		IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		detail := ""
		if task.Failure != nil {
			detail = task.Failure.SafeDetail
		}
		events, _ := harness.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
		t.Fatalf("state = %s, failure = %q events=%#v", task.State, detail, events)
	}
	if len(task.Assets) != 1 || task.Assets[0].SHA256 == "" {
		t.Fatalf("assets = %#v", task.Assets)
	}
	events, err := harness.module.Events(
		context.Background(),
		"owner-a",
		accepted.TaskID,
		0,
		100,
	)
	if err != nil {
		t.Fatalf("%v: cause=%v", err, errors.Unwrap(err))
	}
	if !hasEvent(events, "provider.start") || !hasEvent(events, "task.succeeded") {
		t.Fatalf("events = %#v", eventTypes(events))
	}
	aggregate, loadErr := harness.repository.Load(context.Background(), accepted.TaskID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if aggregate.CurrentAttempt == nil || aggregate.CurrentAttempt.UpstreamJob == nil {
		t.Fatal("missing upstream job")
	}
	if generation.TaskID(aggregate.CurrentAttempt.UpstreamJob.RemoteID) == accepted.TaskID {
		t.Fatal("upstream job ID replaced stable local Task ID")
	}
}

func TestMaterializationReportsCacheActivityWithTaskMetadata(t *testing.T) {
	harness := newHarness(t, mock.New(), nil)
	accepted, err := harness.submitApproved(t,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:        "owner-cache",
			Capability:     generation.TextToImage,
			OfferingID:     "openai/gpt-image-2/text-to-image",
			Input:          []byte(`{"prompt":"cache activity"}`),
			IdempotencyKey: "cache-activity-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-cache", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %#v", task)
	}
	operations, finished := harness.cache.snapshot()
	if len(operations) != 1 || finished != 1 {
		t.Fatalf("operations = %#v, finished = %d", operations, finished)
	}
	operation := operations[0]
	if operation.TaskID != string(task.ID) ||
		operation.TaskType != string(task.Capability) ||
		operation.ProviderID != task.Offering.ProviderID ||
		operation.AssetKind != task.Assets[0].Role {
		t.Fatalf("cache operation = %#v", operation)
	}
}

func TestRepeatedProviderObservationIsDeduplicatedWithoutStoppingPolling(t *testing.T) {
	adapter := &dedupeObservationAdapter{}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:        "owner-a",
		Capability:     generation.TextTo3D,
		OfferingID:     "meshy/meshy-7.1/text-to-3d",
		Input:          []byte(`{"prompt":"a small observatory"}`),
		IdempotencyKey: "observation-dedupe-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		detail := ""
		if task.Failure != nil {
			detail = task.Failure.SafeDetail
		}
		t.Fatalf("state = %s, failure = %q", task.State, detail)
	}
	if adapter.ObservationCount() != 3 {
		t.Fatalf("observation count = %d", adapter.ObservationCount())
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
	if countEvents(events, "provider.observation_deduplicated") != 1 {
		t.Fatalf("events = %#v", eventTypes(events))
	}
	if countEvents(events, "provider.observe") != 2 {
		t.Fatalf("provider.observe events = %d", countEvents(events, "provider.observe"))
	}
}

func TestSubmitIdempotencyReplaysSameTaskAndRejectsDifferentRequest(t *testing.T) {
	harness := newHarness(t, mock.New(), nil)
	command := generation.SubmitCommand{
		OwnerID:        "owner-a",
		Capability:     generation.TextToImage,
		OfferingID:     "openai/gpt-image-2/text-to-image",
		Input:          []byte(`{"prompt":"same"}`),
		IdempotencyKey: "request-2",
	}
	first, err := harness.submitApproved(t, context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := harness.submitApproved(t, context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.TaskID != first.TaskID {
		t.Fatalf("first = %#v, second = %#v", first, second)
	}
	command.Input = []byte(`{"prompt":"different"}`)
	_, err = harness.submitApproved(t, context.Background(), command)
	if !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("error = %#v", err)
	}
}

func TestSubmitAdmissionReplaysBeforeFullLimit(t *testing.T) {
	harness := newHarness(t, mock.New(), nil, 1)
	command := generation.SubmitCommand{
		OwnerID:        "owner-a",
		Capability:     generation.TextToImage,
		OfferingID:     "openai/gpt-image-2/text-to-image",
		Input:          []byte(`{"prompt":"first"}`),
		IdempotencyKey: "first-key",
	}
	first, err := harness.submitApproved(t, context.Background(), command)
	if err != nil || first.Replayed {
		t.Fatalf("first submit = %+v, %v", first, err)
	}
	replay, err := harness.submitApproved(t, context.Background(), command)
	if err != nil || !replay.Replayed || replay.TaskID != first.TaskID {
		t.Fatalf("full-capacity replay = %+v, %v", replay, err)
	}
	command.Input = []byte(`{"prompt":"different"}`)
	if _, err := harness.submitApproved(t, context.Background(), command); !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("full-capacity key conflict = %v", err)
	}
	command.IdempotencyKey = "second-key"
	if _, err := harness.submitApproved(t, context.Background(), command); !generation.IsCode(err, generation.ErrTooManyRequests) {
		t.Fatalf("new task at capacity = %v", err)
	}
	command.OwnerID = "owner-b"
	if _, err := harness.submitApproved(t, context.Background(), command); err != nil {
		t.Fatalf("independent owner submit: %v", err)
	}
	events, err := harness.module.Events(context.Background(), "owner-a", first.TaskID, 0, 10)
	if err != nil || countEvents(events, "task.submitted") != 1 {
		t.Fatalf("replayed task events = %+v, %v", events, err)
	}
}

func TestDefiniteRetryCreatesNewAttempt(t *testing.T) {
	adapter := &failOnceAdapter{next: mock.New()}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		detail := ""
		if task.Failure != nil {
			detail = task.Failure.SafeDetail
		}
		events, _ := harness.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
		t.Fatalf("state = %s, failure = %q events=%#v", task.State, detail, events)
	}
	events, err := harness.module.Events(
		context.Background(),
		"owner-a",
		accepted.TaskID,
		0,
		100,
	)
	if err != nil {
		t.Fatalf("%v: cause=%v", err, errors.Unwrap(err))
	}
	if countEvents(events, "attempt.started") != 2 {
		t.Fatalf("events = %#v", eventTypes(events))
	}
}

func TestAmbiguousSubmissionNeverAutomaticallyResubmits(t *testing.T) {
	harness := newHarness(t, ambiguousAdapter{}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("task = %#v", task)
	}
	events, err := harness.module.Events(
		context.Background(),
		"owner-a",
		accepted.TaskID,
		0,
		100,
	)
	if err != nil {
		t.Fatalf("%v: cause=%v", err, errors.Unwrap(err))
	}
	if countEvents(events, "attempt.started") != 1 ||
		!hasEvent(events, "attempt.submission_unknown") {
		t.Fatalf("events = %#v", eventTypes(events))
	}
}

// 上游取证字段（状态码/错误码/响应摘录）必须在 submission_unknown 归一化后保留：
// SafeDetail 允许被替换为面向用户的通用文案，取证字段只落库供诊断。
func TestSubmissionUnknownPreservesUpstreamEvidence(t *testing.T) {
	harness := newHarness(t, evidenceAdapter{}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil {
		t.Fatalf("task = %#v", task)
	}
	failure := task.Failure
	if failure.Class != generation.FailureSubmissionUnknown {
		t.Fatalf("class = %s", failure.Class)
	}
	if failure.UpstreamStatus != 500 ||
		failure.UpstreamCode != "ModelServiceException" ||
		!strings.Contains(failure.UpstreamExcerpt, "Fail to invoke pose estimation") {
		t.Fatalf("upstream evidence lost: %#v", failure)
	}
	if failure.SafeDetail == "" || strings.Contains(failure.SafeDetail, "pose estimation") {
		t.Fatalf("user-facing detail leaked upstream body: %q", failure.SafeDetail)
	}
}

type evidenceAdapter struct{}

func (evidenceAdapter) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{}, &generation.ProviderError{
		Failure: &generation.Failure{
			Class:           generation.FailureSubmissionUnknown,
			Ambiguous:       true,
			SafeDetail:      "上游返回错误，无法确认是否已接受提交，已停止自动重发",
			UpstreamStatus:  500,
			UpstreamCode:    "ModelServiceException",
			UpstreamExcerpt: `{"message":"Fail to invoke pose estimation"}`,
		},
	}
}

func (evidenceAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unreachable")
}

func (evidenceAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("unreachable")
}

func TestMaterializationRetryDoesNotCreateAnotherBillableAttempt(t *testing.T) {
	harness := newHarness(t, completeAdapter{}, failingAssetStore{})
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.reconcile(t)
	harness.reconcile(t)
	task, err := harness.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskMaterializing ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureIntegrity {
		t.Fatalf("task = %#v", task)
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
	if countEvents(events, "attempt.started") != 1 ||
		!hasEvent(events, "materialization.retry_scheduled") {
		t.Fatalf("events = %#v", eventTypes(events))
	}
}

func TestRequiredMultiClaimDownloadFailureRetriesBeforeTaskSuccess(t *testing.T) {
	adapter := &requiredMultiClaimRetryAdapter{failVariant: ":second"}
	assetStore, err := local.New(filepath.Join(t.TempDir(), "multi-claim-assets"))
	if err != nil {
		t.Fatal(err)
	}
	harness := newHarness(t, adapter, assetStore)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"two distinct required claims","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.reconcile(t)
	harness.reconcile(t)
	task, err := harness.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskMaterializing {
		t.Fatalf("a failed required claim download must keep the task materializing: %#v", task)
	}
	if adapter.downloadCount(":first") == 0 || adapter.downloadCount(":second") == 0 {
		t.Fatal("the fixture must complete one required download before another required claim fails")
	}
	task = harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		detail := ""
		if task.Failure != nil {
			detail = task.Failure.SafeDetail
		}
		t.Fatalf("task after required-claim retry state=%s detail=%q downloads(first=%d second=%d)", task.State, detail, adapter.downloadCount(":first"), adapter.downloadCount(":second"))
	}
	events, err := harness.module.Events(context.Background(), "owner-a", accepted.TaskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if task.MaterializeFailures != 1 || countEvents(events, "materialization.artifact_skipped") != 1 ||
		countEvents(events, "attempt.started") != 1 || countEvents(events, "task.submitted") != 1 {
		t.Fatalf("successful retry lost its failure evidence or identity: failures=%d events=%v", task.MaterializeFailures, eventTypes(events))
	}
	readableBytes := map[string]bool{}
	for _, asset := range task.Assets {
		if asset.Role != "model" {
			continue
		}
		body, openErr := harness.assets.Open(context.Background(), asset)
		if openErr != nil {
			t.Fatalf("required model claim is not locally readable: %v", openErr)
		}
		content, readErr := io.ReadAll(body)
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || len(content) == 0 {
			t.Fatalf("required model claim failed local read: read=%v close=%v bytes=%q", readErr, closeErr, content)
		}
		readableBytes[string(content)] = true
	}
	if !readableBytes[string(validGLB("first"))] || !readableBytes[string(validGLB("second"))] {
		t.Fatalf("both distinct required claims must be locally readable: %v", readableBytes)
	}
	for _, asset := range task.Assets {
		if asset.Role == "thumbnail" {
			t.Fatal("an omitted optional thumbnail must not be fabricated")
		}
	}
}

func TestMaterializationRefreshesClaimsWithoutNewBillableAttempt(t *testing.T) {
	adapter := &refreshingClaimsAdapter{}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded || adapter.observations != 1 {
		t.Fatalf("task = %#v, observations = %d", task, adapter.observations)
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
	if countEvents(events, "attempt.started") != 1 ||
		!hasEvent(events, "materialization.claims_refreshed") {
		t.Fatalf("events = %#v", eventTypes(events))
	}
}

func TestOptionalArtifactFailureDoesNotBlockRequiredTaskSuccess(t *testing.T) {
	harness := newHarness(t, completeWithBrokenOptional{}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Assets) != 6 {
		t.Fatalf("task = %#v", task)
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
	if !hasEvent(events, "materialization.artifact_skipped") {
		t.Fatalf("events = %#v", eventTypes(events))
	}
}

func TestCancelWinsOverStaleProviderResponseWithoutStoppingCoordinator(t *testing.T) {
	adapter := newBlockingStartAdapter()
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileResult := make(chan error, 1)
	go func() {
		_, reconcileErr := harness.module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "blocking-worker",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		)
		reconcileResult <- reconcileErr
	}()
	<-adapter.started
	canceled, err := harness.module.Cancel(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	close(adapter.release)
	if err := <-reconcileResult; err != nil {
		t.Fatalf("coordinator stopped on normal race: %v", err)
	}
	stored, err := harness.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.State != generation.TaskCanceled || stored.State != generation.TaskCanceled {
		t.Fatalf("canceled = %s, stored = %s", canceled.State, stored.State)
	}
}

func TestDeleteUsesSchemaPolicyAndStableUpstreamJobIdentity(t *testing.T) {
	adapter := &deletableAdapter{}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      json.RawMessage(`{"prompt":"temporary model"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.reconcile(t)

	err = harness.module.Delete(context.Background(), generation.DeleteCommand{
		Scope:              generation.OwnTasks("owner-a"),
		ActorID:            "owner-a",
		TaskID:             accepted.TaskID,
		ExpectedCapability: generation.TextTo3D,
		Policy: generation.TaskDeletionPolicy{
			LocalRecordPolicy:     "tombstone",
			UpstreamPolicy:        "when-supported",
			UpstreamFailurePolicy: "abort",
			GeneratedAssetPolicy:  "retain",
			AuditEventType:        "task.deleted",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.deleted == nil ||
		adapter.deleted.Job.RemoteID != "remote-delete-me" ||
		adapter.deleted.Job.ProviderID != "meshy" ||
		adapter.deleted.TaskID != accepted.TaskID {
		t.Fatalf("delete request = %#v", adapter.deleted)
	}
	if _, err := harness.module.Get(
		context.Background(),
		"owner-a",
		accepted.TaskID,
	); !generation.IsCode(err, generation.ErrNotFound) {
		t.Fatalf("get error = %#v", err)
	}
}

func TestOutputMinimumDoesNotMakeEveryVariantMandatory(t *testing.T) {
	harness := newHarness(t, completeWithBrokenSurplusModel{}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"castle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded ||
		len(task.Assets) != 6 ||
		!task.Assets[0].Required {
		t.Fatalf("task = %#v", task)
	}
}

// 回归：部分 CDN（如 Tripo）对所有产物统一返回泛型 octet-stream，
// 不携带类型信息；此时必须保留 Adapter 按协议声明的 claim 类型，
// 否则缩略图丢失 image/* 前缀导致任务历史无预览图。
func TestGenericResponseMediaTypeKeepsClaimDeclaredType(t *testing.T) {
	harness := newHarness(t, genericMediaTypeAdapter{}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"手指","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Assets) != 8 {
		t.Fatalf("task = %#v", task)
	}
	mediaTypes := map[string]string{}
	for _, asset := range task.Assets {
		mediaTypes[asset.Role] = asset.MediaType
	}
	if mediaTypes["model"] != "model/gltf-binary" ||
		mediaTypes["thumbnail"] != "image/webp" {
		t.Fatalf("asset media types = %#v", mediaTypes)
	}
}

func TestActualResponseMediaTypeMustStillSatisfyOutputContract(t *testing.T) {
	harness := newHarness(t, misleadingMediaTypeAdapter{}, nil)
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      []byte(`{"prompt":"bird"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed ||
		task.Failure == nil ||
		task.Failure.Class != generation.FailureIntegrity ||
		len(task.Assets) != 0 {
		t.Fatalf("task = %#v", task)
	}
}

func TestParameterizedResponseWithInvalidImageCannotCompleteTask(t *testing.T) {
	var writes []string
	harness := newConfiguredHarness(t, parameterizedInvalidImageAdapter{}, nil, func(deps *generation.Dependencies) {
		deps.Assets = captureMediaTypeStore{AssetStore: deps.Assets, writes: &writes}
	})
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image", Input: []byte(`{"prompt":"bird"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureIntegrity || len(task.Assets) != 0 {
		t.Fatalf("invalid parameterized response completed: %+v", task)
	}
	if len(writes) == 0 || writes[0] != "image/png" {
		t.Fatalf("response MIME not normalized before Put: %v", writes)
	}
}

type captureMediaTypeStore struct {
	generation.AssetStore
	writes *[]string
}

func (s captureMediaTypeStore) Put(ctx context.Context, write generation.AssetWrite) (generation.Asset, error) {
	*s.writes = append(*s.writes, write.MediaType)
	return s.AssetStore.Put(ctx, write)
}

func (s captureMediaTypeStore) Close() error {
	return s.AssetStore.(io.Closer).Close()
}

type parameterizedInvalidImageAdapter struct{}

func (parameterizedInvalidImageAdapter) Start(_ context.Context, request generation.StartRequest) (generation.Observation, error) {
	return generation.Observation{Phase: generation.UpstreamCompleted, Artifacts: []generation.ArtifactClaim{{
		ID: string(request.AttemptID) + ":image", Role: "image", Locator: "fixture-image", MediaType: "image/png",
	}}}, nil
}

func (parameterizedInvalidImageAdapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (parameterizedInvalidImageAdapter) OpenAsset(context.Context, generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(strings.NewReader("not a png")), generation.AssetMetadata{MediaType: "image/png; charset=utf-8"}, nil
}

func TestTaskReferenceResolvesOwnedSuccessfulUpstreamJob(t *testing.T) {
	adapter := &taskDependencyAdapter{}
	harness := newHarness(t, adapter, nil)
	source, err := harness.inputs.Upload(
		context.Background(),
		"owner-a",
		bytes.NewReader([]byte("glTF source")),
		"model/gltf-binary",
		1<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	rigging, err := harness.submitApproved(t,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:    "owner-a",
			Capability: generation.Rigging,
			OfferingID: "meshy/processing/rigging",
			Input: []byte(
				`{"source_asset":"` + string(source.ID) + `"}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	riggingTask := harness.runUntilTerminal(t, "owner-a", rigging.TaskID)
	if riggingTask.State != generation.TaskSucceeded {
		detail := ""
		if riggingTask.Failure != nil {
			detail = riggingTask.Failure.SafeDetail
			detail += " " + riggingTask.Failure.UpstreamCode + " " + riggingTask.Failure.UpstreamExcerpt
		}
		t.Fatalf("rigging task state=%s failure=%q diagnostic=%+v assets=%#v", riggingTask.State, detail, riggingTask.Failure, riggingTask.Assets)
	}
	animation, err := harness.submitApproved(t,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:    "owner-a",
			Capability: generation.Animation,
			OfferingID: "meshy/processing/animation",
			Input: []byte(
				`{"source_task":"` + string(rigging.TaskID) + `","action_id":3}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	animationTask := harness.runUntilTerminal(t, "owner-a", animation.TaskID)
	if animationTask.State != generation.TaskSucceeded ||
		!adapter.sawResolvedDependency {
		t.Fatalf(
			"animation task = %#v, resolved dependency = %v",
			animationTask,
			adapter.sawResolvedDependency,
		)
	}
	_, err = harness.submitApproved(t,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:    "owner-b",
			Capability: generation.Animation,
			OfferingID: "meshy/processing/animation",
			Input: []byte(
				`{"source_task":"` + string(rigging.TaskID) + `","action_id":3}`,
			),
		},
	)
	if !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("cross-owner task dependency error = %#v", err)
	}
}

func TestSameProviderTaskDependencyRejectsCrossProviderSubmit(t *testing.T) {
	adapter := &countingDependencyAdapter{}
	harness := newHarness(t, adapter, nil)
	source, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"cross provider source","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceTask := harness.runUntilTerminal(t, "owner-a", source.TaskID)
	if sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("source task = %#v", sourceTask)
	}
	_, err = harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input: []byte(
			`{"source_task":"` + string(source.TaskID) + `"}`,
		),
	})
	if err == nil {
		t.Fatal("cross-provider task dependency was accepted")
	}
	if !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("error = %#v", err)
	}
	refusal := generation.UserFacingError(err)
	if refusal == nil || strings.Contains(refusal.Detail, "source_task") {
		t.Fatalf("public refusal = %+v", refusal)
	}
	if !strings.Contains(refusal.Detail, "不满足这一步的来源要求") {
		t.Fatalf("public refusal detail = %q", refusal.Detail)
	}
	page, listErr := harness.module.List(
		context.Background(),
		generation.OwnTasks("owner-a"),
		generation.ListQuery{},
	)
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, task := range page.Tasks {
		if task.Capability == generation.Rigging {
			t.Fatalf("rejected rigging task was persisted: %#v", task)
		}
	}
	if adapter.starts != 1 {
		t.Fatalf("upstream starts = %d", adapter.starts)
	}
}

func TestSameProviderTaskDependencyResolvesAndWiresRemoteID(t *testing.T) {
	adapter := &countingDependencyAdapter{}
	harness := newHarness(t, adapter, nil)
	source, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d",
		Input:      []byte(`{"prompt":"same provider source"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceTask := harness.runUntilTerminal(t, "owner-a", source.TaskID)
	if sourceTask.State != generation.TaskSucceeded {
		t.Fatalf("source task = %#v", sourceTask)
	}
	accepted, err := harness.submitApproved(t, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.Rigging,
		OfferingID: "meshy/processing/rigging",
		Input: []byte(
			`{"source_task":"` + string(source.TaskID) + `"}`,
		),
	})
	if err != nil {
		t.Fatalf("same-provider task dependency rejected: %v", err)
	}
	rigged := harness.runUntilTerminal(t, "owner-a", accepted.TaskID)
	if rigged.State != generation.TaskSucceeded {
		t.Fatalf("rigging task = %#v", rigged)
	}
	dependency, dependencyExists := adapter.lastDependencies["source_task"]
	if !dependencyExists {
		t.Fatalf("adapter dependencies = %#v", adapter.lastDependencies)
	}
	if !strings.HasPrefix(
		dependency.UpstreamJob.RemoteID,
		"cross-source-remote-",
	) {
		t.Fatalf("dependency upstream remote id = %q, want cross-source-remote-*",
			dependency.UpstreamJob.RemoteID)
	}
	if dependency.ProviderID != "meshy" {
		t.Fatalf("dependency provider = %q, want meshy", dependency.ProviderID)
	}
}

func TestProviderWebhookFindsStableTaskAndAppliesNormalizedObservation(
	t *testing.T,
) {
	adapter := &webhookAdapter{}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:        "owner-a",
			Capability:     "text-to-3d",
			OfferingID:     "meshy/meshy-7.1/text-to-3d",
			Input:          []byte(`{"prompt":"webhook observatory"}`),
			IdempotencyKey: "provider-webhook-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.reconcile(t)

	result, err := harness.module.IngestProviderWebhook(
		context.Background(),
		generation.ProviderWebhookCommand{
			ProviderID: "meshy",
			RemoteID:   "remote-webhook-1",
			Payload:    []byte(`{"id":"remote-webhook-1","status":"SUCCEEDED"}`),
			EventType:  "provider.webhook_received",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != accepted.TaskID || !result.Applied {
		t.Fatalf("webhook result = %#v, task = %s", result, accepted.TaskID)
	}

	harness.reconcile(t)
	task, err := harness.module.Get(
		context.Background(),
		"owner-a",
		accepted.TaskID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded || len(task.Assets) != 6 {
		t.Fatalf("task after webhook = %#v", task)
	}
	if adapter.observations != 0 {
		t.Fatalf("poll observations = %d, want 0", adapter.observations)
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
	if countEvents(events, "provider.webhook_received") != 1 {
		t.Fatalf("events = %#v", eventTypes(events))
	}
}

// A provider may decline a webhook that cannot advance the task on its own
// (a two-stage private sub-job driven by polling). The kernel acknowledges it
// and leaves the task untouched.
func TestProviderWebhookNotApplicableIsAcknowledgedWithoutChange(t *testing.T) {
	adapter := &webhookAdapter{notApplicable: true}
	harness := newHarness(t, adapter, nil)
	accepted, err := harness.submitApproved(t,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:        "owner-a",
			Capability:     "text-to-3d",
			OfferingID:     "meshy/meshy-7.1/text-to-3d",
			Input:          []byte(`{"prompt":"two stage"}`),
			IdempotencyKey: "provider-webhook-not-applicable",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	harness.reconcile(t)
	before, err := harness.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := harness.module.IngestProviderWebhook(
		context.Background(),
		generation.ProviderWebhookCommand{
			ProviderID: "meshy",
			RemoteID:   "remote-webhook-1",
			Payload:    []byte(`{"id":"remote-webhook-1","status":"SUCCEEDED"}`),
			EventType:  "provider.webhook_received",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != accepted.TaskID || result.Applied {
		t.Fatalf("webhook result = %#v", result)
	}
	after, err := harness.module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.State.Terminal() {
		t.Fatalf("task state %s -> %s", before.State, after.State)
	}
}

type harness struct {
	module     *generation.Module
	repository *sqlite.Repository
	dbPath     string
	clock      *fakeClock
	inputs     *inputassets.Service
	assets     generation.AssetStore
	cache      *recordingCache
	submitFixtureState
}

func newHarness(
	t *testing.T,
	adapter generation.ProviderPort,
	overrideStore generation.AssetStore,
	maxInflight ...int,
) *harness {
	return newConfiguredHarness(t, adapter, overrideStore, nil, maxInflight...)
}

func newConfiguredHarness(
	t *testing.T,
	adapter generation.ProviderPort,
	overrideStore generation.AssetStore,
	configure func(*generation.Dependencies),
	maxInflight ...int,
) *harness {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "test.db")
	repository, err := sqlitetest.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	store := overrideStore
	var inputs *inputassets.Service
	if store == nil {
		localStore, storeErr := local.New(filepath.Join(root, "assets"))
		err = storeErr
		if err != nil {
			t.Fatal(err)
		}
		store = localStore
		inputs, err = inputassets.New(repository, localStore)
		if err != nil {
			t.Fatal(err)
		}
	}
	clock := &fakeClock{now: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)}
	repository.SetClock(clock.Now)
	cache := &recordingCache{}
	dependencies := generation.Dependencies{
		Repository: repository,
		Catalog:    taskCatalog,
		Providers:  registry,
		Assets:     store,
		Clock:      clock,
		Cache:      cache,
	}
	if len(maxInflight) > 0 {
		dependencies.PerOwnerMaxInflightTasks = maxInflight[0]
	}
	if _, exactContract := adapter.(*variantClaimsAdapter); !exactContract {
		adapter = sixFormatFixtureAdapter{next: adapter}
	}
	for _, key := range []string{
		"openai-images.v1",
		"gemini-images.v1",
		"meshy.v2",
		"tripo.v3",
		"matting.v1",
	} {
		if err := registry.Register(key, adapter); err != nil {
			t.Fatal(err)
		}
	}
	if inputs != nil {
		dependencies.Inputs = inputs
	}
	if configure != nil {
		configure(&dependencies)
	}
	module, err := generation.New(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = module.Close()
	})
	return &harness{
		module:     module,
		repository: repository,
		dbPath:     dbPath,
		clock:      clock,
		inputs:     inputs,
		assets:     store,
		cache:      cache,
	}
}

// sixFormatFixtureAdapter makes single-asset test adapters satisfy the current
// Meshy 7.1 fixture contract. The integration tests here exercise task engine
// behavior, while the provider contract itself is asserted through the catalog
// and schema projection tests.
type sixFormatFixtureAdapter struct {
	next generation.ProviderPort
}

func (a sixFormatFixtureAdapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	observation, err := a.next.Start(ctx, request)
	if err != nil {
		return observation, err
	}
	return completeFixtureOutputs(request.Offering, observation)
}

func (a sixFormatFixtureAdapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	observation, err := a.next.Observe(ctx, request)
	if err != nil {
		return observation, err
	}
	return completeFixtureOutputs(request.Offering, observation)
}

func (a sixFormatFixtureAdapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.next.OpenAsset(ctx, request)
}

func (a sixFormatFixtureAdapter) CancelProviderTask(ctx context.Context, request generation.DeleteProviderRequest) error {
	canceler, ok := a.next.(generation.ProviderTaskCanceler)
	if !ok {
		return errors.New("fixture adapter does not support provider cancellation")
	}
	return canceler.CancelProviderTask(ctx, request)
}

func (a sixFormatFixtureAdapter) DeleteProviderTask(ctx context.Context, request generation.DeleteProviderRequest) error {
	deleter, ok := a.next.(generation.ProviderTaskDeleter)
	if !ok {
		return errors.New("fixture adapter does not support provider deletion")
	}
	return deleter.DeleteProviderTask(ctx, request)
}

func (a sixFormatFixtureAdapter) DecodeWebhook(ctx context.Context, request generation.WebhookDecodeRequest) (generation.Observation, error) {
	decoder, ok := a.next.(generation.ProviderWebhookDecoder)
	if !ok {
		return generation.Observation{}, errors.New("fixture adapter does not support webhooks")
	}
	observation, err := decoder.DecodeWebhook(ctx, request)
	if err != nil {
		return observation, err
	}
	return completeFixtureOutputs(request.Offering, observation)
}

func completeFixtureOutputs(offering generation.Offering, observation generation.Observation) (generation.Observation, error) {
	observation, err := completeMeshyFormats(offering.ID, observation)
	if err != nil {
		return observation, err
	}
	if len(observation.Artifacts) == 0 {
		return observation, nil
	}
	for outputIndex, output := range offering.Outputs {
		if (output.Role != "model" && output.Role != "converted_model") || output.Delivery != "required" {
			continue
		}
		if output.Variant == "" && output.VariantPrefix == "" {
			continue
		}
		minimum := 1
		if output.Minimum != nil {
			minimum = *output.Minimum
		}
		if minimum <= 0 {
			continue
		}
		matches := 0
		for _, artifact := range observation.Artifacts {
			if artifact.Role == output.Role &&
				((output.Variant != "" && artifact.Variant == output.Variant) ||
					(output.VariantPrefix != "" && strings.HasPrefix(artifact.Variant, output.VariantPrefix))) {
				matches++
			}
		}
		for matches < minimum {
			variant := output.Variant
			if output.VariantPrefix != "" {
				variant = output.VariantPrefix + "fixture-" + strconv.Itoa(matches+1)
			}
			mediaType := "model/gltf-binary"
			if len(output.MediaTypes) > 0 {
				mediaType = output.MediaTypes[0]
			}
			inline, mediaType, err := fixtureModelPayload(variant, mediaType)
			if err != nil {
				return observation, err
			}
			if output.Fidelity != "" {
				inline = fidelityFixtureGLB(output.Fidelity)
			}
			observation.Artifacts = append(observation.Artifacts, generation.ArtifactClaim{
				ID:        string(offering.ID) + ":fixture:" + strconv.Itoa(outputIndex) + ":" + strconv.Itoa(matches),
				Role:      output.Role,
				Variant:   variant,
				Inline:    inline,
				MediaType: mediaType,
			})
			matches++
		}
	}
	return observation, nil
}

func completeMeshyFormats(offeringID generation.OfferingID, observation generation.Observation) (generation.Observation, error) {
	if offeringID != "meshy/meshy-7.1/text-to-3d" || len(observation.Artifacts) == 0 {
		return observation, nil
	}
	formats := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
	seen := map[string]bool{}
	for _, artifact := range observation.Artifacts {
		if artifact.Role == "model" {
			seen[artifact.Variant] = true
		}
	}
	sourceIndex := -1
	for i := range observation.Artifacts {
		if observation.Artifacts[i].Role == "model" {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		return observation, nil
	}
	source := observation.Artifacts[sourceIndex]
	if source.Variant == "" {
		source.Variant = "glb"
		observation.Artifacts[sourceIndex].Variant = "glb"
	}
	// The generic mock stores placeholder bytes. This fixture supplies a
	// structurally valid local model for materialization checks.
	if len(source.Inline) == 0 && source.Locator != "expired://model" {
		source.Inline = validGLB("glb")
		source.Locator = ""
		source.MediaType = "model/gltf-binary"
		observation.Artifacts[sourceIndex] = source
	}
	if source.Variant == "glb" {
		seen["glb"] = true
	}
	for _, format := range formats {
		if seen[format] {
			continue
		}
		claim := source
		claim.ID += ":" + format
		claim.Variant = format
		inline, mediaType, err := fixtureModelPayload(format, "model/gltf-binary")
		if err != nil {
			return observation, err
		}
		claim.Inline, claim.MediaType = inline, mediaType
		observation.Artifacts = append(observation.Artifacts, claim)
	}
	return observation, nil
}

// Archive outputs carry a real self-contained ZIP. A GLB renamed as 3MF or
// USDZ cannot exercise the delivered-content isolation contract.
func fixtureModelPayload(variant, mediaType string) ([]byte, string, error) {
	format := strings.TrimPrefix(variant, "converted.")
	if format == "stl" {
		return fixtureBinarySTL(), "model/stl", nil
	}
	if format != "3mf" && format != "usdz" {
		return validGLB(variant), mediaType, nil
	}
	entries := []struct {
		header  *zip.FileHeader
		content string
	}{
		{&zip.FileHeader{Name: "[Content_Types].xml", Method: zip.Deflate}, `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`},
		{&zip.FileHeader{Name: "3D/3dmodel.model", Method: zip.Deflate}, `<model xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources/><build/></model>`},
	}
	mediaType = "application/zip"
	if strings.HasSuffix(variant, "usdz") {
		// USDZ stores entries without compression and aligns their payloads.
		header := &zip.FileHeader{Name: "scene.usda", Method: zip.Store, Extra: make([]byte, 24)}
		binary.LittleEndian.PutUint16(header.Extra[:2], 0x1986)
		binary.LittleEndian.PutUint16(header.Extra[2:4], 20)
		entries = entries[:0]
		entries = append(entries, struct {
			header  *zip.FileHeader
			content string
		}{header, "#usda 1.0\ndef Xform \"fixture\" {}\n"})
		mediaType = "model/vnd.usdz+zip"
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, item := range entries {
		entry, err := archive.CreateHeader(item.header)
		if err != nil {
			return nil, "", err
		}
		if _, err := io.WriteString(entry, item.content); err != nil {
			return nil, "", err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), mediaType, nil
}

// fixtureBinarySTL is one finite triangle in the exact binary STL layout.
func fixtureBinarySTL() []byte {
	body := make([]byte, 84+50)
	copy(body, "fixture binary stl")
	binary.LittleEndian.PutUint32(body[80:84], 1)
	for i, value := range []float32{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0} {
		binary.LittleEndian.PutUint32(body[84+4*i:], math.Float32bits(value))
	}
	return body
}

type recordingCache struct {
	mutex      sync.Mutex
	operations []cachemanagement.Operation
	finished   int
}

func (r *recordingCache) Begin(operation cachemanagement.Operation) func() {
	r.mutex.Lock()
	r.operations = append(r.operations, operation)
	r.mutex.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mutex.Lock()
			r.finished++
			r.mutex.Unlock()
		})
	}
}

func (r *recordingCache) snapshot() ([]cachemanagement.Operation, int) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]cachemanagement.Operation(nil), r.operations...), r.finished
}

func (h *harness) reconcile(t *testing.T) {
	t.Helper()
	_, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID:  "test-worker",
		LeaseFor:  time.Minute,
		BatchSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(31 * time.Second)
}

func (h *harness) runUntilTerminal(
	t *testing.T,
	owner generation.OwnerID,
	taskID generation.TaskID,
) generation.Task {
	t.Helper()
	for range 20 {
		h.reconcile(t)
		task, err := h.module.Get(context.Background(), owner, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			return task
		}
	}
	t.Fatal("task did not reach terminal state")
	return generation.Task{}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

type deletableAdapter struct {
	deleted *generation.DeleteProviderRequest
}

func (a *deletableAdapter) Start(
	_ context.Context,
	_ generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:    generation.UpstreamAccepted,
		Progress: 1,
		Job: &generation.UpstreamJob{
			ProviderID: "meshy",
			RemoteID:   "remote-delete-me",
		},
	}, nil
}

func (a *deletableAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:    generation.UpstreamRunning,
		Progress: 2,
		Job: &generation.UpstreamJob{
			ProviderID: "meshy",
			RemoteID:   "remote-delete-me",
		},
	}, nil
}

func (a *deletableAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("unexpected OpenAsset")
}

func (a *deletableAdapter) DeleteProviderTask(
	_ context.Context,
	request generation.DeleteProviderRequest,
) error {
	copy := request
	a.deleted = &copy
	return nil
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type failOnceAdapter struct {
	mu     sync.Mutex
	failed bool
	next   generation.ProviderPort
}

func (a *failOnceAdapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	if !a.failed {
		a.failed = true
		a.mu.Unlock()
		return generation.Observation{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureUpstreamUnavailable,
				Retryable:  true,
				SafeDetail: "definite 503",
			},
		}
	}
	a.mu.Unlock()
	return a.next.Start(ctx, request)
}

func (a *failOnceAdapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	return a.next.Observe(ctx, request)
}

func (a *failOnceAdapter) OpenAsset(
	ctx context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return a.next.OpenAsset(ctx, request)
}

type ambiguousAdapter struct{}

func (ambiguousAdapter) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{}, context.DeadlineExceeded
}

func (ambiguousAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (ambiguousAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("unexpected OpenAsset")
}

type completeAdapter struct{}

type requiredMultiClaimRetryAdapter struct {
	mu          sync.Mutex
	failVariant string
	failed      bool
	downloads   map[string]int
}

func (a *requiredMultiClaimRetryAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:    generation.UpstreamCompleted,
		Progress: 100,
		Artifacts: []generation.ArtifactClaim{
			{ID: string(request.AttemptID) + ":first", Role: "model", Variant: "converted.gltf", Inline: validGLB("first"), MediaType: "model/gltf-binary"},
			{ID: string(request.AttemptID) + ":second", Role: "model", Variant: "converted.usdz", Inline: validGLB("second"), MediaType: "model/gltf-binary"},
		},
	}, nil
}

func (*requiredMultiClaimRetryAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (a *requiredMultiClaimRetryAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	a.mu.Lock()
	if a.downloads == nil {
		a.downloads = map[string]int{}
	}
	a.downloads[request.Claim.ID]++
	if strings.HasSuffix(request.Claim.ID, a.failVariant) && !a.failed {
		a.failed = true
		a.mu.Unlock()
		return nil, generation.AssetMetadata{}, errors.New("temporary required claim download failure")
	}
	a.mu.Unlock()
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func (a *requiredMultiClaimRetryAdapter) downloadCount(claimSuffix string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for claimID, downloads := range a.downloads {
		if strings.HasSuffix(claimID, claimSuffix) {
			count += downloads
		}
	}
	return count
}

type webhookAdapter struct {
	observations  int
	notApplicable bool
}

func (a *webhookAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	job := &generation.UpstreamJob{
		ProviderID: request.Offering.ProviderID,
		RemoteID:   "remote-webhook-1",
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       job,
		Progress:  1,
		PollAfter: time.Minute,
		DedupeKey: job.RemoteID + ":accepted",
	}, nil
}

func (a *webhookAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	a.observations++
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (a *webhookAdapter) DecodeWebhook(
	_ context.Context,
	request generation.WebhookDecodeRequest,
) (generation.Observation, error) {
	if a.notApplicable {
		return generation.Observation{}, generation.ErrWebhookNotApplicable
	}
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &request.Job,
		Progress:  100,
		DedupeKey: request.Job.RemoteID + ":completed",
		Artifacts: []generation.ArtifactClaim{{
			ID:        request.Job.RemoteID + ":model",
			Role:      "model",
			Inline:    validGLB("webhook"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (a *webhookAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

type dedupeObservationAdapter struct {
	mu           sync.Mutex
	observations int
}

func (a *dedupeObservationAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	job := &generation.UpstreamJob{
		ProviderID: request.Offering.ProviderID,
		RemoteID:   "dedupe-remote-1",
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       job,
		Progress:  1,
		PollAfter: time.Millisecond,
		DedupeKey: job.RemoteID + ":accepted",
	}, nil
}

func (a *dedupeObservationAdapter) Observe(
	_ context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observations++
	if a.observations < 3 {
		return generation.Observation{
			Phase:     generation.UpstreamRunning,
			Job:       &request.Job,
			Progress:  50,
			PollAfter: time.Millisecond,
			DedupeKey: request.Job.RemoteID + ":running",
		}, nil
	}
	return generation.Observation{
		Phase:     generation.UpstreamCompleted,
		Job:       &request.Job,
		Progress:  100,
		DedupeKey: request.Job.RemoteID + ":completed",
		Artifacts: []generation.ArtifactClaim{{
			ID:        request.Job.RemoteID + ":model",
			Role:      "model",
			Inline:    validGLB("dedupe"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (a *dedupeObservationAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

func (a *dedupeObservationAdapter) ObservationCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.observations
}

func (completeAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase:    generation.UpstreamCompleted,
		Progress: 100,
		Artifacts: []generation.ArtifactClaim{{
			ID:        string(request.AttemptID) + ":model",
			Role:      "model",
			Inline:    validGLB("mock"),
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (completeAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (completeAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
		Size:      int64(len(request.Claim.Inline)),
	}, nil
}

type failingAssetStore struct{}

func (failingAssetStore) Put(
	context.Context,
	generation.AssetWrite,
) (generation.Asset, error) {
	return generation.Asset{}, errors.New("disk full")
}

func (failingAssetStore) Open(
	context.Context,
	generation.Asset,
) (io.ReadCloser, error) {
	return nil, errors.New("not found")
}

func (failingAssetStore) Delete(context.Context, generation.Asset) error {
	return nil
}

type completeWithBrokenOptional struct{}

func (completeWithBrokenOptional) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Artifacts: []generation.ArtifactClaim{
			{
				ID:        string(request.AttemptID) + ":model",
				Role:      "model",
				Inline:    validGLB("mock"),
				MediaType: "model/gltf-binary",
			},
			{
				ID:        string(request.AttemptID) + ":thumbnail",
				Role:      "thumbnail",
				Locator:   "broken://thumbnail",
				MediaType: "image/webp",
			},
		},
	}, nil
}

func (completeWithBrokenOptional) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (completeWithBrokenOptional) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if request.Claim.Role == "thumbnail" {
		return nil, generation.AssetMetadata{}, errors.New("expired optional URL")
	}
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
	}, nil
}

type blockingStartAdapter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type completeWithBrokenSurplusModel struct{}

type misleadingMediaTypeAdapter struct{}

type genericMediaTypeAdapter struct{}

type taskDependencyAdapter struct {
	sawResolvedDependency bool
}

type countingDependencyAdapter struct {
	starts           int
	lastDependencies map[string]generation.TaskDependency
}

func (a *countingDependencyAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	a.starts++
	a.lastDependencies = request.TaskDependencies
	// 按输出声明派生主产物变体（与真实 Meshy wire 名对齐），
	// 旧语义声明保持无变体。
	primaryVariant := ""
	for _, output := range request.Offering.Outputs {
		if output.Variant != "" {
			primaryVariant = output.Variant
			break
		}
		if output.VariantPrefix != "" {
			primaryVariant = strings.TrimRight(output.VariantPrefix, "_") + "_glb_url"
			break
		}
	}
	artifacts := []generation.ArtifactClaim{
		{
			ID:        string(request.AttemptID) + ":model",
			Role:      "model",
			Variant:   primaryVariant,
			Inline:    validGLB("source"),
			MediaType: "model/gltf-binary",
		},
	}
	if request.Offering.Capability == generation.Animation {
		artifacts[0].Inline = fidelityFixtureGLB("animation")
	}
	if request.Offering.Capability == generation.Rigging && primaryVariant == "rigged_character_fbx_url" {
		artifacts[0].MediaType = "application/octet-stream"
	}
	if request.Offering.Capability == generation.TextTo3D ||
		request.Offering.Capability == generation.ImageTo3D {
		artifacts = append(artifacts, generation.ArtifactClaim{
			ID:        string(request.AttemptID) + ":thumbnail",
			Role:      "thumbnail",
			Inline:    []byte("RIFF....WEBP"),
			MediaType: "image/webp",
		})
	}
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job: &generation.UpstreamJob{
			ProviderID: request.Offering.ProviderID,
			RemoteID:   "cross-source-remote-" + string(request.AttemptID),
		},
		Artifacts: artifacts,
	}, nil
}

func (*countingDependencyAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (*countingDependencyAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
	}, nil
}

type refreshingClaimsAdapter struct {
	observations int
}

func (*refreshingClaimsAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job: &generation.UpstreamJob{
			ProviderID: request.Offering.ProviderID,
			RemoteID:   "remote-completed",
		},
		Artifacts: []generation.ArtifactClaim{{
			ID:        string(request.AttemptID) + ":expired",
			Role:      "model",
			Locator:   "expired://model",
			MediaType: "model/gltf-binary",
		}},
	}, nil
}

func (a *refreshingClaimsAdapter) Observe(
	_ context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	a.observations++
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job:   &request.Job,
		Artifacts: []generation.ArtifactClaim{{
			ID:        string(request.AttemptID) + ":fresh",
			Role:      "model",
			Inline:    validGLB("refreshed"),
			MediaType: "model/gltf-binary",
		}},
		DedupeKey: "remote-completed:refreshed",
	}, nil
}

func (*refreshingClaimsAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if request.Claim.Locator == "expired://model" {
		return nil, generation.AssetMetadata{}, &generation.ProviderError{
			Failure: &generation.Failure{
				Class:      generation.FailureUpstreamUnavailable,
				Retryable:  true,
				SafeDetail: "signed URL expired",
			},
		}
	}
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
	}, nil
}

func (a *taskDependencyAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	remoteID := "rigging-remote"
	payload := validGLB("output")
	if request.Offering.Capability == generation.Animation {
		payload = fidelityFixtureGLB("animation")
		dependency, exists := request.TaskDependencies["source_task"]
		a.sawResolvedDependency = exists &&
			dependency.TaskID != "" &&
			dependency.Capability == generation.Rigging &&
			dependency.UpstreamJob.RemoteID == "rigging-remote"
		if !a.sawResolvedDependency {
			return generation.Observation{}, errors.New("missing resolved dependency")
		}
		remoteID = "animation-remote"
	}
	// 与真实 Meshy 线上产物对齐：按输出声明派生主产物变体
	// （rigged_character_glb_url / animation_glb_url），旧语义声明无变体。
	variant := ""
	mediaType := "model/gltf-binary"
	for _, output := range request.Offering.Outputs {
		if output.Variant != "" {
			variant = output.Variant
			if len(output.MediaTypes) > 0 {
				mediaType = output.MediaTypes[0]
			}
			break
		}
		if output.VariantPrefix != "" {
			variant = strings.TrimRight(output.VariantPrefix, "_") + "_glb_url"
			if len(output.MediaTypes) > 0 {
				mediaType = output.MediaTypes[0]
			}
			break
		}
	}
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Job: &generation.UpstreamJob{
			ProviderID: request.Offering.ProviderID,
			RemoteID:   remoteID,
		},
		Artifacts: []generation.ArtifactClaim{{
			ID:        string(request.AttemptID) + ":model",
			Role:      "model",
			Variant:   variant,
			Inline:    payload,
			MediaType: mediaType,
		}},
	}, nil
}

func (*taskDependencyAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (*taskDependencyAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
	}, nil
}

func (misleadingMediaTypeAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Artifacts: []generation.ArtifactClaim{{
			ID:        string(request.AttemptID) + ":image",
			Role:      "image",
			Inline:    []byte("<html>not an image</html>"),
			MediaType: "image/png",
		}},
	}, nil
}

func (misleadingMediaTypeAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (misleadingMediaTypeAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: "text/html; charset=utf-8",
	}, nil
}

func (genericMediaTypeAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Artifacts: []generation.ArtifactClaim{
			{
				ID:        string(request.AttemptID) + ":model",
				Role:      "model",
				Inline:    validGLB("binary"),
				MediaType: "model/gltf-binary",
			},
			{
				ID:        string(request.AttemptID) + ":thumbnail",
				Role:      "thumbnail",
				Inline:    []byte("RIFF....WEBP"),
				MediaType: "image/webp",
			},
		},
	}, nil
}

func (genericMediaTypeAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

// 模拟 Tripo CDN：无论什么文件都返回 binary/octet-stream。
func (genericMediaTypeAdapter) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: "binary/octet-stream",
	}, nil
}

func (completeWithBrokenSurplusModel) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{
		Phase: generation.UpstreamCompleted,
		Artifacts: []generation.ArtifactClaim{
			{
				ID:        string(request.AttemptID) + ":glb",
				Role:      "model",
				Inline:    validGLB("good"),
				MediaType: "model/gltf-binary",
			},
			{
				ID:        string(request.AttemptID) + ":fbx",
				Role:      "model",
				Locator:   "broken://fbx",
				MediaType: "application/octet-stream",
			},
		},
	}, nil
}

func (completeWithBrokenSurplusModel) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (completeWithBrokenSurplusModel) OpenAsset(
	_ context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if request.Claim.Locator != "" {
		return nil, generation.AssetMetadata{}, errors.New("expired surplus URL")
	}
	return io.NopCloser(bytes.NewReader(request.Claim.Inline)), generation.AssetMetadata{
		MediaType: request.Claim.MediaType,
	}, nil
}

func newBlockingStartAdapter() *blockingStartAdapter {
	return &blockingStartAdapter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (a *blockingStartAdapter) Start(
	_ context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	a.once.Do(func() { close(a.started) })
	<-a.release
	return generation.Observation{
		Phase: generation.UpstreamAccepted,
		Job: &generation.UpstreamJob{
			ProviderID: request.Offering.ProviderID,
			RemoteID:   "stale-remote-job",
		},
	}, nil
}

func (*blockingStartAdapter) Observe(
	context.Context,
	generation.ObserveRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Observe")
}

func (*blockingStartAdapter) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New("unexpected OpenAsset")
}

func countEvents(events []generation.Event, eventType string) int {
	return len(eventsByType(events, eventType))
}

func hasEvent(events []generation.Event, eventType string) bool {
	return countEvents(events, eventType) > 0
}

func eventsByType(events []generation.Event, eventType string) []generation.Event {
	result := make([]generation.Event, 0)
	for _, event := range events {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	return result
}

func eventTypes(events []generation.Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.Type)
	}
	return result
}

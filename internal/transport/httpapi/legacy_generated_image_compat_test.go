package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyGeneratedImageStreamsUnifiedTaskAsset(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	task := createSucceededImageTask(t, module, "local-user")
	filename := string(task.ID) + "_0.jpeg"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/gpt-images/"+filename,
		nil,
	)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Content-Type") != "image/jpeg" ||
		recorder.Header().Get("Cache-Control") !=
			"private, no-store" ||
		recorder.Body.Len() == 0 {
		t.Fatalf(
			"status = %d, headers = %#v, body = %q",
			recorder.Code,
			recorder.Header(),
			recorder.Body.String(),
		)
	}
	audit := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action=access_asset",
		"",
		nil,
	)
	if audit.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", audit.Code, audit.Body)
	}
	auditBody := decodeAdministrationBody(t, audit)
	if auditBody["total"] != float64(1) {
		t.Fatalf("audit body=%#v", auditBody)
	}
	event := auditBody["data"].([]any)[0].(map[string]any)
	if event["userId"] != "local-user" ||
		event["action"] != "access_asset" ||
		event["resourceType"] != "gpt-image" ||
		event["resourceId"] != filename {
		t.Fatalf("audit event=%#v", event)
	}

	assertJSONResponse(
		t,
		handler,
		http.MethodGet,
		"/api/gpt-images/"+string(task.ID)+"_9.png",
		"",
		http.StatusNotFound,
		map[string]any{"error": "文件不存在"},
	)
}

func TestLegacyGeneratedImageReadsOwnedTaskHistoricalFile(
	t *testing.T,
) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(
		t, []string{"user"}, nil, nil, nil, nil,
		legacyImageTaskSetup("local-user", false),
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/gpt-images/historical_0.webp",
		nil,
	)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Content-Type") != "image/webp" ||
		recorder.Body.String() != "historical-image" {
		t.Fatalf(
			"historical response = %d, headers = %#v, body = %q",
			recorder.Code,
			recorder.Header(),
			recorder.Body.String(),
		)
	}
	task, err := module.Get(t.Context(), "local-user", "historical")
	if err != nil || task.State != generation.TaskFailed || len(task.Assets) != 0 {
		t.Fatalf("legacy read changed public Task: task=%#v error=%v", task, err)
	}
}

func TestLegacyGeneratedImageRefusesUnownedHistoricalFiles(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t, []string{"user"}, nil, nil, nil, nil,
		func(t *testing.T, databasePath string) {
			root := filepath.Join(filepath.Dir(databasePath), "legacy-generated")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "private.webp"), []byte("private image"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	)
	for _, filename := range []string{"historical_0.webp", "private.webp"} {
		assertLegacyGeneratedImageRefused(t, handler, filename, http.StatusNotFound)
	}
}

func TestLegacyGeneratedImageOwnerCheckSurvivesRetainedTombstone(t *testing.T) {
	t.Parallel()
	for _, owner := range []string{"local-user", "another-user"} {
		t.Run(owner, func(t *testing.T) {
			module, handler, _, _ := newHTTPHarnessWithServices(
				t, []string{"user"}, nil, nil, nil, nil,
				legacyImageTaskSetup(generation.OwnerID(owner), false),
			)
			if owner != "local-user" {
				assertLegacyGeneratedImageRefused(t, handler, "historical_0.webp", http.StatusNotFound)
			}
			if err := module.Delete(t.Context(), generation.DeleteCommand{
				Scope: generation.OwnTasks(generation.OwnerID(owner)), ActorID: generation.OwnerID(owner), TaskID: "historical",
				ExpectedCapability: generation.TextToImage,
				Policy: generation.TaskDeletionPolicy{
					LocalRecordPolicy: "tombstone", UpstreamPolicy: "when-supported", UpstreamFailurePolicy: "abort",
					GeneratedAssetPolicy: "retain", AuditEventType: "task.deleted",
				},
			}); err != nil {
				t.Fatal(err)
			}
			if existed, err := module.Exists(t.Context(), "historical"); err != nil || !existed {
				t.Fatalf("retained Task existence=%v error=%v", existed, err)
			}
			assertLegacyGeneratedImageRefused(t, handler, "historical_0.webp", http.StatusNotFound)
		})
	}
}

func TestLegacyGeneratedImageDoesNotMaskStorageFailure(t *testing.T) {
	t.Parallel()
	t.Run("task store unavailable", func(t *testing.T) {
		module, handler, _, _ := newHTTPHarnessWithServices(
			t, []string{"user"}, nil, nil, nil, nil,
			legacyImageTaskSetup("local-user", false),
		)
		if err := module.Close(); err != nil {
			t.Fatal(err)
		}
		assertLegacyGeneratedImageRefused(t, handler, "historical_0.webp", http.StatusInternalServerError)
	})
	t.Run("native asset unavailable", func(t *testing.T) {
		_, handler, _, _ := newHTTPHarnessWithServices(
			t, []string{"user"}, nil, nil, nil, nil,
			legacyImageTaskSetup("local-user", true),
		)
		assertLegacyGeneratedImageRefused(t, handler, "historical_0.webp", http.StatusInternalServerError)
	})
}

func TestLegacyGeneratedImageKeepsAdministratorTaskReadScope(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t, []string{"admin"}, nil, nil, nil, nil,
		legacyImageTaskSetup("another-user", false),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/gpt-images/historical_0.webp", nil))
	if response.Code != http.StatusOK || response.Body.String() != "historical-image" {
		t.Fatalf("administrator read: status=%d body=%q", response.Code, response.Body)
	}
}

// Historical files carry no owner metadata. The fixture supplies the explicit
// migrated owner, Task and Attempt that authorize the compatibility read.
func legacyImageTaskSetup(owner generation.OwnerID, unavailableNative bool) func(*testing.T, string) {
	return func(t *testing.T, databasePath string) {
		t.Helper()
		repository, err := sqlite.Open(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		defer repository.Close()
		now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		offering := generation.Offering{ID: "legacy/openai/text-to-image", ProviderID: "openai", Capability: generation.TextToImage}
		item := sqlite.GenerationTaskImport{
			Task: generation.Task{
				ID: "historical", OwnerID: owner, Capability: generation.TextToImage, Offering: offering, Input: []byte(`{}`),
				State: generation.TaskFailed, Version: 1, CreatedAt: now, UpdatedAt: now, FinishedAt: &now,
				Failure: &generation.Failure{Class: generation.FailureIntegrity, SafeDetail: "historical output missing from native storage"},
			},
			Attempts: []sqlite.GenerationAttemptImport{{
				ID: "historical-attempt-1", Ordinal: 1, Offering: offering, State: generation.AttemptFailed,
				SubmissionKey: "legacy-ledger:historical", StartedAt: now, UpdatedAt: now, FinishedAt: &now,
			}},
		}
		var assets []generation.Asset
		if unavailableNative {
			assets = []generation.Asset{{
				ID: "historical-image", TaskID: "historical", AttemptID: "historical-attempt-1",
				Role: "image", Ordinal: 1, MediaType: "image/webp", StorageKey: "missing-native.webp", CreatedAt: now,
			}}
		}
		if _, err := repository.ImportGenerationTasksAndAssets(t.Context(), []sqlite.GenerationTaskImport{item}, assets); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyGeneratedImageWireChangesWithProductSchema(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(schema *catalog.ApplicationSchema) {
			schema.Runtime.HTTP.LegacyRoutes.GeneratedImage =
				"/api/schema/images/{filename}"
			schema.Runtime.AssetDelivery.LegacyGeneratedImage.
				FilenameSeparator = "-"
			schema.Runtime.AssetDelivery.LegacyGeneratedImage.OrdinalBase = 1
			schema.Runtime.AssetDelivery.LegacyGeneratedImage.
				ExtensionMediaTypes["bin"] = "image/schema"
			schema.Content.Messages[schema.Runtime.AssetDelivery.LegacyGeneratedImage.
				NotFoundMessageKey] = "schema-missing"
		},
	)
	task := createSucceededImageTask(t, module, "local-user")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/schema/images/"+string(task.ID)+"-1.bin",
		nil,
	)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Content-Type") != "image/schema" ||
		recorder.Header().Get("Cache-Control") != "private, no-store" ||
		recorder.Body.Len() == 0 {
		t.Fatalf(
			"schema response = %d, headers = %#v, body = %q",
			recorder.Code,
			recorder.Header(),
			recorder.Body.String(),
		)
	}

	assertJSONResponse(
		t,
		handler,
		http.MethodGet,
		"/api/schema/images/"+string(task.ID)+"-2.bin",
		"",
		http.StatusNotFound,
		map[string]any{"error": "schema-missing"},
	)
}

func createSucceededImageTask(
	t *testing.T,
	module *generation.Module,
	owner generation.OwnerID,
) generation.Task {
	t.Helper()
	accepted, err := submitHarnessTask(t, module,
		context.Background(),
		generation.SubmitCommand{
			OwnerID:        owner,
			Capability:     generation.TextToImage,
			OfferingID:     "openai/gpt-image-2/text-to-image",
			Input:          []byte(`{"prompt":"legacy generated image"}`),
			IdempotencyKey: "legacy-generated-image-" + string(owner),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "legacy-generated-image",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(
			context.Background(),
			owner,
			accepted.TaskID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == generation.TaskSucceeded {
			return task
		}
		if task.State.Terminal() {
			t.Fatalf("task = %#v", task)
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("任务未在测试期限内成功")
	return generation.Task{}
}

// 统一 Task 的原生产物也必须服从当前调用方的读取 scope。
func TestLegacyGeneratedImageNeverFallsBackForOwnedTasks(t *testing.T) {
	t.Parallel()
	// 普通用户（无 tasks.read.any）：任务归别人所有，必须拿不到。
	module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	task := createSucceededImageTask(t, module, "another-user")
	// 历史库里同时存在同名文件：回退一旦发生就一定会被它满足。
	// 返 404 而不是 403：连"这个任务存在"都不该泄露。
	filename := string(task.ID) + "_0.jpeg"
	assertLegacyGeneratedImageRefused(
		t, handler, filename, http.StatusNotFound,
	)

	// 对照：同一身份读自己名下的任务必须成功——否则上面的 404 可能只是
	// "这张图根本取不到"，证明不了拒绝来自归属校验。
	ownTask := createSucceededImageTask(t, module, "local-user")
	ownRecorder := httptest.NewRecorder()
	handler.ServeHTTP(
		ownRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/gpt-images/"+string(ownTask.ID)+"_0.jpeg",
			nil,
		),
	)
	if ownRecorder.Code != http.StatusOK {
		t.Fatalf(
			"读自己名下的任务被拒：%d %s",
			ownRecorder.Code, ownRecorder.Body,
		)
	}
}

func TestLegacyGeneratedImageRefusesUnknownTask(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	assertLegacyGeneratedImageRefused(
		t, handler, "unknown_0.png", http.StatusNotFound,
	)
}

func assertLegacyGeneratedImageRefused(
	t *testing.T,
	handler http.Handler,
	filename string,
	expected int,
) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet, "/api/gpt-images/"+filename, nil,
	)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != expected {
		t.Fatalf(
			"%s status=%d want=%d body=%q",
			filename, recorder.Code, expected, recorder.Body.String(),
		)
	}
	if strings.Contains(recorder.Body.String(), "historical-image") {
		t.Fatalf("%s 回退到了无归属校验的历史文件", filename)
	}
	if recorder.Header().Get("ETag") != "" ||
		recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("%s forbidden headers=%v", filename, recorder.Header())
	}
}

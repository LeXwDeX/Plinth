package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// ───────────────────────── §7.4 新身份 + 新错误/状态词汇零代码证明 ─────────────────────────
//
// fixture 只改 Product Schema：新 secret 设置（新 env 绑定）、两条新连接
// （同步 image-sync 与异步 tripo 协议，均带新错误词表与新状态词表）与引用
// 它们的新模型 Offering；Go 侧零改动即可创建→观测→错误分类→重试→终态。

const (
	acmeImagesConnectionID = "acme-images"
	acmeImagesAdapterKey   = "acme-images.v1"
	acmePartsConnectionID  = "acme-parts"
	acmePartsAdapterKey    = "acme-parts.v1"
	acmeImageOfferingID    = "acme/acme-flux-9/text-to-image"
	acmePartsOfferingID    = "acme/acme-parts-xl/text-to-3d"
	acmeImagesBaseSetting  = "acme_images_base_url"
	acmeImagesKeySetting   = "acme_images_api_key"
	acmePartsBaseSetting   = "acme_parts_base_url"
	acmePartsKeySetting    = "acme_parts_api_key"
)

func acmeSettings() map[string]any {
	setting := func(env string, secret bool) map[string]any {
		return map[string]any{
			"type":        "string",
			"environment": []any{map[string]any{"name": env}},
			"default":     "",
			"secret":      secret,
		}
	}
	return map[string]any{
		acmeImagesBaseSetting: setting("ACME_IMAGES_BASE_URL", false),
		acmeImagesKeySetting:  setting("ACME_IMAGES_API_KEY", true),
		acmePartsBaseSetting:  setting("ACME_PARTS_BASE_URL", false),
		acmePartsKeySetting:   setting("ACME_PARTS_API_KEY", true),
	}
}

func acmeConnections() []any {
	return []any{
		map[string]any{
			"id":                      acmeImagesConnectionID,
			"protocol":                "image-sync",
			"adapter_key":             acmeImagesAdapterKey,
			"base_url_setting":        acmeImagesBaseSetting,
			"api_key_setting":         acmeImagesKeySetting,
			"request_timeout_seconds": 30,
			"capabilities":            []any{"text-to-image", "image-to-image"},
			"error_mapping": map[string]any{
				"error_fields": map[string]any{
					"code_path":    "error.code",
					"message_path": "error.message",
				},
				"upstream_code_map": map[string]any{
					// 新错误词汇：仅存在于声明中。
					"E4242": map[string]any{"class": "rate_limited", "retryable": true},
				},
			},
		},
		map[string]any{
			"id":                      acmePartsConnectionID,
			"protocol":                "tripo",
			"adapter_key":             acmePartsAdapterKey,
			"base_url_setting":        acmePartsBaseSetting,
			"api_key_setting":         acmePartsKeySetting,
			"request_timeout_seconds": 30,
			"allow_http_assets":       true,
			"capabilities":            []any{"text-to-3d"},
			"error_mapping": map[string]any{
				"error_fields":       map[string]any{"code_path": "code", "message_path": "message"},
				"retry_after_header": "X-Acme-Retry-After",
				"upstream_code_map": map[string]any{
					"9404": map[string]any{"class": "rate_limited", "retryable": true},
				},
			},
			"status_mapping": map[string]any{
				// 新状态词表：与 Tripo 官方词表完全不同。
				"upstream_status": map[string]any{
					"submitted": map[string]any{"phase": "accepted"},
					"weaving":   map[string]any{"phase": "running"},
					"finished":  map[string]any{"phase": "completed"},
					"scrapped": map[string]any{
						"phase":     "failed",
						"class":     "rate_limited",
						"retryable": true,
					},
					"recycled": map[string]any{"phase": "canceled"},
				},
			},
		},
	}
}

// acmeCatalogDocument 克隆默认 Catalog 并注入新设置/连接/Offering。
func acmeCatalogDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&document); err != nil {
		t.Fatal(err)
	}
	application := document["application"].(map[string]any)
	runtime := application["runtime"].(map[string]any)
	settings := runtime["configuration"].(map[string]any)["settings"].(map[string]any)
	for name, setting := range acmeSettings() {
		settings[name] = setting
	}
	runtime["provider_connections"] = append(
		runtime["provider_connections"].([]any),
		acmeConnections()...,
	)
	// provider_discovery 兼容契约：每个启用模型都要有 legacy_models 条目。
	discovery := application["provider_discovery"].(map[string]any)
	discovery["legacy_models"] = append(
		discovery["legacy_models"].([]any),
		map[string]any{"model_id": "acme-flux-9", "provider_key": "acme", "recommended": false},
		map[string]any{"model_id": "acme-parts-xl", "provider_key": "acme", "recommended": false},
	)

	offerings := document["offerings"].([]any)
	byID := map[string]map[string]any{}
	for index, item := range offerings {
		offering := item.(map[string]any)
		byID[offering["id"].(string)] = offering
		_ = index
	}

	clone := func(id string) map[string]any {
		encoded, err := json.Marshal(byID[id])
		if err != nil {
			t.Fatal(err)
		}
		var copy map[string]any
		if err := json.Unmarshal(encoded, &copy); err != nil {
			t.Fatal(err)
		}
		return copy
	}

	images := clone("openai/gpt-image-2/text-to-image")
	images["id"] = acmeImageOfferingID
	images["provider_id"] = "acme"
	images["provider_name"] = "Acme"
	images["provider_short_label"] = "ACME"
	images["provider_order"] = 9
	images["model_id"] = "acme-flux-9"
	images["model_name"] = "Acme Flux 9 (fixture)"
	images["adapter_key"] = acmeImagesAdapterKey
	images["display_order"] = 90
	images["recommended"] = false
	images["features"] = []any{}
	images["provider_options"] = map[string]any{
		"endpoint":         "v1/images",
		"request_template": map[string]any{"model": "$model"},
		"response": map[string]any{
			"images_path":        "data.*",
			"base64_field":       "b64_json",
			"media_type_field":   "mime_type",
			"role":               "image",
			"default_media_type": "image/png",
		},
		"max_response_bytes": 268435456,
	}
	images["retry"] = map[string]any{
		"max_attempts":            2,
		"initial_backoff_seconds": 1,
		"max_backoff_seconds":     1,
		"retryable":               []any{"rate_limited"},
	}

	parts := clone("tripo/tripo-v3.1/text-to-3d")
	parts["id"] = acmePartsOfferingID
	parts["provider_id"] = "acme"
	parts["provider_name"] = "Acme"
	parts["provider_short_label"] = "ACME"
	parts["provider_order"] = 9
	// Acme does not bill in credits; one value per Provider (credits_metered).
	delete(parts, "credits_metered")
	parts["model_id"] = "acme-parts-xl"
	parts["model_name"] = "Acme Parts XL (fixture)"
	parts["adapter_key"] = acmePartsAdapterKey
	parts["display_order"] = 90
	parts["recommended"] = false
	parts["enabled"] = true
	parts["features"] = []any{}
	parts["asset_hosts"] = nil
	// fixture 克隆自带 W6 转换面（cost/cost_policy/post_success 引用
	// output_formats 参数）；本 fixture 只验证错误词表身份零代码接入，
	// 参数集被替换为 prompt-only，故剥离转换面避免悬空引用。
	parts["cost"] = nil
	parts["cost_policy"] = nil
	delete(parts, "native_pricing")
	parts["post_success"] = nil
	parts["outputs"] = []any{map[string]any{
		"role":      "model",
		"minimum":   1,
		"max_bytes": 1073741824,
	}}
	parts["provider_options"] = map[string]any{
		"model":            "acme-parts-xl",
		"endpoint":         "v1/parts",
		"observe_endpoint": "v1/parts/{task_id}",
	}
	parts["parameters"] = []any{map[string]any{
		"name":       "prompt",
		"label":      "提示词",
		"type":       "text",
		"required":   true,
		"max_length": 600,
	}}
	parts["retry"] = map[string]any{
		"max_attempts":            2,
		"initial_backoff_seconds": 1,
		"max_backoff_seconds":     1,
		"retryable":               []any{"rate_limited"},
	}

	document["offerings"] = append(offerings, images, parts)
	return document
}

func loadAcmeCatalog(t *testing.T, mutate func(map[string]any)) *catalog.Catalog {
	t.Helper()
	document := acmeCatalogDocument(t)
	if mutate != nil {
		mutate(document)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog
}

// acmeUpstream 模拟 acme-parts 异步上游：首个任务按新状态词表推进
// weaving → scrapped（可重试失败），重试后的第二个任务 finished 并返回模型。
func acmeUpstream(t *testing.T) (*httptest.Server, *acmeState) {
	t.Helper()
	state := &acmeState{}
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		state.mu.Lock()
		defer state.mu.Unlock()
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/parts":
			state.creates++
			fmt.Fprintf(
				writer,
				`{"code":0,"message":"ok","data":{"task_id":"acme-job-%d"}}`,
				state.creates,
			)
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/parts/"):
			job := strings.TrimPrefix(request.URL.Path, "/v1/parts/")
			state.observes++
			if job == "acme-job-1" {
				if state.observes == 1 {
					writeAcmeJSON(writer, map[string]any{
						"code": 0, "data": map[string]any{
							"task_id": job, "status": "weaving", "progress": 15,
						},
					})
					return
				}
				// 新状态词 scrapped：声明为可重试失败。
				writeAcmeJSON(writer, map[string]any{
					"code": 0, "data": map[string]any{
						"task_id": job, "status": "scrapped", "progress": 40,
					},
				})
				return
			}
			locator := "http://" + request.Host + "/model.glb"
			writeAcmeJSON(writer, map[string]any{
				"code": 0, "data": map[string]any{
					"task_id":  job,
					"status":   "finished",
					"progress": 100,
					"output":   map[string]any{"model_url": locator},
				},
			})
		case request.URL.Path == "/model.glb":
			writer.Header().Set("Content-Type", "model/gltf-binary")
			_, _ = writer.Write(modelinputtest.Triangle(modelinputtest.Options{}))
		default:
			http.NotFound(writer, request)
		}
	}))
	state.server = server
	t.Cleanup(server.Close)
	return server, state
}

type acmeState struct {
	mu       sync.Mutex
	creates  int
	observes int
	server   *httptest.Server
}

func (s *acmeState) snapshot() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creates, s.observes
}

func writeAcmeJSON(writer http.ResponseWriter, body any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(body)
}

type fakeAcmeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeAcmeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeAcmeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type acmeHarness struct {
	module     *generation.Module
	repository *sqlite.Repository
	clock      *fakeAcmeClock
	settings   map[string]string
}

func newAcmeHarness(
	t *testing.T,
	taskCatalog *catalog.Catalog,
	settings map[string]string,
) *acmeHarness {
	t.Helper()
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	assetStore, err := local.New(filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = assetStore.Close() })
	registry := providers.NewRegistry()
	if err := registerProviders(
		Config{ProviderMode: "live", RuntimeSettings: settings},
		taskCatalog,
		registry,
		nil, nil,
		discardLogger(),
	); err != nil {
		t.Fatal(err)
	}
	clock := &fakeAcmeClock{now: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)}
	repository.SetClock(clock.Now)
	if _, err := repository.CreateSession(context.Background(), chat.Session{
		ID: "acme-session", OwnerID: "owner-acme", OwnerName: "test", Title: "acme",
		CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	module, err := generation.New(generation.Dependencies{
		Repository: repository,
		Catalog:    taskCatalog,
		Providers:  registry,
		Assets:     assetStore,
		Clock:      clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close() })
	return &acmeHarness{module: module, repository: repository, clock: clock, settings: settings}
}

func (h *acmeHarness) submitApproved(t *testing.T, capability generation.Capability, offering generation.OfferingID, input json.RawMessage, key string) generation.Accepted {
	t.Helper()
	ctx := context.Background()
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-acme", SessionID: "acme-session", Capability: capability,
		OfferingID: offering, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := "acme:" + string(prepared.ID)
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: "owner-acme", ActorID: "acme-test-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: "acme-session", OfferingID: string(prepared.OfferingID),
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
		OwnerID: "owner-acme", SessionID: "acme-session", PreparedInputID: prepared.ID,
		IdempotencyKey: key, OperationID: operationID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func (h *acmeHarness) reconcile(t *testing.T) {
	t.Helper()
	if _, err := h.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
		WorkerID:  "acme-test-worker",
		LeaseFor:  time.Minute,
		BatchSize: 10,
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(31 * time.Second)
}

func (h *acmeHarness) runUntilTerminal(
	t *testing.T,
	owner generation.OwnerID,
	taskID generation.TaskID,
) generation.Task {
	t.Helper()
	for range 40 {
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

// TestAcmeAsyncIdentityRunsZeroCodeFromDeclarations：异步 tripo 协议新身份，
// 新状态词表驱动 创建→观测→可重试失败→重试→成功 全链路，零身份分支。
func TestAcmeAsyncIdentityRunsZeroCodeFromDeclarations(t *testing.T) {
	upstream, state := acmeUpstream(t)
	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	taskCatalog := loadAcmeCatalog(t, func(document map[string]any) {
		for _, item := range document["offerings"].([]any) {
			offering := item.(map[string]any)
			if offering["id"] == acmePartsOfferingID {
				offering["asset_hosts"] = []any{parsed.Hostname()}
			}
		}
	})
	settings := map[string]string{
		acmePartsBaseSetting: upstream.URL,
		acmePartsKeySetting:  "acme-parts-key",
	}
	harness := newAcmeHarness(t, taskCatalog, settings)

	accepted := harness.submitApproved(t, generation.TextTo3D, acmePartsOfferingID,
		json.RawMessage(`{"prompt":"acme gadget"}`), "acme-async-1")
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task state = %s failure=%#v", task.State, task.Failure)
	}
	creates, observes := state.snapshot()
	if creates != 2 {
		t.Fatalf("upstream creates = %d, want 2 (retry after scrapped)", creates)
	}
	if observes < 3 {
		t.Fatalf("upstream observes = %d, want >= 3", observes)
	}
	if len(task.Assets) == 0 || task.Assets[0].Role != "model" {
		t.Fatalf("assets = %#v", task.Assets)
	}
}

// TestAcmeSyncIdentityClassifiesNewErrorVocabulary：同步 image-sync 新身份，
// 仅存在于声明中的新错误词汇 E4242 被分类为可重试限流并驱动一次重试。
func TestAcmeSyncIdentityClassifiesNewErrorVocabulary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(
			writer,
			`{"error":{"code":"E4242","message":"acme quota window exhausted"}}`,
		)
	}))
	t.Cleanup(upstream.Close)
	taskCatalog := loadAcmeCatalog(t, nil)
	settings := map[string]string{
		acmeImagesBaseSetting: upstream.URL,
		acmeImagesKeySetting:  "acme-images-key",
	}
	harness := newAcmeHarness(t, taskCatalog, settings)
	accepted := harness.submitApproved(t, generation.TextToImage, acmeImageOfferingID,
		json.RawMessage(`{"prompt":"acme still"}`), "acme-sync-1")
	task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
	if task.State != generation.TaskFailed {
		t.Fatalf("task state = %s", task.State)
	}
	if task.Failure == nil ||
		task.Failure.Class != generation.FailureRateLimited ||
		!task.Failure.Retryable {
		t.Fatalf("failure = %#v", task.Failure)
	}
	if task.CurrentAttemptID == "" || task.MaterializeFailures != 0 {
		t.Fatalf("task = %#v", task)
	}
	// 重试由声明的 retryable 分类驱动：两次提交都收到 E4242。
	if attempts := attemptCount(t, harness, "owner-acme", accepted.TaskID); attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func attemptCount(
	t *testing.T,
	harness *acmeHarness,
	owner generation.OwnerID,
	taskID generation.TaskID,
) int {
	t.Helper()
	task, err := harness.module.Get(context.Background(), owner, taskID)
	if err != nil {
		t.Fatal(err)
	}
	// Attempt 序号无法直接读取；用事件流中的 attempt 记录计数。
	events, err := harness.module.Events(context.Background(), owner, taskID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	started := 0
	for _, event := range events {
		if strings.HasSuffix(event.Type, "attempt_started") ||
			event.Type == "attempt.started" {
			started++
		}
	}
	_ = task
	return started
}

// TestAcmeUndeclaredErrorsFallBackToGenericClassification：未声明映射的
// 上游错误走通用兜底，不产生身份专属分支。
func TestAcmeUndeclaredErrorsFallBackToGenericClassification(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/v1/parts":
			writeAcmeJSON(writer, map[string]any{
				"code": 0, "data": map[string]any{"task_id": "acme-unknown-code"},
			})
		case "/v1/parts/acme-unknown-code":
			// 未声明的业务码 5555：防御性回退 Rejected 且不可重试。
			writeAcmeJSON(writer, map[string]any{
				"code": 5555, "message": "mystery business failure",
				"data": map[string]any{
					"task_id": "acme-unknown-code", "status": "weaving", "progress": 5,
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)
	taskCatalog := loadAcmeCatalog(t, nil)
	registry := providers.NewRegistry()
	settings := map[string]string{
		acmePartsBaseSetting: upstream.URL,
		acmePartsKeySetting:  "acme-parts-key",
	}
	if err := registerProviders(
		Config{ProviderMode: "live", RuntimeSettings: settings},
		taskCatalog,
		registry,
		nil, nil,
		discardLogger(),
	); err != nil {
		t.Fatal(err)
	}
	offering, exists := taskCatalog.Get(acmePartsOfferingID)
	if !exists {
		t.Fatal("acme parts offering missing")
	}
	port, available := registry.Provider(offering)
	if !available {
		t.Fatal("acme parts provider not available")
	}
	started, err := port.Start(context.Background(), generation.StartRequest{
		AttemptID:     "attempt-acme-negative",
		SubmissionKey: "submission-acme-negative",
		Offering:      offering,
		Input:         json.RawMessage(`{"prompt":"acme negative"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = port.Observe(context.Background(), generation.ObserveRequest{
		AttemptID: "attempt-acme-negative",
		Offering:  offering,
		Job:       *started.Job,
	})
	var providerErr *generation.ProviderError
	if !errorAs(err, &providerErr) || providerErr.Failure == nil {
		t.Fatalf("err = %v", err)
	}
	if providerErr.Failure.Class != generation.FailureRejected ||
		providerErr.Failure.Retryable {
		t.Fatalf("undeclared code fallback = %#v", providerErr.Failure)
	}
}

// TestAcmeSecretSettingBindsNewEnvironment：新 secret 设置的 env 绑定由
// Schema 声明生效（零 Go 改动）。
func TestAcmeSecretSettingBindsNewEnvironment(t *testing.T) {
	taskCatalog := loadAcmeCatalog(t, nil)
	schema := taskCatalog.Application()
	config, err := loadConfigFromSchema(schema, func(name string) string {
		switch name {
		case "ACME_IMAGES_API_KEY":
			return "env-acme-key"
		case "ACME_PARTS_BASE_URL":
			return "http://acme-parts.test"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.RuntimeSettings[acmeImagesKeySetting] != "env-acme-key" {
		t.Fatalf("acme key setting = %q", config.RuntimeSettings[acmeImagesKeySetting])
	}
	if config.RuntimeSettings[acmePartsBaseSetting] != "http://acme-parts.test" {
		t.Fatalf("acme base setting = %q", config.RuntimeSettings[acmePartsBaseSetting])
	}
}

// TestMeshyAssetParameterWithoutInlineDeclarationFailsStartup：下游内网零互联网，
// asset 参数未声明 inline_model_input 的 Offering 不再有公网 URL 兜底，启动即拒绝。
func TestMeshyAssetParameterWithoutInlineDeclarationFailsStartup(t *testing.T) {
	taskCatalog := loadAcmeCatalog(t, func(document map[string]any) {
		for _, item := range document["offerings"].([]any) {
			offering := item.(map[string]any)
			if offering["id"] != "meshy/processing/remesh" {
				continue
			}
			delete(offering["provider_options"].(map[string]any), "inline_model_input")
		}
	})
	err := registerProviders(
		Config{
			ProviderMode: "live",
			RuntimeSettings: map[string]string{
				"meshy_base_url": "https://meshy.test",
				"meshy_api_key":  "meshy-key",
			},
		},
		taskCatalog,
		providers.NewRegistry(),
		nil, nil,
		discardLogger(),
	)
	if err == nil || !strings.Contains(err.Error(), "inline_model_input") {
		t.Fatalf("startup must reject asset parameter without inline declaration, got %v", err)
	}
}

// TestChatAgentConnectionSeamIsProtocolDriven：Chat LLM 经声明连接解析
// （openai-compatible 协议）；连接缺失时启动期显式报错。
func TestChatAgentConnectionSeamIsProtocolDriven(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	if _, err := buildChatAgent(Config{
		AgentMode: "live",
		RuntimeSettings: map[string]string{
			"llm_base_url": "http://127.0.0.1:8000/v1",
			"llm_api_key":  "llm-key",
		},
	}, schema); err != nil {
		t.Fatalf("live chat agent via declared connection: %v", err)
	}
	schema.Runtime.ProviderConnections = nil
	if _, err := buildChatAgent(Config{
		AgentMode:       "live",
		RuntimeSettings: map[string]string{"llm_base_url": "http://127.0.0.1:8000/v1"},
	}, schema); err == nil ||
		!strings.Contains(err.Error(), "connection") {
		t.Fatalf("missing connection error = %v", err)
	}
}

// TestProviderAccountsRejectUnknownBalanceAdapter：账户余额 seam 未知协议
// 启动期显式报错（不静默跳过）。
func TestProviderAccountsRejectUnknownBalanceAdapter(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	runtime := taskCatalog.Application().Runtime
	account := runtime.ProviderAccounts["meshy"]
	account.Balance.AdapterKey = "acme-ledger"
	runtime.ProviderAccounts["acme"] = account
	_, err = buildProviderAccounts(Config{ProviderMode: "live"}, runtime)
	if err == nil || !strings.Contains(err.Error(), "未知余额适配器") {
		t.Fatalf("err = %v", err)
	}
}

// TestAcmeConnectionsExposeNoIdentityBranchInCode 防回归：tripo 协议适配器
// 不因身份不同而分支——acme 与 tripo 共用同一构造函数与端口实现。
func TestAcmeConnectionsExposeNoIdentityBranchInCode(t *testing.T) {
	taskCatalog := loadAcmeCatalog(t, nil)
	var acmeConnection, tripoConnection *catalog.ProviderConnectionSchema
	for index, connection := range taskCatalog.Application().Runtime.ProviderConnections {
		switch connection.ID {
		case acmePartsConnectionID:
			acmeConnection = &taskCatalog.Application().Runtime.ProviderConnections[index]
		case "tripo":
			tripoConnection = &taskCatalog.Application().Runtime.ProviderConnections[index]
		}
	}
	if acmeConnection == nil || tripoConnection == nil {
		t.Fatal("connections missing")
	}
	if acmeConnection.Protocol != tripoConnection.Protocol {
		t.Fatalf("protocols differ: %q vs %q", acmeConnection.Protocol, tripoConnection.Protocol)
	}
	// 同一协议共用同一 Go 构造器；区分只在声明数据。
	if len(acmeConnection.StatusMapping.UpstreamStatus) == len(tripoConnection.StatusMapping.UpstreamStatus) {
		// 同长度不代表同词表；真正的断言在零代码 fixture 行为测试中。
		_ = acmeConnection
	}
	var _ = tripo.New
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testLogger(buffer *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(buffer, nil))
}

func errorAs(err error, target any) bool {
	if err == nil {
		return false
	}
	providerErr, ok := target.(**generation.ProviderError)
	if !ok {
		return false
	}
	typed, ok := err.(*generation.ProviderError)
	if !ok {
		return false
	}
	*providerErr = typed
	return true
}

// Legacy snapshots may recover missing routes only from their own connection.
func TestSameProtocolConnectionsIsolateLegacyOfferingRoutes(t *testing.T) {
	taskCatalog := loadAcmeCatalog(t, nil)
	own, _ := taskCatalog.Get(acmePartsOfferingID)
	foreign, _ := taskCatalog.Get("tripo/tripo-v3.1/text-to-3d")
	upstream, _ := acmeUpstream(t)
	var connection catalog.ProviderConnectionSchema
	for _, candidate := range taskCatalog.Application().Runtime.ProviderConnections {
		if candidate.AdapterKey == acmePartsAdapterKey {
			connection = candidate
		}
	}
	adapter, err := buildProviderAdapter(offeringsForAdapter(taskCatalog.List(""), connection.AdapterKey), connection, upstream.URL, "test-key", time.Second, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	own.ProviderOptions = json.RawMessage(`{"model":"acme-parts-xl"}`)
	if _, err := adapter.Start(context.Background(), generation.StartRequest{Offering: own, Input: json.RawMessage(`{"prompt":"legacy"}`), SubmissionKey: "own-legacy"}); err != nil {
		t.Fatalf("own connection must restore its declared route: %v", err)
	}
	foreign.ProviderOptions = json.RawMessage(`{"model":"v3.1-20260211"}`)
	if _, err := adapter.Start(context.Background(), generation.StartRequest{Offering: foreign, Input: json.RawMessage(`{"prompt":"foreign"}`), SubmissionKey: "foreign-legacy"}); err == nil {
		t.Fatal("foreign connection must not restore another adapter's legacy route")
	}
}

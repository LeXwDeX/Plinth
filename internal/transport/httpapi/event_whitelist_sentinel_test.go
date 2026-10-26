package httpapi_test

// event_whitelist_sentinel_test.go 落实计划 §11.9 P3 的永久哨兵（回顾性探针
// TestRetrospectiveEventsMustNotExposePrivateOpaque 的永久化）：以真实非空的
// Tripo 成功后任务（内核原始事件必须真实携带私有 Opaque 字节与
// post_success.* 数据，否则负断言无意义）驱动 REST `GET /api/v1/tasks/{id}/events`
// 与 legacy SSE 流两个公开面，断言：
//
//	(a) NEGATIVE——两面均不出现私有事件数据字段（opaque/identifier/
//	    downloadable 及检查点、去重、上游取证、内部调度、原始定位符键）；
//	(b) POSITIVE——公开事件流保持信息量（核心生命周期事件与其公开字段
//	    仍在，防止"全过滤空响应"造成的虚假绿）。
//
// 事件类型 + 字段白名单的唯一真源是 catalog 声明
// `application.runtime.task_events.public_projection`（§8.1 SSE·事件行的
// Schema 归属）；声明形状与逐事件一致性由
// TestPublicEventWhitelistIsSchemaDeclaredAndEnforced 断言。SSE 面与 REST 面
// 使用同一份哨兵清单（SSE↔REST parity：SSE 只承载 legacy 任务投影，不得
// 出现任何白名单之外的私有事件字段；legacy 投影字节兼容不变）。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// privateEventDataSentinels 是公开事件面（REST 与 SSE）禁止出现的私有
// JSON 键哨兵：Provider 私有 Opaque 检查点（含 mock/tripo 的
// identifier/downloadable/source_name/successful_ordinal 字段）、上游签名
// 定位符、内部去重/调度键、上游 remote 标识、上游取证字段与派发回执。
var privateEventDataSentinels = []string{
	`"opaque"`,
	`"identifier"`,
	`"downloadable"`,
	`"source_name"`,
	`"successful_ordinal"`,
	`"locator"`,
	`"dedupe_key"`,
	`"remote_id"`,
	`"claim_id"`,
	`"upstream_status"`,
	`"upstream_code"`,
	`"upstream_excerpt"`,
	`"ack_token"`,
	`"dispatch"`,
}

func submitTripoFullFormatEventTask(t *testing.T, module *generation.Module) generation.TaskID {
	t.Helper()
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d",
		Input:      []byte(`{"prompt":"private event sentinel","output_formats":["GLTF","USDZ","FBX","OBJ","STL","3MF"],"authorize_extra_credits":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcileLegacyTask(t, module, accepted.TaskID, "private-event-full-formats")
	task, err := module.Get(context.Background(), "owner-a", accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskSucceeded {
		t.Fatalf("full-format fixture task state=%s failure=%#v", task.State, task.Failure)
	}
	return accepted.TaskID
}

func TestPublicEventSurfacesNeverExposePrivateFields(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(t, nil, nil, nil, nil, nil)
	taskID := submitTripoFullFormatEventTask(t, module)

	// fixture 真实性（负断言非空洞的前提）：内核原始事件必须真实携带
	// 私有 Opaque 字节与 post_success.* 事件数据。
	rawEvents, err := module.Events(context.Background(), "owner-a", taskID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var rawBody strings.Builder
	observed := false
	for _, event := range rawEvents {
		rawBody.Write(event.Data)
		if event.Type == "provider.observe" {
			observed = true
		}
	}
	if !observed {
		t.Fatal("fixture must produce a real provider observation event")
	}
	for _, sentinel := range privateEventDataSentinels {
		if strings.Contains(rawBody.String(), sentinel) {
			t.Fatalf("provider event summary leaked private field %s: %s", sentinel, rawBody.String())
		}
	}

	// NEGATIVE + POSITIVE：REST 公开事件面。
	events := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/v1/tasks/"+string(taskID)+"/events",
		nil,
	)
	if events.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", events.Code, events.Body.String())
	}
	assertNoPrivateEventFields(t, "rest-events", events.Body.String())

	var payload struct {
		Events []struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(events.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v body=%s", err, events.Body.String())
	}
	if len(payload.Events) < 4 {
		t.Fatalf(
			"public events must stay informative, got %d: %s",
			len(payload.Events), events.Body.String(),
		)
	}
	seen := map[string]map[string]json.RawMessage{}
	for _, event := range payload.Events {
		if _, duplicate := seen[event.Type]; !duplicate {
			seen[event.Type] = event.Data
		}
	}
	for _, expected := range []string{
		"task.submitted", "attempt.started", "provider.observe", "task.succeeded",
	} {
		data, ok := seen[expected]
		if !ok {
			t.Fatalf("public events missing %s: %s", expected, events.Body.String())
		}
		if len(data) == 0 {
			t.Fatalf("event %s lost all public data: %s", expected, events.Body.String())
		}
	}
	requireEventDataKeys(t, seen["task.submitted"], "offering_id", "capability")
	requireEventDataKeys(t, seen["task.succeeded"], "asset_count")

	// NEGATIVE：SSE 面（parity）。默认终态断流契约不变，且流内容不得
	// 携带任何私有事件字段。
	stream := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/stream/text-to-3d/"+string(taskID),
		nil,
	)
	if stream.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	if decoded := decodeTaskSSEEvents(t, stream.Body.String()); len(decoded) != 1 {
		t.Fatalf("default stream must close after terminal event; events=%d", len(decoded))
	}
	assertNoPrivateEventFields(t, "sse-stream", stream.Body.String())
}

func assertNoPrivateEventFields(t *testing.T, surface string, body string) {
	t.Helper()
	for _, sentinel := range privateEventDataSentinels {
		if strings.Contains(body, sentinel) {
			t.Errorf("%s surface exposes private event field %s", surface, sentinel)
		}
	}
}

func requireEventDataKeys(
	t *testing.T,
	data map[string]json.RawMessage,
	keys ...string,
) {
	t.Helper()
	for _, key := range keys {
		if _, ok := data[key]; !ok {
			t.Errorf("public event data missing key %q: %v", key, data)
		}
	}
}

// TestPublicEventWhitelistIsSchemaDeclaredAndEnforced 是 P3 的正向哨兵：
// 白名单必须是 catalog 声明（application.runtime.task_events.
// public_projection）且非空、显式；REST 公开事件面逐事件符合声明
// （未声明类型绝不出现、类型内未声明字段绝不出现），并保留多个声明
// 类型——证明过滤真实消费声明而非"全滤空"造成的虚假绿。
func TestPublicEventWhitelistIsSchemaDeclaredAndEnforced(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	projection := application.Runtime.TaskEvents.PublicProjection
	if len(projection) == 0 {
		t.Fatal("task_events.public_projection must declare a nonempty whitelist")
	}
	declared := map[string]map[string]struct{}{}
	for _, entry := range projection {
		if strings.TrimSpace(entry.Type) == "" || len(entry.Fields) == 0 {
			t.Fatalf("whitelist entry must be explicit: %#v", entry)
		}
		if _, duplicate := declared[entry.Type]; duplicate {
			t.Fatalf("duplicate whitelist type %q", entry.Type)
		}
		fields := make(map[string]struct{}, len(entry.Fields))
		for _, field := range entry.Fields {
			fields[field] = struct{}{}
		}
		declared[entry.Type] = fields
	}
	// 检查点/去重/内部调度/私有子作业生命周期类型必须保持未声明（私有）。
	for _, internal := range []string{
		"provider.dispatch_receipt_rejected",
		"provider.observation_deduplicated",
		"post_success.receipt_rejected",
		"post_success.observation_dropped",
		"post_success.schedule_cleared",
	} {
		if _, ok := declared[internal]; ok {
			t.Errorf("internal event type %q must not be declared public", internal)
		}
	}

	module, handler, _, _ := newHTTPHarnessWithServices(t, nil, nil, nil, nil, nil)
	taskID := submitTripoFullFormatEventTask(t, module)

	events := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/v1/tasks/"+string(taskID)+"/events",
		nil,
	)
	if events.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", events.Code, events.Body.String())
	}
	var payload struct {
		Events []struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(events.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode events: %v body=%s", err, events.Body.String())
	}
	if len(payload.Events) == 0 {
		t.Fatalf("public events must not be empty: %s", events.Body.String())
	}
	seen := map[string]int{}
	for _, event := range payload.Events {
		fields, ok := declared[event.Type]
		if !ok {
			t.Fatalf("non-whitelisted event type %q exposed: %s", event.Type, events.Body.String())
		}
		for key := range event.Data {
			if _, allowed := fields[key]; !allowed {
				t.Fatalf(
					"non-whitelisted field %q exposed on %q: %s",
					key, event.Type, events.Body.String(),
				)
			}
		}
		seen[event.Type]++
	}
	if len(seen) < 4 {
		t.Fatalf(
			"whitelist must stay informative (types seen=%d): %s",
			len(seen), events.Body.String(),
		)
	}
	for _, expected := range []string{
		"task.submitted", "attempt.started", "provider.observe", "task.succeeded",
	} {
		if seen[expected] == 0 {
			t.Fatalf("declared type %q missing from public events: %s", expected, events.Body.String())
		}
	}
	assertNoPrivateEventFields(t, "rest-events-schema", events.Body.String())
}

package httpapi

// event_whitelist_projection_test.go 是 P3 的单元级投影哨兵（内部包，直接
// 驱动 publicEvents）：以合成的真实形状事件数据（observationEventData 的
// 完整 Observation 字节、Failure 原始取证字段、post_success.asset_failed
// 的原始错误 detail、内部调度/检查点类型）逐项验证 catalog 白名单声明
// （application.runtime.task_events.public_projection）被真实消费：
// 未声明类型整体丢弃、声明类型只保留声明字段、failure 形状仍走
// publicFailure 文案投影。本文件不复制任何白名单内容（无第二真源）。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPublicEventsProjectsThroughSchemaWhitelist(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	server := &Server{
		application: application,
		taskEventWhitelist: application.Runtime.TaskEvents.
			PublicEventWhitelist(),
	}

	internalEvents := []generation.Event{
		{Type: "provider.dispatch_receipt_rejected", Data: json.RawMessage(`{"reason":"stale_or_mismatched_dispatch_receipt"}`)},
		{Type: "provider.observation_deduplicated", Data: json.RawMessage(`{"dedupe_key":"remote-1:running"}`)},
		{Type: "post_success.receipt_rejected", Data: json.RawMessage(`{"reason":"stale_or_mismatched_dispatch_receipt"}`)},
		{Type: "post_success.observation_dropped", Data: json.RawMessage(`{"phase":"running"}`)},
		{Type: "post_success.schedule_cleared", Data: json.RawMessage(`{"reason":"no_pending_resources"}`)},
		{Type: "task.type_never_declared", Data: json.RawMessage(`{"anything":true}`)},
	}
	leakShapedEvents := []generation.Event{
		{Type: "provider.observe", Data: json.RawMessage(`{"sequence":3,"observed_at":"2026-09-21T00:00:00Z","phase":"completed","job":{"provider_id":"tripo","remote_id":"remote-1"},"progress":100,"artifacts":[{"id":"remote-1:primary","role":"model","variant":"glb","locator":"https://upstream.example/signed?token=SENTINEL_TOKEN","media_type":"model/gltf-binary","metadata":{"identifier":"remote-1"}}],"dedupe_key":"remote-1:completed","opaque":[{"identifier":"remote-1","status":"SUCCEEDED","downloadable":true,"source_name":"glb","successful_ordinal":1}]}`)},
		{Type: "post_success.asset_failed", Data: json.RawMessage(`{"role":"converted_model","variant":"converted.fbx","detail":"open https://upstream.example/signed?token=SENTINEL_TOKEN: SENTINEL_RAW_ERROR"}`)},
		{Type: "materialization.artifact_skipped", Data: json.RawMessage(`{"claim_id":"remote-1:primary","role":"model","variant":"glb","reason":"download"}`)},
		{Type: "materialization.claims_refreshed", Data: json.RawMessage(`{"claim_count":2,"dedupe_key":"remote-1:refresh"}`)},
		{Type: "attempt.failed", Data: json.RawMessage(`{"class":"upstream_unavailable","retryable":true,"detail":"SENTINEL_SAFE_DETAIL","upstream_status":503,"upstream_code":"SENTINEL_UPSTREAM_CODE","upstream_excerpt":"SENTINEL_UPSTREAM_EXCERPT"}`)},
	}
	publicEvents := server.publicEvents("",
		append(append([]generation.Event{}, internalEvents...), leakShapedEvents...),
	)
	if len(publicEvents) != len(leakShapedEvents) {
		types := make([]string, 0, len(publicEvents))
		for _, event := range publicEvents {
			types = append(types, event.Type)
		}
		t.Fatalf(
			"public events = %v; undeclared types must be dropped entirely",
			types,
		)
	}
	byType := map[string]json.RawMessage{}
	for _, event := range publicEvents {
		byType[event.Type] = event.Data
	}

	var observe map[string]any
	if err := json.Unmarshal(byType["provider.observe"], &observe); err != nil {
		t.Fatal(err)
	}
	if len(observe) != 2 || observe["phase"] != "completed" || observe["progress"] != float64(100) {
		t.Fatalf("provider.observe projection = %#v", observe)
	}

	var assetFailed map[string]any
	if err := json.Unmarshal(byType["post_success.asset_failed"], &assetFailed); err != nil {
		t.Fatal(err)
	}
	if len(assetFailed) != 2 ||
		assetFailed["role"] != "converted_model" ||
		assetFailed["variant"] != "converted.fbx" {
		t.Fatalf("post_success.asset_failed projection = %#v", assetFailed)
	}

	var skipped map[string]any
	if err := json.Unmarshal(byType["materialization.artifact_skipped"], &skipped); err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 2 || skipped["role"] != "model" || skipped["reason"] != "download" {
		t.Fatalf("materialization.artifact_skipped projection = %#v", skipped)
	}

	var refreshed map[string]any
	if err := json.Unmarshal(byType["materialization.claims_refreshed"], &refreshed); err != nil {
		t.Fatal(err)
	}
	if len(refreshed) != 1 || refreshed["claim_count"] != float64(2) {
		t.Fatalf("materialization.claims_refreshed projection = %#v", refreshed)
	}

	var failure map[string]any
	if err := json.Unmarshal(byType["attempt.failed"], &failure); err != nil {
		t.Fatal(err)
	}
	if failure["class"] != "upstream_unavailable" || failure["retryable"] != true {
		t.Fatalf("attempt.failed projection = %#v", failure)
	}
	if _, ok := failure["detail"]; !ok {
		t.Fatalf("attempt.failed must keep catalog message detail: %#v", failure)
	}
	for _, forbidden := range []string{
		"upstream_status", "upstream_code", "upstream_excerpt",
	} {
		if _, ok := failure[forbidden]; ok {
			t.Fatalf("attempt.failed leaks forensic field %s: %#v", forbidden, failure)
		}
	}

	encoded, err := json.Marshal(publicEvents)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, sentinel := range []string{
		"SENTINEL_TOKEN", "SENTINEL_RAW_ERROR", "SENTINEL_SAFE_DETAIL",
		"SENTINEL_UPSTREAM_CODE", "SENTINEL_UPSTREAM_EXCERPT",
		`"opaque"`, `"identifier"`, `"downloadable"`, `"source_name"`,
		`"successful_ordinal"`, `"locator"`, `"dedupe_key"`, `"remote_id"`,
		`"claim_id"`, `"ack_token"`, `"dispatch"`, `"job"`, `"artifacts"`,
		`"no_pending_resources"`, `"stale_or_mismatched_dispatch_receipt"`,
	} {
		if strings.Contains(body, sentinel) {
			t.Errorf("projected public events expose %s: %s", sentinel, body)
		}
	}
}

func TestPublicEventsFailsClosedOnNonObjectData(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	server := &Server{
		application: application,
		taskEventWhitelist: application.Runtime.TaskEvents.
			PublicEventWhitelist(),
	}
	projected := server.publicEvents("", []generation.Event{
		{Type: "task.submitted", Data: json.RawMessage(`["not","an","object"]`)},
		{Type: "task.submitted", Data: nil},
	})
	for _, event := range projected {
		if string(event.Data) != "{}" {
			t.Fatalf("non-object data must collapse to {}: %s", event.Data)
		}
	}
}

package wirefixture_test

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func projectionPolicy() wirefixture.Policy {
	policy := testPolicy()
	delete(policy.Aliases, "api.meshy.ai")
	policy.PublicHosts = map[string]bool{"api.meshy.ai": true, "assets.meshy.ai": true}
	policy.QueryKeys = map[string]bool{"X-Amz-Signature": true}
	policy.Projection = map[string]string{
		"id": "id", "model_urls": "url", "thumbnail_url": "url",
		"created_at": "timestamp", "preceding_tasks": "number", "texture_image_url": "url",
	}
	for _, key := range []string{"model_urls", "thumbnail_url", "glb", "png", "created_at", "preceding_tasks", "texture_image_url"} {
		policy.Keys[key] = true
	}
	for _, value := range []string{"openapi", "v2", "text-to-3d", "PENDING", "SUCCEEDED"} {
		policy.Allowed[value] = true
	}
	return policy
}

func TestDeferredProjectionProducesPublicReplay(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	capture(t, recorder, mustRequest(t, http.MethodPost, "https://api.meshy.ai/openapi/v2/text-to-3d", ""),
		jsonResponse(200, "application/json", `{"id":"raw-task-1","status":"PENDING"}`))
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/raw-task-1", ""),
		jsonResponse(200, "application/json", `{"id":"raw-task-1","status":"SUCCEEDED","model_urls":{"glb":"https://assets.meshy.ai/model.glb?X-Amz-Signature=secret"},"thumbnail_url":"https://assets.meshy.ai/thumb.png?X-Amz-Signature=secret","created_at":1700000000001,"preceding_tasks":3,"texture_image_url":""}`))
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://assets.meshy.ai/model.glb?X-Amz-Signature=secret", ""),
		jsonResponse(200, "application/json", `{"status":"SUCCEEDED"}`))
	if err := recorder.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	exchanges := recorder.Exchanges()
	if len(exchanges) != 3 {
		t.Fatalf("exchanges = %d", len(exchanges))
	}
	if !bytes.Contains(exchanges[0].Response.Body.JSON, []byte(`"id-1"`)) {
		t.Fatalf("create response = %s", exchanges[0].Response.Body.JSON)
	}
	if exchanges[1].Request.Path != "/openapi/v2/text-to-3d/id-1" {
		t.Fatalf("poll path = %q", exchanges[1].Request.Path)
	}
	if bytes.Contains(exchanges[1].Response.Body.JSON, []byte("secret")) ||
		!bytes.Contains(exchanges[1].Response.Body.JSON, []byte(`https://assets.meshy.ai/fixture-1.glb`)) ||
		!bytes.Contains(exchanges[1].Response.Body.JSON, []byte(`https://assets.meshy.ai/fixture-2.png`)) ||
		!bytes.Contains(exchanges[1].Response.Body.JSON, []byte(`"created_at":1700000000000`)) ||
		!bytes.Contains(exchanges[1].Response.Body.JSON, []byte(`"preceding_tasks":0`)) ||
		!bytes.Contains(exchanges[1].Response.Body.JSON, []byte(`"texture_image_url":""`)) {
		t.Fatalf("poll response = %s", exchanges[1].Response.Body.JSON)
	}
	if exchanges[2].Request.Host != "assets.meshy.ai" || exchanges[2].Request.Path != "/fixture-1.glb" {
		t.Fatalf("download request = %s%s", exchanges[2].Request.Host, exchanges[2].Request.Path)
	}
	if err := wirefixture.WriteCandidate(t.TempDir(), t.TempDir()+"/candidate.json", exchanges, policy); err == nil {
		t.Fatal("active projection accepted an already projected candidate")
	}

	replayPolicy := policy
	replayPolicy.Projection = nil
	replayPolicy.Aliases = nil
	replayPolicy.Allowed = maps.Clone(policy.Allowed)
	for _, value := range []string{
		"id-1", "fixture-1.glb", "fixture-2.png",
		"https://assets.meshy.ai/fixture-1.glb", "https://assets.meshy.ai/fixture-2.png",
		"1700000000000", "0",
	} {
		replayPolicy.Allowed[value] = true
	}
	replay, err := wirefixture.NewReplay(exchanges, replayPolicy)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		mustRequest(t, http.MethodPost, "https://api.meshy.ai/openapi/v2/text-to-3d", ""),
		mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/id-1", ""),
		mustRequest(t, http.MethodGet, "https://assets.meshy.ai/fixture-1.glb", ""),
	} {
		response, err := replay.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredProjectionRejectsUnregisteredQuery(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task?X-Amz-Other=1", ""),
		jsonResponse(200, "application/json", `{"id":"task","status":"PENDING"}`))
	if err := recorder.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("unregistered query = %v", err)
	}
}

func TestDeferredProjectionRejectsUnknownURLKey(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task", ""),
		jsonResponse(200, "application/json", `{"id":"task","status":"SUCCEEDED","model_urls":{"glb":"https://assets.meshy.ai/a.glb","evil":"https://assets.meshy.ai/b.glb"}}`))
	if err := recorder.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("unknown URL key = %v", err)
	}
}

func TestDeferredProjectionRejectsSensitiveField(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	policy.Keys["token"] = true
	policy.Projection["token"] = "id"
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task", ""),
		jsonResponse(200, "application/json", `{"id":"task","status":"PENDING"}`))
	if err := recorder.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("sensitive projection = %v", err)
	}
}

func TestDeferredProjectionRejectsUnregisteredHost(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	delete(policy.PublicHosts, "assets.meshy.ai")
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task", ""),
		jsonResponse(200, "application/json", `{"id":"task","status":"SUCCEEDED","model_urls":{"glb":"https://assets.meshy.ai/a.glb"}}`))
	if err := recorder.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("unregistered host = %v", err)
	}
}

func TestDeferredProjectionRejectsNonIdentifier(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task", ""),
		jsonResponse(200, "application/json", `{"id":"raw task","status":"PENDING"}`))
	if err := recorder.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("non-identifier = %v", err)
	}
}

func TestDeferredProjectionSupportsIDListsAndStringTimestamps(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	for _, key := range []string{"uuid", "uuids", "submit_time"} {
		policy.Keys[key] = true
	}
	policy.Projection["uuid"] = "id"
	policy.Projection["uuids"] = "ids"
	policy.Projection["submit_time"] = "timestamp"
	capture(t, recorder, mustRequest(t, http.MethodPost, "https://api.meshy.ai/openapi/v2/text-to-3d", ""),
		jsonResponse(200, "application/json", `{"uuid":"raw-task-1","uuids":["raw-task-2","raw-task-1"],"submit_time":"2026-10-26T12:00:00Z"}`))
	if err := recorder.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	body := recorder.Exchanges()[0].Response.Body.JSON
	for _, want := range []string{`"uuid":"id-1"`, `"uuids":["id-2","id-1"]`, `"submit_time":"2026-01-01T00:00:00Z"`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("projected body %s lacks %s", body, want)
		}
	}
	if bytes.Contains(body, []byte("raw-task")) {
		t.Fatalf("raw identifier escaped: %s", body)
	}
}

func TestDeferredProjectionRejectsMalformedStringTimestamp(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	policy.Keys["submit_time"] = true
	policy.Projection["submit_time"] = "timestamp"
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task", ""),
		jsonResponse(200, "application/json", `{"submit_time":"2026/10/26 12:00"}`))
	if err := recorder.Finalize(policy); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("malformed timestamp = %v", err)
	}
}

func TestDeferredProjectionReplacesRegisteredText(t *testing.T) {
	recorder := deferredRecorder(t)
	policy := projectionPolicy()
	policy.Keys["message"] = true
	policy.Projection["message"] = "text"
	policy.Allowed["task"] = true
	capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/openapi/v2/text-to-3d/task", ""),
		jsonResponse(200, "application/json", `{"message":"private upstream detail"}`))
	if err := recorder.Finalize(policy); err != nil {
		t.Fatal(err)
	}
	body := recorder.Exchanges()[0].Response.Body.JSON
	if !bytes.Contains(body, []byte(`"message":"ok"`)) || bytes.Contains(body, []byte("private")) {
		t.Fatalf("text projection = %s", body)
	}
}

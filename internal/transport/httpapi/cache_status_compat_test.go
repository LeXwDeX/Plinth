package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyCacheStatusUsesProductSchemaRouteAndWireFields(t *testing.T) {
	t.Parallel()
	_, handler, _, cacheService := newHTTPHarnessWithServices(
		t,
		nil,
		func(schema *catalog.ApplicationSchema) {
			schema.Runtime.HTTP.LegacyRoutes.CacheStatus = "/api/schema/cache"
			schema.Runtime.Cache.Fields.ActiveDownloads = "running"
			schema.Runtime.Cache.Fields.ActiveItems = "entries"
			schema.Runtime.Cache.Fields.Filename = "file"
			schema.Runtime.Cache.Fields.Elapsed = "age"
			schema.Runtime.Cache.Fields.TaskID = "job"
			schema.Runtime.Cache.Fields.TaskType = "kind"
			schema.Runtime.Cache.Fields.ProviderID = "source"
			schema.Runtime.Cache.Fields.AssetKind = "asset"
			schema.Runtime.Cache.Fields.TotalCachedFiles = "files"
			schema.Runtime.Cache.Fields.TotalCacheSizeMB = "size"
		},
		nil,
		nil,
		nil,
	)
	finish := cacheService.Begin(cachemanagement.Operation{
		Filename:   "preview.png",
		TaskID:     "task-a",
		TaskType:   "text-to-image",
		ProviderID: "openai",
		AssetKind:  "image",
	})
	defer finish()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/schema/cache", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["running"] != float64(1) ||
		body["files"] != float64(0) ||
		body["size"] != float64(0) {
		t.Fatalf("cache status = %#v", body)
	}
	entries, ok := body["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %#v", body["entries"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok ||
		entry["file"] != "preview.png" ||
		entry["job"] != "task-a" ||
		entry["kind"] != "text-to-image" ||
		entry["source"] != "openai" ||
		entry["asset"] != "image" {
		t.Fatalf("entry = %#v", entries[0])
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(
		missing,
		httptest.NewRequest(http.MethodGet, "/api/cache-status", nil),
	)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("default route status = %d", missing.Code)
	}
}

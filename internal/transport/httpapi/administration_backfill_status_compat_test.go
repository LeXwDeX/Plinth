package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAdministrationBackfillStatusRouteWireTextAndRulesUseProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.AdminBackfillStatus =
				"/configured/admin/reconcile"
			schema := &application.Administration.BackfillStatus
			schema.API.Fields.Error = "failure"
			schema.API.Fields.Message = "notice"
			schema.API.Fields.Fixed = "repairs"
			schema.API.Fields.Total = "examined"
			schema.API.Fields.SyncFixed = "instant"
			schema.API.Fields.AsyncFixed = "polled"
			schema.API.Fields.Unchanged = "remaining"
			schema.Statuses.Pending = "WAITING"
			schema.Statuses.Succeeded = "DONE"
			schema.SyncProviderIDs = []string{"instant-provider"}
			schema.CacheResultFields.ModelURLs = "models"
			schema.CacheResultFields.ThumbnailURL = "preview"
			schema.CacheResultFields.ImageURLs = "pictures"
			schema.CacheResultFields.TextureURLs = "textures"
			schema.CacheResultFields.BaseColor = "albedo"
			schema.CacheResultFields.VideoURL = "movie"
			schema.Messages.NoPending = "schema says nothing to repair"
			schema.Messages.Complete = "schema says repair complete"
			schema.Messages.InternalError = "schema says repair failed"
		},
		nil,
		nil,
		nil,
		seedAdministrationBackfillStatusHTTPContract,
	)

	response := administrationRequest(
		handler,
		http.MethodPost,
		"/configured/admin/reconcile",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["notice"] != "schema says repair complete" ||
		body["examined"] != float64(3) ||
		body["instant"] != float64(1) ||
		body["polled"] != float64(1) ||
		body["remaining"] != float64(1) {
		t.Fatalf("body = %#v", body)
	}
	for _, hardCoded := range []string{
		"message",
		"total",
		"syncFixed",
		"asyncFixed",
		"unchanged",
	} {
		if _, leaked := body[hardCoded]; leaked {
			t.Fatalf("hard-coded field %q leaked: %#v", hardCoded, body)
		}
	}

	second := administrationRequest(
		handler,
		http.MethodPost,
		"/configured/admin/reconcile",
		"",
		nil,
	)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	var secondBody map[string]any
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatal(err)
	}
	if secondBody["notice"] != "schema says repair complete" ||
		secondBody["examined"] != float64(1) ||
		secondBody["instant"] != float64(0) ||
		secondBody["polled"] != float64(0) ||
		secondBody["remaining"] != float64(1) {
		t.Fatalf("second body = %#v", secondBody)
	}
	if _, included := secondBody["repairs"]; included {
		t.Fatalf("active response changed legacy shape: %#v", secondBody)
	}
}

func TestAdministrationBackfillStatusNoPendingShapeUsesProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.AdminBackfillStatus =
				"/configured/admin/no-pending"
			schema := &application.Administration.BackfillStatus
			schema.API.Fields.Message = "notice"
			schema.API.Fields.Fixed = "repairs"
			schema.Messages.NoPending = "schema says nothing to repair"
		},
		nil,
		nil,
		nil,
	)
	response := administrationRequest(
		handler,
		http.MethodPost,
		"/configured/admin/no-pending",
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["notice"] != "schema says nothing to repair" ||
		body["repairs"] != float64(0) ||
		len(body) != 2 {
		t.Fatalf("body = %#v", body)
	}
}

func seedAdministrationBackfillStatusHTTPContract(
	t *testing.T,
	databasePath string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE usage_ledger (
			id TEXT PRIMARY KEY,
			userId TEXT NOT NULL,
			userName TEXT NOT NULL,
			userEmail TEXT NOT NULL,
			taskType TEXT NOT NULL,
			taskId TEXT NOT NULL,
			providerId TEXT NOT NULL,
			estimatedCredits INTEGER NOT NULL,
			taskStatus TEXT NOT NULL,
			params TEXT NOT NULL,
			resultUrls TEXT NOT NULL,
			createdAt,
			finishedAt
		);
		CREATE TABLE task_cache (
			taskId TEXT PRIMARY KEY,
			taskType TEXT NOT NULL,
			status TEXT NOT NULL,
			providerId TEXT NOT NULL,
			data TEXT NOT NULL,
			cachedAt,
			updatedAt
		);
		INSERT INTO usage_ledger (
			id, userId, userName, userEmail, taskType, taskId,
			providerId, estimatedCredits, taskStatus, params,
			resultUrls, createdAt, finishedAt
		) VALUES
			('sync-ledger', 'owner', 'Alice', '', 'text-to-image',
			 'sync-task', 'instant-provider', 1, 'WAITING', '{}', '[]', 1, NULL),
			('async-ledger', 'owner', 'Alice', '', 'image-to-3d',
			 'async-task', 'remote-provider', 1, 'WAITING', '{}', '[]', 2, NULL),
			('active-ledger', 'owner', 'Alice', '', 'image-to-3d',
			 'active-task', 'remote-provider', 1, 'WAITING', '{}', '[]', 3, NULL);
		INSERT INTO task_cache (
			taskId, taskType, status, providerId, data, cachedAt, updatedAt
		) VALUES
			('async-task', 'image-to-3d', 'DONE', 'remote-provider',
			 '{"models":{"glb":"https://assets.test/result.glb"},"preview":"https://assets.test/preview.png","textures":[{"albedo":"https://assets.test/albedo.png"}]}',
			 2, 2),
			('active-task', 'image-to-3d', 'WAITING', 'remote-provider',
			 '{}', 3, 3)
	`); err != nil {
		t.Fatal(err)
	}
}

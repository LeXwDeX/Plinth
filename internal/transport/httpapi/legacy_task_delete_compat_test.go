package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyTaskDeleteContractHidesTaskFromItemAndHistory(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"delete me"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	deleted := serveLegacyRequest(
		handler,
		http.MethodDelete,
		"/api/meshy/text-to-image/"+string(accepted.TaskID),
		nil,
	)
	if deleted.Code != http.StatusOK ||
		deleted.Body.String() != "{\"success\":true}\n" {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}

	item := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/text-to-image/"+string(accepted.TaskID),
		nil,
	)
	if item.Code != http.StatusNotFound {
		t.Fatalf("item status = %d, body = %s", item.Code, item.Body.String())
	}
	history := serveLegacyRequest(handler, http.MethodGet, "/api/tasks", nil)
	if history.Code != http.StatusOK ||
		strings.Contains(history.Body.String(), string(accepted.TaskID)) {
		t.Fatalf("history status = %d, body = %s", history.Code, history.Body.String())
	}
}

func TestLegacyTaskDeleteScopePreservesNotFoundAndAdminBehavior(t *testing.T) {
	t.Parallel()
	t.Run("non-owner gets not found", func(t *testing.T) {
		module, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
		accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
			OwnerID:    "owner-a",
			Capability: generation.TextToImage,
			OfferingID: "openai/gpt-image-2/text-to-image",
			Input:      json.RawMessage(`{"prompt":"private"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodDelete,
			"/api/meshy/text-to-image/"+string(accepted.TaskID),
			nil,
		)
		request.Header.Set("X-User-ID", "owner-b")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		if _, err := module.Get(
			context.Background(),
			"owner-a",
			accepted.TaskID,
		); err != nil {
			t.Fatalf("task was unexpectedly deleted: %v", err)
		}
	})

	t.Run("admin can delete another owner's task", func(t *testing.T) {
		module, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
		accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
			OwnerID:    "owner-a",
			Capability: generation.TextToImage,
			OfferingID: "openai/gpt-image-2/text-to-image",
			Input:      json.RawMessage(`{"prompt":"admin cleanup"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(
			http.MethodDelete,
			"/api/meshy/text-to-image/"+string(accepted.TaskID),
			nil,
		)
		request.Header.Set("X-User-ID", "admin-user")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		if _, err := module.Get(
			context.Background(),
			"owner-a",
			accepted.TaskID,
		); !generation.IsCode(err, generation.ErrNotFound) {
			t.Fatalf("get error = %#v", err)
		}
	})
}

func TestLegacyTaskDeleteErrorsComeFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Content.Messages["error.legacy_task_delete_not_found"] =
				"Schema 删除目标不存在"
		},
	)
	response := serveLegacyRequest(
		handler,
		http.MethodDelete,
		"/api/meshy/text-to-image/missing",
		nil,
	)
	if response.Code != http.StatusNotFound ||
		!isErrorBody(response, "Schema 删除目标不存在") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

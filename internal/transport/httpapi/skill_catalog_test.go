package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestSkillCatalogRouteProjectsTheSchemaDocument(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/chat/skills",
		nil,
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload struct {
		Revision string `json:"revision"`
		Skills   []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			Prompt   string `json:"prompt"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Revision == "" || payload.Skills == nil || len(payload.Skills) != 0 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestEmptySkillCatalogRouteAndWireFieldsChangeWithProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.Routes.ChatSkills =
				"/configured/skill-schema"
			fields := &application.Chat.Skills.Fields
			fields.Revision = "configured_revision"
			fields.Skills = "configured_items"
			fields.ID = "configured_id"
			fields.Category = "configured_category"
			fields.Prompt = "configured_prompt"
		},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/configured/skill-schema",
			nil,
		),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["configured_revision"] == nil ||
		payload["revision"] != nil {
		t.Fatalf("payload = %#v", payload)
	}
	items, ok := payload["configured_items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("items = %#v", payload["configured_items"])
	}
}

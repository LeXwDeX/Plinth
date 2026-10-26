package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyPromptEnhanceContract(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/prompt-enhance",
		bytes.NewBufferString(`{
			"prompt":"a castle",
			"capability":"text-to-image",
			"model":"gpt-image-2.5-sunburst"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["prompt"] != "a castle" {
		t.Fatalf("prompt missing: %#v", payload)
	}
	if _, exists := payload["negative_prompt"]; !exists {
		t.Fatalf("legacy workbench negative prompt missing: %#v", payload)
	}
}

func TestLegacyPromptEnhanceWireAndTextComeFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.PromptEnhance =
				"/configured-enhance"
			contract := &application.Chat.API.PromptEnhance
			contract.PromptField = "configuredInput"
			contract.CapabilityField = "configuredCapability"
			contract.ModelField = "configuredModel"
			contract.ResultField = "configuredResult"
			contract.PromptResultField = "configuredPrompt"
			contract.NegativePromptResultField = "configuredNegative"
			contract.ErrorField = "configuredError"
			application.Content.Messages["error.prompt_enhance_required"] =
				"configured required"
		},
	)

	valid := httptest.NewRequest(
		http.MethodPost,
		"/configured-enhance",
		bytes.NewBufferString(`{
			"configuredInput":"configured input",
			"configuredCapability":"text-to-image",
			"configuredModel":"gpt-image-2.5-sunburst"
		}`),
	)
	valid.Header.Set("Content-Type", "application/json")
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			validResponse.Code,
			validResponse.Body.String(),
		)
	}
	var payload map[string]any
	if err := json.Unmarshal(validResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["configuredResult"] != "configured input" ||
		payload["configuredPrompt"] != "configured input" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, exists := payload["prompt"]; exists {
		t.Fatalf("hard-coded prompt field leaked: %#v", payload)
	}

	empty := httptest.NewRequest(
		http.MethodPost,
		"/configured-enhance",
		bytes.NewBufferString(`{"configuredInput":" "}`),
	)
	empty.Header.Set("Content-Type", "application/json")
	emptyResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyResponse, empty)
	if emptyResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, body = %s",
			emptyResponse.Code,
			emptyResponse.Body.String(),
		)
	}
	payload = nil
	if err := json.Unmarshal(emptyResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["configuredError"] != "configured required" {
		t.Fatalf("payload = %#v", payload)
	}
}

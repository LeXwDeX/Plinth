package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestHTTPErrorTextComesFromProductSchema(t *testing.T) {
	t.Parallel()
	const configuredText = "configured invalid JSON text"
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Content.Messages["error.invalid_json"] = configuredText
		},
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	response := doRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/api/v1/tasks",
		"owner-a",
		"application/json",
		bytes.NewBufferString(`{"broken"`),
	)
	defer response.Body.Close()
	var body struct {
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || body.Detail != configuredText {
		t.Fatalf("status = %d, detail = %q", response.StatusCode, body.Detail)
	}
}

func TestServiceAndHomePresentationComeFromProductSchema(t *testing.T) {
	t.Parallel()
	const (
		serviceName = "Configured Candidate Service"
		homeTitle   = "Configured Candidate Home"
	)
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Content.Service.Name = serviceName
			application.Content.Home.Title = homeTitle
		},
	)

	healthRequest := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, healthRequest)
	var health struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Service != serviceName {
		t.Fatalf("service = %q", health.Service)
	}

	homeRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	homeResponse := httptest.NewRecorder()
	handler.ServeHTTP(homeResponse, homeRequest)
	body, err := io.ReadAll(homeResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(homeTitle)) {
		t.Fatalf("home body = %s", body)
	}
}

func TestTaskFailureTextIsProjectedFromProductSchema(t *testing.T) {
	t.Parallel()
	const configuredText = "configured rejected task text"
	module, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Content.Messages["failure.rejected"] = configuredText
		},
	)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID:    "owner-a",
		Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"cancel before execution"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.Cancel(context.Background(), "owner-a", accepted.TaskID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response := doRequest(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/api/v1/tasks/"+string(accepted.TaskID),
		"owner-a",
		"",
		nil,
	)
	defer response.Body.Close()
	var body struct {
		Failure struct {
			Detail string `json:"detail"`
		} `json:"failure"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Failure.Detail != configuredText {
		t.Fatalf("failure detail = %q", body.Failure.Detail)
	}
}

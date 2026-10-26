package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type recordingProviderWebhookIngestor struct {
	commands []generation.ProviderWebhookCommand
	result   generation.ProviderWebhookResult
	err      error
}

func (stub *recordingProviderWebhookIngestor) IngestProviderWebhook(
	_ context.Context,
	command generation.ProviderWebhookCommand,
) (generation.ProviderWebhookResult, error) {
	stub.commands = append(stub.commands, command)
	return stub.result, stub.err
}

func TestProviderWebhookHTTPContractIsSchemaDriven(t *testing.T) {
	t.Parallel()
	productCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := productCatalog.Application()
	webhook := &application.ProviderWebhooks[0]
	webhook.API.Fields.Error = "problem"
	webhook.API.Fields.Received = "accepted"
	webhook.API.Fields.Timestamp = "at"
	webhook.API.Fields.Status = "state"
	webhook.API.Fields.Service = "component"
	webhook.API.Values.NotFound = "hidden"
	webhook.API.Values.Unauthorized = "denied"
	webhook.API.Values.BadRequest = "invalid"
	webhook.API.Values.Unavailable = "retry"
	webhook.API.Values.OK = "healthy"
	webhook.API.Values.Service = "candidate webhook"
	future := *webhook
	future.ID = "future-task-status"
	future.ProviderID = "future-provider"
	future.Route = "/future/{futureSecret}/callback"
	future.PathParameter = "futureSecret"
	future.EventType = "future.webhook_received"
	application.ProviderWebhooks = append(application.ProviderWebhooks, future)

	recorder := &recordingProviderWebhookIngestor{}
	fixedTime := time.Date(
		2026,
		time.July,
		27,
		12,
		34,
		56,
		0,
		time.UTC,
	)
	providerWebhookBindings, err := compileProviderWebhookBindings(
		application.ProviderWebhooks,
		map[string]ProviderWebhookRuntime{
			webhook.ID: {
				PathSecret: "path-secret",
				Key:        "key-secret",
			},
			future.ID: {
				PathSecret: "future-path-secret",
				Key:        "future-key-secret",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		application:             application,
		providerWebhooks:        recorder,
		providerWebhookBindings: providerWebhookBindings,
		clock:                   func() time.Time { return fixedTime },
		logger: slog.New(slog.NewTextHandler(
			io.Discard,
			nil,
		)),
		mux: http.NewServeMux(),
	}
	server.routes()

	t.Run("path secret hides endpoint", func(t *testing.T) {
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodGet,
			"/wrong-secret/wbh",
			"",
			"",
		)
		if response.Code != webhook.API.Statuses.NotFound ||
			response.JSON["problem"] != "hidden" {
			t.Fatalf(
				"response = status %d, body %#v",
				response.Code,
				response.JSON,
			)
		}
	})

	t.Run("GET projects Schema-owned health response", func(t *testing.T) {
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodGet,
			"/path-secret/wbh",
			"",
			"",
		)
		if response.Code != webhook.API.Statuses.OK ||
			response.JSON["state"] != "healthy" ||
			response.JSON["component"] != "candidate webhook" ||
			response.JSON["at"] !=
				fixedTime.Format(webhook.API.TimestampLayout) {
			t.Fatalf(
				"response = status %d, body %#v",
				response.Code,
				response.JSON,
			)
		}
	})

	t.Run("legacy trailing slash reaches the same webhook contract", func(t *testing.T) {
		statusResponse := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodGet,
			"/path-secret/wbh/",
			"",
			"",
		)
		if statusResponse.Code != webhook.API.Statuses.OK ||
			statusResponse.JSON["state"] != "healthy" {
			t.Fatalf(
				"GET response = status %d, body %#v",
				statusResponse.Code,
				statusResponse.JSON,
			)
		}

		receiveResponse := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodPost,
			"/path-secret/wbh/",
			`{"id":"remote-1"}`,
			"",
		)
		if receiveResponse.Code != webhook.API.Statuses.Unauthorized ||
			receiveResponse.JSON["problem"] != "denied" {
			t.Fatalf(
				"POST response = status %d, body %#v",
				receiveResponse.Code,
				receiveResponse.JSON,
			)
		}
	})

	t.Run("POST enforces configured key", func(t *testing.T) {
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodPost,
			"/path-secret/wbh",
			`{"id":"remote-1"}`,
			"",
		)
		if response.Code != webhook.API.Statuses.Unauthorized ||
			response.JSON["problem"] != "denied" {
			t.Fatalf(
				"response = status %d, body %#v",
				response.Code,
				response.JSON,
			)
		}
	})

	t.Run("POST rejects malformed JSON", func(t *testing.T) {
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodPost,
			"/path-secret/wbh",
			"{",
			"key-secret",
		)
		if response.Code != webhook.API.Statuses.BadRequest ||
			response.JSON["problem"] != "invalid" {
			t.Fatalf(
				"response = status %d, body %#v",
				response.Code,
				response.JSON,
			)
		}
	})

	t.Run("POST acknowledges payload without job ID", func(t *testing.T) {
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodPost,
			"/path-secret/wbh",
			`{"status":"SUCCEEDED"}`,
			"key-secret",
		)
		if response.Code != webhook.API.Statuses.OK ||
			response.JSON["accepted"] != true ||
			len(recorder.commands) != 0 {
			t.Fatalf(
				"response = status %d, body %#v, commands %#v",
				response.Code,
				response.JSON,
				recorder.commands,
			)
		}
	})

	t.Run("POST forwards stable provider identity and raw payload", func(t *testing.T) {
		body := `{"task_id":"remote-2","status":"SUCCEEDED"}`
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodPost,
			"/path-secret/wbh",
			body,
			"key-secret",
		)
		if response.Code != webhook.API.Statuses.OK ||
			response.JSON["accepted"] != true ||
			len(recorder.commands) != 1 {
			t.Fatalf(
				"response = status %d, body %#v, commands %#v",
				response.Code,
				response.JSON,
				recorder.commands,
			)
		}
		command := recorder.commands[0]
		if command.ProviderID != webhook.ProviderID ||
			command.RemoteID != "remote-2" ||
			command.EventType != webhook.EventType ||
			string(command.Payload) != body {
			t.Fatalf("command = %#v", command)
		}
	})

	t.Run("POST dispatches a future Provider from the matching Schema contract", func(t *testing.T) {
		body := `{"id":"future-remote-1","status":"SUCCEEDED"}`
		response := executeProviderWebhookRequest(
			t,
			server.mux,
			http.MethodPost,
			"/future/future-path-secret/callback",
			body,
			"future-key-secret",
		)
		if response.Code != future.API.Statuses.OK ||
			response.JSON["accepted"] != true ||
			len(recorder.commands) != 2 {
			t.Fatalf(
				"response = status %d, body %#v, commands %#v",
				response.Code,
				response.JSON,
				recorder.commands,
			)
		}
		command := recorder.commands[1]
		if command.ProviderID != future.ProviderID ||
			command.RemoteID != "future-remote-1" ||
			command.EventType != future.EventType ||
			string(command.Payload) != body {
			t.Fatalf("command = %#v", command)
		}
	})

	t.Run("POST maps normalized decode failures to retry policy", func(t *testing.T) {
		testCases := []struct {
			name       string
			err        error
			wantStatus int
			wantError  string
		}{
			{
				name: "invalid payload",
				err: &generation.Error{
					Code: generation.ErrInvalidInput,
				},
				wantStatus: webhook.API.Statuses.BadRequest,
				wantError:  "invalid",
			},
			{
				name: "unknown completed job",
				err: &generation.Error{
					Code: generation.ErrNotFound,
				},
				wantStatus: webhook.API.Statuses.OK,
			},
			{
				name: "temporary failure",
				err: &generation.Error{
					Code: generation.ErrUnavailable,
				},
				wantStatus: webhook.API.Statuses.Unavailable,
				wantError:  "retry",
			},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				recorder.err = testCase.err
				response := executeProviderWebhookRequest(
					t,
					server.mux,
					http.MethodPost,
					"/path-secret/wbh",
					`{"id":"remote-error"}`,
					"key-secret",
				)
				if response.Code != testCase.wantStatus {
					t.Fatalf(
						"status = %d, body %#v",
						response.Code,
						response.JSON,
					)
				}
				if testCase.wantError != "" &&
					response.JSON["problem"] != testCase.wantError {
					t.Fatalf("body = %#v", response.JSON)
				}
			})
		}
		recorder.err = nil
	})
}

type providerWebhookHTTPResponse struct {
	Code int
	JSON map[string]any
}

func executeProviderWebhookRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body string,
	key string,
) providerWebhookHTTPResponse {
	t.Helper()
	request := httptest.NewRequest(
		method,
		path,
		strings.NewReader(body),
	)
	if key != "" {
		request.Header.Set("X-Webhook-Secret", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	decoded := make(map[string]any)
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf(
			"decode response %q: %v",
			response.Body.String(),
			err,
		)
	}
	return providerWebhookHTTPResponse{
		Code: response.Code,
		JSON: decoded,
	}
}

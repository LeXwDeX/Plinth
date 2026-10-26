package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyTaskStreamEmitsOwnedTerminalTaskAsSSE(t *testing.T) {
	t.Parallel()
	module, handler, _ := newHTTPHarness(t)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"a schema-driven stream","size":"1024x1024","quality":"max"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := accepted.TaskID
	reconcileTaskStreamToTerminal(t, module, taskID)

	streamed := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/stream/text-to-image/"+string(taskID),
		nil,
	)
	if streamed.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", streamed.Code, streamed.Body)
	}
	if got := streamed.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type=%q", got)
	}
	event, payload := decodeSingleTaskSSE(t, streamed.Body.String())
	if event != "message" {
		t.Fatalf("event=%q body=%s", event, streamed.Body)
	}
	if payload["id"] != string(taskID) ||
		payload["status"] != "SUCCEEDED" ||
		payload["progress"] != float64(100) ||
		payload["type"] != "text-to-image" {
		t.Fatalf("payload=%#v", payload)
	}
	outputs, _ := payload["output_urls"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("output_urls=%#v", payload["output_urls"])
	}
}

func TestLegacyTaskStreamEmitsPendingThenTerminalChanges(t *testing.T) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.TaskStream.Timing.PollIntervalMilliseconds = 10
		},
		nil,
		nil,
		nil,
	)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"a schema-driven stream","size":"1024x1024","quality":"max"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := accepted.TaskID
	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/meshy/stream/text-to-image/"+string(taskID),
		nil,
	).WithContext(requestContext)
	request.Header.Set("X-User-ID", "owner-a")
	response := &firstFlushSignalingRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		firstFlush:       make(chan struct{}),
	}
	streamFinished := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(streamFinished)
	}()
	defer func() {
		cancelRequest()
		select {
		case <-streamFinished:
		case <-time.After(2 * time.Second):
			t.Error("task stream handler did not stop after request cancellation")
		}
	}()

	select {
	case <-response.firstFlush:
	case <-streamFinished:
		t.Fatalf("task stream returned before flushing its first event: %s", response.Body.String())
	case <-time.After(2 * time.Second):
		t.Fatal("task stream did not flush its first event")
	}
	reconcileTaskStreamToTerminal(t, module, taskID)
	select {
	case <-streamFinished:
		events := decodeTaskSSEEvents(t, response.Body.String())
		if len(events) < 2 {
			t.Fatalf("events=%#v body=%s", events, response.Body.String())
		}
		if events[0]["status"] != "PENDING" ||
			events[len(events)-1]["status"] != "SUCCEEDED" {
			t.Fatalf("events=%#v", events)
		}
	case <-time.After(2 * time.Second):
		cancelRequest()
		t.Fatal("task stream did not finish after terminal state")
	}
}

type firstFlushSignalingRecorder struct {
	*httptest.ResponseRecorder
	firstFlush chan struct{}
	once       sync.Once
}

func (w *firstFlushSignalingRecorder) Flush() {
	w.ResponseRecorder.Flush()
	w.once.Do(func() {
		close(w.firstFlush)
	})
}

func TestLegacyTaskStreamRejectsUnknownTypeWithSchemaMessage(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/api/meshy/stream/not-a-capability/task",
		nil,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "不支持的任务类型: not-a-capability" {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestLegacyTaskStreamRouteFieldsEventsAndTextComeFromProductSchema(
	t *testing.T,
) {
	t.Parallel()
	module, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.TaskStream =
				"/configured-stream/{configuredType}/{configuredTask}"
			stream := &application.TaskStream
			stream.API.PathParameters.Type = "configuredType"
			stream.API.PathParameters.TaskID = "configuredTask"
			stream.API.Fields.TaskID = "configuredID"
			stream.API.Fields.Type = "configuredKind"
			stream.API.Fields.Status = "configuredStatus"
			stream.API.Fields.Progress = "configuredProgress"
			stream.API.Fields.CreatedAt = "configuredCreated"
			stream.API.Fields.UpdatedAt = "configuredUpdated"
			stream.API.Fields.TaskError = "configuredTaskError"
			stream.API.Fields.Message = "configuredMessage"
			stream.API.Fields.StatusCode = "configuredCode"
			stream.API.Fields.Error = "configuredError"
			stream.Aliases = []catalog.TaskStreamCapabilityAlias{{
				ExternalType: "configured-image",
				Capability:   generation.Capability("text-to-image"),
			}}
			stream.Events.Message = "configured-event"
			stream.Events.Error = "configured-error-event"
			stream.Presentation.UnsupportedType =
				"configured unsupported {type}"
		},
		nil,
		nil,
		nil,
	)
	accepted, err := submitHarnessTask(t, module, context.Background(), generation.SubmitCommand{
		OwnerID: "owner-a", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      json.RawMessage(`{"prompt":"a schema-driven stream","size":"1024x1024","quality":"max"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := accepted.TaskID
	reconcileTaskStreamToTerminal(t, module, taskID)

	response := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/configured-stream/configured-image/"+string(taskID),
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	event, payload := decodeSingleTaskSSE(t, response.Body.String())
	if event != "configured-event" ||
		payload["configuredID"] != string(taskID) ||
		payload["configuredKind"] != "configured-image" ||
		payload["configuredStatus"] != "SUCCEEDED" {
		t.Fatalf("event=%q payload=%#v", event, payload)
	}
	for _, leaked := range []string{"id", "type", "status", "progress"} {
		if _, exists := payload[leaked]; exists {
			t.Fatalf("hard-coded field %q leaked: %#v", leaked, payload)
		}
	}

	unsupported := serveLegacyRequest(
		handler,
		http.MethodGet,
		"/configured-stream/unknown/"+string(taskID),
		nil,
	)
	if unsupported.Code != http.StatusBadRequest {
		t.Fatalf(
			"unsupported status=%d body=%s",
			unsupported.Code,
			unsupported.Body,
		)
	}
	var problem map[string]any
	if err := json.NewDecoder(unsupported.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem["configuredError"] != "configured unsupported unknown" {
		t.Fatalf("problem=%#v", problem)
	}
}

func decodeSingleTaskSSE(
	t *testing.T,
	body string,
) (string, map[string]any) {
	t.Helper()
	var event string
	var payload map[string]any
	for line := range strings.SplitSeq(body, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal(
				[]byte(strings.TrimPrefix(line, "data: ")),
				&payload,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	if event == "" || payload == nil {
		t.Fatalf("invalid SSE body=%q", body)
	}
	return event, payload
}

func decodeTaskSSEEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	var result []map[string]any
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(
			[]byte(strings.TrimPrefix(line, "data: ")),
			&payload,
		); err != nil {
			t.Fatal(err)
		}
		result = append(result, payload)
	}
	return result
}

func submitTextToImageForTaskStream(
	t *testing.T,
	handler http.Handler,
) generation.TaskID {
	t.Helper()
	submitted := serveLegacyRequest(
		handler,
		http.MethodPost,
		"/api/meshy/text-to-image",
		strings.NewReader(`{
			"prompt":"a schema-driven stream",
			"ai_model":"gpt-image-2.5-sunburst",
			"size":"1024x1024",
			"quality":"max"
		}`),
	)
	if submitted.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.Code, submitted.Body)
	}
	var accepted struct {
		Result generation.TaskID `json:"result"`
	}
	if err := json.NewDecoder(submitted.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	return accepted.Result
}

func reconcileTaskStreamToTerminal(
	t *testing.T,
	module *generation.Module,
	taskID generation.TaskID,
) {
	t.Helper()
	for range 8 {
		if _, err := module.ReconcileOnce(
			context.Background(),
			generation.CoordinatorConfig{
				WorkerID:  "task-stream-test",
				LeaseFor:  time.Minute,
				BatchSize: 10,
			},
		); err != nil {
			t.Fatal(err)
		}
		task, err := module.Get(context.Background(), "owner-a", taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			return
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("task did not reach terminal state")
}

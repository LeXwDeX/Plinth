package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestMockChatAgentRunsHTTPTaskAndSSEToCompletion(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	generationDone := make(chan error, 1)
	go func() {
		generationDone <- application.generation.Run(
			runCtx,
			// One claimer on the shared SQLite connection keeps the real task
			// from competing with nine idle workers for the same connection.
			generation.CoordinatorConfig{
				PollEvery:   20 * time.Millisecond,
				LeaseFor:    30 * time.Second,
				BatchSize:   1,
				Concurrency: 1,
			},
		)
	}()
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- application.chatAgent.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		<-generationDone
		<-agentDone
		_ = application.generation.Close()
	})

	handler := application.server.Handler
	create := appChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions",
		`{}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var session map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	sessionID, _ := session["id"].(string)
	if sessionID == "" {
		t.Fatalf("session = %#v", session)
	}
	send := appChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions/"+sessionID+"/messages",
		`{"content":"configured picture request","ai_model":"gpt-image-2.5-sunburst","creative_domain":"image"}`,
	)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body)
	}
	// A new session is in manual mode and text-to-image needs no human: the
	// Agent freezes the exact operation, session policy approves that grant and
	// the same call submits the Task. Nothing waits for a person.
	grantRepository, err := sqlite.Open(filepath.Join(root, "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer grantRepository.Close()
	deadline := time.Now().Add(20 * time.Second)
	var assistantMessageID string
	for time.Now().Before(deadline) && assistantMessageID == "" {
		listed := appChatRequest(handler, http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages", "")
		var current []map[string]any
		if err := json.Unmarshal(listed.Body.Bytes(), &current); err == nil && len(current) == 2 {
			assistantMessageID, _ = current[1]["id"].(string)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if assistantMessageID == "" {
		t.Fatal("assistant message was not created")
	}
	turnDeadline := time.Now().Add(20 * time.Second)
	turnState := ""
	for time.Now().Before(turnDeadline) {
		turn, readErr := grantRepository.GetTurn(context.Background(), "owner-a", sessionID, assistantMessageID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		turnState = turn.State
		if turnState == application.schema.Chat.State.TurnDone || turnState == application.schema.Chat.State.TurnError {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if turnState != application.schema.Chat.State.TurnDone {
		t.Fatalf("agent turn did not finish after task completion: %s", turnState)
	}

	rawDatabase := openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))
	var grantStatus, grantDecidedBy, grantSource string
	if err := rawDatabase.QueryRow(`SELECT status, COALESCE(decided_by,''), source FROM execution_grants
		WHERE owner_id='owner-a' AND session_id=?`, sessionID).Scan(&grantStatus, &grantDecidedBy, &grantSource); err != nil {
		t.Fatal(err)
	}
	if grantStatus != "bound" || grantDecidedBy != "policy:manual" || grantSource != executiongrant.AgentPending {
		t.Fatalf("grant = status %q decided_by %q source %q, want bound by policy:manual", grantStatus, grantDecidedBy, grantSource)
	}
	var auditActor string
	if err := rawDatabase.QueryRow(`SELECT actor_id FROM execution_grant_audit WHERE owner_id='owner-a' AND event='approved'`).Scan(&auditActor); err != nil || auditActor != "policy:manual" {
		t.Fatalf("policy approval audit actor = %q, %v", auditActor, err)
	}

	streamContext, stopStream := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopStream()
	streamRequest := httptest.NewRequest(http.MethodGet,
		"/api/chat/sessions/"+sessionID+"/stream?fromId=0", nil).WithContext(streamContext)
	streamRequest.Header.Set("X-User-ID", "owner-a")
	stream := httptest.NewRecorder()
	handler.ServeHTTP(stream, streamRequest)
	if stream.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", stream.Code, stream.Body)
	}
	body := stream.Body.String()
	for _, eventType := range []string{
		`"type":"msg_created"`,
		`"type":"task_registered"`,
		`"type":"images"`,
		`"type":"done"`,
	} {
		if !strings.Contains(body, eventType) {
			t.Fatalf("SSE missing %s: %s", eventType, body)
		}
	}

	messages := appChatRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions/"+sessionID+"/messages",
		"",
	)
	var stored []map[string]any
	if err := json.Unmarshal(messages.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 ||
		stored[1]["content"] == "__GENERATING__" ||
		stored[1]["imageUrls"] == "[]" {
		t.Fatalf("stored messages = %#v", stored)
	}
	// The accepted step carries its estimated credits and who approved it.
	var calls []map[string]any
	if err := json.Unmarshal([]byte(stored[1]["toolCalls"].(string)), &calls); err != nil || len(calls) == 0 {
		t.Fatalf("tool calls = %v, %v", stored[1]["toolCalls"], err)
	}
	if calls[0]["approvalSource"] != "policy" {
		t.Fatalf("tool call approval source = %v, want policy: %v", calls[0]["approvalSource"], calls[0])
	}
	if credits, _ := calls[0]["estimatedCredits"].(float64); credits <= 0 {
		t.Fatalf("tool call estimated credits = %v", calls[0]["estimatedCredits"])
	}
	// The session DTO carries the cumulative estimate and the mode.
	read := appChatRequest(handler, http.MethodGet, "/api/v1/chat/sessions/"+sessionID+"/approval", "")
	var approval map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &approval); err != nil || read.Code != http.StatusOK {
		t.Fatalf("approval read = %d %s", read.Code, read.Body)
	}
	if approval["approvalMode"] != "manual" {
		t.Fatalf("new session approval mode = %v, want manual", approval["approvalMode"])
	}
	if credits, _ := approval["estimatedCredits"].(float64); credits <= 0 {
		t.Fatalf("session estimated credits = %v", approval["estimatedCredits"])
	}
}

func openRawAppDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestMockChatAgentAppliesSelectedSkillFromRuntimeCatalog(t *testing.T) {
	root := t.TempDir()
	skillCatalogFile := filepath.Join(root, "configured-skills.json")
	if err := os.WriteFile(skillCatalogFile, []byte(`{
		"revision":"configured-runtime-revision",
		"skills":[{
			"id":"configured-runtime-skill",
			"name":"Configured runtime skill",
			"category":"anime",
			"recommended_size":"1024x1024",
			"thumbnail_path":"/configured-runtime-thumbnail.webp",
			"raw_image_url":"https://configured.invalid/runtime.png",
			"prompt":"configured runtime catalog prompt"
		}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		SkillCatalogFile:          skillCatalogFile,
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		RuntimeSettings:           map[string]string{"llm_model": "configured-test-model"},
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- application.chatAgent.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		<-agentDone
		_ = application.generation.Close()
	})

	handler := application.server.Handler
	create := appChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions",
		`{}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var session map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	sessionID, _ := session["id"].(string)
	send := appChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions/"+sessionID+"/messages",
		`{
			"content":"a girl holding a sword",
			"ai_model":"gpt-image-2.5-sunburst",
			"_skill":{
				"id":"configured-runtime-skill",
				"name":"untrusted client name"
			}
		}`,
	)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body)
	}
	repository, err := sqlite.Open(filepath.Join(root, "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	rawDatabase := openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))
	var preparedInputID generation.PreparedInputID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && preparedInputID == "" {
		var id string
		if rawDatabase.QueryRow(`SELECT prepared_input_id FROM agent_generation_operations
			WHERE owner_id='owner-a' AND session_id=?`, sessionID).Scan(&id) == nil {
			preparedInputID = generation.PreparedInputID(id)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if preparedInputID == "" {
		t.Fatal("skill request did not prepare an agent operation")
	}
	prepared, err := repository.GetPreparedInput(context.Background(), "owner-a", preparedInputID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil ||
		!strings.HasPrefix(input.Prompt, "a girl holding a sword") ||
		!strings.Contains(input.Prompt, "configured runtime catalog prompt") ||
		strings.Index(input.Prompt, "Composition priority:") < 0 ||
		strings.Index(input.Prompt, "Composition priority:") >= strings.Index(input.Prompt, "configured runtime catalog prompt") {
		t.Fatalf("skill prompt missing from frozen Provider input: %q %v", input.Prompt, err)
	}
}

func appChatRequest(
	handler http.Handler,
	method string,
	target string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		method,
		target,
		bytes.NewBufferString(body),
	)
	request.Header.Set("X-User-ID", "owner-a")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
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
		// The session has no brief, so the default 2D purpose applies and its
		// first recommendation is the model the workspace picker must show:
		// a different pick makes the Agent ask before switching (#103 C1).
		`{"content":"configured picture request","ai_model":"gemini-3-pro-image","creative_domain":"image"}`,
	)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body)
	}
	// A new session is in the Schema's default mode (auto, #406): the Agent
	// freezes the exact operation, session policy approves that grant and the
	// same call submits the Task. Nothing waits for a person.
	grantRepository, err := sqlitetest.Open(filepath.Join(root, "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer grantRepository.Close()
	rawDatabase := openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))
	var assistantMessageID string
	waitForChat(t, "the assistant message to be created", func() (bool, string) {
		listed := appChatRequest(handler, http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages", "")
		var current []map[string]any
		if err := json.Unmarshal(listed.Body.Bytes(), &current); err == nil && len(current) == 2 {
			assistantMessageID, _ = current[1]["id"].(string)
		}
		return assistantMessageID != "", fmt.Sprintf("%d messages", len(current))
	})
	// The turn runs the Agent twice, before and after the Task: running,
	// waiting_task, queued, running, done. The wait is on the end state. A
	// timeout lists the turn and Task states seen with their times, so a slow
	// phase can be told from a turn that never wakes after its Task finished.
	turnState, turnEndReason := "", ""
	waitForChat(t, "the Agent turn to end after the Task completed", func() (bool, string) {
		turn, readErr := grantRepository.GetTurn(context.Background(), "owner-a", sessionID, assistantMessageID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		turnState, turnEndReason = turn.State, turn.EndReason
		var taskStates string
		if err := rawDatabase.QueryRow(`SELECT COALESCE(group_concat(state), 'none') FROM generation_tasks
			WHERE owner_id='owner-a' AND session_id=?`, sessionID).Scan(&taskStates); err != nil {
			taskStates = "unreadable"
		}
		ended := turnState == application.schema.Chat.State.TurnDone || turnState == application.schema.Chat.State.TurnError
		return ended, "turn " + turnState + ", task " + taskStates
	})
	if turnState != application.schema.Chat.State.TurnDone {
		t.Fatalf("agent turn did not finish after task completion: %s", turnState)
	}
	// #328: the assembled app stores the Schema's end reason with the turn.
	if want := application.schema.Chat.State.TurnEndReasons.Completed; want == "" || turnEndReason != want {
		t.Fatalf("finished turn end reason = %q, want the Schema's %q", turnEndReason, want)
	}

	var grantStatus, grantDecidedBy, grantSource string
	if err := rawDatabase.QueryRow(`SELECT status, COALESCE(decided_by,''), source FROM execution_grants
		WHERE owner_id='owner-a' AND session_id=?`, sessionID).Scan(&grantStatus, &grantDecidedBy, &grantSource); err != nil {
		t.Fatal(err)
	}
	if grantStatus != "bound" || grantDecidedBy != "policy:auto" || grantSource != executiongrant.AgentPending {
		t.Fatalf("grant = status %q decided_by %q source %q, want bound by policy:auto", grantStatus, grantDecidedBy, grantSource)
	}
	var auditActor string
	if err := rawDatabase.QueryRow(`SELECT actor_id FROM execution_grant_audit WHERE owner_id='owner-a' AND event='approved'`).Scan(&auditActor); err != nil || auditActor != "policy:auto" {
		t.Fatalf("policy approval audit actor = %q, %v", auditActor, err)
	}

	// The turn has ended, so the stream replays the stored events and returns at
	// the done event without waiting for more. The timeout only bounds a stream
	// that does not end; it is not an expectation of how fast the replay is.
	streamContext, stopStream := context.WithTimeout(context.Background(), chatWaitBudget)
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
	// The accepted step carries who approved it. The mock image Offering does not bill in credits, so the
	// step states none (#719); the session's cumulative estimate below still weighs the internal one.
	var calls []map[string]any
	if err := json.Unmarshal([]byte(stored[1]["toolCalls"].(string)), &calls); err != nil || len(calls) == 0 {
		t.Fatalf("tool calls = %v, %v", stored[1]["toolCalls"], err)
	}
	if calls[0]["approvalSource"] != "policy" {
		t.Fatalf("tool call approval source = %v, want policy: %v", calls[0]["approvalSource"], calls[0])
	}
	if credits, _ := calls[0]["estimatedCredits"].(float64); credits != 0 {
		t.Fatalf("tool call of an Offering that does not bill in credits states %v credits", calls[0]["estimatedCredits"])
	}
	// The session DTO carries the cumulative estimate and the mode.
	read := appChatRequest(handler, http.MethodGet, "/api/v1/chat/sessions/"+sessionID+"/approval", "")
	var approval map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &approval); err != nil || read.Code != http.StatusOK {
		t.Fatalf("approval read = %d %s", read.Code, read.Body)
	}
	if approval["approvalMode"] != "auto" {
		t.Fatalf("new session approval mode = %v, want auto", approval["approvalMode"])
	}
	if credits, _ := approval["estimatedCredits"].(float64); credits <= 0 {
		t.Fatalf("session estimated credits = %v", approval["estimatedCredits"])
	}
}

// chatWaitBudget bounds each wait in the chat integration tests (#411). Every
// wait polls a condition and returns the moment it holds, so a long budget
// costs nothing on a healthy stack and only decides when a real hang is
// reported. It is sized for the slowest runner, not for the fast case: every
// Agent request copies the Offering catalog many times over, so one Agent pass
// takes about 4.5 s under -race on an idle 14-core machine and a whole turn
// (Agent, Task, Agent) about 11 s. The hosted CI runner has far fewer cores
// and shares them with the race tests of other packages, and the fixed 20 s
// windows these tests used before ran out there.
const (
	chatWaitBudget = 3 * time.Minute
	// chatWaitPoll is kept coarse on purpose: a poll is a database read made
	// while the CPU is the scarce resource, so a fast poll only slows the stack
	// it is waiting for.
	chatWaitPoll = 50 * time.Millisecond
)

// waitForChat polls until check reports done or chatWaitBudget runs out. check
// also returns a short description of what it sees; the changes of that
// description are kept with their times and printed when the wait times out,
// so the report says which phase was slow instead of only that time ran out.
func waitForChat(t *testing.T, what string, check func() (done bool, observed string)) {
	t.Helper()
	started := time.Now()
	var trail []string
	last := ""
	for {
		done, observed := check()
		if observed != last {
			trail = append(trail, fmt.Sprintf("%s (%.1fs)", observed, time.Since(started).Seconds()))
			last = observed
		}
		if done {
			return
		}
		if time.Since(started) >= chatWaitBudget {
			t.Fatalf("timed out after %s waiting for %s; observed: %s", chatWaitBudget, what, strings.Join(trail, " -> "))
		}
		time.Sleep(chatWaitPoll)
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
	// The Skill needs an Offering that features skills, so lock that model
	// explicitly: a session lock outranks the default 2D purpose recommendation.
	lock := appChatRequest(handler, http.MethodPut, "/api/v1/chat/sessions/"+sessionID+"/model2d",
		`{"explicit_model_2d":"gpt-image-2.6-sunburst"}`)
	if lock.Code != http.StatusOK {
		t.Fatalf("model2d lock status=%d body=%s", lock.Code, lock.Body)
	}
	send := appChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions/"+sessionID+"/messages",
		`{
			"content":"a girl holding a sword",
			"ai_model":"gpt-image-2.6-sunburst",
			"_skill":{
				"id":"configured-runtime-skill",
				"name":"untrusted client name"
			}
		}`,
	)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body)
	}
	repository, err := sqlitetest.Open(filepath.Join(root, "art-ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	rawDatabase := openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))
	var preparedInputID generation.PreparedInputID
	waitForChat(t, "the skill request to prepare an agent operation", func() (bool, string) {
		var id string
		if rawDatabase.QueryRow(`SELECT prepared_input_id FROM agent_generation_operations
			WHERE owner_id='owner-a' AND session_id=?`, sessionID).Scan(&id) == nil {
			preparedInputID = generation.PreparedInputID(id)
		}
		if preparedInputID == "" {
			return false, "no operation yet"
		}
		return true, "operation prepared"
	})
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

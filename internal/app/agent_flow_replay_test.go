package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// The reviewed Agent flow sample is replayed through the whole application
// rather than exchange by exchange: the person's chat message, the model's
// tool call, the approval card, the Generation kernel's Task, the Hyper3D
// submit/observe/download wire and the result posted back into the chat all
// run as in production, driven through the public HTTP routes with a signed-in
// session. Only the HTTP transports of the declared connections are replaced,
// so an upstream request the sample does not contain has nowhere to go: it
// fails the replay and never reaches a network.
//
// The sample is public-safe, so it is lossy, and the stack needs four explicit
// accommodations; each is counted or asserted where it is used:
//   - The conversation, tool declarations and thinking switches of a chat
//     request are recorded as the opaque "ok"; the live request is reduced to
//     the same shape before it is compared (opaqueChatFields).
//   - The tool the model called and the names of the downloaded files were
//     sanitized to opaque ids; they are bound back to the declared tool and to
//     the declared download rules (replaySanitizedTool, bindDownloadNames).
//   - The sample was recorded over half an hour, so the Rodin Offering's poll
//     interval is cut to one second (oneSecondRodinCatalog) and the adapter's
//     own wait between state changes is shortened (fastObservationRegistry).
//   - The title response recorded its message as the opaque "ok", which the
//     title request cannot decode; the title stays at its default, as the
//     application does for any failed title.

const (
	replayLLMConnection   = "LLM"
	replayRodinConnection = "hyper3d"
	replayAdminUser       = "replay-admin"
	replayAdminPassword   = "correct-horse"
	replayModel           = "rodin-gen-2.5"
	replayPrompt          = "生成一个简单的无文字蓝色立方体 3D 模型。只生成一次，不要重试。"

	// The sample sanitizes every "name" value to an opaque id, so the tool the
	// model called is recorded as replaySanitizedTool. The binding below is
	// the only edit made to a replayed response.
	replaySanitizedTool = "id-5"
	replayBoundTool     = "create_model_3d"
)

// replayTransport counts completed upstream round trips of one connection and
// can rewrite a replayed response. It forwards to the replay and nothing else.
type replayTransport struct {
	inner http.RoundTripper
	// project reduces an outgoing request body to its recorded shape; rewrite
	// binds a replayed response body. Either may be nil.
	project func([]byte) []byte
	rewrite func(*http.Request, []byte) []byte
	// sent keeps the outgoing request bodies, before projection, for assertions.
	mu      sync.Mutex
	sent    [][]byte
	calls   atomic.Int64
	refused atomic.Int64
}

func (r *replayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil && request.Body != http.NoBody {
		raw, err := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.sent = append(r.sent, raw)
		r.mu.Unlock()
		if r.project != nil {
			raw = r.project(raw)
		}
		request = request.Clone(request.Context())
		request.Body = io.NopCloser(bytes.NewReader(raw))
		request.ContentLength = int64(len(raw))
		request.GetBody = nil
	}
	response, err := r.inner.RoundTrip(request)
	if err != nil {
		r.refused.Add(1)
		return nil, err
	}
	if r.rewrite != nil {
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		body = r.rewrite(request, body)
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
	}
	r.calls.Add(1)
	return response, nil
}

// oneSecondRodinCatalog writes the shipped Product Schema with the Rodin
// Offering's poll interval cut from five seconds to one. The kernel reschedules
// a repeated "still running" observation at the Offering's own interval, so
// this is the one declared knob that shortens the 57 recorded status calls
// without touching the kernel; nothing else in the Schema changes.
func oneSecondRodinCatalog(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "catalog", "default_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	patched := 0
	offerings, _ := document["offerings"].([]any)
	for _, entry := range offerings {
		if offering, _ := entry.(map[string]any); offering["id"] == "hyper3d/rodin-gen-2.5/text-to-3d" {
			offering["poll_interval_seconds"] = json.Number("1")
			patched++
		}
	}
	if patched != 1 {
		t.Fatalf("patched %d Rodin Offerings, want 1", patched)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fastObservationRegistry keeps the adapter's own observation logic and only
// shortens the wait the adapter asks for between state changes.
type fastObservationRegistry struct{ generation.ProviderRegistry }

func (r fastObservationRegistry) Provider(offering generation.Offering) (generation.ProviderPort, bool) {
	port, ok := r.ProviderRegistry.Provider(offering)
	if !ok || offering.ProviderID != "hyper3d" {
		return port, ok
	}
	return fastObservationPort{port}, true
}

type fastObservationPort struct{ generation.ProviderPort }

func (p fastObservationPort) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	observation, err := p.ProviderPort.Start(ctx, request)
	if observation.PollAfter > 0 {
		observation.PollAfter = time.Millisecond
	}
	return observation, err
}

func (p fastObservationPort) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	observation, err := p.ProviderPort.Observe(ctx, request)
	if observation.PollAfter > 0 {
		observation.PollAfter = time.Millisecond
	}
	return observation, err
}

type replayStack struct {
	t            *testing.T
	application  *Application
	handler      http.Handler
	cookie       *http.Cookie
	database     *sql.DB
	databasePath string
	replay       *wirefixture.Replay
	// language is sent as Accept-Language when set.
	language   string
	llm, rodin *replayTransport
	// stray counts upstream requests on a connection that has no replay.
	stray atomic.Int64
	// toolsBound and namesBound count the opaque names given back their
	// meaning in replayed responses (see bindDownloadNames).
	toolsBound, namesBound atomic.Int64
}

// newReplayStack assembles the application in live Provider and Agent mode
// with the given exchanges as the only upstream, runs its background workers
// and signs in the administrator through the public login route.
func newReplayStack(t *testing.T, exchanges []wirefixture.Exchange, policy wirefixture.Policy) *replayStack {
	t.Helper()
	replay, err := wirefixture.NewReplayWithMedia(exchanges, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	stack := &replayStack{t: t, replay: replay}
	stack.llm = &replayTransport{inner: replay.WithConnection(replayLLMConnection), project: opaqueChatFields, rewrite: func(_ *http.Request, body []byte) []byte {
		needle := []byte(`"name":"` + replaySanitizedTool + `"`)
		stack.toolsBound.Add(int64(bytes.Count(body, needle)))
		return bytes.ReplaceAll(body, needle, []byte(`"name":"`+replayBoundTool+`"`))
	}}
	stack.rodin = &replayTransport{inner: replay.WithConnection(replayRodinConnection), rewrite: func(request *http.Request, body []byte) []byte {
		if !strings.HasSuffix(request.URL.Path, "/download") {
			return body
		}
		bound, count := bindDownloadNames(t, body)
		stack.namesBound.Add(int64(count))
		return bound
	}}

	root := t.TempDir()
	application, err := newWithDependencies(Config{
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		CatalogFile:               oneSecondRodinCatalog(t, root),
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "live",
		AgentMode:                 "live",
		RuntimeSettings: map[string]string{
			"llm_base_url":    "https://example.test/v1",
			"llm_api_key":     "replay-llm-credential",
			"llm_model":       "deepseek",
			"hyper3d_api_key": "replay-rodin-credential",
		},
		AuthMode:      "session",
		SessionSecret: strings.Repeat("s", 32),
		AdminUsername: replayAdminUser,
		AdminPassword: replayAdminPassword,
		AdminName:     "Replay",
		AdminEmail:    "replay@example.test",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), assemblyOverrides{
		providers: func(base generation.ProviderRegistry) generation.ProviderRegistry {
			return fastObservationRegistry{base}
		},
		httpTransport: stack.connectionTransport,
	})
	if err != nil {
		t.Fatal(err)
	}
	stack.application = application
	stack.handler = application.server.Handler
	stack.databasePath = filepath.Join(root, "art-ai.db")
	stack.database = openRawAppDatabase(t, stack.databasePath)

	runCtx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	start := func(run func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			run(runCtx)
		}()
	}
	start(func(ctx context.Context) {
		_ = application.generation.Run(ctx, generation.CoordinatorConfig{
			PollEvery: 5 * time.Millisecond, LeaseFor: 30 * time.Second, BatchSize: 1, Concurrency: 1,
		})
	})
	start(func(ctx context.Context) { _ = application.chatAgent.Run(ctx) })
	start(func(ctx context.Context) {
		for ctx.Err() == nil {
			_, _ = application.pendingApprovals.Sweep(ctx)
			select {
			case <-ctx.Done():
			case <-time.After(20 * time.Millisecond):
			}
		}
	})
	t.Cleanup(func() {
		cancel()
		workers.Wait()
		_ = application.Close()
	})

	login := stack.do(http.MethodPost, "/api/auth/login",
		`{"username":"`+replayAdminUser+`","password":"`+replayAdminPassword+`"}`)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login = %d %s", login.Code, login.Body)
	}
	stack.cookie = login.Result().Cookies()[0]
	return stack
}

// connectionTransport is the application's only way out. The base transport
// the application offers (the real network) is never used: a connection with a
// recorded wire gets its replay, every other one is refused.
func (s *replayStack) connectionTransport(id string, _ http.RoundTripper) http.RoundTripper {
	switch id {
	case replayLLMConnection:
		return s.llm
	case replayRodinConnection:
		return s.rodin
	}
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		s.stray.Add(1)
		return nil, errors.New("replay: connection has no recorded wire")
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// do calls the application's public HTTP handler as the signed-in person.
func (s *replayStack) do(method, target, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Origin", "http://example.com")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if s.language != "" {
		request.Header.Set("Accept-Language", s.language)
	}
	if s.cookie != nil {
		request.AddCookie(s.cookie)
	}
	response := httptest.NewRecorder()
	s.handler.ServeHTTP(response, request)
	return response
}

func (s *replayStack) decode(response *httptest.ResponseRecorder, wantStatus int, into any) {
	s.t.Helper()
	if response.Code != wantStatus {
		s.t.Fatalf("status = %d, want %d: %s", response.Code, wantStatus, response.Body)
	}
	if into != nil {
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			s.t.Fatalf("response %s: %v", response.Body, err)
		}
	}
}

func (s *replayStack) newSession() string {
	s.t.Helper()
	var session struct {
		ID string `json:"id"`
	}
	s.decode(s.do(http.MethodPost, "/api/chat/sessions", `{}`), http.StatusCreated, &session)
	return session.ID
}

func (s *replayStack) say(session, content string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content, "ai_model": replayModel, "creative_domain": "model-3d"})
	s.decode(s.do(http.MethodPost, "/api/chat/sessions/"+session+"/messages", string(body)), http.StatusAccepted, nil)
}

func (s *replayStack) await(what string, within time.Duration, done func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for %s (llm=%d rodin=%d)", what, s.llm.calls.Load(), s.rodin.calls.Load())
}

func (r *replayTransport) requests() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.sent...)
}

// pendingGrants counts the session's approval cards that still wait for a person.
func (s *replayStack) pendingGrants(session string) int {
	var count int
	if s.database.QueryRow(`SELECT COUNT(*) FROM execution_grants WHERE session_id=? AND status='pending'`, session).Scan(&count) != nil {
		return 0
	}
	return count
}

// taskState reads the kernel's state of a Task without going through HTTP, so a
// polling loop does not meet the public rate limit.
func (s *replayStack) taskState(id string) string {
	var state string
	if s.database.QueryRow(`SELECT state FROM generation_tasks WHERE id=?`, id).Scan(&state) != nil {
		return ""
	}
	return state
}

// turnsEnded reports whether every Agent turn of the session has ended.
func (s *replayStack) turnsEnded(session string) bool {
	var open, total int
	if s.database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=? AND state NOT IN (?, ?)`, session,
		s.application.schema.Chat.State.TurnDone, s.application.schema.Chat.State.TurnError).Scan(&open) != nil ||
		s.database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=?`, session).Scan(&total) != nil {
		return false
	}
	return total > 0 && open == 0
}

type replayCard struct {
	RequestID  string `json:"request_id"`
	Capability string `json:"capability"`
	Model      string `json:"model"`
	Summary    string `json:"summary"`
}

func (s *replayStack) cards(session string) []replayCard {
	s.t.Helper()
	var body struct {
		Items []replayCard `json:"items"`
	}
	s.decode(s.do(http.MethodGet, "/api/v1/chat/sessions/"+session+"/pending-approvals", ""), http.StatusOK, &body)
	return body.Items
}

type replayDecision struct {
	TaskID    string `json:"task_id"`
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

func (s *replayStack) decide(card replayCard, decision string) (*httptest.ResponseRecorder, replayDecision) {
	s.t.Helper()
	response := s.do(http.MethodPut, "/api/v1/tasks/grants/"+card.RequestID, `{"decision":"`+decision+`"}`)
	var outcome replayDecision
	if response.Code == http.StatusOK {
		_ = json.Unmarshal(response.Body.Bytes(), &outcome)
	}
	return response, outcome
}

type replayTask struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Capability string `json:"capability"`
	Assets     []struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		Variant   string `json:"variant"`
		MediaType string `json:"media_type"`
	} `json:"assets"`
}

func (s *replayStack) task(id string) replayTask {
	s.t.Helper()
	var task replayTask
	s.decode(s.do(http.MethodGet, "/api/v1/tasks/"+id, ""), http.StatusOK, &task)
	return task
}

func (s *replayStack) assistantMessages(session string) []map[string]any {
	s.t.Helper()
	var messages []map[string]any
	s.decode(s.do(http.MethodGet, "/api/chat/sessions/"+session+"/messages", ""), http.StatusOK, &messages)
	var assistant []map[string]any
	for _, message := range messages {
		if message["role"] == "assistant" {
			assistant = append(assistant, message)
		}
	}
	return assistant
}

func (s *replayStack) tasks(session string) int {
	s.t.Helper()
	var count int
	if err := s.database.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE session_id=?`, session).Scan(&count); err != nil {
		s.t.Fatal(err)
	}
	return count
}

// awaitApprovalCard drives the first turn up to the approval card: the model
// has spoken twice and the session title was requested, and nothing paid has
// been sent upstream.
func (s *replayStack) awaitApprovalCard(session string) replayCard {
	s.t.Helper()
	s.say(session, replayPrompt)
	s.await("the approval card", 60*time.Second, func() bool {
		return s.llm.calls.Load() >= 3 && s.turnsEnded(session) && s.pendingGrants(session) == 1
	})
	if s.rodin.calls.Load() != 0 || s.tasks(session) != 0 {
		s.t.Fatal("something was submitted before the person approved")
	}
	return s.cards(session)[0]
}

// bindDownloadNames gives the files of a replayed Rodin download list back the
// names the sample had to sanitize to opaque ids. The sample keeps each file's
// URL path, and with it the extension, so the first GLB gets the declared
// "_pbr.glb" suffix, a later GLB a plain ".glb" and the WebP the declared
// "preview.webp": exactly what the Offering's download_files rules select on.
// It returns the number of names it bound.
func bindDownloadNames(t *testing.T, body []byte) ([]byte, int) {
	t.Helper()
	var list struct {
		List []map[string]string `json:"list"`
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(body, &list) != nil || json.Unmarshal(body, &document) != nil {
		t.Fatalf("download response is not the recorded shape: %s", body)
	}
	pbr := false
	for _, file := range list.List {
		switch {
		case strings.HasSuffix(file["url"], ".glb") && !pbr:
			file["name"] += "_pbr.glb"
			pbr = true
		case strings.HasSuffix(file["url"], ".glb"):
			file["name"] += ".glb"
		case strings.HasSuffix(file["url"], ".webp"):
			file["name"] = "preview.webp"
		}
	}
	encoded, err := json.Marshal(list.List)
	if err != nil {
		t.Fatal(err)
	}
	document["list"] = encoded
	rebound, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return rebound, len(list.List)
}

// opaqueChatFields reduces the chat request fields the sample records as
// opaque (the conversation, the tool declarations and the thinking switches)
// to the recorded "ok"; every other field is still compared as it is.
func opaqueChatFields(raw []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	for _, key := range []string{"messages", "tools", "thinking", "stream_options"} {
		if _, present := fields[key]; present {
			fields[key] = json.RawMessage(`"ok"`)
		}
	}
	reduced, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return reduced
}

// ownerID is the signed-in person's ID as the application stored it.
func (s *replayStack) ownerID() string {
	s.t.Helper()
	var owner string
	if err := s.database.QueryRow(`SELECT id FROM authentication_users WHERE username=?`, replayAdminUser).Scan(&owner); err != nil {
		s.t.Fatal(err)
	}
	return owner
}

type replayGrant struct{ Status, DecidedBy, Source, TaskID string }

func (s *replayStack) grant(requestID string) replayGrant {
	s.t.Helper()
	var g replayGrant
	if err := s.database.QueryRow(`SELECT status, COALESCE(decided_by,''), source, COALESCE(task_id,'') FROM execution_grants WHERE request_id=?`,
		requestID).Scan(&g.Status, &g.DecidedBy, &g.Source, &g.TaskID); err != nil {
		s.t.Fatal(err)
	}
	return g
}

func (s *replayStack) auditEvents(requestID string) []string {
	s.t.Helper()
	rows, err := s.database.Query(`SELECT event || ':' || actor_id FROM execution_grant_audit WHERE request_id=? ORDER BY id`, requestID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			s.t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

// expire moves a waiting approval past its expiry the way time would: the
// grant's own clock is its last update, which the Schema's TTL counts from.
func (s *replayStack) expire(requestID string) {
	s.t.Helper()
	past := time.Now().UTC().Add(-time.Duration(s.application.schema.Chat.PendingApprovals.TTLSeconds+3600) * time.Second)
	writer, err := sql.Open("sqlite", "file:"+s.databasePath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		s.t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Exec(`UPDATE execution_grants SET updated_at=?, created_at=? WHERE request_id=? AND status='pending'`,
		past.Format(time.RFC3339Nano), past.Format(time.RFC3339Nano), requestID); err != nil {
		s.t.Fatal(err)
	}
}

// message returns the stored assistant message with the ID, as the chat API
// lists it.
func (s *replayStack) message(session, id string) (map[string]any, bool) {
	s.t.Helper()
	for _, message := range s.assistantMessages(session) {
		if message["id"] == id {
			return message, true
		}
	}
	return nil, false
}

// storedContent reads a message's text straight from the store, so a polling
// loop does not meet the public rate limit.
func (s *replayStack) storedContent(session, id string) (string, bool) {
	var content string
	err := s.database.QueryRow(`SELECT content FROM go_chat_messages WHERE session_id=? AND id=?`, session, id).Scan(&content)
	return content, err == nil
}

// awaitMessage waits until the message exists and returns it as the chat API
// lists it.
func (s *replayStack) awaitMessage(session, id string) map[string]any {
	s.t.Helper()
	s.await("the message "+id+" in the chat", 30*time.Second, func() bool {
		_, ok := s.storedContent(session, id)
		return ok
	})
	message, ok := s.message(session, id)
	if !ok {
		s.t.Fatalf("message %s is stored but not listed", id)
	}
	return message
}

// chatRequest is the part of a recorded outgoing chat request the Agent
// assertions read.
type chatRequest struct {
	Messages []struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			Function struct{ Name, Arguments string } `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct{ Name string } `json:"function"`
	} `json:"tools"`
}

func (s *replayStack) chatRequest(index int) chatRequest {
	s.t.Helper()
	sent := s.llm.requests()
	if index >= len(sent) {
		s.t.Fatalf("only %d chat requests were sent", len(sent))
	}
	var request chatRequest
	if err := json.Unmarshal(sent[index], &request); err != nil {
		s.t.Fatal(err)
	}
	return request
}

func TestAgentFlowSampleRunsThroughTheWholeStack(t *testing.T) {
	exchanges, policy := loadAgentFlowLiveCandidate(t)
	stack := newReplayStack(t, exchanges, policy)
	session := stack.newSession()
	card := stack.awaitApprovalCard(session)
	if card.Capability != "text-to-3d" || card.Model == "" {
		t.Fatalf("approval card = %+v", card)
	}

	// Turn one: the Agent saw the person's message and the declared tools,
	// called create_model_3d with the prompt the sample recorded, and got the
	// "waiting for approval" result back instead of a submission.
	first := stack.chatRequest(0)
	if last := first.Messages[len(first.Messages)-1]; last.Role != "user" || last.Content != replayPrompt {
		t.Fatalf("first request does not end with the person's message: %+v", first.Messages)
	}
	declared := false
	for _, tool := range first.Tools {
		declared = declared || tool.Function.Name == replayBoundTool
	}
	if !declared {
		t.Fatalf("%s was not offered to the model", replayBoundTool)
	}
	followUp := stack.chatRequest(1)
	var call, result string
	for _, message := range followUp.Messages {
		if message.Role == "assistant" && len(message.ToolCalls) == 1 {
			call = message.ToolCalls[0].Function.Name + " " + message.ToolCalls[0].Function.Arguments
		}
		if message.Role == "tool" {
			result = message.Content
		}
	}
	if !strings.HasPrefix(call, replayBoundTool+` {"prompt":"A simple solid blue cube`) ||
		!strings.Contains(result, stack.application.schema.Chat.Agent.ConsentWaitingContent) {
		t.Fatalf("tool call %q and result %q were not fed back to the model", call, result)
	}
	if title := stack.chatRequest(2); len(title.Tools) != 0 {
		t.Fatalf("the third request is the title request, got tools %v", title.Tools)
	}
	if stack.grant(card.RequestID).Status != "pending" {
		t.Fatalf("grant before the decision = %+v", stack.grant(card.RequestID))
	}

	// The person approves on the card: one Task, bound to that grant.
	response, outcome := stack.decide(card, "approve")
	if response.Code != http.StatusOK || outcome.TaskID == "" || outcome.MessageID == "" {
		t.Fatalf("approve = %d %s", response.Code, response.Body)
	}
	stack.await("the public Task to succeed", 180*time.Second, func() bool {
		return stack.taskState(outcome.TaskID) == "SUCCEEDED"
	})
	if stack.tasks(session) != 1 {
		t.Fatalf("tasks = %d, want exactly one", stack.tasks(session))
	}
	task := stack.task(outcome.TaskID)
	if task.State != "SUCCEEDED" || task.Capability != "text-to-3d" {
		t.Fatalf("task = %+v", task)
	}
	var model, thumbnail string
	for _, asset := range task.Assets {
		switch {
		case asset.Role == "model" && asset.Variant == "glb" && asset.MediaType == "model/gltf-binary":
			model = asset.ID
		case asset.Role == "thumbnail" && asset.MediaType == "image/webp":
			thumbnail = asset.ID
		}
	}
	if model == "" || thumbnail == "" {
		t.Fatalf("required model and background thumbnail slots are not both delivered: %+v", task.Assets)
	}
	_, glb, err := wirefixture.SyntheticMedia("glb-triangle")
	if err != nil {
		t.Fatal(err)
	}
	delivered := stack.do(http.MethodGet, "/api/v1/tasks/"+outcome.TaskID+"/assets/"+model, "")
	if delivered.Code != http.StatusOK || !bytes.Equal(delivered.Body.Bytes(), glb) {
		t.Fatalf("model delivery = %d, %d bytes (want %d recorded bytes)", delivered.Code, delivered.Body.Len(), len(glb))
	}

	// The approval record: decided by the person, then bound to the Task.
	owner := stack.ownerID()
	granted := stack.grant(card.RequestID)
	if granted.Status != "bound" || granted.DecidedBy != owner || granted.TaskID != outcome.TaskID ||
		granted.Source != string(executiongrant.AgentPending) {
		t.Fatalf("grant = %+v, want bound to %s and decided by %s", granted, outcome.TaskID, owner)
	}
	if events := stack.auditEvents(card.RequestID); !slices.Contains(events, "approved:"+owner) {
		t.Fatalf("audit = %v, want the person's approval", events)
	}

	// The result came back into the chat in the default (Chinese) copy.
	modelName := ""
	for _, offering := range stack.application.generation.Offerings(generation.TextTo3D) {
		if offering.ID == "hyper3d/rodin-gen-2.5/text-to-3d" {
			modelName = offering.ModelName
		}
	}
	wantContent := strings.NewReplacer("{model}", modelName).Replace(stack.application.schema.Chat.PendingApprovals.Messages.Succeeded)
	stack.await("the finished result in the chat", 60*time.Second, func() bool {
		posted, _ := stack.storedContent(session, outcome.MessageID)
		return posted == wantContent
	})
	message := stack.awaitMessage(session, outcome.MessageID)
	var calls []map[string]any
	if err := json.Unmarshal([]byte(message["toolCalls"].(string)), &calls); err != nil || len(calls) != 1 ||
		calls[0]["name"] != replayBoundTool || calls[0]["taskId"] != outcome.TaskID || calls[0]["approvalSource"] != "human" {
		t.Fatalf("posted tool calls = %v, %v", message["toolCalls"], err)
	}

	// Everything the sample recorded was sent, in order, and only to it.
	if err := stack.replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	stack.assertClosed(1, 3)
}

// assertClosed checks that the opaque-name bindings were applied exactly where
// the sample needs them and that nothing was refused or reached a connection
// without a recording.
func (s *replayStack) assertClosed(toolBindings, nameBindings int64) {
	s.t.Helper()
	if s.toolsBound.Load() != toolBindings || s.namesBound.Load() != nameBindings {
		s.t.Fatalf("bindings: tool names %d (want %d), file names %d (want %d)",
			s.toolsBound.Load(), toolBindings, s.namesBound.Load(), nameBindings)
	}
	if s.stray.Load() != 0 || s.llm.refused.Load() != 0 || s.rodin.refused.Load() != 0 {
		s.t.Fatalf("refused upstream requests: stray=%d llm=%d rodin=%d", s.stray.Load(), s.llm.refused.Load(), s.rodin.refused.Load())
	}
}

// firstTurn is the part of the sample before the paid step: the model's two
// answers and the title request. Nothing in it reaches a provider.
func firstTurn(t *testing.T) ([]wirefixture.Exchange, wirefixture.Policy) {
	t.Helper()
	exchanges, policy := loadAgentFlowLiveCandidate(t)
	for i, exchange := range exchanges[:3] {
		if exchange.Metadata.Connection != replayLLMConnection {
			t.Fatalf("exchange %d (%s) is not part of the first turn", i, exchange.Metadata.Connection)
		}
	}
	if exchanges[3].Metadata.Connection != replayRodinConnection {
		t.Fatal("the paid submission no longer follows the first turn")
	}
	return exchanges[:3], policy
}

// The person refuses the card, in English: nothing is submitted, the chat says
// so in the turn's language, and the English copy differs from the default.
func TestAgentFlowRefusedApprovalSubmitsNothingAndAnswersInTheTurnLanguage(t *testing.T) {
	exchanges, policy := firstTurn(t)
	stack := newReplayStack(t, exchanges, policy)
	stack.language = "en"
	session := stack.newSession()
	card := stack.awaitApprovalCard(session)

	response, outcome := stack.decide(card, "deny")
	if response.Code != http.StatusOK || outcome.Status != "denied" || outcome.TaskID != "" {
		t.Fatalf("deny = %d %s", response.Code, response.Body)
	}
	message := stack.awaitMessage(session, outcome.MessageID)
	zh := stack.application.schema.Chat.PendingApprovals.Messages
	content, _ := message["content"].(string)
	// The refused quote carries the frozen native amounts, in the English copy
	// of the Schema; no Chinese text of the default copy may remain.
	if !strings.HasPrefix(content, "Declined: this Rodin Gen-2.5 step was not run.") ||
		!strings.Contains(content, "a total of 3 Hyper3D credits") ||
		strings.ContainsFunc(content, func(r rune) bool { return unicode.Is(unicode.Han, r) }) ||
		content == zh.Denied || content == zh.DeniedNativeExtra {
		t.Fatalf("denial = %q, want the Schema's English copy of the frozen quote", content)
	}
	granted := stack.grant(card.RequestID)
	if granted.Status != "denied" || granted.DecidedBy != stack.ownerID() || granted.TaskID != "" {
		t.Fatalf("grant = %+v", granted)
	}
	if stack.tasks(session) != 0 || stack.rodin.calls.Load() != 0 {
		t.Fatalf("a refused card submitted work: tasks=%d provider calls=%d", stack.tasks(session), stack.rodin.calls.Load())
	}
	// A decision is final: approving the refused card later is a conflict.
	if again, _ := stack.decide(card, "approve"); again.Code != http.StatusConflict {
		t.Fatalf("approving a refused card = %d %s, want 409", again.Code, again.Body)
	}
	if err := stack.replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	stack.assertClosed(1, 0)
}

// Nobody decides in time: the sweep tells the chat the approval expired, and a
// late approval is refused without any submission.
func TestAgentFlowExpiredApprovalCannotBeApprovedLate(t *testing.T) {
	exchanges, policy := firstTurn(t)
	stack := newReplayStack(t, exchanges, policy)
	session := stack.newSession()
	card := stack.awaitApprovalCard(session)

	stack.expire(card.RequestID)
	message := stack.awaitMessage(session, pendingapproval.MessageID(card.RequestID))
	if message["content"] != stack.application.schema.Chat.PendingApprovals.Messages.Expired {
		t.Fatalf("expiry message = %q", message["content"])
	}
	late, _ := stack.decide(card, "approve")
	if late.Code != http.StatusConflict || !strings.Contains(late.Body.String(), "pending_approval_expired") {
		t.Fatalf("late approval = %d %s, want 409 pending_approval_expired", late.Code, late.Body)
	}
	if stack.grant(card.RequestID).Status != "pending" {
		t.Fatalf("an expired grant must not become approved: %+v", stack.grant(card.RequestID))
	}
	if stack.tasks(session) != 0 || stack.rodin.calls.Load() != 0 {
		t.Fatalf("an expired card submitted work: tasks=%d provider calls=%d", stack.tasks(session), stack.rodin.calls.Load())
	}
	if err := stack.replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	stack.assertClosed(1, 0)
}

// An upstream request the sample does not contain has nowhere to go: the paid
// submission finds an exhausted replay, the Task fails as an ambiguous
// submission that is not retried blindly, and the replay reports the failure.
func TestAgentFlowUnrecordedUpstreamRequestFailsTheReplayAndNeverResubmits(t *testing.T) {
	exchanges, policy := firstTurn(t)
	stack := newReplayStack(t, exchanges, policy)
	session := stack.newSession()
	card := stack.awaitApprovalCard(session)

	response, outcome := stack.decide(card, "approve")
	if response.Code != http.StatusOK || outcome.TaskID == "" {
		t.Fatalf("approve = %d %s", response.Code, response.Body)
	}
	stack.await("the unrecorded submission to be refused", 60*time.Second, func() bool {
		return stack.rodin.refused.Load() > 0
	})
	stack.await("the Task to fail", 60*time.Second, func() bool { return stack.taskState(outcome.TaskID) == "FAILED" })
	var failed struct {
		Failure struct {
			Class     string `json:"class"`
			Retryable bool   `json:"retryable"`
			Ambiguous bool   `json:"ambiguous"`
		} `json:"failure"`
	}
	stack.decode(stack.do(http.MethodGet, "/api/v1/tasks/"+outcome.TaskID, ""), http.StatusOK, &failed)
	if failed.Failure.Class != "submission_unknown" || failed.Failure.Retryable || !failed.Failure.Ambiguous {
		t.Fatalf("failure = %+v, want an ambiguous submission_unknown", failed.Failure)
	}
	// The provider may have taken the job: no blind paid retry, no new request.
	refusedBefore := stack.rodin.refused.Load()
	retry := stack.do(http.MethodPost, "/api/v1/tasks/"+outcome.TaskID+"/retry", "")
	time.Sleep(500 * time.Millisecond)
	if retry.Code != http.StatusConflict || !strings.Contains(retry.Body.String(), "task_not_retryable") ||
		stack.rodin.refused.Load() != refusedBefore || stack.tasks(session) != 1 {
		t.Fatalf("retry of an ambiguous failure = %d %s; refused %d -> %d; tasks=%d",
			retry.Code, retry.Body, refusedBefore, stack.rodin.refused.Load(), stack.tasks(session))
	}
	if err := stack.replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("AssertConsumed = %v, want ErrReplayFailed", err)
	}
	// The refusal is the replay's, not the network's.
	if stack.stray.Load() != 0 || stack.rodin.calls.Load() != 0 {
		t.Fatalf("stray=%d provider calls=%d", stack.stray.Load(), stack.rodin.calls.Load())
	}
}

// The base transport the application offers is the real network. The replay
// stack never uses it, whatever the request.
func TestReplayStackNeverFallsBackToTheNetwork(t *testing.T) {
	exchanges, policy := firstTurn(t)
	stack := newReplayStack(t, exchanges, policy)
	var reached atomic.Int64
	network := roundTripFunc(func(*http.Request) (*http.Response, error) {
		reached.Add(1)
		return nil, errors.New("the real network was used")
	})
	for _, id := range []string{replayLLMConnection, replayRodinConnection, "meshy", "tripo", "unknown"} {
		transport := stack.connectionTransport(id, network)
		request, err := http.NewRequest(http.MethodPost, "https://api.example.invalid/v1/anything", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if response, err := transport.RoundTrip(request); err == nil {
			_ = response.Body.Close()
			t.Fatalf("%s: an unrecorded request was answered", id)
		}
	}
	if reached.Load() != 0 {
		t.Fatalf("the real network was reached %d times", reached.Load())
	}
	if stack.stray.Load() != 3 {
		t.Fatalf("stray connections refused = %d, want 3", stack.stray.Load())
	}
}

// The person cancels the Task while the provider is still working: polling
// stops for good, and the canceled paid Task cannot be retried behind the
// person's back.
func TestAgentFlowCancelledTaskStopsPollingAndIsNotRetried(t *testing.T) {
	exchanges, policy := loadAgentFlowLiveCandidate(t)
	// The first turn, the paid submission and the first two status polls.
	stack := newReplayStack(t, exchanges[:6], policy)
	session := stack.newSession()
	card := stack.awaitApprovalCard(session)
	_, outcome := stack.decide(card, "approve")
	if outcome.TaskID == "" {
		t.Fatal("approval did not create a Task")
	}
	stack.await("the submission and two status polls", 60*time.Second, func() bool { return stack.rodin.calls.Load() == 3 })
	if state := stack.taskState(outcome.TaskID); state != "RUNNING" {
		t.Fatalf("task = %q, want RUNNING while the provider works", state)
	}

	var canceled replayTask
	stack.decode(stack.do(http.MethodPost, "/api/v1/tasks/"+outcome.TaskID+"/cancel", ""), http.StatusOK, &canceled)
	if canceled.State != "CANCELED" {
		t.Fatalf("cancel = %+v", canceled)
	}
	// Longer than the one-second poll interval of the patched Offering.
	time.Sleep(2500 * time.Millisecond)
	if stack.rodin.calls.Load() != 3 || stack.rodin.refused.Load() != 0 || stack.taskState(outcome.TaskID) != "CANCELED" {
		t.Fatalf("polling continued after the cancellation: calls=%d refused=%d state=%s",
			stack.rodin.calls.Load(), stack.rodin.refused.Load(), stack.taskState(outcome.TaskID))
	}

	// A canceled paid Task is not retried on the person's behalf: the retry is
	// refused, opens no approval request and sends nothing upstream.
	again := stack.do(http.MethodPost, "/api/v1/tasks/"+outcome.TaskID+"/retry", "")
	if again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), "task_not_retryable") {
		t.Fatalf("retry of a canceled Task = %d %s, want 409 task_not_retryable", again.Code, again.Body)
	}
	time.Sleep(1500 * time.Millisecond)
	var grants int
	if err := stack.database.QueryRow(`SELECT COUNT(*) FROM execution_grants WHERE session_id=?`, session).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("grants = %d (%v), want only the original", grants, err)
	}
	if stack.tasks(session) != 1 || stack.rodin.calls.Load() != 3 {
		t.Fatalf("a retry reached the provider: tasks=%d provider calls=%d", stack.tasks(session), stack.rodin.calls.Load())
	}
	if err := stack.replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	stack.assertClosed(1, 0)
}

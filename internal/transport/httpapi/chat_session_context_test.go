package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func reasoningPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/reasoning"
}

func contextPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/context"
}

func decodeMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return decoded
}

// #322: the session thinking level is one of the official levels of the
// session's model (the harness chats with DeepSeek: low/high/max, default
// low), persisted on the session and scoped to its owner.
func TestChatSessionReasoningReadsAndStoresTheModelLevels(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	read := grantTestRequest(t, handler, cookie, http.MethodGet, reasoningPath(sessionID), "", "")
	body := decodeMap(t, read.Body.String())
	if read.Code != http.StatusOK || body["reasoningEffort"] != "low" || body["reasoningDefault"] != "low" ||
		fmt.Sprint(body["reasoningLevels"]) != "[low high max]" || body["id"] != sessionID {
		t.Fatalf("default level: %d %s", read.Code, read.Body.String())
	}
	put := grantTestRequest(t, handler, cookie, http.MethodPut, reasoningPath(sessionID), `{"reasoningEffort":"max"}`, "")
	if put.Code != http.StatusOK || decodeMap(t, put.Body.String())["reasoningEffort"] != "max" {
		t.Fatalf("set max: %d %s", put.Code, put.Body.String())
	}
	for _, invalid := range []string{`{"reasoningEffort":"xhigh"}`, `{"reasoningEffort":"MAX"}`, `{}`, `not json`} {
		response := grantTestRequest(t, handler, cookie, http.MethodPut, reasoningPath(sessionID), invalid, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: %d %s", invalid, response.Code, response.Body.String())
		}
	}
	reread := grantTestRequest(t, handler, cookie, http.MethodGet, reasoningPath(sessionID), "", "")
	if decodeMap(t, reread.Body.String())["reasoningEffort"] != "max" {
		t.Fatalf("level not persisted: %s", reread.Body.String())
	}
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	now := time.Now().UTC()
	if _, err := repository.CreateSession(context.Background(), chat.Session{ID: "foreign-session", OwnerID: "owner-b",
		OwnerName: "Other", Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, response := range []*httptest.ResponseRecorder{
		grantTestRequest(t, handler, cookie, http.MethodGet, reasoningPath("foreign-session"), "", ""),
		grantTestRequest(t, handler, cookie, http.MethodPut, reasoningPath("foreign-session"), `{"reasoningEffort":"high"}`, ""),
		grantTestRequest(t, handler, cookie, http.MethodGet, contextPath("foreign-session"), "", ""),
		grantTestRequest(t, handler, cookie, http.MethodGet, reasoningPath("no-such-session"), "", ""),
	} {
		if response.Code != http.StatusNotFound {
			t.Fatalf("foreign or missing session: %d %s", response.Code, response.Body.String())
		}
	}
	if state, found, err := repository.SessionContext(context.Background(), "owner-b", "foreign-session"); err != nil || !found || state.ReasoningEffort != "" {
		t.Fatalf("foreign session changed: %+v %v %v", state, found, err)
	}
	// Without the owner's cookie the session is not found, like any foreign one.
	if anonymous := grantTestRequest(t, handler, nil, http.MethodGet, reasoningPath(sessionID), "", ""); anonymous.Code != http.StatusNotFound &&
		anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %d", anonymous.Code)
	}
}

// The context member is null until the upstream reports usage, never an
// estimate; the window is the profile's declared value.
func TestChatSessionContextReportsUsageWindowAndCompactions(t *testing.T) {
	t.Parallel()
	module, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	read := grantTestRequest(t, handler, cookie, http.MethodGet, contextPath(sessionID), "", "")
	if read.Code != http.StatusOK ||
		!strings.Contains(read.Body.String(), `"contextTokens":null`) ||
		!strings.Contains(read.Body.String(), `"contextWindow":1000000`) ||
		!strings.Contains(read.Body.String(), `"compactions":[]`) {
		t.Fatalf("empty context: %d %s", read.Code, read.Body.String())
	}
	value, _ := fixtureExecutions.Load(module)
	repository := value.(*fixtureExecutionState).repository
	ctx := context.Background()
	if err := repository.RecordContextUsage(ctx, "owner-a", sessionID, &chat.ContextUsage{PromptTokens: 356000, CompletionTokens: 120}); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	if _, err := repository.SaveCompaction(ctx, chat.Compaction{OwnerID: "owner-a", SessionID: sessionID, Trigger: chat.CompactionAuto,
		Summary: "private summary text", ThroughMessageID: "message-9", Rounds: 4, CreatedAt: created}, ""); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordContextUsage(ctx, "owner-a", sessionID, &chat.ContextUsage{PromptTokens: 2000, CompletionTokens: 345}); err != nil {
		t.Fatal(err)
	}
	read = grantTestRequest(t, handler, cookie, http.MethodGet, contextPath(sessionID), "", "")
	body := decodeMap(t, read.Body.String())
	compactions, _ := body["compactions"].([]any)
	if read.Code != http.StatusOK || body["contextTokens"] != float64(2345) || len(compactions) != 1 {
		t.Fatalf("context: %d %s", read.Code, read.Body.String())
	}
	record, _ := compactions[0].(map[string]any)
	if record["trigger"] != "auto" || record["rounds"] != float64(4) || record["throughMessageId"] != "message-9" ||
		record["createdAt"] != "2026-10-22T09:30:00Z" {
		t.Fatalf("compaction record = %#v", record)
	}
	// The summary itself stays on the server.
	if strings.Contains(read.Body.String(), "private summary text") {
		t.Fatalf("context read leaks the summary: %s", read.Body.String())
	}
}

// compactingScheduler is a turn scheduler that also compacts, answering with
// the error the test sets.
type compactingScheduler struct {
	noOpChatScheduler
	err   error
	calls []string
}

func (scheduler *compactingScheduler) CompactContext(_ context.Context, ownerID, sessionID string) (chat.ContextView, error) {
	scheduler.calls = append(scheduler.calls, ownerID+"/"+sessionID)
	if scheduler.err != nil {
		return chat.ContextView{}, scheduler.err
	}
	return chat.ContextView{Window: 1000000, Compactions: []chat.Compaction{{ID: "c1", Trigger: chat.CompactionManual, Rounds: 3,
		ThroughMessageID: "m6", CreatedAt: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)}}}, nil
}

func compactionBrowser(t *testing.T, scheduler *compactingScheduler) (http.Handler, *http.Cookie, string) {
	t.Helper()
	record := authentication.UserRecord{
		ID: "owner-a", Username: "grant-user",
		PasswordHash: "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2.",
		Nickname:     "Grant User", Email: "grant@example.test", Active: true,
	}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{record.Username: record},
		byID:       map[string]authentication.UserRecord{record.ID: record},
	}
	_, handler, _, _ := newHTTPHarnessWithCatalogOverride(t, []string{"user"}, nil,
		func(config *authentication.Config) { config.Directory = directory }, scheduler, nil, nil)
	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"grant-user","password":"correct-horse"}`))
	request.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(login, request)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	session := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions", `{"title":"compaction"}`, "")
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("session=%s err=%v", session.Body.String(), err)
	}
	return handler, cookie, created.ID
}

func TestChatSessionCompactionMapsEveryOutcome(t *testing.T) {
	t.Parallel()
	scheduler := &compactingScheduler{}
	handler, cookie, sessionID := compactionBrowser(t, scheduler)
	path := contextPath(sessionID) + "/compaction"
	ok := grantTestRequest(t, handler, cookie, http.MethodPost, path, "", "")
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"throughMessageId":"m6"`) ||
		!strings.Contains(ok.Body.String(), `"contextTokens":null`) || len(scheduler.calls) != 1 || scheduler.calls[0] != "owner-a/"+sessionID {
		t.Fatalf("compaction: %d %s calls=%v", ok.Code, ok.Body.String(), scheduler.calls)
	}
	for _, testCase := range []struct {
		err    error
		status int
		text   string
	}{
		{chat.ErrCompactionTurnActive, http.StatusConflict, "回复还在进行中"},
		{chat.ErrNothingToCompact, http.StatusUnprocessableEntity, "最近 5 轮以前没有可压缩的内容"},
		{fmt.Errorf("%w: upstream 503", chat.ErrCompactionFailed), http.StatusBadGateway, "对话保持原样"},
		{chat.ErrSessionNotFound, http.StatusNotFound, ""},
	} {
		scheduler.err = testCase.err
		response := grantTestRequest(t, handler, cookie, http.MethodPost, path, "", "")
		if response.Code != testCase.status || !strings.Contains(response.Body.String(), testCase.text) {
			t.Fatalf("%v: %d %s", testCase.err, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "upstream 503") {
			t.Fatalf("the cause leaked into the response: %s", response.Body.String())
		}
	}
	calls := len(scheduler.calls)
	if anonymous := grantTestRequest(t, handler, nil, http.MethodPost, path, "", ""); anonymous.Code == http.StatusOK ||
		len(scheduler.calls) != calls && scheduler.calls[len(scheduler.calls)-1] == "owner-a/"+sessionID {
		t.Fatalf("anonymous compaction: %d calls=%v", anonymous.Code, scheduler.calls)
	}
}

func TestChatSessionCompactionIsUnavailableWithoutACompactor(t *testing.T) {
	t.Parallel()
	handler, cookie, sessionID := grantTestBrowser(t)
	response := grantTestRequest(t, handler, cookie, http.MethodPost, contextPath(sessionID)+"/compaction", "", "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "没有开启上下文压缩") {
		t.Fatalf("no compactor: %d %s", response.Code, response.Body.String())
	}
}

func modelPath(sessionID string) string {
	return "/api/v1/chat/sessions/" + sessionID + "/model"
}

// #322 Scope 5: the harness deployment has one chat model and no settings
// source, so the session lists that model and cannot pick another; foreign
// sessions are not found.
func TestChatSessionModelReadsTheOfferedModelsOwnerScoped(t *testing.T) {
	t.Parallel()
	_, handler, cookie, sessionID := grantTestBrowserWithApplication(t, nil, []string{"user"})
	read := grantTestRequest(t, handler, cookie, http.MethodGet, modelPath(sessionID), "", "")
	body := decodeMap(t, read.Body.String())
	models, _ := body["chatModels"].([]any)
	if read.Code != http.StatusOK || body["id"] != sessionID || body["chatModelFallback"] != false || len(models) != 1 ||
		models[0].(map[string]any)["model"] != "deepseek" {
		t.Fatalf("read: %d %s", read.Code, read.Body.String())
	}
	if put := grantTestRequest(t, handler, cookie, http.MethodPut, modelPath(sessionID), `{"chatModel":"GLM"}`, ""); put.Code != http.StatusConflict {
		t.Fatalf("pick without a settings source: %d %s", put.Code, put.Body.String())
	}
	for _, invalid := range []string{`{}`, `{"chatModel":7}`, `not json`} {
		if put := grantTestRequest(t, handler, cookie, http.MethodPut, modelPath(sessionID), invalid, ""); put.Code != http.StatusBadRequest {
			t.Fatalf("body %q: %d %s", invalid, put.Code, put.Body.String())
		}
	}
	for _, response := range []*httptest.ResponseRecorder{
		grantTestRequest(t, handler, cookie, http.MethodGet, modelPath("no-such-session"), "", ""),
		grantTestRequest(t, handler, cookie, http.MethodPut, modelPath("no-such-session"), `{"chatModel":""}`, ""),
	} {
		if response.Code != http.StatusNotFound {
			t.Fatalf("missing session: %d %s", response.Code, response.Body.String())
		}
	}
}

package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #665: the request that starts a turn may carry an Idempotency-Key. When its
// answer is lost the browser sends the same request again with the same key;
// the server answers the turn the key started instead of starting a second,
// paid one.

// lockedChatScheduler records the scheduled turns of concurrent requests.
type lockedChatScheduler struct {
	mu       sync.Mutex
	commands []chat.ProcessTurn
	err      error
}

func (scheduler *lockedChatScheduler) Schedule(_ context.Context, command chat.ProcessTurn) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.commands = append(scheduler.commands, command)
	return scheduler.err
}

func (scheduler *lockedChatScheduler) CancelTurn(context.Context, string, string) (bool, error) {
	return false, nil
}

func (scheduler *lockedChatScheduler) ResumeConsentWait(string, string) bool { return false }

func (scheduler *lockedChatScheduler) scheduled() int {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return len(scheduler.commands)
}

func keyedChatSend(handler http.Handler, user, sessionID, key, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages", bytes.NewReader([]byte(body)))
	request.Header.Set("X-User-ID", user)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newKeyedChatSession(t *testing.T, handler http.Handler) string {
	t.Helper()
	var session map[string]any
	decodeChatRecorder(t, performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`), &session)
	return session["id"].(string)
}

func acceptedMessageID(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	var body map[string]any
	decodeChatRecorder(t, response, &body)
	id, _ := body["messageId"].(string)
	if id == "" {
		t.Fatalf("accepted send without message_id: %s", response.Body)
	}
	return id
}

func chatMessageCount(t *testing.T, handler http.Handler, sessionID string) int {
	t.Helper()
	var messages []map[string]any
	decodeChatRecorder(t, performChatRequest(handler, http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages", ""), &messages)
	return len(messages)
}

func TestChatSendIdempotencyKeyRepeatAnswersTheFirstTurn665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)

	first := acceptedMessageID(t, keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`))
	// The first turn still runs (the recording scheduler never ends it): the
	// repeat is answered with it, not refused as a busy session.
	repeat := keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`)
	if got := acceptedMessageID(t, repeat); got != first {
		t.Fatalf("repeat answered %q, want the first turn %q", got, first)
	}
	if scheduler.scheduled() != 1 {
		t.Fatalf("scheduled %d turns, want 1", scheduler.scheduled())
	}
	if count := chatMessageCount(t, handler, sessionID); count != 2 {
		t.Fatalf("session has %d messages after a repeat, want 2", count)
	}
}

func TestChatSendIdempotencyKeyWithAnotherRequestIsAConflict665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)
	acceptedMessageID(t, keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`))

	conflict := keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a dog"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", conflict.Code, conflict.Body)
	}
	var problem map[string]any
	decodeChatRecorder(t, conflict, &problem)
	if problem["code"] != "idempotency_conflict" || problem["detail"] == "" || problem["request_id"] == "" {
		t.Fatalf("conflict body = %s", conflict.Body)
	}
	if scheduler.scheduled() != 1 || chatMessageCount(t, handler, sessionID) != 2 {
		t.Fatalf("a conflicting repeat started something: scheduled=%d", scheduler.scheduled())
	}
}

func TestChatSendWithoutIdempotencyKeyStartsEveryTurn665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)
	acceptedMessageID(t, keyedChatSend(handler, "local-user", sessionID, "", `{"content":"draw a cat"}`))
	// Without a key the same request is a new send: the running turn refuses
	// it as before.
	repeat := keyedChatSend(handler, "local-user", sessionID, "", `{"content":"draw a cat"}`)
	if repeat.Code != http.StatusConflict {
		t.Fatalf("unkeyed repeat status=%d body=%s, want the busy-session 409", repeat.Code, repeat.Body)
	}
	var body map[string]any
	decodeChatRecorder(t, repeat, &body)
	if body["code"] == "idempotency_conflict" {
		t.Fatalf("unkeyed repeat answered as a key conflict: %s", repeat.Body)
	}
	if scheduler.scheduled() != 1 {
		t.Fatalf("scheduled %d turns", scheduler.scheduled())
	}
}

func TestChatSendIdempotencyKeyIsScopedToOwnerAndSession665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)
	otherSession := newKeyedChatSession(t, handler)
	first := acceptedMessageID(t, keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`))

	// The same key in another session of the owner is another send.
	other := acceptedMessageID(t, keyedChatSend(handler, "local-user", otherSession, "send-1", `{"content":"draw a cat"}`))
	if other == first {
		t.Fatal("the key of one session answered a send to another session")
	}
	// Another person never reads the owner's turn through the key.
	foreign := keyedChatSend(handler, "intruder", sessionID, "send-1", `{"content":"draw a cat"}`)
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), first) {
		t.Fatalf("foreign repeat status=%d body=%s, want 404 without the turn", foreign.Code, foreign.Body)
	}
	if scheduler.scheduled() != 2 {
		t.Fatalf("scheduled %d turns, want 2", scheduler.scheduled())
	}
}

func TestChatSendConcurrentCopiesStartOneTurn665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)

	const copies = 8
	responses := make([]*httptest.ResponseRecorder, copies)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range copies {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			responses[index] = keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`)
		}()
	}
	start.Done()
	done.Wait()
	ids := map[string]bool{}
	for _, response := range responses {
		ids[acceptedMessageID(t, response)] = true
	}
	if len(ids) != 1 || scheduler.scheduled() != 1 || chatMessageCount(t, handler, sessionID) != 2 {
		t.Fatalf("concurrent copies: ids=%v scheduled=%d", ids, scheduler.scheduled())
	}
}

func TestChatSendIdempotencyKeyTooLongIsRefused665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)
	response := keyedChatSend(handler, "local-user", sessionID, strings.Repeat("k", 201), `{"content":"draw a cat"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "idempotency_key_required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if scheduler.scheduled() != 0 {
		t.Fatal("a refused key started a turn")
	}
}

// A turn whose scheduling was refused failed at once and never ran: its key
// is released, so the repeat starts a fresh turn.
func TestChatSendRefusedSchedulingReleasesTheKey665(t *testing.T) {
	t.Parallel()
	scheduler := &lockedChatScheduler{err: errors.New("queue full")}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	sessionID := newKeyedChatSession(t, handler)
	refused := keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`)
	if refused.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", refused.Code, refused.Body)
	}
	scheduler.mu.Lock()
	failed := scheduler.commands[0].AssistantMessageID
	scheduler.err = nil
	scheduler.mu.Unlock()
	// The real scheduler fails the refused turn at once; this one leaves it
	// queued, so the fresh send is refused as a busy session. What must not
	// happen is an answer with the turn that never ran.
	retry := keyedChatSend(handler, "local-user", sessionID, "send-1", `{"content":"draw a cat"}`)
	if retry.Code == http.StatusAccepted || strings.Contains(retry.Body.String(), failed) {
		t.Fatalf("the repeat answered the turn that never ran: status=%d body=%s", retry.Code, retry.Body)
	}
}

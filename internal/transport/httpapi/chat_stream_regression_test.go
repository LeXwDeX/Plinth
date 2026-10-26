package httpapi_test

import (
	"database/sql"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatRejectsConcurrentSessionTurnWithConflict(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, func(app *catalog.ApplicationSchema) {
		app.Chat.Presentation.Messages.TurnActive = "configured conflict"
	})
	response := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
	var session map[string]any
	decodeChatRecorder(t, response, &session)
	path := "/api/chat/sessions/" + session["id"].(string) + "/messages"
	first := performChatRequest(handler, http.MethodPost, path, `{"content":"one"}`)
	second := performChatRequest(handler, http.MethodPost, path, `{"content":"two"}`)
	if first.Code != http.StatusAccepted || second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "configured conflict") || len(scheduler.commands) != 1 {
		t.Fatalf("first=%d second=%d body=%s scheduled=%d", first.Code, second.Code, second.Body, len(scheduler.commands))
	}
	deleted := performChatRequest(handler, http.MethodDelete, path+"/"+scheduler.commands[0].AssistantMessageID, "")
	if deleted.Code != http.StatusConflict {
		t.Fatalf("active assistant deletion=%d body=%s", deleted.Code, deleted.Body)
	}
}

func streamRegressionHandler(t *testing.T) http.Handler {
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, func(app *catalog.ApplicationSchema) { app.Chat.Stream.WriteTimeoutMilliseconds = 20 }, nil, nil, nil, func(t *testing.T, path string) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		statements := []string{
			`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('replay','local-user','Owner','Replay','2026-09-04T00:00:00Z','2026-09-04T00:00:00Z')`,
			`INSERT INTO go_chat_streams(session_id,status,assistant_message_id) VALUES ('replay','done','assistant')`,
			`INSERT INTO go_chat_events(session_id,event_id,data_json,created_at) VALUES ('replay',0,'{"type":"text","content":"old"}','2026-09-04T00:00:00Z'),('replay',1,'{"type":"text","content":"new"}','2026-09-04T00:00:00Z'),('replay',2,'{"type":"done"}','2026-09-04T00:00:00Z')`,
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	})
	return handler
}

func TestChatSSEEmitsCursorAndReplaysAfterLastEventID(t *testing.T) {
	t.Parallel()
	handler := streamRegressionHandler(t)
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/chat/sessions/replay/stream", nil)
	request.Header.Set("Last-Event-ID", "0")
	handler.ServeHTTP(writer, request)
	body := writer.Body.String()
	if writer.Code != http.StatusOK || strings.Contains(body, `"old"`) || !strings.Contains(body, "id: 1\n") || !strings.Contains(body, `"new"`) || !strings.Contains(body, "id: 2\n") {
		t.Fatalf("status=%d body=%s", writer.Code, body)
	}
}

type stalledSSEWriter struct {
	header       http.Header
	deadline     time.Time
	deadlineSeen bool
}

func (w *stalledSSEWriter) Header() http.Header { return w.header }
func (w *stalledSSEWriter) WriteHeader(int)     {}
func (w *stalledSSEWriter) SetWriteDeadline(at time.Time) error {
	w.deadline = at
	if !at.IsZero() {
		w.deadlineSeen = true
	}
	return nil
}
func (w *stalledSSEWriter) Flush() {}
func (w *stalledSSEWriter) Write([]byte) (int, error) {
	if w.deadline.IsZero() {
		return 0, errors.New("missing write deadline")
	}
	time.Sleep(time.Until(w.deadline))
	return 0, errors.New("write timeout")
}
func TestChatSSEBoundsStalledNetworkWrite(t *testing.T) {
	t.Parallel()
	handler := streamRegressionHandler(t)
	writer := &stalledSSEWriter{header: http.Header{}}
	start := time.Now()
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/api/chat/sessions/replay/stream", nil))
	if !writer.deadlineSeen || !writer.deadline.IsZero() || time.Since(start) > time.Second {
		t.Fatalf("deadlineSeen=%v deadline=%v elapsed=%v", writer.deadlineSeen, writer.deadline, time.Since(start))
	}
}

package httpapi_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// heartbeatProbe is a response writer that cancels the request once the stream
// has written the wanted number of heartbeat frames, so the active stream under
// test ends without sleeping. ServeHTTP runs on the caller's goroutine and the
// probe is only touched from it.
type heartbeatProbe struct {
	header http.Header
	body   strings.Builder
	frame  string
	want   int
	seen   int
	cancel context.CancelFunc
}

func (p *heartbeatProbe) Header() http.Header { return p.header }
func (p *heartbeatProbe) WriteHeader(int)     {}
func (p *heartbeatProbe) Flush()              {}
func (p *heartbeatProbe) Write(data []byte) (int, error) {
	p.body.Write(data)
	if string(data) == p.frame {
		p.seen++
		if p.seen >= p.want {
			p.cancel()
		}
	}
	return len(data), nil
}

// readChatStreamHeartbeats opens the stream of an active turn that already has
// two stored events (ids 0 and 1), lets the handler write two heartbeat frames
// and returns the bytes it wrote and how many events the session still holds.
func readChatStreamHeartbeats(
	t *testing.T,
	customize func(*catalog.ApplicationSchema),
	frame string,
) (string, int) {
	t.Helper()
	var databasePath string
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, func(app *catalog.ApplicationSchema) {
		app.Chat.Stream.HeartbeatMilliseconds = 5
		customize(app)
	}, nil, nil, nil, func(t *testing.T, path string) {
		databasePath = path
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, statement := range []string{
			`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('live','local-user','Owner','Live','2026-09-04T00:00:00Z','2026-09-04T00:00:00Z')`,
			`INSERT INTO go_chat_streams(session_id,status,assistant_message_id) VALUES ('live','active','assistant')`,
			`INSERT INTO go_chat_events(session_id,event_id,data_json,created_at) VALUES ('live',0,'{"type":"text","content":"one"}','2026-09-04T00:00:00Z'),('live',1,'{"type":"text","content":"two"}','2026-09-04T00:00:00Z')`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := &heartbeatProbe{header: http.Header{}, frame: frame, want: 2, cancel: cancel}
	request := httptest.NewRequest(http.MethodGet, "/api/chat/sessions/live/stream", nil).WithContext(ctx)
	handler.ServeHTTP(probe, request)
	if probe.seen < probe.want {
		t.Fatalf("saw %d of %d heartbeat frames %q before the deadline; body=%q", probe.seen, probe.want, frame, probe.body.String())
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM go_chat_events WHERE session_id = 'live'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	return probe.body.String(), stored
}

// #375: with the heartbeat event declared the stream writes it as a data frame
// an EventSource can observe, after the replayed events and without an id line,
// and it is neither stored nor taken for an event the client must replay.
func TestChatStreamHeartbeatIsAVisibleDataFrameWithoutID(t *testing.T) {
	t.Parallel()
	frame := "data: {\"type\":\"heartbeat\"}\n\n"
	body, stored := readChatStreamHeartbeats(t, func(app *catalog.ApplicationSchema) {
		if app.Chat.Stream.EventTypes.Heartbeat != "heartbeat" {
			t.Fatalf("default heartbeat event = %q", app.Chat.Stream.EventTypes.Heartbeat)
		}
	}, frame)
	replayed := "id: 0\ndata: {\"content\":\"one\",\"type\":\"text\"}\n\n" +
		"id: 1\ndata: {\"content\":\"two\",\"type\":\"text\"}\n\n"
	if !strings.Contains(body, replayed+frame) {
		t.Fatalf("heartbeat frames must follow the replayed events; body=%q", body)
	}
	if strings.Count(body, "id: ") != 2 || strings.Contains(body, ": heartbeat") {
		t.Fatalf("a heartbeat frame carries no id and replaces the comment frame; body=%q", body)
	}
	if stored != 2 {
		t.Fatalf("the heartbeat must not be stored as an event; %d events stored", stored)
	}
}

// The frame is built from the Schema's event type field name, the heartbeat
// event name and the data prefix and suffix, not from hardcoded wire text.
func TestChatStreamHeartbeatFollowsTheSchemaWire(t *testing.T) {
	t.Parallel()
	frame := "data:{\"kind\":\"beat\"}\r\n\r\n"
	body, _ := readChatStreamHeartbeats(t, func(app *catalog.ApplicationSchema) {
		app.Chat.Stream.EventFields.Type = "kind"
		app.Chat.Stream.EventTypes.Heartbeat = "beat"
		app.Chat.Stream.EventPrefix = "data:"
		app.Chat.Stream.EventSuffix = "\r\n\r\n"
	}, frame)
	if strings.Contains(body, "heartbeat") {
		t.Fatalf("heartbeat wire is hardcoded; body=%q", body)
	}
}

// Without the declaration the stream keeps writing the comment frame, which no
// EventSource handler sees, exactly as before.
func TestChatStreamKeepsTheCommentHeartbeatWhenNoEventIsDeclared(t *testing.T) {
	t.Parallel()
	frame := ": heartbeat\n\n"
	body, stored := readChatStreamHeartbeats(t, func(app *catalog.ApplicationSchema) {
		app.Chat.Stream.EventTypes.Heartbeat = ""
	}, frame)
	if strings.Contains(body, "data: {\"type\":\"heartbeat\"}") {
		t.Fatalf("a data heartbeat was written without a declared event; body=%q", body)
	}
	if strings.Count(body, "id: ") != 2 || stored != 2 {
		t.Fatalf("body=%q stored=%d", body, stored)
	}
}

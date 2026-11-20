package httpapi_test

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #403: the public message projection marks the assistant message of a turn that
// has not ended (queued, running, waiting for its Tasks) under the Schema's
// `is_pending` member, whatever the stored text is. Before it only the
// placeholder text did, so a reply that already held its first round's text read
// as finished while its turn was still running.

// turnReadSeeds is one session per turn state. Every assistant message holds
// the text of a finished first round, not the placeholder.
var turnReadSeeds = []struct {
	session string
	owner   string
	state   string
	running bool
}{
	{"s-queued", "local-user", "queued", true},
	{"s-running", "local-user", "running", true},
	{"s-waiting", "local-user", "waiting_task", true},
	{"s-done", "local-user", "done", false},
	{"s-error", "local-user", "error", false},
	{"s-foreign", "someone-else", "running", true},
}

func seedTurnReads403(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, seed := range turnReadSeeds {
		statements := []string{
			`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES ('` + seed.session + `','` + seed.owner + `','Owner','` + seed.state + `','2026-11-20T00:00:00Z','2026-11-20T00:00:00Z')`,
			`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('u-` + seed.session + `','` + seed.session + `','user','draw twice','[]','[]','[]','','2026-11-20T00:00:01Z')`,
			`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('a-` + seed.session + `','` + seed.session + `','assistant','first round text','[]','[]','[]','','2026-11-20T00:00:02Z')`,
			`INSERT INTO go_chat_turns(assistant_message_id,session_id,owner_id,options_json,state,created_at,updated_at) VALUES ('a-` + seed.session + `','` + seed.session + `','` + seed.owner + `','{}','` + seed.state + `','2026-11-20T00:00:02Z','2026-11-20T00:00:03Z')`,
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestChatMessagesMarkTheAssistantOfAnUnfinishedTurnWithTheSchemaMember403(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, nil, nil, nil, nil, seedTurnReads403)
	for _, seed := range turnReadSeeds {
		if seed.owner != "local-user" {
			continue
		}
		for name, target := range map[string]string{
			"list": "/api/chat/sessions/" + seed.session + "/messages",
			"page": "/api/chat/sessions/" + seed.session + "/messages?limit=50",
		} {
			response := performChatRequest(handler, http.MethodGet, target, "")
			if response.Code != http.StatusOK {
				t.Fatalf("%s %s: status=%d body=%s", seed.state, name, response.Code, response.Body)
			}
			var messages []map[string]any
			if name == "page" {
				var page struct {
					Messages []map[string]any `json:"messages"`
				}
				decodeChatRecorder(t, response, &page)
				messages = page.Messages
			} else {
				decodeChatRecorder(t, response, &messages)
			}
			if len(messages) != 2 {
				t.Fatalf("%s %s: messages = %#v", seed.state, name, messages)
			}
			user, reply := messages[0], messages[1]
			if _, marked := user["isPending"]; marked {
				t.Fatalf("%s %s: the user message carries the running mark: %#v", seed.state, name, user)
			}
			marked := reply["isPending"] == true
			if marked != seed.running {
				t.Fatalf("%s %s: isPending = %#v, want running=%v: %#v", seed.state, name, reply["isPending"], seed.running, reply)
			}
			if !seed.running {
				if _, present := reply["isPending"]; present {
					t.Fatalf("%s %s: a finished turn keeps the member: %#v", seed.state, name, reply)
				}
			}
			// The mark never replaces the stored text.
			if reply["content"] != "first round text" {
				t.Fatalf("%s %s: content = %#v", seed.state, name, reply["content"])
			}
		}
	}
}

func TestChatMessagesOfAForeignRunningSessionStayHidden403(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, nil, nil, nil, nil, seedTurnReads403)
	for _, target := range []string{
		"/api/chat/sessions/s-foreign/messages",
		"/api/chat/sessions/s-foreign/messages?limit=50",
	} {
		if response := performChatRequest(handler, http.MethodGet, target, ""); response.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d body=%s", target, response.Code, response.Body)
		}
	}
}

func TestChatMessagesUseTheConfiguredMemberNameForTheRunningMark403(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, func(application *catalog.ApplicationSchema) {
		application.Chat.API.ResponseFields.IsPending = "configuredRunning"
	}, nil, nil, nil, seedTurnReads403)
	response := performChatRequest(handler, http.MethodGet, "/api/chat/sessions/s-running/messages", "")
	var messages []map[string]any
	decodeChatRecorder(t, response, &messages)
	if len(messages) != 2 || messages[1]["configuredRunning"] != true {
		t.Fatalf("messages = %#v", messages)
	}
	if _, present := messages[1]["isPending"]; present {
		t.Fatalf("the default member name was used: %#v", messages[1])
	}
}

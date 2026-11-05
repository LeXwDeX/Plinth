package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// backgroundChatScheduler records the background-continue calls (#137).
type backgroundChatScheduler struct {
	recordingChatScheduler
	available bool
	err       error
	reads     []string
	continued []string
}

func (s *backgroundChatScheduler) TurnBackgroundAvailable(_ context.Context, ownerID, sessionID string) (bool, error) {
	s.reads = append(s.reads, ownerID+"/"+sessionID)
	return s.available, nil
}

func (s *backgroundChatScheduler) ContinueTurnInBackground(_ context.Context, ownerID, sessionID string) error {
	s.continued = append(s.continued, ownerID+"/"+sessionID)
	return s.err
}

// #137: the background route has the stop route's shape and its members come
// from the Schema; a refused request answers 409 with the Schema's reason and
// never stops the turn.
func TestChatTurnBackgroundRouteReadsAndContinues(t *testing.T) {
	t.Parallel()
	scheduler := &backgroundChatScheduler{available: true}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, func(application *catalog.ApplicationSchema) {
		application.Runtime.HTTP.LegacyRoutes.ChatTurnBackground = "/api/chat/sessions/{sessionID}/turn/keep-running"
		application.Chat.API.Fields.TurnBackgrounded = "configuredBackgrounded"
		application.Chat.API.Fields.TurnBackgroundAvailable = "configuredAvailable"
	})
	read := performChatRequest(handler, http.MethodGet, "/api/chat/sessions/session-1/turn/keep-running", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body)
	}
	var available map[string]any
	decodeChatRecorder(t, read, &available)
	if available["configuredAvailable"] != true || len(scheduler.reads) != 1 || scheduler.reads[0] != "local-user/session-1" {
		t.Fatalf("read = %#v, calls = %v", available, scheduler.reads)
	}
	accepted := performChatRequest(handler, http.MethodPost, "/api/chat/sessions/session-1/turn/keep-running", "")
	if accepted.Code != http.StatusOK {
		t.Fatalf("continue status=%d body=%s", accepted.Code, accepted.Body)
	}
	var continued map[string]any
	decodeChatRecorder(t, accepted, &continued)
	if continued["configuredBackgrounded"] != true || len(scheduler.continued) != 1 || scheduler.canceled {
		t.Fatalf("continue = %#v, calls = %v, canceled = %v", continued, scheduler.continued, scheduler.canceled)
	}

	scheduler.err = chat.ErrTurnBackgroundUnavailable
	refused := performChatRequest(handler, http.MethodPost, "/api/chat/sessions/session-1/turn/keep-running", "")
	if refused.Code != http.StatusConflict {
		t.Fatalf("refused status=%d body=%s", refused.Code, refused.Body)
	}
	var body map[string]any
	decodeChatRecorder(t, refused, &body)
	application, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if body["error"] != application.Application().Chat.Agent.BackgroundWait.UnavailableMessage || scheduler.canceled {
		t.Fatalf("refusal = %#v", body)
	}
}

// A scheduler without the capability, or a Schema without the route, keeps the
// stop route alone.
func TestChatTurnBackgroundRouteNeedsTheSchedulerAndTheRoute(t *testing.T) {
	t.Parallel()
	_, plain, _ := newHTTPHarnessWithChatScheduler(t, &recordingChatScheduler{}, nil)
	if response := performChatRequest(plain, http.MethodPost, "/api/chat/sessions/session-1/turn/background", ""); response.Code == http.StatusOK {
		t.Fatalf("a scheduler without background support served the route: %d", response.Code)
	}
	scheduler := &backgroundChatScheduler{available: true}
	_, withoutRoute, _ := newHTTPHarnessWithChatScheduler(t, scheduler, func(application *catalog.ApplicationSchema) {
		application.Runtime.HTTP.LegacyRoutes.ChatTurnBackground = ""
		application.Chat.Frontend.BackgroundWaitPollMilliseconds = 0
	})
	if response := performChatRequest(withoutRoute, http.MethodPost, "/api/chat/sessions/session-1/turn/background", ""); response.Code == http.StatusOK ||
		len(scheduler.continued) != 0 {
		t.Fatalf("an undeclared route was served: %d", response.Code)
	}
}

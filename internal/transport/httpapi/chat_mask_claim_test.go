package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #244: the selection mask is a top-level request member decoded with the
// Schema's names. A mask without a canvas turn refuses the turn with its own
// 400 text; an absent or null member leaves the turn as it was.
func TestSelectionMaskRequestMemberIsDecodedAndRefusedWithoutCanvas(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	create := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
	var session map[string]any
	decodeChatRecorder(t, create, &session)
	sessionID := session["id"].(string)
	path := "/api/chat/sessions/" + sessionID + "/messages"

	refused := performChatRequest(handler, http.MethodPost, path,
		`{"content":"change this area","selectionMask":{"maskAssetId":"mask-1"}}`)
	if refused.Code != http.StatusBadRequest ||
		!strings.Contains(refused.Body.String(), "选区蒙版") {
		t.Fatalf("mask without canvas status=%d body=%s", refused.Code, refused.Body)
	}
	if len(scheduler.commands) != 0 {
		t.Fatalf("refused mask started a turn: %#v", scheduler.commands)
	}
	var refusedBody map[string]any
	decodeChatRecorder(t, refused, &refusedBody)
	if refusedBody["error"] == "" {
		t.Fatalf("refusal body = %#v", refusedBody)
	}

	// An empty mask object is a mask with no asset and is refused too.
	empty := performChatRequest(handler, http.MethodPost, path,
		`{"content":"change this area","selectionMask":{}}`)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty mask status=%d body=%s", empty.Code, empty.Body)
	}

	// No member, or null, is an ordinary turn.
	for _, body := range []string{
		`{"content":"plain turn"}`,
		`{"content":"plain turn","selectionMask":null}`,
	} {
		fresh := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
		var newSession map[string]any
		decodeChatRecorder(t, fresh, &newSession)
		response := performChatRequest(handler, http.MethodPost,
			"/api/chat/sessions/"+newSession["id"].(string)+"/messages", body)
		if response.Code != http.StatusAccepted {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body)
		}
	}
}

// #244: the field and member names come from the Schema.
func TestSelectionMaskRequestMembersFollowTheSchema(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, func(application *catalog.ApplicationSchema) {
		application.Chat.API.Fields.SelectionMask = "configuredMask"
		application.Chat.API.SelectionMask.MaskAssetID = "configuredMaskAsset"
	})
	create := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
	var session map[string]any
	decodeChatRecorder(t, create, &session)
	sessionID := session["id"].(string)
	path := "/api/chat/sessions/" + sessionID + "/messages"
	// The default spelling is ignored, so this is an ordinary turn.
	defaultNames := performChatRequest(handler, http.MethodPost, path,
		`{"content":"plain","selectionMask":{"maskAssetId":"mask-1"}}`)
	if defaultNames.Code != http.StatusAccepted {
		t.Fatalf("default member names used: status=%d body=%s", defaultNames.Code, defaultNames.Body)
	}
	configured := performChatRequest(handler, http.MethodPost, path,
		`{"content":"plain","configuredMask":{"configuredMaskAsset":"mask-1"}}`)
	if configured.Code != http.StatusBadRequest {
		t.Fatalf("configured member names not read: status=%d body=%s", configured.Code, configured.Body)
	}
}

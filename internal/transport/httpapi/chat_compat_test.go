package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestLegacyChatSubmissionSchedulesAllSchemaConfiguredOptions(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(
		t,
		scheduler,
		func(application *catalog.ApplicationSchema) {
			fields := &application.Chat.API.Fields
			fields.Content = "configuredContent"
			fields.ImageURLs = "configuredImages"
			fields.AspectRatio = "configuredAspect"
			fields.PoseMode = "configuredPose"
			fields.AIModel = "configuredModel"
			fields.CreativeDomain = "configuredDomain"
			fields.Size = "configuredSize"
			fields.ImageSize = "configuredImageSize"
			fields.Parameters = "configuredParameters"
			fields.Skill = "configuredSkill"
			fields.SkillID = "configuredSkillId"
			fields.SkillName = "configuredSkillName"
			fields.ReferenceImageIndices = "configuredReferences"
		},
	)
	create := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions",
		`{}`,
	)
	var session map[string]any
	decodeChatRecorder(t, create, &session)
	sessionID := session["id"].(string)
	send := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions/"+sessionID+"/messages",
		`{
			"configuredContent":"configured request",
			"configuredImages":["data:image/png;base64,YQ=="],
			"configuredAspect":"16:9",
			"configuredPose":"configured pose",
			"configuredModel":"configured model",
			"configuredDomain":"model-3d",
			"configuredSize":"configured size",
			"configuredImageSize":"configured image size",
			"configuredSkill":{
				"configuredSkillId":"configured skill id",
				"configuredSkillName":"configured skill name"
			},
			"configuredReferences":[2,4],
			"configuredParameters":{"configuredQuality":"xhigh","configuredMask":"data:image/png;base64,YQ=="}
		}`,
	)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body)
	}
	if len(scheduler.commands) != 1 {
		t.Fatalf("commands = %#v", scheduler.commands)
	}
	command := scheduler.commands[0]
	if command.OwnerID != "local-user" ||
		command.SessionID != sessionID ||
		command.AssistantMessageID == "" ||
		command.Options.AspectRatio != "16:9" ||
		command.Options.PoseMode != "configured pose" ||
		command.Options.AIModel != "configured model" ||
		command.Options.CreativeDomain != "model-3d" ||
		command.Options.Size != "configured size" ||
		command.Options.ImageSize != "configured image size" ||
		command.Options.SkillID != "configured skill id" ||
		command.Options.SkillName != "configured skill name" ||
		len(command.Options.ReferenceImageIndices) != 2 ||
		command.Options.ReferenceImageIndices[0] != 2 ||
		command.Options.ReferenceImageIndices[1] != 4 ||
		string(command.Options.Parameters["configuredQuality"]) != `"xhigh"` ||
		string(command.Options.Parameters["configuredMask"]) != `"data:image/png;base64,YQ=="` {
		t.Fatalf("command = %#v", command)
	}
}

// Attachment claims and the selection travel under the Schema's member names;
// only verified IDs come back, with their handles.
func TestLegacyChatCarriesVerifiedAttachmentsUnderSchemaNames(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, func(application *catalog.ApplicationSchema) {
		application.Chat.API.Fields.Attachments = "configuredClaims"
		application.Chat.API.Fields.Selection = "configuredSelected"
		application.Chat.API.ResponseFields.Attachments = "configuredObjects"
		names := &application.Chat.API.Attachment
		names.Handle, names.TaskID, names.CaptureID, names.ImageSlot = "configuredHandle", "configuredTask", "configuredCapture", "configuredSlot"
	})
	var session map[string]any
	decodeChatRecorder(t, performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`), &session)
	sessionID := session["id"].(string)
	send := performChatRequest(handler, http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages", `{
		"content":"use the 3D view",
		"image_urls":["/api/v1/assets/shot","/api/v1/assets/other"],
		"configuredClaims":[
			{"kind":"image","configuredSlot":1,"configuredCapture":"own-capture","configuredTask":"own-model","assetId":"own-shot"},
			{"kind":"image","configuredSlot":2,"configuredTask":"someone-elses-task"}
		],
		"configuredSelected":[
			{"kind":"model","configuredTask":"own-model","url":"/thumb"},
			{"kind":"image","configuredTask":"someone-elses-task"}
		]
	}`)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body)
	}
	var messages []map[string]any
	decodeChatRecorder(t, performChatRequest(handler, http.MethodGet, "/api/chat/sessions/"+sessionID+"/messages", ""), &messages)
	objects, _ := messages[0]["configuredObjects"].([]any)
	if len(objects) != 3 {
		t.Fatalf("attachments = %#v", messages[0])
	}
	first, _ := objects[0].(map[string]any)
	if first["configuredHandle"] != "@img1" || first["configuredCapture"] != "own-capture" || first["configuredTask"] != "own-model" ||
		first["assetId"] != "own-shot" || first["role"] != "capture" || first["configuredSlot"] != float64(1) {
		t.Fatalf("verified claim = %#v", first)
	}
	second, _ := objects[1].(map[string]any)
	if second["configuredHandle"] != "@img2" || second["configuredTask"] != nil || second["assetId"] != nil {
		t.Fatalf("unverified claim leaked IDs: %#v", second)
	}
	selected, _ := objects[2].(map[string]any)
	if selected["configuredHandle"] != "@model1" || selected["kind"] != "model" || selected["url"] != "/thumb" {
		t.Fatalf("selection = %#v", selected)
	}
	if body := send.Body.String() + messagesBody(messages); strings.Contains(body, "someone-elses-task") {
		t.Fatalf("a foreign ID was echoed: %s", body)
	}
}

func TestChatAttachmentPurposeRequests(t *testing.T) {
	for name, tc := range map[string]struct {
		purposes string
		status   int
	}{
		"valid":          {`[{"imageSlot":1,"purpose":"style"}]`, http.StatusAccepted},
		"bad slot":       {`[{"imageSlot":3,"purpose":"style"}]`, http.StatusBadRequest},
		"bad purpose":    {`[{"imageSlot":1,"purpose":"unknown"}]`, http.StatusBadRequest},
		"duplicate":      {`[{"imageSlot":1,"purpose":"style"},{"imageSlot":1,"purpose":"layout"}]`, http.StatusBadRequest},
		"two inputs":     {`[{"imageSlot":1,"purpose":"input"},{"imageSlot":2,"purpose":"input"}]`, http.StatusBadRequest},
		"foreign claim":  {`[{"imageSlot":2,"purpose":"style"}]`, http.StatusBadRequest},
		"unknown member": {`[{"imageSlot":1,"purpose":"style","extra":1}]`, http.StatusBadRequest},
		"malformed":      {`{}`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			_, handler, _ := newHTTPHarnessWithChatScheduler(t, &recordingChatScheduler{}, nil)
			var session map[string]any
			decodeChatRecorder(t, performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`), &session)
			body := `{"content":"edit","image_urls":["/api/v1/assets/shot","/api/v1/assets/foreign"],"attachments":[{"kind":"image","imageSlot":1,"captureId":"own-capture","taskId":"own-model","assetId":"own-shot"},{"kind":"image","imageSlot":2,"taskId":"someone-elses-task"}],"attachmentPurposes":` + tc.purposes + `}`
			response := performChatRequest(handler, http.MethodPost, "/api/chat/sessions/"+session["id"].(string)+"/messages", body)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if tc.status == http.StatusBadRequest {
				var body map[string]any
				decodeChatRecorder(t, response, &body)
				code := "attachment_purpose_invalid"
				if name == "foreign claim" {
					code = "attachment_purpose_attachment_invalid"
				}
				if body["code"] != code || body["detail"] == "" {
					t.Fatalf("problem = %#v", body)
				}
			}
		})
	}
}

func TestChatAttachmentPurposeUsesSchemaNames(t *testing.T) {
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, &recordingChatScheduler{}, func(app *catalog.ApplicationSchema) {
		app.Chat.API.Fields.AttachmentPurposes = "roles"
		app.Chat.API.Attachment.ImageSlot = "slot"
		app.Chat.API.Attachment.Purpose = "role"
	})
	var session map[string]any
	decodeChatRecorder(t, performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`), &session)
	body := `{"content":"edit","image_urls":["/api/v1/assets/shot"],"attachments":[{"kind":"image","slot":1,"captureId":"own-capture","taskId":"own-model","assetId":"own-shot"}],"roles":[{"slot":1,"role":"input"}]}`
	response := performChatRequest(handler, http.MethodPost, "/api/chat/sessions/"+session["id"].(string)+"/messages", body)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func messagesBody(messages []map[string]any) string {
	encoded, _ := json.Marshal(messages)
	return string(encoded)
}

// 停止轮次端点必须走服务端取消（而不是只断开前端 SSE），
// 以 Schema 声明的字段名返回是否有轮次被中断。
func TestLegacyCancelChatTurnReportsActiveCancellation(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(t, scheduler, nil)
	response := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions/session-1/turn/cancel",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var payload map[string]any
	decodeChatRecorder(t, response, &payload)
	if payload["stopped"] != true {
		t.Fatalf("payload = %#v", payload)
	}
	if !scheduler.canceled {
		t.Fatal("scheduler.CancelTurn was not called")
	}
}

func TestLegacyChatRejectsNonObjectParameters(t *testing.T) {
	t.Parallel()
	scheduler := &recordingChatScheduler{}
	_, handler, _ := newHTTPHarnessWithChatScheduler(
		t,
		scheduler,
		func(application *catalog.ApplicationSchema) {
			application.Chat.API.Fields.Parameters = "configuredParameters"
		},
	)
	create := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions",
		`{}`,
	)
	var session map[string]any
	decodeChatRecorder(t, create, &session)
	sessionID := session["id"].(string)
	for name, parameters := range map[string]string{
		"scalar": `"xhigh"`,
		"array":  `["xhigh"]`,
		"null":   `null`,
	} {
		send := performChatRequest(
			handler,
			http.MethodPost,
			"/api/chat/sessions/"+sessionID+"/messages",
			`{"content":"x","configuredParameters":`+parameters+`}`,
		)
		if send.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", name, send.Code, send.Body)
		}
	}
	if len(scheduler.commands) != 0 {
		t.Fatalf("commands = %#v", scheduler.commands)
	}
}

func TestLegacyChatSessionAndMessageContracts(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	create := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions",
		`{}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf(
			"create status=%d body=%s",
			create.Code,
			create.Body,
		)
	}
	var session map[string]any
	decodeChatRecorder(t, create, &session)
	sessionID, _ := session["id"].(string)
	if sessionID == "" || session["title"] != "新对话" {
		t.Fatalf("session = %#v", session)
	}

	list := performChatRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions",
		"",
	)
	var sessions []map[string]any
	decodeChatRecorder(t, list, &sessions)
	count, _ := sessions[0]["_count"].(map[string]any)
	if len(sessions) != 1 || count["messages"] != float64(0) {
		t.Fatalf("sessions = %#v", sessions)
	}
	noStream := performChatRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions/"+sessionID+"/stream",
		"",
	)
	if noStream.Code != http.StatusNoContent {
		t.Fatalf(
			"no stream status=%d body=%s",
			noStream.Code,
			noStream.Body,
		)
	}

	send := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions/"+sessionID+"/messages",
		`{"content":"hello"}`,
	)
	if send.Code != http.StatusAccepted {
		t.Fatalf(
			"send status=%d body=%s",
			send.Code,
			send.Body,
		)
	}
	var accepted map[string]any
	decodeChatRecorder(t, send, &accepted)
	if accepted["messageId"] == "" || accepted["sessionId"] != sessionID {
		t.Fatalf("accepted = %#v", accepted)
	}

	messagesResponse := performChatRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions/"+sessionID+"/messages",
		"",
	)
	var messages []map[string]any
	decodeChatRecorder(t, messagesResponse, &messages)
	if len(messages) != 2 ||
		messages[0]["role"] != "user" ||
		messages[1]["content"] != "__GENERATING__" {
		t.Fatalf("messages = %#v", messages)
	}

	messageID, _ := messages[0]["id"].(string)
	deleteMessage := performChatRequest(
		handler,
		http.MethodDelete,
		"/api/chat/sessions/"+sessionID+"/messages/"+messageID,
		"",
	)
	if deleteMessage.Code != http.StatusNoContent {
		t.Fatalf(
			"delete message status=%d body=%s",
			deleteMessage.Code,
			deleteMessage.Body,
		)
	}

	rename := performChatRequest(
		handler,
		http.MethodPatch,
		"/api/chat/sessions/"+sessionID,
		`{"title":"renamed"}`,
	)
	if rename.Code != http.StatusOK {
		t.Fatalf(
			"rename status=%d body=%s",
			rename.Code,
			rename.Body,
		)
	}
	var renamed map[string]any
	decodeChatRecorder(t, rename, &renamed)
	if renamed["title"] != "renamed" {
		t.Fatalf("renamed = %#v", renamed)
	}

	remove := performChatRequest(
		handler,
		http.MethodDelete,
		"/api/chat/sessions/"+sessionID,
		"",
	)
	if remove.Code != http.StatusNoContent {
		t.Fatalf(
			"delete session status=%d body=%s",
			remove.Code,
			remove.Body,
		)
	}
}

func TestLegacyChatAuditEventsArePersistedForAdministration(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	create := performChatRequest(
		handler,
		http.MethodPost,
		"/api/chat/sessions",
		`{}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var session map[string]any
	decodeChatRecorder(t, create, &session)
	sessionID := session["id"].(string)
	assertPersistedChatAudit(
		t,
		handler,
		"create_task",
		"chat_session",
		sessionID,
	)

	remove := performChatRequest(
		handler,
		http.MethodDelete,
		"/api/chat/sessions/"+sessionID,
		"",
	)
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", remove.Code, remove.Body)
	}
	assertPersistedChatAudit(
		t,
		handler,
		"delete_task",
		"chat_session",
		sessionID,
	)
}

func assertPersistedChatAudit(
	t *testing.T,
	handler http.Handler,
	action string,
	resourceType string,
	resourceID string,
) {
	t.Helper()
	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action="+action,
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["total"] != float64(1) {
		t.Fatalf("%s audit body=%#v", action, body)
	}
	events := body["data"].([]any)
	event := events[0].(map[string]any)
	if event["action"] != action ||
		event["userId"] != "local-user" ||
		event["resourceType"] != resourceType ||
		event["resourceId"] != resourceID {
		t.Fatalf("%s audit event=%#v", action, event)
	}
}

func TestLegacyChatWireAndVisibleTextChangeWithProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			routes := &application.Runtime.HTTP.LegacyRoutes
			routes.ChatSessions = "/configured/chat"
			routes.ChatSession = "/configured/chat/{sessionID}"
			routes.ChatMessages = "/configured/chat/{sessionID}/turns"
			routes.ChatMessage = "/configured/chat/{sessionID}/turns/{messageID}"
			routes.ChatStream = "/configured/chat/{sessionID}/events"
			application.Chat.API.Fields.Title = "configuredTitle"
			application.Chat.API.ResponseFields.ID = "configuredId"
			application.Chat.API.ResponseFields.Title = "configuredTitle"
			application.Chat.API.ResponseFields.Error = "configuredError"
			application.Chat.Defaults.Title = "configured default title"
			application.Chat.Presentation.Messages.EmptyTitle = "configured empty title"
		},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/configured/chat",
		bytes.NewBufferString(`{}`),
	)
	request.Header.Set("X-User-ID", "local-user")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["configuredId"] == "" ||
		created["configuredTitle"] != "configured default title" ||
		created["id"] != nil ||
		created["title"] != nil {
		t.Fatalf("created = %#v", created)
	}

	sessionID := created["configuredId"].(string)
	emptyTitle := httptest.NewRequest(
		http.MethodPatch,
		"/configured/chat/"+sessionID,
		bytes.NewBufferString(`{"configuredTitle":" "}`),
	)
	emptyTitle.Header.Set("X-User-ID", "local-user")
	emptyTitle.Header.Set("Content-Type", "application/json")
	emptyResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyResponse, emptyTitle)
	var failure map[string]any
	if err := json.Unmarshal(emptyResponse.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if emptyResponse.Code != http.StatusBadRequest ||
		failure["configuredError"] != "configured empty title" ||
		failure["error"] != nil {
		t.Fatalf(
			"failure status=%d body=%#v",
			emptyResponse.Code,
			failure,
		)
	}
}

// The session list projects each session's current production plan, read in
// one batch through the SQLite repository instead of one query per session.
// The budget is the same public summary the single-session read returns
// (#103 review C6).
func TestChatSessionListProjectsProductionBudgets(t *testing.T) {
	t.Parallel()
	var databasePath string
	_, handler, _, _ := newHTTPHarnessWithServices(t, nil, nil, nil, nil, nil,
		func(_ *testing.T, path string) { databasePath = path })
	sessionIDs := make([]string, 0, 2)
	for _, title := range []string{"批量预算一", "批量预算二"} {
		response := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{"title":"`+title+`"}`)
		if response.Code != http.StatusCreated {
			t.Fatalf("create session status=%d body=%s", response.Code, response.Body)
		}
		var created map[string]any
		decodeChatRecorder(t, response, &created)
		sessionIDs = append(sessionIDs, created["id"].(string))
	}

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := "2026-01-01T00:00:00Z"
	// A step stored before #120 (integer estimated_credits) reads as legacy credits.
	planJSON := `{"title":"批量","steps":[{"capability":"text-to-image","estimated_credits":5}]}`
	for index, sessionID := range sessionIDs {
		budgetID := "pb-" + strconv.Itoa(index)
		limits := `[{"unit":"legacy.credits","unit_label":"legacy.credits","amount":"` + strconv.Itoa(20+index*10) + `"}]`
		if _, err := db.Exec(`INSERT INTO production_budgets
 (id, owner_id, session_id, plan_json, limits_json, state, created_at, decided_at, decided_by)
 VALUES (?, 'local-user', ?, ?, ?, 'active', ?, ?, 'human')`,
			budgetID, sessionID, planJSON, limits, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		for _, reservation := range []struct {
			request string
			credits int
			state   string
		}{
			{"charged-" + strconv.Itoa(index), 3 + index, "charged"},
			{"reserved-" + strconv.Itoa(index), 2, "reserved"},
		} {
			if _, err := db.Exec(`INSERT INTO budget_reservations
 (grant_request_id, budget_id, task_id, unit, amount_micros, state, created_at)
 VALUES (?, ?, '', 'legacy.credits', ?, ?, ?)`,
				reservation.request, budgetID, reservation.credits*1000000, reservation.state, stamp); err != nil {
				t.Fatal(err)
			}
		}
	}

	list := performChatRequest(handler, http.MethodGet, "/api/chat/sessions", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body)
	}
	var sessions []map[string]any
	decodeChatRecorder(t, list, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v, want 2", sessions)
	}
	for _, session := range sessions {
		// The list orders by updated_at DESC, so match each entry by ID.
		sessionID := session["id"].(string)
		position := slices.Index(sessionIDs, sessionID)
		if position < 0 {
			t.Fatalf("listed session %s was not created by this test", sessionID)
		}
		budgetObject, ok := session["budget"].(map[string]any)
		if !ok {
			t.Fatalf("session %s has no budget member: %#v", sessionID, session)
		}
		limit := 20 + position*10
		spent := 3 + position
		units, _ := budgetObject["units"].([]any)
		want := map[string]any{"unit": "legacy.credits", "unit_label": "legacy.credits",
			"limit": strconv.Itoa(limit), "spent": strconv.Itoa(spent), "reserved": "2",
			"remaining": strconv.Itoa(limit - spent - 2)}
		if budgetObject["id"] != "pb-"+strconv.Itoa(position) ||
			budgetObject["state"] != "active" || len(units) != 1 || !reflect.DeepEqual(units[0], want) {
			t.Fatalf("session %s budget = %#v", sessionID, budgetObject)
		}
	}
}

func performChatRequest(
	handler http.Handler,
	method string,
	target string,
	body string,
) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("X-User-ID", "local-user")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeChatRecorder(
	t *testing.T,
	response *httptest.ResponseRecorder,
	target any,
) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

type recordingChatScheduler struct {
	commands []chat.ProcessTurn
	err      error
	canceled bool
	resumed  []string
}

func (s *recordingChatScheduler) ResumeConsentWait(ownerID, sessionID string) bool {
	s.resumed = append(s.resumed, ownerID+"/"+sessionID)
	return true
}

func (scheduler *recordingChatScheduler) Schedule(
	_ context.Context,
	command chat.ProcessTurn,
) error {
	scheduler.commands = append(scheduler.commands, command)
	return scheduler.err
}

func (scheduler *recordingChatScheduler) CancelTurn(
	_ context.Context,
	_, _ string,
) (bool, error) {
	scheduler.canceled = true
	return true, scheduler.err
}

func performChatRequestAs(handler http.Handler, user, method, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("X-User-ID", user)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLegacyChatSessionPagingSearchAndSingleReadAreOwnerScoped(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	ids := make([]string, 0, 5)
	for _, title := range []string{"Alpha cat", "Beta", "gamma CAT", "Delta", "Epsilon"} {
		response := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{"title":"`+title+`"}`)
		var created map[string]any
		decodeChatRecorder(t, response, &created)
		ids = append(ids, created["id"].(string))
	}

	page := func(query string) (sessions []map[string]any, next string, total int, status int) {
		response := performChatRequest(handler, http.MethodGet, "/api/chat/sessions?"+query, "")
		var body map[string]any
		if response.Code == http.StatusOK {
			decodeChatRecorder(t, response, &body)
		}
		raw, _ := body["sessions"].([]any)
		for _, item := range raw {
			sessions = append(sessions, item.(map[string]any))
		}
		next, _ = body["next_cursor"].(string)
		rawTotal, _ := body["total"].(float64)
		return sessions, next, int(rawTotal), response.Code
	}
	first, next, total, status := page("limit=2")
	if status != http.StatusOK || len(first) != 2 || next == "" || total != len(ids) {
		t.Fatalf("first page = %d %v %q total %d", status, first, next, total)
	}
	seen := map[string]bool{}
	for _, item := range first {
		seen[item["id"].(string)] = true
	}
	for next != "" {
		var more []map[string]any
		var pageTotal int
		more, next, pageTotal, status = page("limit=2&cursor=" + next)
		if status != http.StatusOK || pageTotal != len(ids) {
			t.Fatalf("status = %d total = %d", status, pageTotal)
		}
		for _, item := range more {
			id := item["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate %s across pages", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != len(ids) {
		t.Fatalf("paged %d of %d sessions", len(seen), len(ids))
	}
	found, _, foundTotal, _ := page("search=CAT")
	if len(found) != 2 || foundTotal != 2 {
		t.Fatalf("case-insensitive search = %d rows, total %d: %v", len(found), foundTotal, found)
	}
	if _, _, _, status := page("limit=1000"); status != http.StatusBadRequest {
		t.Fatalf("oversized limit status = %d", status)
	}
	// Deleting one session lowers the total, and clearing the search has no extra filter.
	if deleted := performChatRequest(handler, http.MethodDelete, "/api/chat/sessions/"+ids[2], ""); deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d %s", deleted.Code, deleted.Body)
	}
	afterDelete, _, afterDeleteTotal, _ := page("search=CAT")
	if len(afterDelete) != 1 || afterDeleteTotal != 1 {
		t.Fatalf("search after delete = %d rows, total %d", len(afterDelete), afterDeleteTotal)
	}

	own := performChatRequest(handler, http.MethodGet, "/api/chat/sessions/"+ids[0], "")
	var single map[string]any
	decodeChatRecorder(t, own, &single)
	if own.Code != http.StatusOK || single["id"] != ids[0] {
		t.Fatalf("own session = %d %v", own.Code, single)
	}
	if foreign := performChatRequestAs(handler, "someone-else", http.MethodGet, "/api/chat/sessions/"+ids[0]); foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign session status = %d", foreign.Code)
	}
	if missing := performChatRequest(handler, http.MethodGet, "/api/chat/sessions/nope", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("missing session status = %d", missing.Code)
	}
	otherList := performChatRequestAs(handler, "someone-else", http.MethodGet, "/api/chat/sessions?limit=5")
	var otherBody map[string]any
	if otherList.Code != http.StatusOK || strings.Contains(otherList.Body.String(), ids[0]) {
		t.Fatalf("foreign list leaked: %s", otherList.Body)
	}
	decodeChatRecorder(t, otherList, &otherBody)
	if otherBody["total"].(float64) != 0 {
		t.Fatalf("foreign total = %v", otherBody["total"])
	}
	unpaged := performChatRequest(handler, http.MethodGet, "/api/chat/sessions", "")
	var all []map[string]any
	decodeChatRecorder(t, unpaged, &all)
	if unpaged.Code != http.StatusOK || len(all) != len(ids)-1 {
		t.Fatalf("unpaged list = %d %s", unpaged.Code, unpaged.Body)
	}
}

func TestLegacyChatMessagePagingKeepsUnpagedShapeAndIsOwnerScoped(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	created := performChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{"title":"Long"}`)
	var session map[string]any
	decodeChatRecorder(t, created, &session)
	sessionID, _ := session["id"].(string)
	if created.Code != http.StatusCreated || sessionID == "" {
		t.Fatalf("create session: %d %s", created.Code, created.Body)
	}
	path := "/api/chat/sessions/" + sessionID + "/messages"
	if sent := performChatRequest(handler, http.MethodPost, path, `{"content":"make a version"}`); sent.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", sent.Code, sent.Body)
	}

	unpaged := performChatRequest(handler, http.MethodGet, path, "")
	var all []map[string]any
	decodeChatRecorder(t, unpaged, &all)
	if unpaged.Code != http.StatusOK || len(all) != 2 {
		t.Fatalf("unpaged = %d %s", unpaged.Code, unpaged.Body)
	}
	type page struct {
		Messages   []map[string]any `json:"messages"`
		NextCursor string           `json:"next_cursor"`
	}
	read := func(query string) (page, int) {
		response := performChatRequest(handler, http.MethodGet, path+"?"+query, "")
		var result page
		if response.Code == http.StatusOK {
			decodeChatRecorder(t, response, &result)
		}
		return result, response.Code
	}
	newest, status := read("limit=1")
	if status != http.StatusOK || len(newest.Messages) != 1 || newest.Messages[0]["id"] != all[1]["id"] || newest.NextCursor == "" {
		t.Fatalf("newest page = %d %#v", status, newest)
	}
	older, status := read("limit=1&before=" + url.QueryEscape(newest.NextCursor))
	if status != http.StatusOK || len(older.Messages) != 1 || older.Messages[0]["id"] != all[0]["id"] || older.NextCursor != "" {
		t.Fatalf("older page = %d %#v", status, older)
	}
	defaulted, status := read("before=")
	if status != http.StatusOK || len(defaulted.Messages) != 2 || defaulted.Messages[0]["id"] != all[0]["id"] {
		t.Fatalf("default page = %d %#v", status, defaulted)
	}
	for _, query := range []string{"limit=0", "limit=201", "limit=abc", "before=not-a-cursor"} {
		if _, status := read(query); status != http.StatusBadRequest {
			t.Fatalf("%s status = %d", query, status)
		}
	}
	if foreign := performChatRequestAs(handler, "someone-else", http.MethodGet, path+"?limit=5"); foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign page status = %d", foreign.Code)
	}
}

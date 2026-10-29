package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #92: the real Agent tool path, not the Features settings. The person names
// Hyper3D texture, refers to the chat's @model1 and attaches @img1; the step
// is prepared (nothing executed, nothing paid) with the picture as the
// required reference image and the model file as the source.
func TestAgentToolSubmitsHyper3DTextureWithTheChatImageAndModel(t *testing.T) {
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
		// A browser session: only a person may switch the session to automatic approval.
		AuthMode:      "session",
		SessionSecret: strings.Repeat("s", 32),
		AdminUsername: "harness-admin",
		AdminPassword: "correct-horse",
		AdminName:     "Harness",
		AdminEmail:    "harness@example.test",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	generationDone := make(chan error, 1)
	go func() {
		generationDone <- application.generation.Run(runCtx, generation.CoordinatorConfig{PollEvery: 5 * time.Millisecond, LeaseFor: time.Second, BatchSize: 10})
	}()
	agentDone := make(chan error, 1)
	go func() { agentDone <- application.chatAgent.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		<-generationDone
		<-agentDone
		_ = application.generation.Close()
	})
	handler := application.server.Handler
	var cookie *http.Cookie
	send := func(request *http.Request) *httptest.ResponseRecorder {
		request.Header.Set("Origin", "http://example.com")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	appChatRequest := func(_ http.Handler, method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		return send(request)
	}
	login := appChatRequest(handler, http.MethodPost, "/api/auth/login", `{"username":"harness-admin","password":"correct-horse"}`)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login %d %s", login.Code, login.Body)
	}
	cookie = login.Result().Cookies()[0]

	create := appChatRequest(handler, http.MethodPost, "/api/chat/sessions", `{}`)
	var session struct {
		ID string `json:"id"`
	}
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatalf("create session %d %s", create.Code, create.Body)
	}
	if response := appChatRequest(handler, http.MethodPut, "/api/v1/chat/sessions/"+session.ID+"/approval", `{"approvalMode":"auto"}`); response.Code != http.StatusOK {
		t.Fatalf("approval mode %d %s", response.Code, response.Body)
	}
	database := openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))
	waitTurns := func(want int) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var done, open int
			_ = database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=? AND state IN (?,?)`, session.ID,
				application.schema.Chat.State.TurnDone, application.schema.Chat.State.TurnError).Scan(&done)
			_ = database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=? AND state NOT IN (?,?)`, session.ID,
				application.schema.Chat.State.TurnDone, application.schema.Chat.State.TurnError).Scan(&open)
			if done >= want && open == 0 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("chat turns did not finish (want %d)", want)
	}

	// 1. A finished model in this session to retexture.
	if response := appChatRequest(handler, http.MethodPost, "/api/chat/sessions/"+session.ID+"/messages",
		`{"content":"做个3D模型：一只木盒","ai_model":"meshy-7.1","creative_domain":"model-3d"}`); response.Code != http.StatusAccepted {
		t.Fatalf("send model request %d %s", response.Code, response.Body)
	}
	waitTurns(1)
	var sourceTask, sourceAsset string
	if err := database.QueryRow(`SELECT t.id, a.id FROM generation_tasks t JOIN generation_assets a ON a.task_id=t.id
		WHERE t.session_id=? AND t.state='SUCCEEDED' AND a.role='model' AND a.variant='glb' LIMIT 1`, session.ID).Scan(&sourceTask, &sourceAsset); err != nil {
		t.Fatalf("no finished GLB model in the session: %v", err)
	}

	// 2. A picture of the chat (@img1).
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="wood.png"`)
	header.Set("Content-Type", "image/png")
	part, _ := writer.CreatePart(header)
	_, _ = part.Write(picture.Bytes())
	_ = writer.Close()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &form)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	upload := send(uploadRequest)
	var uploaded struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if upload.Code/100 != 2 || json.Unmarshal(upload.Body.Bytes(), &uploaded) != nil || uploaded.ID == "" {
		t.Fatalf("upload %d %s", upload.Code, upload.Body)
	}
	pictureURL := uploaded.URL
	if pictureURL == "" {
		pictureURL = "/api/v1/assets/" + uploaded.ID
	}
	message, _ := json.Marshal(map[string]any{"content": "给 @model1 换贴图：选 Hyper3D 纹理生成（本地模型），附上的是参考图 @img1", "image_urls": []string{pictureURL}})
	if response := appChatRequest(handler, http.MethodPost, "/api/chat/sessions/"+session.ID+"/messages", string(message)); response.Code != http.StatusAccepted {
		t.Fatalf("send retexture request %d %s", response.Code, response.Body)
	}
	waitTurns(2)
	var payload string
	if err := database.QueryRow(`SELECT p.payload_json FROM execution_grants g JOIN generation_prepared_inputs p ON p.id=g.prepared_input_id
		WHERE g.offering_id=? AND g.session_id=?`, "hyper3d/texture/local-model", session.ID).Scan(&payload); err != nil {
		messages := appChatRequest(handler, http.MethodGet, "/api/chat/sessions/"+session.ID+"/messages", "")
		t.Fatalf("no Hyper3D texture step was prepared: %v; messages=%s", err, messages.Body)
	}
	var prepared struct {
		NormalizedInput map[string]any `json:"normalized_input"`
	}
	if err := json.Unmarshal([]byte(payload), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.NormalizedInput["reference_image"] != uploaded.ID || prepared.NormalizedInput["source_asset"] != sourceAsset {
		t.Fatalf("prepared input %s; want reference_image=%s source_asset=%s", payload, uploaded.ID, sourceAsset)
	}
}

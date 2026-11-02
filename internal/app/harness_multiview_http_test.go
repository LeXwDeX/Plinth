package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const multiviewOfferingID = "tripo/tripo-v3.1-multiview/image-to-3d"

type multiviewHTTP struct {
	t        *testing.T
	do       func(method, target, body string) *httptest.ResponseRecorder
	send     func(*http.Request) *httptest.ResponseRecorder
	database *sql.DB
	session  string
	app      *Application
}

func newMultiviewHTTP(t *testing.T) *multiviewHTTP {
	t.Helper()
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
		AuthMode:                  "session",
		SessionSecret:             strings.Repeat("s", 32),
		AdminUsername:             "harness-admin",
		AdminPassword:             "correct-horse",
		AdminName:                 "Harness",
		AdminEmail:                "harness@example.test",
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
	f := &multiviewHTTP{t: t, app: application}
	var cookie *http.Cookie
	f.send = func(request *http.Request) *httptest.ResponseRecorder {
		request.Header.Set("Origin", "http://example.com")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		application.server.Handler.ServeHTTP(response, request)
		return response
	}
	f.do = func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		return f.send(request)
	}
	login := f.do(http.MethodPost, "/api/auth/login", `{"username":"harness-admin","password":"correct-horse"}`)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login %d %s", login.Code, login.Body)
	}
	cookie = login.Result().Cookies()[0]
	create := f.do(http.MethodPost, "/api/chat/sessions", `{}`)
	var session struct {
		ID string `json:"id"`
	}
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatalf("create session %d %s", create.Code, create.Body)
	}
	f.session = session.ID
	if response := f.do(http.MethodPut, "/api/v1/chat/sessions/"+f.session+"/approval", `{"approvalMode":"auto"}`); response.Code != http.StatusOK {
		t.Fatalf("approval mode %d %s", response.Code, response.Body)
	}
	f.database = openRawAppDatabase(t, filepath.Join(root, "art-ai.db"))
	return f
}

func (f *multiviewHTTP) waitTurns(want int) {
	f.t.Helper()
	chat := f.app.schema.Chat
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		var done, open int
		_ = f.database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=? AND state IN (?,?)`, f.session, chat.State.TurnDone, chat.State.TurnError).Scan(&done)
		_ = f.database.QueryRow(`SELECT COUNT(*) FROM go_chat_turns WHERE session_id=? AND state NOT IN (?,?)`, f.session, chat.State.TurnDone, chat.State.TurnError).Scan(&open)
		if done >= want && open == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("chat turns did not finish (want %d)", want)
}

// upload stores a distinct PNG and returns its asset ID and URL.
func (f *multiviewHTTP) upload(name string, shade uint8) (string, string) {
	f.t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			canvas.Set(x, y, color.RGBA{R: shade, G: uint8(x * 4), B: uint8(y * 4), A: 255})
		}
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, canvas); err != nil {
		f.t.Fatal(err)
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	header.Set("Content-Type", "image/png")
	part, _ := writer.CreatePart(header)
	_, _ = part.Write(picture.Bytes())
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &form)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := f.send(request)
	var uploaded struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if response.Code/100 != 2 || json.Unmarshal(response.Body.Bytes(), &uploaded) != nil || uploaded.ID == "" {
		f.t.Fatalf("upload %d %s", response.Code, response.Body)
	}
	if uploaded.URL == "" {
		uploaded.URL = "/api/v1/assets/" + uploaded.ID
	}
	return uploaded.ID, uploaded.URL
}

func (f *multiviewHTTP) message(content string, urls ...string) {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"content": content, "image_urls": urls})
	if response := f.do(http.MethodPost, "/api/chat/sessions/"+f.session+"/messages", string(body)); response.Code != http.StatusAccepted {
		f.t.Fatalf("send message %d %s", response.Code, response.Body)
	}
}

func (f *multiviewHTTP) preparedViews() ([]any, string) {
	f.t.Helper()
	var grantStatus, payload string
	if err := f.database.QueryRow(`SELECT g.status, p.payload_json FROM execution_grants g JOIN generation_prepared_inputs p ON p.id=g.prepared_input_id
		WHERE g.offering_id=? AND g.session_id=?`, multiviewOfferingID, f.session).Scan(&grantStatus, &payload); err != nil {
		f.t.Fatalf("no multi-view step was prepared: %v; messages=%s", err, f.do(http.MethodGet, "/api/chat/sessions/"+f.session+"/messages", "").Body)
	}
	var prepared struct {
		NormalizedInput map[string]any `json:"normalized_input"`
	}
	if err := json.Unmarshal([]byte(payload), &prepared); err != nil {
		f.t.Fatal(err)
	}
	assets, _ := prepared.NormalizedInput["reference_assets"].([]any)
	return assets, grantStatus
}

func (f *multiviewHTTP) assistantCalled(tool string) bool {
	return strings.Contains(f.do(http.MethodGet, "/api/chat/sessions/"+f.session+"/messages", "").Body.String(), tool)
}

// #27 over HTTP: with 三视图生成 saved in the Features settings (no model
// source), the pictures attached to the message are the Offering's reference
// assets, in the order they were attached.
func TestHarnessMultiviewOperationTakesThePicturesAttachedToTheMessage(t *testing.T) {
	f := newMultiviewHTTP(t)
	var offering generation.Offering
	for _, candidate := range f.app.generation.Offerings(generation.ImageTo3D) {
		if candidate.ID == multiviewOfferingID {
			offering = candidate
		}
	}
	if offering.ID == "" || !f.app.generation.OfferingAvailable(offering.ID) {
		t.Fatal("Tripo multiview image-to-3D is not available in mock mode")
	}
	harnessPath := "/api/v1/chat/sessions/" + f.session + "/harness"
	selection, _ := json.Marshal(map[string]any{"expected_revision": 0, "selection": map[string]any{
		"operation_id": "multiview-to-3d", "offering_id": offering.ID, "offering_revision": offering.Revision,
		"enabled_operations": []string{"multiview-to-3d"}, "allow_prerequisites": false,
		"sources": []map[string]any{}, "fixed_parameters": map[string]any{}, "fillable_parameters": []string{},
	}})
	if response := f.do(http.MethodPut, harnessPath, string(selection)); response.Code != http.StatusOK {
		t.Fatalf("save harness %d %s", response.Code, response.Body)
	}
	var view struct {
		Status            string   `json:"status"`
		MissingParameters []string `json:"missing_parameters"`
	}
	read := f.do(http.MethodGet, harnessPath, "")
	if json.Unmarshal(read.Body.Bytes(), &view) != nil || view.Status != "ready" || len(view.MissingParameters) != 0 {
		t.Fatalf("saved harness view %d %s", read.Code, read.Body)
	}
	frontID, frontURL := f.upload("front.png", 20)
	sideID, sideURL := f.upload("side.png", 120)
	f.message("用我附的两张三视图生成 3D 模型", frontURL, sideURL)
	f.waitTurns(1)
	assets, grantStatus := f.preparedViews()
	if !reflect.DeepEqual(assets, []any{frontID, sideID}) || grantStatus != "bound" {
		t.Fatalf("reference_assets = %v grant=%q; want [%s %s] bound", assets, grantStatus, frontID, sideID)
	}
	if !f.assistantCalled("create_model_3d_from_views") {
		t.Fatal("assistant did not call create_model_3d_from_views")
	}
}

// #27 in free mode (no Feature saved): two views of one object reach the same
// tool; a single picture still goes to the ordinary image-to-3D tool.
func TestFreeModeMultiviewPicturesChooseTheMultiviewTool(t *testing.T) {
	f := newMultiviewHTTP(t)
	frontID, frontURL := f.upload("front.png", 20)
	backID, backURL := f.upload("back.png", 200)
	f.message("用这两张三视图生成 3D 模型", frontURL, backURL)
	f.waitTurns(1)
	assets, grantStatus := f.preparedViews()
	if !reflect.DeepEqual(assets, []any{frontID, backID}) || grantStatus != "bound" {
		t.Fatalf("reference_assets = %v grant=%q; want [%s %s] bound", assets, grantStatus, frontID, backID)
	}
	if !f.assistantCalled("create_model_3d_from_views") {
		t.Fatal("assistant did not call create_model_3d_from_views")
	}
	f.message("把这张图转成 3D 模型", frontURL)
	f.waitTurns(2)
	if !f.assistantCalled("create_model_3d_from_image") {
		t.Fatal("a single picture did not go to the ordinary image-to-3D tool")
	}
}

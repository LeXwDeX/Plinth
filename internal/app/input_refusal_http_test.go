package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
)

// #288: a reference the Offering refuses (here 300×15, wider than Seedream's
// 16:1 limit) is answered over HTTP with the Schema's reason, the same text
// the Agent path already gives, and nothing is prepared. Mock provider only.
func TestPrepareReturnsSchemaRefusalReasonOverHTTP(t *testing.T) {
	root := t.TempDir()
	application, err := New(Config{
		RuntimeSettings:           map[string]string{"llm_model": "test-model"},
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "session",
		SessionSecret:             strings.Repeat("s", 32),
		AdminUsername:             "refusal-admin",
		AdminPassword:             "correct-horse",
		AdminName:                 "Refusal",
		AdminEmail:                "refusal@example.test",
	}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	handler := application.server.Handler
	send := func(request *http.Request, cookie *http.Cookie) *httptest.ResponseRecorder {
		if request.Header.Get("Content-Type") == "" {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Origin", "http://example.com")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	login := send(httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"refusal-admin","password":"correct-horse"}`)), nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body)
	}
	cookie := login.Result().Cookies()[0]
	session := send(httptest.NewRequest(http.MethodPost, "/api/chat/sessions", strings.NewReader(`{"title":"refusal"}`)), cookie)
	var created struct {
		ID string `json:"id"`
	}
	if session.Code != http.StatusCreated || json.Unmarshal(session.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body)
	}

	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewGray(image.Rect(0, 0, 300, 15))); err != nil {
		t.Fatal(err)
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="wide.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(picture.Bytes())
	_ = writer.Close()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &form)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	upload := send(uploadRequest, cookie)
	var uploaded struct {
		ID      string `json:"id"`
		AssetID string `json:"asset_id"`
	}
	if upload.Code/100 != 2 || json.Unmarshal(upload.Body.Bytes(), &uploaded) != nil {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body)
	}
	assetID := uploaded.AssetID
	if assetID == "" {
		assetID = uploaded.ID
	}
	if assetID == "" {
		t.Fatalf("upload body has no asset id: %s", upload.Body)
	}

	prepare := send(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/prepare", strings.NewReader(
		`{"session_id":"`+created.ID+`","capability":"image-to-image","offering_id":"jimeng/seedream-4-5/image-to-image",`+
			`"input":{"prompt":"make it brighter","reference_assets":["`+assetID+`"]}}`)), cookie)
	var refused struct {
		Code       string `json:"code"`
		Detail     string `json:"detail"`
		UserFacing bool   `json:"user_facing"`
		RequestID  string `json:"request_id"`
	}
	if prepare.Code != http.StatusBadRequest || json.Unmarshal(prepare.Body.Bytes(), &refused) != nil {
		t.Fatalf("prepare status=%d body=%s", prepare.Code, prepare.Body)
	}
	if refused.Code != "invalid_input" || !refused.UserFacing || refused.RequestID == "" ||
		!strings.Contains(refused.Detail, "300×15") || !strings.Contains(refused.Detail, "16 倍") ||
		!strings.Contains(refused.Detail, "「参考图」") {
		t.Fatalf("refusal = %+v", refused)
	}
	for _, private := range []string{assetID, "reference_assets", "resolve_input_assets", root} {
		if strings.Contains(prepare.Body.String(), private) {
			t.Fatalf("refusal leaked %q: %s", private, prepare.Body)
		}
	}
}

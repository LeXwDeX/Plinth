package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// sendMultiview posts a message that carries the multiview panel's structured
// request, the way the browser sends it.
func (f *multiviewHTTP) sendMultiview(content, model string, views map[string]string, urls ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	entries := make([]map[string]string, 0, len(views))
	for _, view := range []string{"front", "left", "back", "right"} {
		if assetID, labelled := views[view]; labelled {
			entries = append(entries, map[string]string{"view": view, "asset_id": assetID})
		}
	}
	body, _ := json.Marshal(map[string]any{
		"content": content, "image_urls": urls,
		"multiview": map[string]any{"model": model, "views": entries},
	})
	return f.do(http.MethodPost, "/api/chat/sessions/"+f.session+"/messages", string(body))
}

func (f *multiviewHTTP) lockModel3D(model string) {
	f.t.Helper()
	response := f.do(http.MethodPut, "/api/v1/chat/sessions/"+f.session+"/model3d", `{"explicit_model_3d":"`+model+`"}`)
	if response.Code != http.StatusOK {
		f.t.Fatalf("lock %q: %d %s", model, response.Code, response.Body)
	}
}

func (f *multiviewHTTP) lockedModel3D() string {
	f.t.Helper()
	response := f.do(http.MethodGet, "/api/v1/chat/sessions/"+f.session+"/model3d", "")
	var body struct {
		Model string `json:"explicit_model_3d"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		f.t.Fatalf("read lock: %d %s", response.Code, response.Body)
	}
	return body.Model
}

func (f *multiviewHTTP) storedMessages() int {
	f.t.Helper()
	var count int
	if err := f.database.QueryRow(`SELECT COUNT(*) FROM go_chat_messages WHERE session_id=?`, f.session).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

// #373 over HTTP: the person locked Rodin (a model that takes no views), then
// labelled three pictures on the multiview panel. The panel's model is their pick
// of that turn: the step is prepared on it, the Agent is not refused, and the
// session's lock is left as it was.
func TestMultiviewRequestRunsOnTheModelOfThePanelWhateverTheSessionLocked(t *testing.T) {
	f := newMultiviewHTTP(t)
	f.lockModel3D("rodin-gen-2.5")
	frontID, frontURL := f.upload("front.png", 20)
	leftID, leftURL := f.upload("left.png", 120)
	backID, backURL := f.upload("back.png", 200)
	response := f.sendMultiview("用这几张图的 正面 / 左侧 / 背面 视图生成 3D 模型", "tripo-v3.1-multiview",
		map[string]string{"front": frontID, "left": leftID, "back": backID}, frontURL, leftURL, backURL)
	if response.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", response.Code, response.Body)
	}
	f.waitTurns(1)
	assets, grantStatus := f.preparedViews()
	if !reflect.DeepEqual(assets, []any{frontID, leftID, backID}) || grantStatus != "bound" {
		t.Fatalf("reference_assets = %v grant=%q; want [%s %s %s] bound", assets, grantStatus, frontID, leftID, backID)
	}
	if !f.assistantCalled("create_model_3d_from_views") {
		t.Fatal("assistant did not call create_model_3d_from_views")
	}
	if locked := f.lockedModel3D(); locked != "rodin-gen-2.5" {
		t.Fatalf("the session's lock became %q; a multiview request must not edit it", locked)
	}
}

// #373: a request whose model takes no views, or that no Offering carries, is
// refused when the turn starts: a 4xx with the Schema's text and nothing stored,
// not a whole Agent turn that can only end in a refusal.
func TestMultiviewRequestForAModelThatTakesNoViewsIsRefusedAtOnce(t *testing.T) {
	f := newMultiviewHTTP(t)
	f.lockModel3D("rodin-gen-2.5")
	frontID, frontURL := f.upload("front.png", 20)
	leftID, leftURL := f.upload("left.png", 120)
	want := f.app.schema.Chat.Presentation.Messages.MultiviewModelUnavailable
	if want == "" {
		t.Fatal("the shipped Schema must word the refusal of an unavailable multiview model")
	}
	for _, model := range []string{"rodin-gen-2.5", "tripo-p2", "missing-model"} {
		response := f.sendMultiview("生成 3D", model, map[string]string{"front": frontID, "left": leftID}, frontURL, leftURL)
		var body map[string]string
		if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &body) != nil ||
			body[f.app.schema.Chat.API.ResponseFields.Error] != want {
			t.Fatalf("model %q: %d %s, want 400 with %q", model, response.Code, response.Body, want)
		}
	}
	if stored := f.storedMessages(); stored != 0 {
		t.Fatalf("refused requests stored %d messages", stored)
	}
	// The text is the Schema's in the reader's locale: an English reader gets the English pair.
	english := f.app.schema.Localization.Bundles["en"].Fields["chat.presentation.messages.multiview_model_unavailable"]
	if english == "" || english == want {
		t.Fatalf("the shipped Schema must pair the refusal with an English text: %q", english)
	}
	body, _ := json.Marshal(map[string]any{
		"content": "make it 3D", "image_urls": []string{frontURL, leftURL},
		"multiview": map[string]any{"model": "rodin-gen-2.5", "views": []map[string]string{
			{"view": "front", "asset_id": frontID}, {"view": "left", "asset_id": leftID}}},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/chat/sessions/"+f.session+"/messages", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", "en")
	response := f.send(request)
	var reply map[string]string
	if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &reply) != nil ||
		reply[f.app.schema.Chat.API.ResponseFields.Error] != english {
		t.Fatalf("english request: %d %s, want 400 with %q", response.Code, response.Body, english)
	}
}

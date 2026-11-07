package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func styleLock187Session(t *testing.T, handler http.Handler, cookie *http.Cookie, title string) string {
	t.Helper()
	created := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/chat/sessions",
		`{"title":"`+title+`"}`, "")
	var session struct {
		ID string `json:"id"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatalf("create session: %d %s", created.Code, created.Body.String())
	}
	return session.ID
}

func styleLock187Prepare(t *testing.T, handler http.Handler, cookie *http.Cookie, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/prepare", string(encoded), "")
}

func styleLock187Prepared(t *testing.T, module *generation.Module, preparedID string) generation.PreparedInput {
	t.Helper()
	prepared, err := module.GetPreparedInput(context.Background(), "owner-a", generation.PreparedInputID(preparedID))
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func styleLock187Normalized(t *testing.T, prepared generation.PreparedInput) (string, []string) {
	t.Helper()
	var input struct {
		Prompt          string   `json:"prompt"`
		ReferenceAssets []string `json:"reference_assets"`
	}
	if err := json.Unmarshal(prepared.NormalizedInput, &input); err != nil {
		t.Fatalf("normalized input %s: %v", prepared.NormalizedInput, err)
	}
	return input.Prompt, input.ReferenceAssets
}

func TestDirectPrepareAppliesTheStyleLockOnlyToDeclaredImageCapabilities(t *testing.T) {
	module, handler, cookie, lockedSession := grantTestBrowserWithApplication(t, nil, []string{"user"})
	server := httptest.NewServer(handler)
	defer server.Close()
	turnPicture := uploadInputAsset(t, server.URL, "owner-a")
	stylePicture := uploadInputAsset(t, server.URL, "owner-a")
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	if _, err := repository.SetStyleLock(context.Background(), "owner-a", lockedSession, chat.StyleLock{
		Enabled: true, Text: "ink and paper", References: []string{stylePicture},
	}); err != nil {
		t.Fatal(err)
	}
	controlSession := styleLock187Session(t, handler, cookie, "control")

	imageToImage := func(session string, skipped bool) map[string]any {
		return map[string]any{
			"session_id": session, "capability": "image-to-image",
			"offering_id": "openai/gpt-image-2/image-to-image",
			"input":       map[string]any{"prompt": "make the cube blue", "reference_assets": []string{turnPicture}},
			"style_lock_skipped": skipped,
		}
	}

	// The lock's text follows the turn prompt and its reference follows the
	// turn's own picture, not the other way round.
	locked := styleLock187Prepare(t, handler, cookie, imageToImage(lockedSession, false))
	if locked.Code != http.StatusCreated {
		t.Fatalf("locked prepare: %d %s", locked.Code, locked.Body.String())
	}
	var lockedView struct {
		ID string `json:"prepared_input_id"`
	}
	if err := json.Unmarshal(locked.Body.Bytes(), &lockedView); err != nil || lockedView.ID == "" {
		t.Fatalf("locked prepare body: %s err=%v", locked.Body.String(), err)
	}
	frozen := styleLock187Prepared(t, module, lockedView.ID)
	prompt, references := styleLock187Normalized(t, frozen)
	if !strings.Contains(prompt, "Style lock:") || !strings.Contains(prompt, "Style description: ink and paper") ||
		!strings.Contains(prompt, "Edit scope:") {
		t.Fatalf("locked prompt = %q", prompt)
	}
	if len(references) != 2 || references[0] != turnPicture || references[1] != stylePicture {
		t.Fatalf("locked references = %#v", references)
	}

	// A retry or recovery hands the frozen NormalizedInput straight to
	// PrepareInput. It must not append the template a second time.
	replayed, err := module.PrepareInput(context.Background(), generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: lockedSession, Capability: generation.ImageToImage,
		OfferingID: "openai/gpt-image-2/image-to-image", Input: frozen.NormalizedInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	replayedPrompt, _ := styleLock187Normalized(t, replayed)
	if strings.Count(replayedPrompt, "Style lock:") != 1 || replayedPrompt != prompt {
		t.Fatalf("replayed prompt = %q", replayedPrompt)
	}

	// The one-turn exemption leaves the input exactly as the unlocked control
	// session prepares it.
	skipped := styleLock187Prepare(t, handler, cookie, imageToImage(lockedSession, true))
	control := styleLock187Prepare(t, handler, cookie, imageToImage(controlSession, false))
	if skipped.Code != http.StatusCreated || control.Code != http.StatusCreated {
		t.Fatalf("skipped/control prepare: %d/%d %s%s", skipped.Code, control.Code, skipped.Body.String(), control.Body.String())
	}
	var skippedView, controlView struct {
		ID string `json:"prepared_input_id"`
	}
	_ = json.Unmarshal(skipped.Body.Bytes(), &skippedView)
	_ = json.Unmarshal(control.Body.Bytes(), &controlView)
	skippedInput := styleLock187Prepared(t, module, skippedView.ID)
	controlInput := styleLock187Prepared(t, module, controlView.ID)
	if !bytes.Equal(skippedInput.NormalizedInput, controlInput.NormalizedInput) {
		t.Fatalf("skipped input = %s, control = %s", skippedInput.NormalizedInput, controlInput.NormalizedInput)
	}
	if skippedPrompt, _ := styleLock187Normalized(t, skippedInput); strings.Contains(skippedPrompt, "Style lock") {
		t.Fatalf("skipped prompt = %q", skippedPrompt)
	}

	// Background removal and image-to-3D stay outside the declared
	// capabilities: their frozen input is byte-identical to the control.
	for _, excluded := range []struct {
		name       string
		capability string
		offering   string
		input      map[string]any
	}{
		{name: "background removal", capability: "background-removal",
			offering: "matting/default/background-removal",
			input:    map[string]any{"source_assets": []string{turnPicture}}},
		{name: "image to 3d", capability: "image-to-3d",
			offering: "meshy/meshy-7.1/image-to-3d",
			input:    map[string]any{"reference_assets": []string{turnPicture}}},
	} {
		body := func(session string) map[string]any {
			return map[string]any{
				"session_id": session, "capability": excluded.capability,
				"offering_id": excluded.offering, "input": excluded.input,
			}
		}
		lockedResponse := styleLock187Prepare(t, handler, cookie, body(lockedSession))
		controlResponse := styleLock187Prepare(t, handler, cookie, body(controlSession))
		if lockedResponse.Code != http.StatusCreated || controlResponse.Code != http.StatusCreated {
			t.Fatalf("%s prepare: %d/%d %s%s", excluded.name, lockedResponse.Code, controlResponse.Code,
				lockedResponse.Body.String(), controlResponse.Body.String())
		}
		var lockedBody, controlBody struct {
			ID string `json:"prepared_input_id"`
		}
		_ = json.Unmarshal(lockedResponse.Body.Bytes(), &lockedBody)
		_ = json.Unmarshal(controlResponse.Body.Bytes(), &controlBody)
		lockedInput := styleLock187Prepared(t, module, lockedBody.ID)
		controlInput := styleLock187Prepared(t, module, controlBody.ID)
		if !bytes.Equal(lockedInput.NormalizedInput, controlInput.NormalizedInput) {
			t.Fatalf("%s input = %s, control = %s", excluded.name, lockedInput.NormalizedInput, controlInput.NormalizedInput)
		}
	}

	// A model limit trims the lock's pictures, never the turn's own ones: the
	// first five stay and the prepare answer carries the Schema note.
	pictures := []string{turnPicture}
	for range 4 {
		pictures = append(pictures, uploadInputAsset(t, server.URL, "owner-a"))
	}
	trimmed := styleLock187Prepare(t, handler, cookie, map[string]any{
		"session_id": lockedSession, "capability": "image-to-image",
		"offering_id": "meshy/nano-banana-pro/image-to-image",
		"input":       map[string]any{"prompt": "make the cube blue", "reference_assets": pictures},
	})
	var trimmedView struct {
		ID            string `json:"prepared_input_id"`
		ReferenceNote string `json:"reference_note"`
	}
	if trimmed.Code != http.StatusCreated || json.Unmarshal(trimmed.Body.Bytes(), &trimmedView) != nil ||
		trimmedView.ReferenceNote == "" {
		t.Fatalf("trimmed prepare: %d %s", trimmed.Code, trimmed.Body.String())
	}
	_, trimmedReferences := styleLock187Normalized(t, styleLock187Prepared(t, module, trimmedView.ID))
	if len(trimmedReferences) != 5 {
		t.Fatalf("trimmed references = %#v", trimmedReferences)
	}
	for index, picture := range pictures {
		if trimmedReferences[index] != picture {
			t.Fatalf("trimmed references = %#v, want %v", trimmedReferences, pictures)
		}
	}

	// A model with no reference parameter refuses rather than silently
	// dropping the lock's pictures.
	unavailable := styleLock187Prepare(t, handler, cookie, map[string]any{
		"session_id": lockedSession, "capability": "text-to-image",
		"offering_id": "openai/gpt-image-2/text-to-image",
		"input":       map[string]any{"prompt": "a red cube"},
	})
	if unavailable.Code != http.StatusBadRequest ||
		!strings.Contains(unavailable.Body.String(), "当前模型无法使用风格锁定参考图") {
		t.Fatalf("unavailable reference answer: %d %s", unavailable.Code, unavailable.Body.String())
	}

	// The frozen task keeps the injected prompt: the task detail shows it.
	grant := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks/grants",
		`{"prepared_input_id":"`+lockedView.ID+`"}`, "style-lock-187")
	var requested struct {
		ID     string `json:"request_id"`
		Status string `json:"status"`
	}
	if grant.Code != http.StatusCreated || json.Unmarshal(grant.Body.Bytes(), &requested) != nil || requested.ID == "" {
		t.Fatalf("grant: %d %s", grant.Code, grant.Body.String())
	}
	if requested.Status == "pending" {
		decided := grantTestRequest(t, handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requested.ID,
			`{"decision":"approve"}`, "")
		if decided.Code != http.StatusOK {
			t.Fatalf("approve: %d %s", decided.Code, decided.Body.String())
		}
	}
	submitted := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/tasks",
		`{"prepared_input_id":"`+lockedView.ID+`","authorization_request_id":"`+requested.ID+`"}`, "style-lock-187")
	if submitted.Code != http.StatusAccepted && submitted.Code != http.StatusOK && submitted.Code != http.StatusCreated {
		t.Fatalf("submit: %d %s", submitted.Code, submitted.Body.String())
	}
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("submit body: %s err=%v", submitted.Body.String(), err)
	}
	detail := grantTestRequest(t, handler, cookie, http.MethodGet, "/api/v1/tasks/"+accepted.TaskID, "", "")
	var task struct {
		Input *struct {
			Prompt string `json:"prompt"`
		} `json:"input"`
	}
	if detail.Code != http.StatusOK || json.Unmarshal(detail.Body.Bytes(), &task) != nil ||
		task.Input == nil || !strings.Contains(task.Input.Prompt, "Style lock:") ||
		!strings.Contains(task.Input.Prompt, "ink and paper") {
		t.Fatalf("task detail: %d %s", detail.Code, detail.Body.String())
	}
}

func TestDirectPrepareLeavesADisabledStyleLockAlone(t *testing.T) {
	module, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	server := httptest.NewServer(handler)
	defer server.Close()
	picture := uploadInputAsset(t, server.URL, "owner-a")
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture repository")
	}
	repository := value.(*fixtureExecutionState).repository
	if _, err := repository.SetStyleLock(context.Background(), "owner-a", session, chat.StyleLock{
		Enabled: false, Text: "ink and paper", References: []string{picture},
	}); err != nil {
		t.Fatal(err)
	}
	control := styleLock187Session(t, handler, cookie, "disabled control")
	body := func(session string) map[string]any {
		return map[string]any{
			"session_id": session, "capability": "image-to-image",
			"offering_id": "openai/gpt-image-2/image-to-image",
			"input":       map[string]any{"prompt": "make the cube blue", "reference_assets": []string{picture}},
		}
	}
	disabled := styleLock187Prepare(t, handler, cookie, body(session))
	unlocked := styleLock187Prepare(t, handler, cookie, body(control))
	if disabled.Code != http.StatusCreated || unlocked.Code != http.StatusCreated {
		t.Fatalf("prepare: %d/%d %s%s", disabled.Code, unlocked.Code, disabled.Body.String(), unlocked.Body.String())
	}
	var disabledView, unlockedView struct {
		ID string `json:"prepared_input_id"`
	}
	_ = json.Unmarshal(disabled.Body.Bytes(), &disabledView)
	_ = json.Unmarshal(unlocked.Body.Bytes(), &unlockedView)
	if !bytes.Equal(styleLock187Prepared(t, module, disabledView.ID).NormalizedInput,
		styleLock187Prepared(t, module, unlockedView.ID).NormalizedInput) {
		t.Fatal("a disabled lock changed the prepared input")
	}
}
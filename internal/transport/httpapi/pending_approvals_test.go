package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// pendingApprovalWorld is a session with one Agent approval whose turn ended:
// the operation is detached and its grant is still pending.
type pendingApprovalWorld struct {
	module    *generation.Module
	handler   http.Handler
	cookie    *http.Cookie
	other     *http.Cookie
	sessionID string
	state     *fixtureExecutionState
	inputs    *inputassets.Service
}

// pendingApprovalBrowsers logs two users in; the first owns the session.
func pendingApprovalBrowsers(t *testing.T, customize func(*catalog.ApplicationSchema)) pendingApprovalWorld {
	t.Helper()
	return pendingApprovalBrowsersWithCatalog(t, customize, nil)
}

// pendingApprovalBrowsersWithCatalog is pendingApprovalBrowsers with a test
// Offering catalog; nil keeps the shipped one.
func pendingApprovalBrowsersWithCatalog(
	t *testing.T,
	customize func(*catalog.ApplicationSchema),
	taskCatalog *catalog.Catalog,
) pendingApprovalWorld {
	t.Helper()
	hash := "$2b$04$UVs2NmHqVLnDFtkcPXeOhuCLZNX27fLFYc/TbHf7w5wkWsWZXCP2."
	first := authentication.UserRecord{ID: "owner-a", Username: "grant-user", PasswordHash: hash,
		Nickname: "A", Email: "a@example.test", Active: true}
	second := authentication.UserRecord{ID: "owner-b", Username: "other-user", PasswordHash: hash,
		Nickname: "B", Email: "b@example.test", Active: true}
	directory := &authDirectory{
		byUsername: map[string]authentication.UserRecord{first.Username: first, second.Username: second},
		byID:       map[string]authentication.UserRecord{first.ID: first, second.ID: second},
	}
	module, handler, inputs, _ := newHTTPHarnessWithCatalogOverride(t, []string{"user"}, customize,
		func(config *authentication.Config) { config.Directory = directory }, nil, nil, taskCatalog)
	login := func(username string) *http.Cookie {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"username":"`+username+`","password":"correct-horse"}`))
		request.Header.Set("Origin", "http://example.com")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
			t.Fatalf("login %s status=%d body=%s", username, response.Code, response.Body.String())
		}
		return response.Result().Cookies()[0]
	}
	world := pendingApprovalWorld{module: module, handler: handler, inputs: inputs, cookie: login("grant-user"), other: login("other-user")}
	created := grantTestRequest(t, handler, world.cookie, http.MethodPost, "/api/chat/sessions", `{"title":"approvals"}`, "")
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("session=%s err=%v", created.Body.String(), err)
	}
	world.sessionID = session.ID
	value, ok := fixtureExecutions.Load(module)
	if !ok {
		t.Fatal("missing fixture state")
	}
	world.state = value.(*fixtureExecutionState)
	return world
}

// detachedApproval creates an ended turn, a frozen text-to-3d operation with a
// pending Agent grant, and detaches it the way an elapsed consent wait does.
func (w pendingApprovalWorld) detachedApproval(t *testing.T, callID, prompt string, customize ...func(*generation.PrepareInputCommand)) (generationtool.Operation, executiongrant.Grant) {
	t.Helper()
	return w.detach(t, callID, prompt, false, true, customize...)
}

// detachedApprovalInRunningTurn is detachedApproval while the turn is still
// running: the card exists and the agent is streaming its closing text.
func (w pendingApprovalWorld) detachedApprovalInRunningTurn(t *testing.T, callID, prompt string) (generationtool.Operation, executiongrant.Grant) {
	t.Helper()
	return w.detach(t, callID, prompt, false, false)
}

// stoppedDuringClosingText is detachedApproval where the user presses Stop
// after the card exists and before the agent has finished its closing text.
func (w pendingApprovalWorld) stoppedDuringClosingText(t *testing.T, callID, prompt string) (generationtool.Operation, executiongrant.Grant) {
	t.Helper()
	return w.detach(t, callID, prompt, true, true)
}

func (w pendingApprovalWorld) detach(t *testing.T, callID, prompt string, stop, finish bool, customize ...func(*generation.PrepareInputCommand)) (generationtool.Operation, executiongrant.Grant) {
	t.Helper()
	ctx := context.Background()
	operation, grant := w.inTurnApproval(t, callID, prompt, customize...)
	operation, detached, err := w.state.repository.DetachAgentOperation(ctx, operation)
	if err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	if stop {
		if _, active, err := w.state.chat.RequestTurnStop(ctx, "owner-a", w.sessionID); err != nil || !active {
			t.Fatalf("stop: active=%v err=%v", active, err)
		}
	}
	if !finish {
		return operation, grant
	}
	// The turn then finishes with its answer; the approval outlives it.
	if err := w.state.chat.FinishTurn(ctx, operation.AssistantMessageID, false); err != nil {
		t.Fatal(err)
	}
	return operation, grant
}

// inTurnApproval starts a turn and gives it a frozen text-to-3d operation with
// a pending Agent grant that has not been detached: the agent is still waiting
// for the person inside its running turn.
func (w pendingApprovalWorld) inTurnApproval(t *testing.T, callID, prompt string, customize ...func(*generation.PrepareInputCommand)) (generationtool.Operation, executiongrant.Grant) {
	return w.inTurnApprovalWithOperation(t, callID, prompt, nil, customize...)
}

func (w pendingApprovalWorld) inTurnApprovalWithOperation(t *testing.T, callID, prompt string, changeOperation func(*generationtool.Operation), customize ...func(*generation.PrepareInputCommand)) (generationtool.Operation, executiongrant.Grant) {
	t.Helper()
	ctx := context.Background()
	turnResponse := grantTestRequest(t, w.handler, w.cookie, http.MethodPost,
		"/api/chat/sessions/"+w.sessionID+"/messages", `{"content":"make `+prompt+`"}`, "")
	if turnResponse.Code != http.StatusAccepted {
		t.Fatalf("start turn status=%d body=%s", turnResponse.Code, turnResponse.Body.String())
	}
	var turn struct {
		AssistantID string `json:"messageId"`
	}
	if err := json.Unmarshal(turnResponse.Body.Bytes(), &turn); err != nil || turn.AssistantID == "" {
		t.Fatalf("turn=%s err=%v", turnResponse.Body.String(), err)
	}
	prepare := generation.PrepareInputCommand{
		OwnerID: "owner-a", SessionID: w.sessionID, Capability: generation.TextTo3D,
		OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"` + prompt + `"}`),
	}
	for _, change := range customize {
		change(&prepare)
	}
	prepared, err := w.module.PrepareInput(ctx, prepare)
	if err != nil {
		t.Fatal(err)
	}
	repository := w.state.repository
	operationInput := generationtool.Operation{
		OwnerID: "owner-a", SessionID: w.sessionID, AssistantMessageID: turn.AssistantID,
		CallID: callID, ToolName: "create_model_3d", CommandHash: "hash-" + callID,
		IdempotencyKey: "agent-key-" + callID, PreparedInputID: prepared.ID,
		PreparedInputHash: prepared.InputHash, OfferingID: prepared.OfferingID,
		OfferingRevision: prepared.OfferingRevision,
	}
	if changeOperation != nil {
		changeOperation(&operationInput)
	}
	operation, err := repository.InsertAgentOperation(ctx, operationInput)
	if err != nil {
		t.Fatal(err)
	}
	grants := executiongrant.Service{Grants: repository, Prepared: repository}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: executiongrant.Principal{OwnerID: "owner-a", ActorID: "agent:" + turn.AssistantID, Source: executiongrant.AgentPending},
		SessionID: w.sessionID, OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: operation.IdempotencyKey, OperationID: operation.OperationID,
		TriggerTurnID: turn.AssistantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation, err = repository.AttachAgentGrantRequest(ctx, operation, grant.RequestID); err != nil {
		t.Fatal(err)
	}
	return operation, grant
}

func TestActiveConsentProjectsFrozenReferenceSource(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	asset, err := w.inputs.UploadAsset(context.Background(), "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(pngBytes.Bytes()), OriginalFilename: "picture.png", MediaType: "image/png", AssetType: "image", MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, _ := w.inTurnApprovalWithOperation(t, "call-reference", "chair", func(op *generationtool.Operation) {
		op.ToolName = "create_model_3d_from_image"
		op.ReferenceSource = generationtool.ReferenceSourceFact{Version: 1, Selected: 2, Resolved: 1, Frozen: 1, Deduped: true, Identity: "private-source-fact"}
	}, func(cmd *generation.PrepareInputCommand) {
		cmd.Capability = generation.ImageTo3D
		cmd.OfferingID = "meshy/meshy-7.1/image-to-3d"
		cmd.Input = json.RawMessage(`{"reference_assets":["` + string(asset.ID) + `"]}`)
	})
	prepared, err := w.module.GetPreparedInput(context.Background(), "owner-a", op.PreparedInputID)
	if err != nil || len(prepared.Assets) != 1 {
		t.Fatalf("prepared assets: %+v %v", prepared.Assets, err)
	}
	path := "/api/v1/agent/consent?scope=" + w.sessionID
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	localizer, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{}
	for _, locale := range []string{"", "en"} {
		response := w.getIn(t, w.cookie, path, locale)
		var body struct {
			Consent struct {
				Pending struct {
					ReferenceNote string `json:"reference_note"`
					Assets        []struct {
						Parameter string `json:"parameter"`
						AssetID   string `json:"asset_id"`
						Role      string `json:"role"`
						MediaType string `json:"media_type"`
					} `json:"reference_assets"`
				} `json:"consent_pending"`
			} `json:"consent"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Consent.Pending.Assets) != 1 || body.Consent.Pending.Assets[0].AssetID != string(prepared.Assets[0].ID) || body.Consent.Pending.Assets[0].Parameter != "reference_assets" || body.Consent.Pending.Assets[0].Role != "image" || body.Consent.Pending.Assets[0].MediaType != "image/png" || body.Consent.Pending.ReferenceNote == "" {
			t.Fatalf("%q active consent: %d %s", locale, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private-source-fact") || strings.Contains(response.Body.String(), "front_task_id") {
			t.Fatalf("private fact leaked: %s", response.Body.String())
		}
		want := strings.NewReplacer("{model}", "Meshy 7.1", "{count}", "1", "{total}", "2").Replace(localizer.Text(locale, application.Runtime.GenerationMessages.ReferenceSelectionNote))
		if body.Consent.Pending.ReferenceNote != want {
			t.Fatalf("%q note = %q, want %q", locale, body.Consent.Pending.ReferenceNote, want)
		}
		notes[locale] = want
	}
	foreign := w.getIn(t, w.other, path, "en")
	if foreign.Code == http.StatusOK || strings.Contains(foreign.Body.String(), string(prepared.Assets[0].ID)) {
		t.Fatalf("foreign consent: %d %s", foreign.Code, foreign.Body.String())
	}
	assetPath := "/api/v1/assets/" + string(prepared.Assets[0].ID)
	if owned := w.getIn(t, w.cookie, assetPath, ""); owned.Code != http.StatusOK {
		t.Fatalf("owner asset: %d", owned.Code)
	}
	if other := w.getIn(t, w.other, assetPath, ""); other.Code == http.StatusOK {
		t.Fatal("foreign owner read frozen asset")
	}
	if preview := w.getIn(t, w.cookie, assetPath+"?size=preview", ""); preview.Code != http.StatusOK || preview.Body.Len() == 0 {
		t.Fatalf("owner image preview: %d", preview.Code)
	}
	if other := w.getIn(t, w.other, assetPath+"?size=preview", ""); other.Code == http.StatusOK {
		t.Fatal("foreign owner read frozen image preview")
	}
	if _, detached, err := w.state.repository.DetachAgentOperation(context.Background(), op); err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	for _, locale := range []string{"", "en"} {
		status, items := w.pendingListIn(t, w.cookie, w.sessionID, locale)
		if status != http.StatusOK || len(items) != 1 || items[0]["reference_note"] != notes[locale] {
			t.Fatalf("%q detached: %d %+v", locale, status, items)
		}
		assets, ok := items[0]["inputs"].([]any)
		if !ok || len(assets) != 1 {
			t.Fatalf("detached inputs: %+v", items)
		}
		input := assets[0].(map[string]any)
		if input["asset_id"] != string(prepared.Assets[0].ID) || input["kind"] != "asset" || input["media_type"] != prepared.Assets[0].MediaType || input["handle"] != nil && input["handle"] != "" {
			t.Fatalf("detached input: %+v", input)
		}
	}
	legacyWorld := pendingApprovalBrowsers(t, nil)
	legacyAsset, err := legacyWorld.inputs.UploadAsset(context.Background(), "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(pngBytes.Bytes()), OriginalFilename: "picture.png", MediaType: "image/png", AssetType: "image", MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyWorld.inTurnApprovalWithOperation(t, "call-legacy-reference", "legacy", func(op *generationtool.Operation) {
		op.ToolName = "create_model_3d_from_image"
		op.ReferenceCount, op.ReferenceTotal = 1, 2
	}, func(cmd *generation.PrepareInputCommand) {
		cmd.Capability, cmd.OfferingID = generation.ImageTo3D, "meshy/meshy-7.1/image-to-3d"
		cmd.Input = json.RawMessage(`{"reference_assets":["` + string(legacyAsset.ID) + `"]}`)
	})
	legacy := legacyWorld.getIn(t, legacyWorld.cookie, "/api/v1/agent/consent?scope="+legacyWorld.sessionID, "en")
	if legacy.Code != http.StatusOK || strings.Contains(legacy.Body.String(), "reference_note") {
		t.Fatalf("legacy consent made a source claim: %d %s", legacy.Code, legacy.Body.String())
	}
	if w.taskCount(t) != 0 {
		t.Fatal("reading consent created a task")
	}
}

func TestTextureReferenceStaysFrozenImageInBothApprovalPaths(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	ctx := context.Background()
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	input, err := w.inputs.UploadAsset(ctx, "owner-a", generation.InputAssetUpload{Body: bytes.NewReader(picture.Bytes()), OriginalFilename: "texture.png", MediaType: "image/png", AssetType: "image", MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := submitHarnessTask(t, w.module, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: w.sessionID, Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"historical model"}`), IdempotencyKey: "historical-model"})
	if err != nil {
		t.Fatal(err)
	}
	task := w.runTasksToCompletion(t, string(accepted.TaskID))
	if task.State != generation.TaskSucceeded || task.SessionID != w.sessionID {
		t.Fatalf("historical task: %+v", task)
	}
	hasModel := false
	for _, asset := range task.Assets {
		if asset.Role == "model" && asset.MediaType == "model/gltf-binary" {
			hasModel = true
		}
	}
	if !hasModel {
		t.Fatalf("historical task has no model asset: %+v", task.Assets)
	}
	now := time.Now().UTC()
	attempt := generation.AttemptID("historical-texture-attempt")
	assets := []generation.Asset{
		{ID: "historical-texture", TaskID: task.ID, AttemptID: attempt, Role: "texture", MediaType: "image/png", StorageKey: input.StorageKey, Size: input.Size, SHA256: input.SHA256, CreatedAt: now},
		{ID: "historical-thumbnail", TaskID: task.ID, AttemptID: attempt, Role: "thumbnail", MediaType: "image/png", StorageKey: input.StorageKey, Size: input.Size, SHA256: input.SHA256, CreatedAt: now},
	}
	_, err = w.state.repository.ImportGenerationTasksAndAssets(ctx, []sqlite.GenerationTaskImport{{Task: task, Attempts: []sqlite.GenerationAttemptImport{{ID: attempt, Ordinal: 2, Offering: task.Offering, State: generation.AttemptUpstreamSucceeded, SubmissionKey: "historical-texture", StartedAt: now, UpdatedAt: now}}}}, assets)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.state.chat.PostAssistantMessage(ctx, "owner-a", chat.Message{ID: "historical-model-message", SessionID: w.sessionID, Content: "historical model", Attachments: []chat.Attachment{{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: string(task.ID)}}}); err != nil {
		t.Fatal(err)
	}
	op, _ := w.inTurnApprovalWithOperation(t, "texture-reference", "image", func(op *generationtool.Operation) { op.ToolName = "create_model_3d_from_image" }, func(cmd *generation.PrepareInputCommand) {
		cmd.Capability, cmd.OfferingID = generation.ImageTo3D, "meshy/meshy-7.1/image-to-3d"
		cmd.Input = json.RawMessage(`{"reference_assets":["` + string(input.ID) + `"]}`)
	})
	active := w.getIn(t, w.cookie, "/api/v1/agent/consent?scope="+w.sessionID, "")
	var body struct {
		Consent struct {
			Pending struct {
				Assets []struct {
					AssetID   string `json:"asset_id"`
					MediaType string `json:"media_type"`
				} `json:"reference_assets"`
			} `json:"consent_pending"`
		} `json:"consent"`
	}
	if active.Code != http.StatusOK || json.Unmarshal(active.Body.Bytes(), &body) != nil || len(body.Consent.Pending.Assets) != 1 || body.Consent.Pending.Assets[0].AssetID != string(input.ID) || body.Consent.Pending.Assets[0].MediaType != "image/png" {
		t.Fatalf("active: %d %s", active.Code, active.Body.String())
	}
	if foreign := w.getIn(t, w.other, "/api/v1/agent/consent?scope="+w.sessionID, ""); foreign.Code == http.StatusOK {
		t.Fatal("foreign active consent")
	}
	if _, detached, err := w.state.repository.DetachAgentOperation(ctx, op); err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	status, items := w.pendingList(t, w.cookie, w.sessionID)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("pending: %d %+v", status, items)
	}
	inputs, ok := items[0]["inputs"].([]any)
	if !ok || len(inputs) != 1 {
		t.Fatalf("inputs: %+v", items)
	}
	asset := inputs[0].(map[string]any)
	if asset["kind"] != "asset" || asset["asset_id"] != string(input.ID) || asset["media_type"] != "image/png" || asset["task_id"] != nil || asset["handle"] != nil {
		t.Fatalf("detached reference is not frozen image: %+v", asset)
	}
	if foreign, _ := w.pendingList(t, w.other, w.sessionID); foreign == http.StatusOK {
		t.Fatal("foreign detached consent")
	}
	path := "/api/v1/assets/" + string(input.ID) + "?size=preview"
	if owner := w.getIn(t, w.cookie, path, ""); owner.Code != http.StatusOK || owner.Body.Len() == 0 {
		t.Fatalf("owner preview: %d", owner.Code)
	}
	if other := w.getIn(t, w.other, path, ""); other.Code == http.StatusOK {
		t.Fatal("foreign image preview")
	}
}

func TestDetachedApprovalProjectsFrozenUploadedModelMediaType(t *testing.T) {
	w := pendingApprovalBrowsers(t, nil)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	profile := product.Application().Runtime.InputAssets.ModelProfile
	if profile == nil {
		t.Fatal("missing uploaded-model profile")
	}
	data := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
	asset, err := w.inputs.UploadAsset(context.Background(), "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(data), OriginalFilename: "source.glb", MediaType: profile.UploadMediaType,
		AssetType: profile.AssetType, MaxBytes: int64(len(data)) + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, _ := w.inTurnApprovalWithOperation(t, "call-uploaded-model", "remesh", func(op *generationtool.Operation) {
		op.ToolName = "process_model"
	}, func(cmd *generation.PrepareInputCommand) {
		cmd.Capability, cmd.OfferingID = generation.Remesh, "meshy/processing/remesh"
		cmd.Input = json.RawMessage(`{"source_asset":"` + string(asset.ID) + `"}`)
	})
	if _, detached, err := w.state.repository.DetachAgentOperation(context.Background(), op); err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	if err := w.state.chat.FinishTurn(context.Background(), op.AssistantMessageID, false); err != nil {
		t.Fatal(err)
	}
	prepared, err := w.module.GetPreparedInput(context.Background(), "owner-a", op.PreparedInputID)
	if err != nil || len(prepared.Assets) != 1 || prepared.Assets[0].MediaType != profile.UploadMediaType {
		t.Fatalf("prepared model: %+v, %v", prepared.Assets, err)
	}
	response := w.getIn(t, w.cookie, "/api/v1/chat/sessions/"+w.sessionID+"/pending-approvals", "")
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Items) != 1 {
		t.Fatalf("pending: %d %s", response.Code, response.Body.String())
	}
	t.Logf("DETACHED_HTTP_RESPONSE=%s", strings.TrimSpace(response.Body.String()))
	inputs, ok := body.Items[0]["inputs"].([]any)
	if !ok || len(inputs) != 1 {
		t.Fatalf("inputs: %+v", body.Items)
	}
	input, ok := inputs[0].(map[string]any)
	if !ok || input["kind"] != "asset" || input["parameter"] != "source_asset" || input["asset_id"] != string(prepared.Assets[0].ID) || input["media_type"] != prepared.Assets[0].MediaType || input["handle"] != nil || input["task_id"] != nil {
		t.Fatalf("detached frozen model input: %+v", inputs[0])
	}
	if _, leaked := input["sha256"]; leaked {
		t.Fatal("input digest leaked")
	}
	if foreign, _ := w.pendingList(t, w.other, w.sessionID); foreign == http.StatusOK {
		t.Fatal("foreign owner read pending model")
	}
	if w.taskCount(t) != 0 {
		t.Fatal("reading pending approval submitted a task")
	}
}

func TestDetachedApprovalModelIdentityUsesFrozenAssetID(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact_task_asset=%t", exact), func(t *testing.T) {
			w := pendingApprovalBrowsers(t, nil)
			ctx := context.Background()
			product, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			profile := product.Application().Runtime.InputAssets.ModelProfile
			if profile == nil {
				t.Fatal("missing uploaded-model profile")
			}
			data := modelinputtest.Triangle(modelinputtest.Options{Indexed: true})
			upload, err := w.inputs.UploadAsset(ctx, "owner-a", generation.InputAssetUpload{Body: bytes.NewReader(data), OriginalFilename: "source.glb", MediaType: profile.UploadMediaType, AssetType: profile.AssetType, MaxBytes: int64(len(data)) + 1})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := submitHarnessTask(t, w.module, ctx, generation.SubmitCommand{OwnerID: "owner-a", SessionID: w.sessionID, Capability: generation.TextTo3D, OfferingID: "meshy/meshy-7.1/text-to-3d", Input: json.RawMessage(`{"prompt":"historical model"}`), IdempotencyKey: "identity-model"})
			if err != nil {
				t.Fatal(err)
			}
			task := w.runTasksToCompletion(t, string(accepted.TaskID))
			if task.State != generation.TaskSucceeded || task.SessionID != w.sessionID {
				t.Fatalf("historical task: %+v", task)
			}
			now := time.Now().UTC()
			attempt := generation.AttemptID("identity-model-attempt")
			model := generation.Asset{ID: "identity-task-model", TaskID: task.ID, AttemptID: attempt, Role: "model", MediaType: upload.MediaType, StorageKey: upload.StorageKey, Size: upload.Size, SHA256: upload.SHA256, CreatedAt: now}
			_, err = w.state.repository.ImportGenerationTasksAndAssets(ctx, []sqlite.GenerationTaskImport{{Task: task, Attempts: []sqlite.GenerationAttemptImport{{ID: attempt, Ordinal: 2, Offering: task.Offering, State: generation.AttemptUpstreamSucceeded, SubmissionKey: "identity-model", StartedAt: now, UpdatedAt: now}}}}, []generation.Asset{model})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := w.state.chat.PostAssistantMessage(ctx, "owner-a", chat.Message{ID: "identity-model-message", SessionID: w.sessionID, Content: "historical model", Attachments: []chat.Attachment{{Handle: "@model1", Kind: chat.AttachmentKindModel, TaskID: string(task.ID), AssetID: string(model.ID)}}}); err != nil {
				t.Fatal(err)
			}
			source := upload.ID
			if exact {
				source = model.ID
			}
			op, _ := w.inTurnApprovalWithOperation(t, "identity-approval", "remesh", func(op *generationtool.Operation) { op.ToolName = "process_model" }, func(cmd *generation.PrepareInputCommand) {
				cmd.Capability, cmd.OfferingID = generation.Remesh, "meshy/processing/remesh"
				cmd.Input = json.RawMessage(`{"source_asset":"` + string(source) + `"}`)
			})
			prepared, err := w.module.GetPreparedInput(ctx, "owner-a", op.PreparedInputID)
			if err != nil || len(prepared.Assets) != 1 || prepared.Assets[0].ID != source || prepared.Assets[0].SHA256 != model.SHA256 || upload.ID == model.ID {
				t.Fatalf("frozen input identity: %+v err=%v", prepared.Assets, err)
			}
			if _, detached, err := w.state.repository.DetachAgentOperation(ctx, op); err != nil || !detached {
				t.Fatalf("detach: %v %v", detached, err)
			}
			status, items := w.pendingList(t, w.cookie, w.sessionID)
			if status != http.StatusOK || len(items) != 1 {
				t.Fatalf("pending: %d %+v", status, items)
			}
			inputs, ok := items[0]["inputs"].([]any)
			if !ok || len(inputs) != 1 {
				t.Fatalf("inputs: %+v", items)
			}
			input := inputs[0].(map[string]any)
			if exact {
				if input["kind"] != "task" || input["task_id"] != string(task.ID) || input["handle"] != "@model1" {
					t.Fatalf("task source: %+v", input)
				}
				if preview, ok := input["asset_id"].(string); ok && preview != "" {
					valid := false
					for _, asset := range task.Assets {
						valid = valid || asset.Role == "thumbnail" && string(asset.ID) == preview
					}
					if !valid {
						t.Fatalf("preview is not from the source task: %+v", input)
					}
				}
			} else if input["kind"] != "asset" || input["asset_id"] != string(upload.ID) || input["media_type"] != upload.MediaType || input["task_id"] != nil || input["handle"] != nil {
				t.Fatalf("independent upload: %+v", input)
			}
			if foreign, _ := w.pendingList(t, w.other, w.sessionID); foreign == http.StatusOK {
				t.Fatal("foreign owner read pending model")
			}
		})
	}
}

func (w pendingApprovalWorld) pendingList(t *testing.T, cookie *http.Cookie, sessionID string) (int, []map[string]any) {
	t.Helper()
	return w.pendingListIn(t, cookie, sessionID, "")
}

func (w pendingApprovalWorld) pendingListIn(t *testing.T, cookie *http.Cookie, sessionID, locale string) (int, []map[string]any) {
	t.Helper()
	response := w.getIn(t, cookie, "/api/v1/chat/sessions/"+sessionID+"/pending-approvals", locale)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("list body=%s err=%v", response.Body.String(), err)
		}
	}
	return response.Code, body.Items
}

// getIn is a GET in a UI locale ("" is the default one).
func (w pendingApprovalWorld) getIn(t *testing.T, cookie *http.Cookie, path, locale string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Origin", "http://example.com")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if locale != "" {
		request.Header.Set("Accept-Language", locale)
	}
	response := httptest.NewRecorder()
	w.handler.ServeHTTP(response, request)
	return response
}

func (w pendingApprovalWorld) decide(t *testing.T, cookie *http.Cookie, requestID, decision string) *httptest.ResponseRecorder {
	t.Helper()
	return grantTestRequest(t, w.handler, cookie, http.MethodPut, "/api/v1/tasks/grants/"+requestID,
		`{"decision":"`+decision+`"}`, "")
}

func (w pendingApprovalWorld) messages(t *testing.T) []chat.Message {
	t.Helper()
	messages, err := w.state.chat.ListMessages(context.Background(), "owner-a", w.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

func (w pendingApprovalWorld) messageByID(t *testing.T, id string) (chat.Message, bool) {
	t.Helper()
	for _, message := range w.messages(t) {
		if message.ID == id {
			return message, true
		}
	}
	return chat.Message{}, false
}

func (w pendingApprovalWorld) taskCount(t *testing.T) int {
	t.Helper()
	tasks, err := w.module.List(context.Background(), generation.OwnTasks("owner-a"),
		generation.ListQuery{SessionID: w.sessionID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(tasks.Tasks)
}

func (w pendingApprovalWorld) runTasksToCompletion(t *testing.T, taskID string) generation.Task {
	t.Helper()
	for range 20 {
		if _, err := w.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID: "pending-approvals", LeaseFor: time.Minute, BatchSize: 10,
		}); err != nil {
			t.Fatal(err)
		}
		task, err := w.module.Get(context.Background(), "owner-a", generation.TaskID(taskID))
		if err != nil {
			t.Fatal(err)
		}
		if task.State.Terminal() {
			return task
		}
		time.Sleep(12 * time.Millisecond)
	}
	t.Fatal("task did not finish")
	return generation.Task{}
}

func TestPendingApprovalSurvivesItsTurnAndRunsExactlyOnce(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApproval(t, "call-1", "an oak chair")

	status, items := world.pendingList(t, world.cookie, world.sessionID)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("list status=%d items=%v", status, items)
	}
	item := items[0]
	if item["request_id"] != grant.RequestID || item["status"] != "pending" || item["capability"] != "text-to-3d" ||
		item["prompt"] != "an oak chair" || item["tool"] != "create_model_3d" || item["model"] == "" ||
		item["created_at"] == nil || item["expires_at"] == nil || item["estimated_credits"] == nil {
		t.Fatalf("projection = %v", item)
	}
	created, _ := time.Parse(time.RFC3339Nano, item["created_at"].(string))
	expires, _ := time.Parse(time.RFC3339Nano, item["expires_at"].(string))
	if expires.Sub(created) != 24*time.Hour {
		t.Fatalf("approval must stay open for the Schema's day, got %v", expires.Sub(created))
	}
	if world.taskCount(t) != 0 {
		t.Fatal("a pending approval must not have created a task")
	}

	approved := world.decide(t, world.cookie, grant.RequestID, "approve")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var first struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
		TaskID    string `json:"task_id"`
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &first); err != nil || first.TaskID == "" || first.MessageID == "" ||
		first.Status != "bound" && first.Status != "approved" {
		t.Fatalf("approve body=%s err=%v", approved.Body.String(), err)
	}
	// A double approve (lost response, double click) returns the same task.
	again := world.decide(t, world.cookie, grant.RequestID, "approve")
	var second struct {
		TaskID    string `json:"task_id"`
		MessageID string `json:"message_id"`
	}
	if again.Code != http.StatusOK || json.Unmarshal(again.Body.Bytes(), &second) != nil ||
		second.TaskID != first.TaskID || second.MessageID != first.MessageID {
		t.Fatalf("double approve status=%d body=%s", again.Code, again.Body.String())
	}
	if world.taskCount(t) != 1 {
		t.Fatalf("double approve created %d tasks", world.taskCount(t))
	}
	if _, items := world.pendingList(t, world.cookie, world.sessionID); len(items) != 0 {
		t.Fatalf("a decided approval is still listed: %v", items)
	}
	started, ok := world.messageByID(t, first.MessageID)
	if !ok || started.Role != "assistant" || len(started.ToolCalls) != 1 || started.ToolCalls[0].TaskID != first.TaskID ||
		started.ToolCalls[0].ApprovalSource != "human" || started.ToolCalls[0].Name != "create_model_3d" {
		t.Fatalf("started message = %+v ok=%v", started, ok)
	}

	// The task finishes later; the background sweep turns the same message into
	// the result with its picture and model handle.
	task := world.runTasksToCompletion(t, first.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %+v", task)
	}
	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, _ := world.messageByID(t, first.MessageID)
	product, _ := catalog.LoadDefault()
	if result.Content == started.Content || !strings.Contains(result.Content, "生成完成") ||
		len(result.ToolCalls) != 1 || result.ToolCalls[0].TaskID != first.TaskID {
		t.Fatalf("result message = %+v (schema %q)", result, product.Application().Chat.PendingApprovals.Messages.Succeeded)
	}
	// Settled: another sweep and another approve change nothing.
	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range world.messages(t) {
		if message.ID == first.MessageID {
			count++
		}
	}
	if count != 1 || world.taskCount(t) != 1 {
		t.Fatalf("result posted %d times with %d tasks", count, world.taskCount(t))
	}
}

func TestPendingApprovalIsApprovableThroughTheConsentEndpoint(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApproval(t, "call-consent", "a pine table")
	path := "/api/v1/agent/consent?scope=" + world.sessionID
	// The in-turn card has nothing to show: the turn is over.
	get := grantTestRequest(t, world.handler, world.cookie, http.MethodGet, path, "", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"consent":{}`) {
		t.Fatalf("consent projection of an ended turn: %d %s", get.Code, get.Body.String())
	}
	wrongScope := grantTestRequest(t, world.handler, world.cookie, http.MethodPut,
		"/api/v1/agent/consent?scope=another-session", `{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if wrongScope.Code == http.StatusOK {
		t.Fatalf("decision accepted for another scope: %s", wrongScope.Body.String())
	}
	put := grantTestRequest(t, world.handler, world.cookie, http.MethodPut, path,
		`{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"task_id"`) ||
		!strings.Contains(put.Body.String(), `"approved":true`) {
		t.Fatalf("consent approve status=%d body=%s", put.Code, put.Body.String())
	}
	if world.taskCount(t) != 1 {
		t.Fatalf("tasks = %d", world.taskCount(t))
	}
}

func TestPendingApprovalDenialMarksGrantDeniedAndPostsAMessage(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApproval(t, "call-deny", "a granite bench")
	denied := world.decide(t, world.cookie, grant.RequestID, "deny")
	var body struct {
		Status    string `json:"status"`
		MessageID string `json:"message_id"`
	}
	if denied.Code != http.StatusOK || json.Unmarshal(denied.Body.Bytes(), &body) != nil ||
		body.Status != "denied" || body.MessageID == "" {
		t.Fatalf("deny status=%d body=%s", denied.Code, denied.Body.String())
	}
	message, ok := world.messageByID(t, body.MessageID)
	product, _ := catalog.LoadDefault()
	if !ok || message.Content != product.Application().Chat.PendingApprovals.Messages.Denied ||
		message.Role != "assistant" || len(message.ImageURLs) != 0 {
		t.Fatalf("denial message = %+v ok=%v", message, ok)
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Denied {
		t.Fatalf("grant = %+v err=%v", stored, err)
	}
	// A denial cannot be turned into an approval afterwards, and nothing ran.
	if late := world.decide(t, world.cookie, grant.RequestID, "approve"); late.Code != http.StatusConflict {
		t.Fatalf("approve after deny status=%d body=%s", late.Code, late.Body.String())
	}
	if world.taskCount(t) != 0 {
		t.Fatal("a denied approval created a task")
	}
	if _, items := world.pendingList(t, world.cookie, world.sessionID); len(items) != 0 {
		t.Fatalf("denied approval still listed: %v", items)
	}
}

func TestPendingApprovalExpiresAndCannotBeApproved(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, func(application *catalog.ApplicationSchema) {
		application.Chat.PendingApprovals.TTLSeconds = 1
	})
	_, grant := world.detachedApproval(t, "call-expire", "a short lived chair")
	time.Sleep(1200 * time.Millisecond)

	if _, items := world.pendingList(t, world.cookie, world.sessionID); len(items) != 0 {
		t.Fatalf("an expired approval must leave the pending list: %v", items)
	}
	late := world.decide(t, world.cookie, grant.RequestID, "approve")
	if late.Code != http.StatusConflict || !strings.Contains(late.Body.String(), "pending_approval_expired") ||
		!strings.Contains(late.Body.String(), "已过期") {
		t.Fatalf("expired approve status=%d body=%s", late.Code, late.Body.String())
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("an expired approval must not change state: %+v %v", stored, err)
	}
	if world.taskCount(t) != 0 {
		t.Fatal("an expired approval created a task")
	}
	// The consent endpoint refuses it the same way.
	put := grantTestRequest(t, world.handler, world.cookie, http.MethodPut, "/api/v1/agent/consent?scope="+world.sessionID,
		`{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if put.Code != http.StatusConflict || !strings.Contains(put.Body.String(), "pending_approval_expired") {
		t.Fatalf("expired consent status=%d body=%s", put.Code, put.Body.String())
	}
	// There is no resubmit shortcut: no route, no state change, no new task.
	if deny := world.decide(t, world.cookie, grant.RequestID, "deny"); deny.Code != http.StatusOK {
		t.Fatalf("an expired approval can still be dismissed: %d %s", deny.Code, deny.Body.String())
	}
}

func TestPendingApprovalsAreOwnerAndSessionScoped(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApproval(t, "call-scope", "a private chair")

	if status, _ := world.pendingList(t, world.other, world.sessionID); status != http.StatusNotFound {
		t.Fatalf("another owner's list status=%d", status)
	}
	if status, _ := world.pendingList(t, nil, world.sessionID); status == http.StatusOK {
		t.Fatal("anonymous list succeeded")
	}
	for _, decision := range []string{"approve", "deny"} {
		if foreign := world.decide(t, world.other, grant.RequestID, decision); foreign.Code != http.StatusNotFound {
			t.Fatalf("another owner's %s status=%d body=%s", decision, foreign.Code, foreign.Body.String())
		}
	}
	foreignConsent := grantTestRequest(t, world.handler, world.other, http.MethodPut, "/api/v1/agent/consent?scope="+world.sessionID,
		`{"request_id":"`+grant.RequestID+`","approved":true}`, "")
	if foreignConsent.Code == http.StatusOK {
		t.Fatalf("another owner's consent accepted: %s", foreignConsent.Body.String())
	}
	// Credentials alone (no browser session) can never approve.
	if noCookie := world.decide(t, nil, grant.RequestID, "approve"); noCookie.Code != http.StatusForbidden {
		t.Fatalf("cookie-less approve status=%d", noCookie.Code)
	}
	// The owner's second session does not show the first session's approval.
	other := grantTestRequest(t, world.handler, world.cookie, http.MethodPost, "/api/chat/sessions", `{"title":"second"}`, "")
	var second struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(other.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if status, items := world.pendingList(t, world.cookie, second.ID); status != http.StatusOK || len(items) != 0 {
		t.Fatalf("second session list status=%d items=%v", status, items)
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Pending || world.taskCount(t) != 0 {
		t.Fatalf("foreign attempts changed state: %+v %v", stored, err)
	}
}

func TestSwitchingApprovalModeNeverApprovesAPendingApproval(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApproval(t, "call-mode", "a mode chair")
	switched := grantTestRequest(t, world.handler, world.cookie, http.MethodPut,
		"/api/v1/chat/sessions/"+world.sessionID+"/approval", `{"approvalMode":"auto"}`, "")
	if switched.Code != http.StatusOK {
		t.Fatalf("switch mode status=%d body=%s", switched.Code, switched.Body.String())
	}
	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("switching to auto approved the pending grant: %+v %v", stored, err)
	}
	if _, items := world.pendingList(t, world.cookie, world.sessionID); len(items) != 1 || world.taskCount(t) != 0 {
		t.Fatalf("auto mode changed the pending approval: %v tasks=%d", items, world.taskCount(t))
	}
}

func TestDecidedPendingApprovalIsFinishedByTheSweepAfterACrash(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	operation, grant := world.detachedApproval(t, "call-crash", "a crash chair")
	// The decision committed but the process died before it was submitted.
	service := executiongrant.Service{Grants: world.state.repository, Prepared: world.state.repository}
	if _, err := service.Decide(context.Background(),
		executiongrant.Principal{OwnerID: "owner-a", ActorID: "owner-a", Source: executiongrant.HumanBrowser},
		grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	if world.taskCount(t) != 0 {
		t.Fatal("nothing should be submitted yet")
	}
	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if world.taskCount(t) != 1 {
		t.Fatalf("the sweep must submit the decided approval once, tasks=%d", world.taskCount(t))
	}
	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if world.taskCount(t) != 1 {
		t.Fatalf("a second sweep submitted again, tasks=%d", world.taskCount(t))
	}
	posted, ok := world.messageByID(t, "pa_"+operation.GrantRequestID)
	if !ok || len(posted.ToolCalls) != 1 || posted.ToolCalls[0].TaskID == "" {
		t.Fatalf("the swept approval did not post its message: %+v ok=%v", posted, ok)
	}
}

// Defect 1, end to end through the real kernel: Stop while the agent streams its
// closing text must not make the card unusable; approving later submits once.
func TestStopDuringTheClosingTextKeepsThePendingApprovalApprovable(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.stoppedDuringClosingText(t, "call-stop", "a stopped chair")

	status, items := world.pendingList(t, world.cookie, world.sessionID)
	if status != http.StatusOK || len(items) != 1 || items[0]["request_id"] != grant.RequestID || items[0]["status"] != "pending" {
		t.Fatalf("a stopped turn's detached card must stay listed: status=%d items=%v", status, items)
	}
	approved := world.decide(t, world.cookie, grant.RequestID, "approve")
	var first struct {
		TaskID    string `json:"task_id"`
		MessageID string `json:"message_id"`
	}
	if approved.Code != http.StatusOK || json.Unmarshal(approved.Body.Bytes(), &first) != nil || first.TaskID == "" || first.MessageID == "" {
		t.Fatalf("approve after stop status=%d body=%s", approved.Code, approved.Body.String())
	}
	again := world.decide(t, world.cookie, grant.RequestID, "approve")
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), first.TaskID) {
		t.Fatalf("second approve status=%d body=%s", again.Code, again.Body.String())
	}
	if world.taskCount(t) != 1 {
		t.Fatalf("tasks = %d, want exactly one", world.taskCount(t))
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Bound {
		t.Fatalf("grant = %+v err=%v", stored, err)
	}
}

// Any rejection reaches the card as a server message, never a bare failure.
func TestARejectedApprovalAnswersWithAServerMessage(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApproval(t, "call-reject", "a rejected chair")
	if denied := world.decide(t, world.cookie, grant.RequestID, "deny"); denied.Code != http.StatusOK {
		t.Fatalf("deny status=%d", denied.Code)
	}
	late := world.decide(t, world.cookie, grant.RequestID, "approve")
	var body struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if late.Code != http.StatusConflict || json.Unmarshal(late.Body.Bytes(), &body) != nil || body.Detail == "" {
		t.Fatalf("rejected approve status=%d body=%s", late.Code, late.Body.String())
	}
}

// Defect 2, through the sweep: the expired approval is announced once in the chat.
func TestExpiredApprovalIsAnnouncedOnceBySweep(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, func(application *catalog.ApplicationSchema) {
		application.Chat.PendingApprovals.TTLSeconds = 1
	})
	_, grant := world.detachedApproval(t, "call-announce", "an announced chair")
	time.Sleep(1200 * time.Millisecond)
	for range 2 {
		if _, err := world.state.pending.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	message, ok := world.messageByID(t, "pa_"+grant.RequestID)
	product, _ := catalog.LoadDefault()
	if !ok || message.Content != product.Application().Chat.PendingApprovals.Messages.Expired {
		t.Fatalf("expiry message = %+v ok=%v", message, ok)
	}
	if _, items := world.pendingList(t, world.cookie, world.sessionID); len(items) != 0 {
		t.Fatalf("listed after expiry: %v", items)
	}
}

// A detached card is shown by the pending-approvals list only. While the turn
// still streams its closing text, the in-turn consent endpoint must not project
// the same request a second time.
func TestDetachedApprovalIsNotProjectedAsAnInTurnConsent(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	_, grant := world.detachedApprovalInRunningTurn(t, "call-running", "a chair still being described")

	response := grantTestRequest(t, world.handler, world.cookie, http.MethodGet,
		"/api/v1/agent/consent?scope="+world.sessionID, "", "")
	var body struct {
		Consent map[string]any `json:"consent"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("consent status=%d body=%s", response.Code, response.Body.String())
	}
	if len(body.Consent) != 0 || strings.Contains(response.Body.String(), grant.RequestID) {
		t.Fatalf("a detached card was projected as an in-turn consent: %s", response.Body.String())
	}
	status, items := world.pendingList(t, world.cookie, world.sessionID)
	if status != http.StatusOK || len(items) != 1 || items[0]["request_id"] != grant.RequestID {
		t.Fatalf("the card must be listed once, as pending: status=%d items=%v", status, items)
	}
}

func (w pendingApprovalWorld) consent(t *testing.T, requestID string, approved bool) *httptest.ResponseRecorder {
	t.Helper()
	return grantTestRequest(t, w.handler, w.cookie, http.MethodPut, "/api/v1/agent/consent?scope="+w.sessionID,
		fmt.Sprintf(`{"request_id":%q,"approved":%t}`, requestID, approved), "")
}

// F1: the user presses Approve and Stop lands after the handler saw a live turn
// but before the decision is written. The approval must not be recorded (the
// grant could never be claimed and the user would have been told 200); the
// sweep then ends the card with the cancellation message, and nothing is
// submitted or charged.
func TestApproveRacingAStopIsRefusedAndTheCardIsCancelled(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	operation, grant := world.inTurnApproval(t, "call-race", "a raced chair")
	stops := 0
	world.state.beforeDecide = func(string, executiongrant.Decision) {
		stops++
		if _, active, err := world.state.chat.RequestTurnStop(context.Background(), "owner-a", world.sessionID); err != nil || !active {
			t.Errorf("stop: active=%v err=%v", active, err)
		}
	}
	response := world.consent(t, grant.RequestID, true)
	if stops != 1 {
		t.Fatalf("the Stop was injected %d times, want once", stops)
	}
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "consent_not_pending") {
		t.Fatalf("approve racing a Stop status=%d body=%s, want 409 consent_not_pending", response.Code, response.Body.String())
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || stored.Status != executiongrant.Pending {
		t.Fatalf("grant = %+v err=%v, want still pending", stored, err)
	}
	world.state.beforeDecide = nil

	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err = world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || !stored.CancelledByStop() {
		t.Fatalf("grant after sweep = %+v err=%v, want cancelled by the system", stored, err)
	}
	if message, ok := world.messageByID(t, pendingapproval.MessageID(operation.GrantRequestID)); !ok || message.Content == "" {
		t.Fatalf("no cancellation message: %+v ok=%v", message, ok)
	}
	if world.taskCount(t) != 0 {
		t.Fatalf("tasks = %d, want none", world.taskCount(t))
	}
}

// F1: the approval committed first and the Stop landed right after, before the
// turn claimed the grant: the sweep cancels the approved-but-unclaimable grant
// and tells the user, instead of leaving a silent dead operation.
func TestApprovedThenStoppedUndetachedGrantIsCancelledBySweep(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	operation, grant := world.inTurnApproval(t, "call-approved-stop", "an approved then stopped chair")
	if response := world.consent(t, grant.RequestID, true); response.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", response.Code, response.Body.String())
	}
	if _, active, err := world.state.chat.RequestTurnStop(context.Background(), "owner-a", world.sessionID); err != nil || !active {
		t.Fatalf("stop: active=%v err=%v", active, err)
	}
	if _, err := world.state.pending.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := world.state.repository.GetExecutionGrant(context.Background(), "owner-a", grant.RequestID)
	if err != nil || !stored.CancelledByStop() {
		t.Fatalf("grant after sweep = %+v err=%v, want cancelled by the system", stored, err)
	}
	if _, ok := world.messageByID(t, pendingapproval.MessageID(operation.GrantRequestID)); !ok {
		t.Fatal("the user was not told the approval was cancelled")
	}
	if world.taskCount(t) != 0 {
		t.Fatalf("tasks = %d, want none", world.taskCount(t))
	}
}

const (
	chineseAdjustmentAdvice = "默认值优先保证生成质量和完整交付，不建议更改。Agent 可按需求说明理由并调整合法取值；必需格式、参数组合和交付要求仍须满足。"
	englishAdjustmentAdvice = "The default prioritizes generation quality and complete delivery, so changing it is not recommended. The Agent may adjust it to any valid value when needed and explain why; required formats, parameter combinations and delivery requirements must still be met."
)

func TestActiveConsentProjectsFrozenParameterAdjustments(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	op, _ := world.inTurnApproval(t, "call-advised", "a draft", func(cmd *generation.PrepareInputCommand) {
		cmd.Input = json.RawMessage(`{"prompt":"a draft","geometry_resolution":"standard"}`)
		cmd.ParameterReasons = map[string]string{"geometry_resolution": "几何分辨率"}
	})
	path := "/api/v1/agent/consent?scope=" + world.sessionID
	read := func(locale string) generation.ParameterAdjustment {
		get := world.getIn(t, world.cookie, path, locale)
		var body struct {
			Consent struct {
				Pending struct {
					Adjustments []generation.ParameterAdjustment `json:"parameter_adjustments"`
				} `json:"consent_pending"`
			} `json:"consent"`
		}
		if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &body) != nil || len(body.Consent.Pending.Adjustments) != 1 {
			t.Fatalf("%q active consent omitted frozen adjustments: %d %s", locale, get.Code, get.Body.String())
		}
		return body.Consent.Pending.Adjustments[0]
	}
	check := func(locale, label, advice string) generation.ParameterAdjustment {
		item := read(locale)
		if item.Name != "geometry_resolution" || item.Label != label || item.Advice != advice ||
			item.Reason != "几何分辨率" || item.Source != "agent" ||
			string(item.Value) != `"standard"` || string(item.RecommendedValue) != `"4k"` {
			t.Fatalf("%q adjustment changed: %+v", locale, item)
		}
		return item
	}
	check("en", "Geometry resolution", englishAdjustmentAdvice)
	check("", "几何分辨率", chineseAdjustmentAdvice)
	item := check("en", "Geometry resolution", englishAdjustmentAdvice)
	stored, err := world.module.GetPreparedInput(context.Background(), "owner-a", op.PreparedInputID)
	if err != nil || len(stored.ParameterAdjustments) != 1 {
		t.Fatalf("frozen input: %+v %v", stored, err)
	}
	frozen := stored.ParameterAdjustments[0]
	if frozen.Label != "几何分辨率" || frozen.Advice != chineseAdjustmentAdvice ||
		frozen.Reason != "几何分辨率" || string(frozen.Value) != `"standard"` ||
		string(frozen.RecommendedValue) != `"4k"` {
		t.Fatalf("projecting adjustments changed the frozen input: %+v", frozen)
	}
	foreign := grantTestRequest(t, world.handler, world.other, http.MethodGet, path, "", "")
	if foreign.Code == http.StatusOK || strings.Contains(foreign.Body.String(), item.Reason) {
		t.Fatal("foreign user read adjustment reason")
	}
}

func TestPendingApprovalsProjectFrozenParameterAdjustments(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsers(t, nil)
	op, _ := world.detachedApproval(t, "call-detached-advised", "a draft", func(cmd *generation.PrepareInputCommand) {
		cmd.Input = json.RawMessage(`{"prompt":"a draft","geometry_resolution":"standard"}`)
		cmd.ParameterReasons = map[string]string{"geometry_resolution": "几何分辨率"}
	})
	read := func(locale string) generation.ParameterAdjustment {
		status, items := world.pendingListIn(t, world.cookie, world.sessionID, locale)
		if status != http.StatusOK || len(items) != 1 {
			t.Fatalf("%q list status=%d items=%v", locale, status, items)
		}
		raw, _ := json.Marshal(items[0]["parameter_adjustments"])
		var adjustments []generation.ParameterAdjustment
		if err := json.Unmarshal(raw, &adjustments); err != nil || len(adjustments) != 1 {
			t.Fatalf("%q adjustments=%s err=%v", locale, raw, err)
		}
		return adjustments[0]
	}
	check := func(locale, label, advice string) {
		item := read(locale)
		if item.Name != "geometry_resolution" || item.Label != label || item.Advice != advice ||
			item.Reason != "几何分辨率" || item.Source != "agent" ||
			string(item.Value) != `"standard"` || string(item.RecommendedValue) != `"4k"` {
			t.Fatalf("%q adjustment changed: %+v", locale, item)
		}
	}
	check("en", "Geometry resolution", englishAdjustmentAdvice)
	check("", "几何分辨率", chineseAdjustmentAdvice)
	check("en", "Geometry resolution", englishAdjustmentAdvice)
	stored, err := world.module.GetPreparedInput(context.Background(), "owner-a", op.PreparedInputID)
	if err != nil || len(stored.ParameterAdjustments) != 1 {
		t.Fatalf("frozen input: %+v %v", stored, err)
	}
	frozen := stored.ParameterAdjustments[0]
	if frozen.Label != "几何分辨率" || frozen.Advice != chineseAdjustmentAdvice ||
		frozen.Reason != "几何分辨率" || string(frozen.Value) != `"standard"` ||
		string(frozen.RecommendedValue) != `"4k"` {
		t.Fatalf("projecting adjustments changed the frozen input: %+v", frozen)
	}
}

// pendingApprovalExtraCreditsCatalog gives Meshy text-to-3d the same
// extra-credit shape Tripo declares, so the legacy extra-label/items branch
// runs without a paid Provider call. One option label is Chinese copy with an
// English translation, to prove the chosen items are projected, not stored.
func pendingApprovalExtraCreditsCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(catalogtest.DefaultCatalogBytes(t), &document); err != nil {
		t.Fatal(err)
	}
	patched := false
	for _, raw := range document["offerings"].([]any) {
		offering := raw.(map[string]any)
		if offering["id"] != "meshy/meshy-7.1/text-to-3d" {
			continue
		}
		offering["cost"] = map[string]any{
			"base_credits":        40,
			"extra_credits":       1,
			"selection_parameter": "target_formats",
			"consent_parameter":   "authorize_extra_credits",
			"extra_label":         "完整附加导出格式",
		}
		offering["cost_policy"] = map[string]any{"max_authorized_extra_credits": 10}
		parameters := offering["parameters"].([]any)
		for _, rawParameter := range parameters {
			parameter := rawParameter.(map[string]any)
			if parameter["name"] != "target_formats" {
				continue
			}
			for _, rawOption := range parameter["options"].([]any) {
				option := rawOption.(map[string]any)
				if option["value"] == "glb" {
					option["label"] = "标准"
				}
			}
		}
		offering["parameters"] = append(parameters, map[string]any{
			"name":  "authorize_extra_credits",
			"label": "同意完整 Tripo 原生积分报价",
			"type":  "boolean",
			"presentation": map[string]any{
				"surface": "hidden",
				"control": "boolean",
			},
		})
		patched = true
		break
	}
	if !patched {
		t.Fatal("missing Meshy text-to-3d Offering")
	}
	// The legacy discovery projection lists every parameter of the Offering.
	application := document["application"].(map[string]any)
	discovery := application["provider_discovery"].(map[string]any)
	for _, raw := range discovery["legacy_models"].([]any) {
		model := raw.(map[string]any)
		if model["model_id"] != "meshy-7.1" {
			continue
		}
		textTo3D := model["parameter_projections"].(map[string]any)["text-to-3d"].(map[string]any)
		textTo3D["order"] = append(textTo3D["order"].([]any), "authorize_extra_credits")
		break
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// The legacy extra-credit projection follows the reader's locale for the
// Offering's label and chosen item names, in the turn and after it.
func TestApprovalExtraCreditsFollowTheLocale(t *testing.T) {
	t.Parallel()
	world := pendingApprovalBrowsersWithCatalog(t, nil, pendingApprovalExtraCreditsCatalog(t))
	op, grant := world.inTurnApproval(t, "call-extra", "a draft", func(cmd *generation.PrepareInputCommand) {
		cmd.Input = json.RawMessage(`{"prompt":"a draft","authorize_extra_credits":true}`)
	})
	type extraProjection struct {
		ExtraLabel   string   `json:"extra_label"`
		ExtraItems   []string `json:"extra_items"`
		ExtraCredits int      `json:"extra_credits"`
		BaseCredits  int      `json:"base_credits"`
	}
	readInTurn := func(locale string) extraProjection {
		response := world.getIn(t, world.cookie, "/api/v1/agent/consent?scope="+world.sessionID, locale)
		var body struct {
			Consent struct {
				Pending extraProjection `json:"consent_pending"`
			} `json:"consent"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("%q consent: %d %s", locale, response.Code, response.Body.String())
		}
		return body.Consent.Pending
	}
	readDetached := func(locale string) extraProjection {
		status, items := world.pendingListIn(t, world.cookie, world.sessionID, locale)
		if status != http.StatusOK || len(items) != 1 {
			t.Fatalf("%q list status=%d items=%v", locale, status, items)
		}
		raw, _ := json.Marshal(items[0])
		var item extraProjection
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("%q item=%s err=%v", locale, raw, err)
		}
		return item
	}
	check := func(stage, locale, label string, items []string) {
		var got extraProjection
		if stage == "turn" {
			got = readInTurn(locale)
		} else {
			got = readDetached(locale)
		}
		if got.ExtraLabel != label || !slices.Equal(got.ExtraItems, items) ||
			got.ExtraCredits != 6 || got.BaseCredits != 40 {
			t.Fatalf("%s %q extra credits changed: %+v", stage, locale, got)
		}
	}
	englishItems := []string{"Standard", "OBJ", "FBX", "STL", "USDZ", "3MF"}
	chineseItems := []string{"标准", "OBJ", "FBX", "STL", "USDZ", "3MF"}
	check("turn", "en", "Full additional export formats", englishItems)
	check("turn", "", "完整附加导出格式", chineseItems)

	_, detached, err := world.state.repository.DetachAgentOperation(context.Background(), op)
	if err != nil || !detached {
		t.Fatalf("detach: %v %v", detached, err)
	}
	check("detached", "en", "Full additional export formats", englishItems)
	check("detached", "", "完整附加导出格式", chineseItems)

	stored, err := world.module.GetPreparedInput(context.Background(), "owner-a", op.PreparedInputID)
	if err != nil || stored.ExtraCredits != 6 {
		t.Fatalf("frozen extra credits: %+v %v", stored, err)
	}
	if world.taskCount(t) != 0 {
		t.Fatalf("reading the extra-credit projection created a task with grant %s", grant.RequestID)
	}
}

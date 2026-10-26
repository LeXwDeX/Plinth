package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/catalogrecipes"
	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

func uploadSkillSource(t *testing.T, handler http.Handler, cookie *http.Cookie) string {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 4, 4))
	imageData.Set(1, 1, color.RGBA{R: 220, G: 30, B: 20, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "source.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(encoded.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &body)
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload source: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.ID == "" {
		t.Fatalf("source response: %s %v", response.Body.String(), err)
	}
	return result.ID
}

func TestThreeViewSkillUsesPreparedGrantAndOnePaidTask(t *testing.T) {
	t.Parallel()
	_, handler, cookie, session := grantTestBrowserWithApplication(t, nil, []string{"user"})
	asset := uploadSkillSource(t, handler, cookie)
	createBody := `{"session_id":"` + session + `","recipe_id":"image-three-view",` +
		`"recipe_revision":"2026-10-13.1","offering_id":"openai/gpt-image-2/image-to-image",` +
		`"source_asset_id":"` + asset + `"}`
	create := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/skill-runs", createBody, "three-view-request")
	if create.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", create.Code, create.Body.String())
	}
	var run struct {
		ID    string `json:"id"`
		Steps []struct {
			Spec struct {
				Key string `json:"key"`
			} `json:"spec"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &run); err != nil || run.ID == "" || len(run.Steps) != 3 {
		t.Fatalf("three-view run: %s %v", create.Body.String(), err)
	}
	replayedCreate := grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/skill-runs", createBody, "three-view-request")
	if replayedCreate.Code != http.StatusCreated || !strings.Contains(replayedCreate.Body.String(), run.ID) {
		t.Fatalf("create replay: %d %s", replayedCreate.Code, replayedCreate.Body.String())
	}
	stepRoute := "/api/v1/skill-runs/" + run.ID + "/steps/front"
	prepare := grantTestRequest(t, handler, cookie, http.MethodPost, stepRoute, `{"action":"prepare"}`, "")
	if prepare.Code != http.StatusOK || !strings.Contains(prepare.Body.String(), "prepared_input_id") {
		t.Fatalf("prepare step: %d %s", prepare.Code, prepare.Body.String())
	}
	grant := grantTestRequest(t, handler, cookie, http.MethodPost, stepRoute, `{"action":"grant"}`, "")
	if grant.Code != http.StatusOK {
		t.Fatalf("request human grant: %d %s", grant.Code, grant.Body.String())
	}
	var granted struct {
		Result struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		} `json:"result"`
	}
	// The default manual mode asks a person only for 3D steps, so an image step
	// is approved by session policy as soon as the grant is requested.
	if err := json.Unmarshal(grant.Body.Bytes(), &granted); err != nil || granted.Result.RequestID == "" || granted.Result.Status != "approved" {
		t.Fatalf("policy-approved grant: %s %v", grant.Body.String(), err)
	}
	consentRoute := "/api/v1/agent/consent?scope=" + session
	visible := grantTestRequest(t, handler, cookie, http.MethodGet, consentRoute, "", "")
	if visible.Code != http.StatusOK || strings.Contains(visible.Body.String(), granted.Result.RequestID) {
		t.Fatalf("policy-approved step must not ask for consent: %d %s", visible.Code, visible.Body.String())
	}
	stale := grantTestRequest(t, handler, cookie, http.MethodPut, consentRoute,
		`{"approved":true,"request_id":"`+granted.Result.RequestID+`"}`, "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("decision on a policy-approved grant: %d %s", stale.Code, stale.Body.String())
	}
	submit := grantTestRequest(t, handler, cookie, http.MethodPost, stepRoute, `{"action":"submit"}`, "")
	if submit.Code != http.StatusOK || !strings.Contains(submit.Body.String(), `"task_id":"`) {
		t.Fatalf("approved step: %d %s", submit.Code, submit.Body.String())
	}
	replay := grantTestRequest(t, handler, cookie, http.MethodPost, stepRoute, `{"action":"submit"}`, "")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("paid step replay: %d %s", replay.Code, replay.Body.String())
	}
}

func TestThreeViewRecipePublicationPreservesOldRunAndPreparedInputs(t *testing.T) {
	t.Parallel()
	// This exercises recipe publication over persisted runs, not application-version
	// submission compatibility: the shared harness Generation has no SchemaVersion.
	// Production SubmitPrepared still rejects an unaccepted input from another
	// application revision; no such execution boundary is relaxed by this test.
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var revised catalog.ChatSkillRecipeSchema
	for _, recipe := range source.Application().Chat.Skills.Recipes {
		if recipe.ID == "image-three-view" {
			revised = recipe
		}
	}
	legacy := revised
	legacy.Revision = "2026-09-29.1"
	legacy.Steps = append([]catalog.ChatSkillRecipeStepSchema(nil), revised.Steps...)
	legacyPrompts := []string{
		"Use the reference character as the exact identity. Render a clean full-body front view, facing the camera. Preserve clothing, colors, facial features, proportions, and accessories. Neutral pose, plain background, no text.",
		"Use the reference character as the exact identity. Render a clean full-body right-side profile view. Preserve clothing, colors, facial features, proportions, and accessories. Neutral pose, plain background, no text.",
		"Use the reference character as the exact identity. Render a clean full-body back view. Preserve clothing, colors, proportions, and accessories. Neutral pose, plain background, no text.",
	}
	for index := range legacy.Steps {
		legacy.Steps[index].PromptTemplate = legacyPrompts[index]
	}
	// Use a real older Catalog in the HTTP harness, not Application()'s
	// defensive copy. Publishing below reassembles the resolver/runtime over
	// the same persisted SQLite ledger with the current Catalog.
	data, err := os.ReadFile("../../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Version     int                       `json:"version"`
		Application catalog.ApplicationSchema `json:"application"`
		Offerings   []json.RawMessage         `json:"offerings"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for index := range document.Application.Chat.Skills.Recipes {
		if document.Application.Chat.Skills.Recipes[index].ID == revised.ID {
			document.Application.Chat.Skills.Recipes[index] = legacy
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	legacyCatalog, err := catalog.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	var databasePath string
	module, handler, cookie, session := grantTestBrowserWithCatalog(t, nil, []string{"user"}, legacyCatalog,
		func(_ *testing.T, path string) { databasePath = path })
	asset := uploadSkillSource(t, handler, cookie)
	var published *skillrun.Runtime
	create := func(revision, key string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"session_id": session, "recipe_id": revised.ID, "recipe_revision": revision,
			"offering_id": "openai/gpt-image-2/image-to-image", "source_asset_id": asset,
		})
		if err != nil {
			t.Fatal(err)
		}
		return grantTestRequest(t, handler, cookie, http.MethodPost, "/api/v1/skill-runs", string(body), key)
	}
	decodeRun := func(response *httptest.ResponseRecorder) skillrun.Run {
		t.Helper()
		var run skillrun.Run
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &run) != nil || len(run.Steps) != 3 {
			t.Fatalf("create run: %d %s", response.Code, response.Body.String())
		}
		return run
	}
	prepare := func(run skillrun.Run, index int) generation.PreparedInput {
		t.Helper()
		step := run.Steps[index].Spec
		var prepared generation.PreparedInput
		if published != nil {
			_, prepared, err = published.PrepareStep(context.Background(), "owner-a", run.ID, step.Key)
		} else {
			response := grantTestRequest(t, handler, cookie, http.MethodPost,
				"/api/v1/skill-runs/"+run.ID+"/steps/"+step.Key, `{"action":"prepare"}`, "")
			var result struct {
				Result struct {
					PreparedInputID generation.PreparedInputID `json:"prepared_input_id"`
				} `json:"result"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
				t.Fatalf("prepare %s: %d %s", step.Key, response.Code, response.Body.String())
			}
			prepared, err = module.GetPreparedInput(context.Background(), "owner-a", result.Result.PreparedInputID)
		}
		if err != nil || prepared.ID == "" || prepared.InputHash == "" || prepared.SessionID != session ||
			prepared.OfferingID != step.OfferingID || prepared.OfferingRevision != step.OfferingRevision ||
			!skillrun.MatchesPreparedInput(step, generation.AssetID(asset), prepared.NormalizedInput) {
			t.Fatalf("resolver/run/prepared contract for %s: %+v error=%v", step.Key, prepared, err)
		}
		return prepared
	}
	oldRun := decodeRun(create(legacy.Revision, "old-three-view"))
	oldPrepared := prepare(oldRun, 0)
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	runs, err := skillrun.NewService(repository, catalogrecipes.New(source))
	if err != nil {
		t.Fatal(err)
	}
	published, err = skillrun.NewRuntime(runs, module, &executiongrant.Service{Grants: repository, Prepared: repository})
	if err != nil {
		t.Fatal(err)
	}
	command := skillrun.CreateCommand{OwnerID: "owner-a", SessionID: session, RecipeID: revised.ID,
		ExpectedRevision: legacy.Revision, SelectedOfferingID: "openai/gpt-image-2/image-to-image", SourceAssetID: generation.AssetID(asset),
		IdempotencyKey: "stale-three-view"}
	if _, err := published.Create(context.Background(), command); !errors.Is(err, skillrun.ErrConflict) {
		t.Fatalf("new Run accepted a stale recipe revision: %v", err)
	}
	replayed := prepare(oldRun, 0)
	if replayed.ID != oldPrepared.ID || replayed.InputHash != oldPrepared.InputHash ||
		!bytes.Equal(replayed.NormalizedInput, oldPrepared.NormalizedInput) {
		t.Fatal("publishing a recipe changed the old already-prepared input")
	}
	// The old side/back were not prepared before publication: their future
	// PreparedInputs must still use the old Run's frozen templates and source.
	for index := 1; index < len(oldRun.Steps); index++ {
		prepare(oldRun, index)
	}
	frozen, err := published.Get(context.Background(), "owner-a", oldRun.ID)
	if err != nil ||
		frozen.RecipeRevision != legacy.Revision || frozen.RecipeHash != oldRun.RecipeHash {
		t.Fatalf("old Run freeze changed: %+v %v", frozen, err)
	}
	for index, step := range frozen.Steps {
		if step.Spec.PromptTemplate != legacyPrompts[index] || step.TaskID != "" || step.GrantRequestID != "" {
			t.Fatalf("old %s template or execution changed: %+v", step.Spec.Key, step)
		}
	}
	command.ExpectedRevision, command.IdempotencyKey = revised.Revision, "new-three-view"
	newRun, err := published.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if newRun.RecipeRevision != revised.Revision || newRun.RecipeHash == oldRun.RecipeHash || newRun.SourceAssetID != oldRun.SourceAssetID {
		t.Fatalf("new Run did not freeze revised recipe with same source: %+v", newRun)
	}
	for index, step := range newRun.Steps {
		if step.Spec.PromptTemplate != revised.Steps[index].PromptTemplate || step.Spec.SourceStepKey != "" ||
			step.TaskID != "" || step.GrantRequestID != "" {
			t.Fatalf("new %s template/source or execution changed: %+v", step.Spec.Key, step)
		}
		prepared := prepare(newRun, index)
		if strings.Contains(string(prepared.NormalizedInput), "reference character") ||
			strings.Contains(string(prepared.NormalizedInput), "full-body") {
			t.Fatalf("new PreparedInput still requires character/full-body: %s", prepared.NormalizedInput)
		}
	}
}

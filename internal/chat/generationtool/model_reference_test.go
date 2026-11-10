package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// reviewSelfCheckStore is the in-memory record store of the tool tests.
type reviewSelfCheckStore struct {
	mu      sync.Mutex
	records map[string]chat.SelfCheckRecord
}

func (store *reviewSelfCheckStore) key(owner, session, task string) string {
	return owner + "\x00" + session + "\x00" + task
}

func (store *reviewSelfCheckStore) ListSelfChecks(_ context.Context, owner, session string) ([]chat.SelfCheckRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var records []chat.SelfCheckRecord
	for key, record := range store.records {
		if strings.HasPrefix(key, owner+"\x00"+session+"\x00") {
			records = append(records, record)
		}
	}
	return records, nil
}

func (store *reviewSelfCheckStore) SelfCheck(_ context.Context, owner, session, task string) (chat.SelfCheckRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, found := store.records[store.key(owner, session, task)]
	return record, found, nil
}

func (store *reviewSelfCheckStore) SaveSelfCheck(_ context.Context, owner, session string,
	record chat.SelfCheckRecord, expected int64) (chat.SelfCheckRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := store.key(owner, session, record.TaskID)
	stored, found := store.records[key]
	if expected <= 0 {
		if found {
			return chat.SelfCheckRecord{}, true, nil
		}
		record.Revision = 1
		store.records[key] = record
		return record, false, nil
	}
	if !found || stored.Revision != expected {
		return chat.SelfCheckRecord{}, true, nil
	}
	record.Revision = expected + 1
	store.records[key] = record
	return record, false, nil
}

// reviewPictures serves the reference picture bytes by asset ID and records
// every owner-scoped read.
type reviewPictures struct {
	pictures map[string][]byte
	opened   []string
	owners   []string
	err      error
}

func (pictures *reviewPictures) OpenPicture(_ context.Context, owner string, attachment chat.Attachment,
	_ string) ([]byte, error) {
	pictures.owners = append(pictures.owners, owner)
	pictures.opened = append(pictures.opened, attachment.AssetID)
	if pictures.err != nil {
		return nil, pictures.err
	}
	data, found := pictures.pictures[attachment.AssetID]
	if !found {
		return nil, fmt.Errorf("asset %s is not a stored picture", attachment.AssetID)
	}
	return data, nil
}

func referencePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			fill := color.NRGBA{R: 250, G: 250, B: 250, A: 255}
			if x >= width/4 && x < width*3/4 && y >= height/4 && y < height*3/4 {
				fill = color.NRGBA{R: 200, G: 30, B: 30, A: 255}
			}
			picture.SetNRGBA(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

// referenceTask is a finished image-to-3D result whose frozen input names one
// reference picture and its view.
func referenceTask(id, owner, session string, glb []byte) generation.Task {
	task := reviewTask(id, owner, session, glb, generation.TaskSucceeded)
	task.Capability = "image-to-3d"
	task.Offering = generation.Offering{
		ID: "meshy/image-to-3d", ProviderID: "meshy", ProviderName: "Meshy", ModelName: "meshy-6",
		Parameters: []generation.Parameter{
			{Name: "reference_assets", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 4},
			{Name: "reference_views", Type: "string-array", Semantic: generation.SemanticReferenceViews,
				Options: []generation.ParameterOption{{Value: "front", Label: "正面"}}},
		},
	}
	task.Input = json.RawMessage(`{"reference_assets":["ref-asset"],"reference_views":["front"]}`)
	return task
}

func referenceExecutor(t *testing.T, gateway *reviewGateway, assets *reviewAssets, pictures *reviewPictures,
	store *reviewSelfCheckStore, mutate func(*catalog.ApplicationSchema)) *generationtool.Executor {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	app.Chat.ModelReview.Render.Size = 24
	app.Chat.ModelReview.Render.MaxSize = 48
	app.Chat.ModelReview.Render.Supersample = 1
	app.Chat.ModelReview.Render.Workers = 1
	app.Chat.ModelReview.Render.DefaultViews = []string{"front"}
	app.Chat.SelfCheck.Reference.Size = 24
	app.Chat.SelfCheck.Reference.Supersample = 1
	if mutate != nil {
		mutate(&app)
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat,
		TaskStates: app.TaskStates, ModelAssets: assets, InputAssets: assets,
		InputAssetRoute: app.Runtime.HTTP.Routes.AssetItem,
		PictureReader:   pictures,
		SelfChecks:      chat.NewSelfCheckService(store, app.Chat.SelfCheck),
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func newReviewSelfCheckStore() *reviewSelfCheckStore {
	return &reviewSelfCheckStore{records: map[string]chat.SelfCheckRecord{}}
}

func TestCompareModelReferenceComparesTheFrozenReferenceBesideTheRender(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(referenceTask("task-a", "owner-a", "session-a", glb))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
	store := newReviewSelfCheckStore()
	executor := referenceExecutor(t, gateway, assets, pictures, store, nil)

	result, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.Error != "" || result.TaskID != "" || len(result.ImageURLs) != 1 || len(result.Pictures) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if gateway.submitted != 0 || len(pictures.opened) != 1 || pictures.opened[0] != "ref-asset" ||
		pictures.owners[0] != "owner-a" {
		t.Fatalf("submitted=%d pictures=%v owners=%v", gateway.submitted, pictures.opened, pictures.owners)
	}
	if len(assets.uploads) != 1 || assets.uploads[0].AssetType != "model-render" ||
		!strings.Contains(assets.uploads[0].OriginalFilename, "task-a") {
		t.Fatalf("uploads = %+v", assets.uploads)
	}
	payload := reviewPayload(t, result)
	if payload["ok"] != true || payload["caveats"] == "" {
		t.Fatalf("payload = %v", payload)
	}
	views, ok := payload["views"].([]any)
	if !ok || len(views) != 1 {
		t.Fatalf("views = %v", payload["views"])
	}
	view := views[0].(map[string]any)
	if view["view"] != "front" || view["image"] != "ref-asset" || view["width"] != float64(52) || view["height"] != float64(24) {
		t.Fatalf("view = %v", view)
	}
	metrics := view["metrics"].(map[string]any)
	iou, _ := metrics["silhouette_iou"].(float64)
	if iou < 0 || iou > 1 {
		t.Fatalf("iou = %v", iou)
	}
	for _, key := range []string{"bbox_aspect_delta", "dominant_color_distance", "dominant_reference",
		"dominant_render", "reference_subject_ratio", "render_subject_ratio"} {
		if _, present := metrics[key]; !present {
			t.Fatalf("metrics lack %q: %v", key, metrics)
		}
	}
	if !strings.HasPrefix(metrics["dominant_reference"].(string), "#") {
		t.Fatalf("dominant reference = %v", metrics["dominant_reference"])
	}
	if selfCheck := payload["self_check"].(map[string]any); selfCheck["status"] != "unchecked" ||
		selfCheck["renders_in_round"] != float64(1) || selfCheck["renders_limit"] != float64(6) {
		t.Fatalf("self_check = %v", selfCheck)
	}

	record, found, err := store.SelfCheck(context.Background(), "owner-a", "session-a", "task-a")
	if err != nil || !found || len(record.Checks) != 1 {
		t.Fatalf("record = %+v found=%v err=%v", record, found, err)
	}
	check := record.Checks[0]
	if check.Kind != chat.SelfCheckKindReference || check.Tool != "compare_model_reference" ||
		check.Round != 1 || check.OK != true || len(check.Views) != 1 || check.Views[0] != "front" || check.Metrics == nil {
		t.Fatalf("check = %+v", check)
	}
}

func TestCompareModelReferenceAcceptsAnExplicitPictureAndRefusesBadPairs(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	task := referenceTask("task-a", "owner-a", "session-a", glb)
	gateway := newReviewGateway(task)
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
	store := newReviewSelfCheckStore()
	executor := referenceExecutor(t, gateway, assets, pictures, store, nil)

	explicit, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a","image":"ref-asset","view":"front"}`))
	if err != nil || explicit.Error != "" || len(explicit.ImageURLs) != 1 {
		t.Fatalf("explicit = %+v err=%v", explicit, err)
	}
	for name, arguments := range map[string]string{
		"too many pairs": `{"model":"task-a","pairs":[
			{"image":"ref-asset","view":"front"},{"image":"ref-asset","view":"back"},
			{"image":"ref-asset","view":"left"},{"image":"ref-asset","view":"right"},
			{"image":"ref-asset","view":"three_quarter"}]}`,
		"undeclared view": `{"model":"task-a","image":"ref-asset","view":"top"}`,
		"image and pairs": `{"model":"task-a","image":"ref-asset","pairs":[{"image":"ref-asset","view":"front"}]}`,
		"empty pair":      `{"model":"task-a","pairs":[{"image":"","view":"front"}]}`,
	} {
		result, err := executor.CompareModelReference(context.Background(),
			reviewCommand("compare_model_reference", arguments))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.Error == "" || !result.NoSubmission || len(result.ImageURLs) != 0 {
			t.Fatalf("%s = %+v", name, result)
		}
	}
	// A refusal stores no evidence.
	record, found, err := store.SelfCheck(context.Background(), "owner-a", "session-a", "task-a")
	if err != nil || !found || len(record.Checks) != 1 {
		t.Fatalf("record after refusals = %+v found=%v err=%v", record, found, err)
	}

	// Without a frozen reference picture and without an explicit image, the
	// tool refuses in the Schema's words.
	plain := reviewTask("task-b", "owner-a", "session-a", glb, generation.TaskSucceeded)
	plainGateway := newReviewGateway(plain)
	plainAssets := newReviewAssets(map[generation.AssetID][]byte{"task-b-glb": glb})
	plainExecutor := referenceExecutor(t, plainGateway, plainAssets, pictures, store, nil)
	refused, err := plainExecutor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-b"}`))
	if err != nil || refused.Error == "" || !strings.Contains(refused.Error, "no frozen reference picture") {
		t.Fatalf("missing reference = %+v err=%v", refused, err)
	}
}

func TestSelfCheckLimitsRefuseMoreRoundsAndRendersButAllowTheConclusion(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(referenceTask("task-a", "owner-a", "session-a", glb))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
	store := newReviewSelfCheckStore()
	executor := referenceExecutor(t, gateway, assets, pictures, store, func(app *catalog.ApplicationSchema) {
		app.Chat.SelfCheck.Limits.MaxRoundsPerResult = 1
		app.Chat.SelfCheck.Limits.MaxRendersPerRound = 1
	})
	ctx := context.Background()

	// A conclusion without evidence is refused and saves nothing.
	early, err := executor.RecordSelfCheck(ctx, reviewCommand("record_self_check",
		`{"model":"task-a","status":"passed","summary":"looks right"}`))
	if err != nil || early.Error == "" {
		t.Fatalf("early conclusion = %+v err=%v", early, err)
	}
	if _, found, _ := store.SelfCheck(ctx, "owner-a", "session-a", "task-a"); found {
		t.Fatal("a refused conclusion created a record")
	}

	inspect, err := executor.InspectModel(ctx, reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || inspect.Error != "" {
		t.Fatalf("inspect = %+v err=%v", inspect, err)
	}
	second, err := executor.InspectModel(ctx, reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || second.Error == "" || !strings.Contains(second.Error, "limit") {
		t.Fatalf("second round = %+v err=%v", second, err)
	}
	render, err := executor.RenderModelViews(ctx, reviewCommand("render_model_views",
		`{"model":"task-a","views":["front"]}`))
	if err != nil || render.Error != "" {
		t.Fatalf("render = %+v err=%v", render, err)
	}
	secondRender, err := executor.RenderModelViews(ctx, reviewCommand("render_model_views",
		`{"model":"task-a","views":["back"]}`))
	if err != nil || secondRender.Error == "" {
		t.Fatalf("second render = %+v err=%v", secondRender, err)
	}

	recorded, err := executor.RecordSelfCheck(ctx, reviewCommand("record_self_check",
		`{"model":"task-a","status":"passed","summary":"checked front and geometry"}`))
	if err != nil || recorded.Error != "" || len(recorded.Summary) == 0 {
		t.Fatalf("conclusion = %+v err=%v", recorded, err)
	}
	record, found, err := store.SelfCheck(ctx, "owner-a", "session-a", "task-a")
	if err != nil || !found {
		t.Fatalf("record = %+v err=%v", record, err)
	}
	if record.Conclusion == nil || record.Conclusion.Status != "passed" || len(record.Checks) != 2 {
		t.Fatalf("stored conclusion = %+v checks=%d", record.Conclusion, len(record.Checks))
	}
	counters := chat.SelfCheckCounts(executorSelfCheckSchema(t), record)
	if counters.RoundsUsed != 1 || counters.RendersInRound != 1 {
		t.Fatalf("counters = %+v", counters)
	}

	// A later conclusion can still be written even at the round limit, and a
	// has_issues conclusion requires a remaining item.
	issues, err := executor.RecordSelfCheck(ctx, reviewCommand("record_self_check",
		`{"model":"task-a","status":"has_issues","summary":"one crack","remaining":["crack on the back"]}`))
	if err != nil || issues.Error != "" {
		t.Fatalf("issues conclusion = %+v err=%v", issues, err)
	}
	empty, err := executor.RecordSelfCheck(ctx, reviewCommand("record_self_check",
		`{"model":"task-a","status":"has_issues","summary":"no item"}`))
	if err != nil || empty.Error == "" {
		t.Fatalf("empty remaining = %+v err=%v", empty, err)
	}
}

func executorSelfCheckSchema(t *testing.T) catalog.ChatSelfCheckSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat.SelfCheck
}

func TestRecordSelfCheckRejectsUnknownArgumentsAndForeignModels(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(referenceTask("task-a", "owner-a", "session-a", glb))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
	store := newReviewSelfCheckStore()
	executor := referenceExecutor(t, gateway, assets, pictures, store, nil)
	ctx := context.Background()
	for name, arguments := range map[string]string{
		"unknown field":  `{"model":"task-a","status":"passed","summary":"x","secret":true}`,
		"missing status": `{"model":"task-a","summary":"x"}`,
		"not an object":  `[]`,
	} {
		result, err := executor.RecordSelfCheck(ctx, reviewCommand("record_self_check", arguments))
		if err != nil || result.Error == "" || !result.NoSubmission {
			t.Fatalf("%s = %+v err=%v", name, result, err)
		}
	}
	foreign, err := executor.RecordSelfCheck(ctx, reviewCommand("record_self_check",
		`{"model":"missing-task","status":"passed","summary":"x"}`))
	if err != nil || foreign.Error == "" {
		t.Fatalf("foreign model = %+v err=%v", foreign, err)
	}
}

func TestReferenceToolWithoutItsDependenciesIsUnavailable(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(referenceTask("task-a", "owner-a", "session-a", glb))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: catalog.ChatSchema{},
		ModelAssets: assets, InputAssets: assets,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.CompareModelReference(context.Background(),
		reviewCommand("compare_model_reference", `{"model":"task-a"}`))
	if err != nil || result.Error != "reference comparison unavailable" || !result.NoSubmission {
		t.Fatalf("result = %+v err=%v", result, err)
	}
}

// #246: a paid processing step counts as one correction against the result it
// changes, and its result tells the Agent how many remain. The correction is
// never refused at the limit: the Schema asks for a truthful leftover report
// instead.
func TestPaidCorrectionCountsAgainstTheSourceSelfCheck(t *testing.T) {
	h := newProcessingHarness(t, "auto", meshyCatalogTasks)
	store := newReviewSelfCheckStore()
	schema := h.app.Chat.SelfCheck
	service := chat.NewSelfCheckService(store, schema)
	h.config.SelfChecks = service
	executor, err := generationtool.New(h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.executor = executor
	ctx := context.Background()
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "model-1", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "model-1", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordConclusion(ctx, "owner-a", "session-a", "model-1", "",
		chat.SelfCheckConclusion{Status: "passed", Summary: "looked right"}); err != nil {
		t.Fatal(err)
	}
	for correction := 1; correction <= schema.Limits.MaxCorrectionsPerResult+1; correction++ {
		result, _ := h.run("retexture_model", `{"source_task_id":"model-1","prompt":"mossy stone"}`)
		if result.Error != "" {
			t.Fatalf("correction %d refused: %+v", correction, result)
		}
		content := h.content(result)
		block, ok := content["self_check"].(map[string]any)
		if !ok {
			t.Fatalf("correction %d has no self_check block: %v", correction, content)
		}
		if block["corrections_used"] != float64(correction) ||
			block["corrections_limit"] != float64(schema.Limits.MaxCorrectionsPerResult) ||
			block["status"] != "unchecked" {
			t.Fatalf("correction %d block = %v", correction, block)
		}
		_, note := content["self_check_note"]
		if correction > schema.Limits.MaxCorrectionsPerResult && note != true {
			t.Fatalf("correction %d over the limit lacks the note: %v", correction, content)
		}
	}
	record, found, err := store.SelfCheck(ctx, "owner-a", "session-a", "model-1")
	if err != nil || !found {
		t.Fatalf("record = %+v err=%v", record, err)
	}
	if record.CorrectionsUsed != schema.Limits.MaxCorrectionsPerResult+1 || record.Conclusion != nil {
		t.Fatalf("record = %+v", record)
	}
}

func TestEveryReadOnlyReviewCallAppendsItsEvidenceButRefusalsDoNot(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(referenceTask("task-a", "owner-a", "session-a", glb))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	pictures := &reviewPictures{pictures: map[string][]byte{"ref-asset": referencePNG(t, 32, 32)}}
	store := newReviewSelfCheckStore()
	executor := referenceExecutor(t, gateway, assets, pictures, store, nil)
	ctx := context.Background()
	countChecks := func() int {
		record, found, err := store.SelfCheck(ctx, "owner-a", "session-a", "task-a")
		if err != nil || !found {
			t.Fatalf("record = %+v err=%v", record, err)
		}
		return len(record.Checks)
	}

	if result, err := executor.InspectModel(ctx, reviewCommand("inspect_model", `{"model":"task-a"}`)); err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	if got := countChecks(); got != 1 {
		t.Fatalf("checks after inspect = %d", got)
	}
	if result, err := executor.RenderModelViews(ctx, reviewCommand("render_model_views",
		`{"model":"task-a","views":["front","back"]}`)); err != nil || result.Error != "" {
		t.Fatalf("render = %+v err=%v", result, err)
	}
	if got := countChecks(); got != 2 {
		t.Fatalf("checks after render = %d", got)
	}
	if result, err := executor.CompareModelReference(ctx, reviewCommand("compare_model_reference",
		`{"model":"task-a"}`)); err != nil || result.Error != "" {
		t.Fatalf("reference = %+v err=%v", result, err)
	}
	if got := countChecks(); got != 3 {
		t.Fatalf("checks after reference = %d", got)
	}
	if result, err := executor.CompareModels(ctx, reviewCommand("compare_models",
		`{"models":["task-a","task-a"],"views":["front"]}`)); err != nil || result.Error != "" {
		t.Fatalf("compare = %+v err=%v", result, err)
	}
	if got := countChecks(); got != 5 {
		t.Fatalf("checks after compare = %d (one per model)", got)
	}

	// Argument and ownership refusals append nothing.
	if result, err := executor.InspectModel(ctx, reviewCommand("inspect_model", `{"model":"missing"}`)); err != nil || result.Error == "" {
		t.Fatalf("missing model = %+v err=%v", result, err)
	}
	if result, err := executor.InspectModel(ctx, reviewCommand("inspect_model", `{"model":`)); err != nil || result.Error == "" {
		t.Fatalf("invalid arguments = %+v err=%v", result, err)
	}
	if got := countChecks(); got != 5 {
		t.Fatalf("a refusal appended evidence: %d", got)
	}

	record, _, _ := store.SelfCheck(ctx, "owner-a", "session-a", "task-a")
	if len(record.Checks) != 5 {
		t.Fatalf("checks = %+v", record.Checks)
	}
	if record.Checks[0].Kind != chat.SelfCheckKindInspect || record.Checks[1].Kind != chat.SelfCheckKindRender ||
		record.Checks[2].Kind != chat.SelfCheckKindReference ||
		record.Checks[3].Kind != chat.SelfCheckKindCompare || record.Checks[4].Kind != chat.SelfCheckKindCompare {
		t.Fatalf("kinds = %+v", record.Checks)
	}
	if record.Checks[3].AssetID != "task-a-glb" || record.Checks[4].AssetID != "task-a-glb" {
		t.Fatalf("compare assets = %v / %v", record.Checks[3].AssetID, record.Checks[4].AssetID)
	}
}

package generationtool_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// reviewGateway serves saved Tasks to the read-only review tools and records
// that they never submit a paid Task. Its Offerings and availability let a
// test check which fix capabilities inspect_model may name.
type reviewGateway struct {
	tasks       map[generation.TaskID]generation.Task
	offerings   map[generation.Capability][]generation.Offering
	unavailable map[generation.OfferingID]bool
	submitted   int
}

func newReviewGateway(tasks ...generation.Task) *reviewGateway {
	gateway := &reviewGateway{tasks: map[generation.TaskID]generation.Task{}}
	for _, task := range tasks {
		gateway.tasks[task.ID] = task
	}
	return gateway
}

func (gateway *reviewGateway) Offerings(capability generation.Capability) []generation.Offering {
	return gateway.offerings[capability]
}

func (gateway *reviewGateway) OfferingAvailable(id generation.OfferingID) bool {
	return !gateway.unavailable[id]
}

func (gateway *reviewGateway) Submit(
	context.Context,
	generation.SubmitCommand,
) (generation.Accepted, error) {
	gateway.submitted++
	return generation.Accepted{}, errors.New("review tools must not submit")
}

func (gateway *reviewGateway) Get(
	_ context.Context,
	owner generation.OwnerID,
	id generation.TaskID,
) (generation.Task, error) {
	task, found := gateway.tasks[id]
	if !found || task.OwnerID != owner {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get"}
	}
	return task, nil
}

// reviewAssets stands in for the generation kernel's asset gateways: it
// returns the GLB bytes the test registered and collects the rendered PNGs.
type reviewAssets struct {
	contents    map[generation.AssetID][]byte
	opens       []generation.AssetID
	openErr     error
	uploadErr   error
	uploads     []generation.InputAssetUpload
	uploaded    [][]byte
	uploadOwner []generation.OwnerID
}

func newReviewAssets(contents map[generation.AssetID][]byte) *reviewAssets {
	return &reviewAssets{contents: contents}
}

func (assets *reviewAssets) OpenAsset(
	_ context.Context,
	_ generation.OwnerID,
	_ generation.TaskID,
	id generation.AssetID,
) (generation.AssetContent, error) {
	assets.opens = append(assets.opens, id)
	if assets.openErr != nil {
		return generation.AssetContent{}, assets.openErr
	}
	data, found := assets.contents[id]
	if !found {
		return generation.AssetContent{}, &generation.Error{Code: generation.ErrNotFound, Operation: "open_asset"}
	}
	return generation.AssetContent{
		Body: io.NopCloser(bytes.NewReader(data)), MediaType: "model/gltf-binary",
		Size: int64(len(data)), SHA256: reviewDigest(data), Role: "model",
	}, nil
}

func (assets *reviewAssets) UploadAsset(
	_ context.Context,
	owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	if assets.uploadErr != nil {
		return generation.InputAsset{}, assets.uploadErr
	}
	var data bytes.Buffer
	if upload.Body != nil {
		if _, err := io.Copy(&data, upload.Body); err != nil {
			return generation.InputAsset{}, err
		}
	}
	assets.uploads = append(assets.uploads, upload)
	assets.uploaded = append(assets.uploaded, data.Bytes())
	assets.uploadOwner = append(assets.uploadOwner, owner)
	id := generation.AssetID("render-" + strconv.Itoa(len(assets.uploads)))
	return generation.InputAsset{
		ID: id, OwnerID: owner, MediaType: upload.MediaType,
		AssetType: upload.AssetType, Size: int64(data.Len()), SHA256: reviewDigest(data.Bytes()),
	}, nil
}

func reviewDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// reviewModelGLB builds a GLB with a two-triangle quad in the XY plane. Each
// instance node adds one part, so instances=2 also exercises the report's
// truncation and comparison counts.
func reviewModelGLB(t testing.TB, edge float32, instances int) []byte {
	t.Helper()
	positions := []float32{-edge, -edge, 0, edge, -edge, 0, edge, edge, 0, -edge, edge, 0}
	indices := []uint16{0, 1, 2, 0, 2, 3}
	var bin bytes.Buffer
	for _, value := range positions {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], math.Float32bits(value))
		bin.Write(encoded[:])
	}
	for _, value := range indices {
		var encoded [2]byte
		binary.LittleEndian.PutUint16(encoded[:], value)
		bin.Write(encoded[:])
	}
	for bin.Len()%4 != 0 {
		bin.WriteByte(0)
	}
	nodeIndices := make([]int, instances)
	nodes := make([]any, instances)
	for index := range nodeIndices {
		nodeIndices[index] = index
		nodes[index] = map[string]any{"name": "quad", "mesh": 0}
	}
	document := map[string]any{
		"asset":   map[string]any{"version": "2.0"},
		"scene":   0,
		"scenes":  []any{map[string]any{"nodes": nodeIndices}},
		"nodes":   nodes,
		"buffers": []any{map[string]any{"byteLength": bin.Len()}},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": len(positions) * 4, "target": 34962},
			map[string]any{"buffer": 0, "byteOffset": len(positions) * 4, "byteLength": len(indices) * 2, "target": 34963},
		},
		"accessors": []any{
			map[string]any{
				"bufferView": 0, "componentType": 5126, "count": 4, "type": "VEC3",
				"min": []float64{-float64(edge), -float64(edge), 0},
				"max": []float64{float64(edge), float64(edge), 0},
			},
			map[string]any{"bufferView": 1, "componentType": 5123, "count": 6, "type": "SCALAR"},
		},
		"meshes": []any{map[string]any{"primitives": []any{map[string]any{
			"attributes": map[string]any{"POSITION": 0},
			"indices":    1,
		}}}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode GLB document: %v", err)
	}
	for len(encoded)%4 != 0 {
		encoded = append(encoded, ' ')
	}
	total := 12 + 8 + len(encoded) + 8 + bin.Len()
	out := make([]byte, 0, total)
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:], 0x46546C67) // glTF
	binary.LittleEndian.PutUint32(header[4:], 2)
	binary.LittleEndian.PutUint32(header[8:], uint32(total))
	out = append(out, header...)
	chunk := make([]byte, 8)
	binary.LittleEndian.PutUint32(chunk[0:], uint32(len(encoded)))
	binary.LittleEndian.PutUint32(chunk[4:], 0x4E4F534A) // JSON
	out = append(out, chunk...)
	out = append(out, encoded...)
	binary.LittleEndian.PutUint32(chunk[0:], uint32(bin.Len()))
	binary.LittleEndian.PutUint32(chunk[4:], 0x004E4942) // BIN
	out = append(out, chunk...)
	out = append(out, bin.Bytes()...)
	return out
}

// reviewTask is a succeeded 3D Task with one saved GLB and a thumbnail.
func reviewTask(id, owner, session string, data []byte, state generation.TaskState) generation.Task {
	return generation.Task{
		ID:         generation.TaskID(id),
		OwnerID:    generation.OwnerID(owner),
		SessionID:  session,
		Capability: "image-to-3d",
		Offering: generation.Offering{
			ID: "meshy/image-to-3d", ProviderID: "meshy", ProviderName: "Meshy", ModelName: "meshy-6",
		},
		State: state,
		Assets: []generation.Asset{
			{
				ID: generation.AssetID(id + "-thumb"), TaskID: generation.TaskID(id), Role: "thumbnail",
				StorageKey: "key/thumb", MediaType: "image/png", Size: 128,
			},
			{
				ID: generation.AssetID(id + "-glb"), TaskID: generation.TaskID(id), Role: "model", Variant: "glb",
				StorageKey: "key/glb", MediaType: "model/gltf-binary",
				Size: int64(len(data)), SHA256: reviewDigest(data),
			},
		},
	}
}

// reviewExecutor builds the executor with small render dimensions, so a test
// draws a 24-pixel view instead of the shipped 768-pixel one.
func reviewExecutor(
	t *testing.T,
	gateway *reviewGateway,
	assets *reviewAssets,
	mutate func(*catalog.ApplicationSchema),
) (*generationtool.Executor, catalog.ChatSchema) {
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
	if mutate != nil {
		mutate(&app)
	}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: gateway, References: &fakeReferences{}, Chat: app.Chat,
		TaskStates: app.TaskStates, ModelAssets: assets, InputAssets: assets,
		InputAssetRoute: app.Runtime.HTTP.Routes.AssetItem,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor, app.Chat
}

func reviewCommand(name, arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Call: agents.ToolCall{ID: "call-1", Name: name, Arguments: []byte(arguments)},
		Tool: catalog.ChatToolSchema{Name: name},
	}
}

func reviewPayload(t *testing.T, result chat.ToolResult) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatalf("tool result is not JSON: %v\n%s", err, result.AgentContent)
	}
	return payload
}

func reviewNumber(t *testing.T, payload map[string]any, path ...string) float64 {
	t.Helper()
	var current any = payload
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%v: %q is not inside an object in %v", path, key, payload)
		}
		current = object[key]
	}
	number, ok := current.(float64)
	if !ok {
		t.Fatalf("%v = %v, want a number", path, current)
	}
	return number
}

func TestModelReviewInspectReturnsTheSavedModelsReport(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, schema := reviewExecutor(t, gateway, assets, nil)

	result, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoSubmission || result.TaskID != "" || result.Error != "" || len(result.ImageURLs) != 0 {
		t.Fatalf("inspect result = %+v", result)
	}
	if len(assets.opens) != 1 || assets.opens[0] != "task-a-glb" || gateway.submitted != 0 {
		t.Fatalf("opens = %v, submitted = %d", assets.opens, gateway.submitted)
	}
	payload := reviewPayload(t, result)
	if payload["ok"] != true || payload["parts_total"] != float64(1) || payload["parts_truncated"] != false {
		t.Fatalf("payload = %v", payload)
	}
	if got := reviewNumber(t, payload, "report", "triangles"); got != 2 {
		t.Fatalf("triangles = %v, want 2", got)
	}
	if got := reviewNumber(t, payload, "report", "vertices"); got != 4 {
		t.Fatalf("vertices = %v, want 4", got)
	}
	parts, _ := payload["report"].(map[string]any)["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts = %v", parts)
	}
	model := payload["model"].(map[string]any)
	if model["task_id"] != "task-a" || model["asset_id"] != "task-a-glb" ||
		model["provider"] != "Meshy" || model["model"] != "meshy-6" || model["capability"] != "image-to-3d" {
		t.Fatalf("identity = %v", model)
	}
	if message, _ := payload["message"].(string); !strings.Contains(message, "meshy-6") {
		t.Fatalf("message = %q", message)
	}
	if schema.ModelReview.Texts.InspectReady == "" {
		t.Fatal("the Schema lost its inspect text")
	}
}

func TestModelReviewInspectTruncatesThePartListButReportsTheTotal(t *testing.T) {
	glb := reviewModelGLB(t, 1, 3)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, _ := reviewExecutor(t, gateway, assets, func(app *catalog.ApplicationSchema) {
		app.Chat.ModelReview.Inspect.MaxListedParts = 2
	})
	result, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	payload := reviewPayload(t, result)
	if payload["parts_total"] != float64(3) || payload["parts_truncated"] != true {
		t.Fatalf("payload = %v", payload)
	}
	if parts := payload["report"].(map[string]any)["parts"].([]any); len(parts) != 2 {
		t.Fatalf("listed parts = %d, want 2", len(parts))
	}
	if got := reviewNumber(t, payload, "report", "triangles"); got != 6 {
		t.Fatalf("triangles = %v, want 6 (all parts counted)", got)
	}
}

func TestModelReviewRefusesAForeignOrUnfinishedSource(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	cases := []struct {
		name  string
		task  generation.Task
		tool  string
		args  string
		texts func(catalog.ChatModelReviewSchema) string
	}{
		{
			name: "another owners task",
			task: reviewTask("task-a", "owner-b", "session-a", glb, generation.TaskSucceeded),
			tool: "inspect_model", args: `{"model":"task-a"}`,
			texts: func(review catalog.ChatModelReviewSchema) string { return review.Source.SourceNotFound },
		},
		{
			name: "another session",
			task: reviewTask("task-a", "owner-a", "session-b", glb, generation.TaskSucceeded),
			tool: "render_model_views", args: `{"model":"task-a"}`,
			texts: func(review catalog.ChatModelReviewSchema) string { return review.Source.SourceNotFound },
		},
		{
			name: "a task that has not succeeded",
			task: reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskRunning),
			tool: "compare_models", args: `{"models":["task-a","task-a"]}`,
			texts: func(review catalog.ChatModelReviewSchema) string { return review.Source.SourceNotReady },
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			gateway := newReviewGateway(tt.task)
			assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
			executor, schema := reviewExecutor(t, gateway, assets, nil)
			command := reviewCommand(tt.tool, tt.args)
			var result chat.ToolResult
			var err error
			switch tt.tool {
			case "inspect_model":
				result, err = executor.InspectModel(context.Background(), command)
			case "render_model_views":
				result, err = executor.RenderModelViews(context.Background(), command)
			case "compare_models":
				result, err = executor.CompareModels(context.Background(), command)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || result.TaskID != "" || result.AgentContent != "" || len(result.ImageURLs) != 0 {
				t.Fatalf("refusal = %+v", result)
			}
			if want := tt.texts(schema.ModelReview); result.Error != want {
				t.Fatalf("refusal = %q, want %q", result.Error, want)
			}
			if len(assets.opens) != 0 || gateway.submitted != 0 {
				t.Fatalf("refused call read %v or submitted %d", assets.opens, gateway.submitted)
			}
		})
	}
}

func TestModelReviewResolvesHandlesAndTheSelectedModel(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, _ := reviewExecutor(t, gateway, assets, nil)
	history := []chat.Message{{Role: "assistant", Attachments: []chat.Attachment{{
		Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated,
		TaskID: "task-a", AssetID: "task-a-glb",
	}}}}

	byHandle := reviewCommand("inspect_model", `{"model":"@model1"}`)
	byHandle.History = history
	result, err := executor.InspectModel(context.Background(), byHandle)
	if err != nil || result.Error != "" {
		t.Fatalf("handle inspection = %+v err=%v", result, err)
	}
	if payload := reviewPayload(t, result); payload["model"].(map[string]any)["task_id"] != "task-a" {
		t.Fatalf("handle resolved to %v", payload["model"])
	}

	selected := reviewCommand("inspect_model", `{}`)
	selected.Options.Selection = []chat.Attachment{{
		Handle: "@model7", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated,
		TaskID: "task-a", AssetID: "task-a-glb",
	}}
	result, err = executor.InspectModel(context.Background(), selected)
	if err != nil || result.Error != "" {
		t.Fatalf("selected inspection = %+v err=%v", result, err)
	}

	missing := reviewCommand("inspect_model", `{"model":"@model9"}`)
	missing.History = history
	result, err = executor.InspectModel(context.Background(), missing)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" || !result.NoSubmission {
		t.Fatalf("unknown handle = %+v", result)
	}
}

func TestModelReviewRefusesAnExplicitFileThatIsNotAReadableGLB(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	loaded := reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded)
	notGLB := loaded
	notGLB.Assets = []generation.Asset{
		{ID: "task-a-glb", TaskID: "task-a", Role: "model", Variant: "glb", StorageKey: "key/glb",
			MediaType: "model/gltf-binary", Size: int64(len(glb)), SHA256: reviewDigest(glb)},
		{ID: "task-a-3mf", TaskID: "task-a", Role: "model", Variant: "3mf", StorageKey: "key/3mf",
			MediaType: "application/zip", Size: 2048},
	}
	cases := []struct {
		name   string
		task   generation.Task
		args   string
		assets func(*reviewAssets)
		mutate func(*catalog.ApplicationSchema)
		// want returns the exact text, or a prefix when partial is true (the
		// parser's own reason is not this package's copy).
		want    func(catalog.ChatModelReviewSchema) string
		partial bool
	}{
		{
			name: "a thumbnail is not a model file",
			task: loaded,
			args: `{"model":"task-a","source_asset_id":"task-a-thumb"}`,
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.ReplaceAll(review.Source.SourceNotModel, "{asset}", "task-a-thumb")
			},
		},
		{
			name: "a foreign asset is not one of the models files",
			task: loaded,
			args: `{"model":"task-a","source_asset_id":"task-z-glb"}`,
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.ReplaceAll(review.Source.SourceNotModel, "{asset}", "task-z-glb")
			},
		},
		{
			name: "a 3MF package is not a GLB",
			task: notGLB,
			args: `{"model":"task-a","source_asset_id":"task-a-3mf"}`,
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.NewReplacer("{asset}", "task-a-3mf", "{media}", "application/zip").
					Replace(review.Source.SourceNotGLB)
			},
		},
		{
			name: "the file exceeds the byte limit",
			task: loaded,
			args: `{"model":"task-a"}`,
			mutate: func(app *catalog.ApplicationSchema) {
				app.Chat.ModelReview.Inspect.Limits.MaxBytes = 8
			},
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.NewReplacer("{bytes}", strconv.Itoa(len(glb)), "{limit}", "8").
					Replace(review.Texts.TooLarge)
			},
		},
		{
			name: "the bytes cannot be read",
			task: loaded,
			args: `{"model":"task-a"}`,
			assets: func(assets *reviewAssets) {
				assets.openErr = &generation.Error{Code: generation.ErrUnavailable, Operation: "open_asset"}
			},
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.ReplaceAll(review.Texts.SourceReadFailed, "{reason}", "the asset store could not open the saved file")
			},
		},
		{
			name: "the bytes are not a GLB",
			task: func() generation.Task {
				broken := loaded
				broken.Assets = []generation.Asset{{
					ID: "task-a-glb", TaskID: "task-a", Role: "model", Variant: "glb", StorageKey: "key/glb",
					MediaType: "model/gltf-binary", Size: 11, SHA256: reviewDigest([]byte("not a model")),
				}}
				return broken
			}(),
			args: `{"model":"task-a"}`,
			assets: func(assets *reviewAssets) {
				assets.contents["task-a-glb"] = []byte("not a model")
			},
			want: func(review catalog.ChatModelReviewSchema) string {
				return review.Texts.Unsupported[:strings.Index(review.Texts.Unsupported, "{reason}")]
			},
			partial: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			gateway := newReviewGateway(tt.task)
			assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
			if tt.assets != nil {
				tt.assets(assets)
			}
			executor, schema := reviewExecutor(t, gateway, assets, tt.mutate)
			result, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", tt.args))
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || result.Error == "" || result.TaskID != "" {
				t.Fatalf("refusal = %+v", result)
			}
			if want := tt.want(schema.ModelReview); (tt.partial && (!strings.HasPrefix(result.Error, want) || result.Error == want)) ||
				(!tt.partial && result.Error != want) {
				t.Fatalf("refusal = %q, want prefix %q (partial=%v)", result.Error, want, tt.partial)
			}
			if gateway.submitted != 0 {
				t.Fatal("a refused inspection submitted a Task")
			}
		})
	}
}

func TestModelReviewRenderStoresEachViewAndReportsItsPicture(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, schema := reviewExecutor(t, gateway, assets, nil)

	result, err := executor.RenderModelViews(context.Background(),
		reviewCommand("render_model_views", `{"model":"task-a","views":["front","back"],"size":32}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || !result.NoSubmission || result.TaskID != "" {
		t.Fatalf("render result = %+v", result)
	}
	if len(result.ImageURLs) != 2 || len(result.Pictures) != 2 {
		t.Fatalf("images = %v pictures = %v", result.ImageURLs, result.Pictures)
	}
	payload := reviewPayload(t, result)
	if payload["ok"] != true || payload["render_calls_remaining"] != float64(schema.ModelReview.Render.MaxRenderCallsPerTurn-1) {
		t.Fatalf("payload = %v", payload)
	}
	views := payload["views"].([]any)
	if len(views) != 2 || len(payload["failed_views"].([]any)) != 0 {
		t.Fatalf("views = %v failed = %v", views, payload["failed_views"])
	}
	for index, name := range []string{"front", "back"} {
		view := views[index].(map[string]any)
		if view["view"] != name || view["mode"] != "textured" || view["width"] != float64(32) || view["height"] != float64(32) {
			t.Fatalf("view %d = %v", index, view)
		}
		if result.Pictures[index].URL != result.ImageURLs[index] || !strings.Contains(result.ImageURLs[index], "render-") {
			t.Fatalf("picture %d = %+v urls=%v", index, result.Pictures[index], result.ImageURLs)
		}
	}
	if !strings.Contains(payload["note"].(string), strings.ReplaceAll(schema.ModelReview.Texts.ImagesAttached, "{count}", "2")) {
		t.Fatalf("note = %q", payload["note"])
	}
	if len(assets.uploads) != 2 {
		t.Fatalf("uploads = %d", len(assets.uploads))
	}
	for index, upload := range assets.uploads {
		if upload.MediaType != "image/png" || upload.AssetType != schema.ModelReview.Render.AssetType ||
			!strings.HasSuffix(upload.OriginalFilename, ".png") || assets.uploadOwner[index] != "owner-a" {
			t.Fatalf("upload %d = %+v owner=%q", index, upload, assets.uploadOwner[index])
		}
		decoded, err := png.Decode(bytes.NewReader(assets.uploaded[index]))
		if err != nil || decoded.Bounds().Dx() != 32 || decoded.Bounds().Dy() != 32 {
			t.Fatalf("upload %d is not a 32x32 PNG: %v", index, err)
		}
	}
	if gateway.submitted != 0 {
		t.Fatal("rendering submitted a Task")
	}
}

func TestModelReviewRenderCapsSizeAtTheSchemaMaximum(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, schema := reviewExecutor(t, gateway, assets, nil)
	result, err := executor.RenderModelViews(context.Background(),
		reviewCommand("render_model_views", `{"model":"task-a","size":4096}`))
	if err != nil || result.Error != "" {
		t.Fatalf("render = %+v err=%v", result, err)
	}
	payload := reviewPayload(t, result)
	view := payload["views"].([]any)[0].(map[string]any)
	if view["width"] != float64(schema.ModelReview.Render.MaxSize) {
		t.Fatalf("width = %v, want the Schema maximum %d", view["width"], schema.ModelReview.Render.MaxSize)
	}
}

func TestModelReviewRenderRefusesBadOrExhaustedCallsBeforeReadingTheModel(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	cases := []struct {
		name   string
		args   string
		calls  []chat.ToolCall
		mutate func(*catalog.ApplicationSchema)
		want   func(catalog.ChatModelReviewSchema) string
		// readsModel is true when the refusal needs the parsed report, so the
		// saved file is opened before the call is refused.
		readsModel bool
	}{
		{
			name: "too many views",
			args: `{"model":"task-a","views":["front","back","left"]}`,
			mutate: func(app *catalog.ApplicationSchema) {
				app.Chat.ModelReview.Render.MaxViewsPerCall = 2
			},
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.NewReplacer("{count}", "3", "{limit}", "2").Replace(review.Texts.TooManyViews)
			},
		},
		{
			name: "unknown view",
			args: `{"model":"task-a","views":["under"]}`,
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.NewReplacer("{view}", "under", "{views}", strings.Join(review.Render.Views, ", ")).
					Replace(review.Texts.UnknownView)
			},
		},
		{
			name: "unknown mode",
			args: `{"model":"task-a","mode":"xray"}`,
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.NewReplacer("{mode}", "xray", "{modes}", strings.Join(review.Render.Modes, ", ")).
					Replace(review.Texts.UnknownMode)
			},
		},
		{
			name: "isolate without parts",
			args: `{"model":"task-a","isolate":true}`,
			want: func(review catalog.ChatModelReviewSchema) string { return review.Texts.PartsRequired },
		},
		{
			name: "unknown part",
			args: `{"model":"task-a","parts":["n9.p0"]}`,
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.ReplaceAll(review.Texts.UnknownPart, "{part}", "n9.p0")
			},
			readsModel: true,
		},
		{
			name: "wireframe above the triangle bound",
			args: `{"model":"task-a","mode":"wireframe"}`,
			mutate: func(app *catalog.ApplicationSchema) {
				app.Chat.ModelReview.Render.WireframeMaxTriangles = 1
			},
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.NewReplacer("{triangles}", "2", "{limit}", "1").Replace(review.Texts.WireframeTooDense)
			},
			readsModel: true,
		},
		{
			name:  "the reply used its render calls",
			args:  `{"model":"task-a"}`,
			calls: []chat.ToolCall{{Name: "render_model_views"}, {Name: "compare_models"}},
			mutate: func(app *catalog.ApplicationSchema) {
				app.Chat.ModelReview.Render.MaxRenderCallsPerTurn = 2
			},
			want: func(review catalog.ChatModelReviewSchema) string {
				return strings.ReplaceAll(review.Texts.BudgetExhausted, "{limit}", "2")
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
			assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
			executor, schema := reviewExecutor(t, gateway, assets, tt.mutate)
			command := reviewCommand("render_model_views", tt.args)
			command.EarlierCalls = tt.calls
			result, err := executor.RenderModelViews(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoSubmission || result.Error == "" || len(result.ImageURLs) != 0 {
				t.Fatalf("refusal = %+v", result)
			}
			if want := tt.want(schema.ModelReview); result.Error != want {
				t.Fatalf("refusal = %q, want %q", result.Error, want)
			}
			if len(assets.uploads) != 0 {
				t.Fatalf("refused render uploaded %d pictures", len(assets.uploads))
			}
			opens := 0
			if tt.readsModel {
				opens = 1
			}
			if len(assets.opens) != opens {
				t.Fatalf("refused render opened %v, want %d", assets.opens, opens)
			}
		})
	}
}

func TestModelReviewRenderReportsAStorageFailureWithoutFailingTheCall(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	assets.uploadErr = errors.New("storage is full")
	executor, schema := reviewExecutor(t, gateway, assets, nil)
	result, err := executor.RenderModelViews(context.Background(), reviewCommand("render_model_views", `{"model":"task-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ImageURLs) != 0 || len(result.Pictures) != 0 {
		t.Fatalf("failed upload produced pictures: %+v", result)
	}
	payload := reviewPayload(t, result)
	if payload["ok"] != false || len(payload["views"].([]any)) != 0 || len(payload["failed_views"].([]any)) != 1 {
		t.Fatalf("payload = %v", payload)
	}
	failure := payload["failed_views"].([]any)[0].(map[string]any)
	if want := strings.ReplaceAll(schema.ModelReview.Texts.UploadFailed, "{reason}", "the asset store rejected the picture"); failure["error"] != want {
		t.Fatalf("failure = %v, want %q", failure, want)
	}
}

func TestModelReviewRenderSaysWhenTheAgentCannotSeeItsPictures(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, schema := reviewExecutor(t, gateway, assets, func(app *catalog.ApplicationSchema) {
		app.Chat.Agent.Vision = false
	})
	result, err := executor.RenderModelViews(context.Background(), reviewCommand("render_model_views", `{"model":"task-a"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("render = %+v err=%v", result, err)
	}
	// The pictures and their handles stay available to the reply; only the
	// model cannot see them, and the note says so.
	if len(result.ImageURLs) != 1 || !strings.Contains(reviewPayload(t, result)["note"].(string), schema.ModelReview.Texts.VisionOff) {
		t.Fatalf("result = %+v", result)
	}
}

func TestModelReviewComparePutsTheModelsSideBySideAndReportsTheDifference(t *testing.T) {
	small := reviewModelGLB(t, 1, 1)
	large := reviewModelGLB(t, 2, 1)
	gateway := newReviewGateway(
		reviewTask("task-a", "owner-a", "session-a", small, generation.TaskSucceeded),
		reviewTask("task-b", "owner-a", "session-a", large, generation.TaskSucceeded),
	)
	assets := newReviewAssets(map[generation.AssetID][]byte{
		"task-a-glb": small,
		"task-b-glb": large,
	})
	executor, schema := reviewExecutor(t, gateway, assets, nil)
	result, err := executor.CompareModels(context.Background(),
		reviewCommand("compare_models", `{"models":["task-a","task-b"],"views":["front"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || len(result.ImageURLs) != 1 || len(result.Pictures) != 1 {
		t.Fatalf("compare = %+v err=%v", result, err)
	}
	payload := reviewPayload(t, result)
	if payload["ok"] != true {
		t.Fatalf("payload = %v", payload)
	}
	models := payload["models"].([]any)
	if len(models) != 2 || models[0].(map[string]any)["task_id"] != "task-a" || models[1].(map[string]any)["task_id"] != "task-b" {
		t.Fatalf("models = %v", models)
	}
	differences := payload["differences"].(map[string]any)
	if differences["triangles_b_minus_a"] != float64(0) || differences["vertices_b_minus_a"] != float64(0) {
		t.Fatalf("differences = %v", differences)
	}
	box := differences["bbox_size_b_minus_a"].([]any)
	for index, want := range []float64{2, 2, 0} {
		if box[index] != want {
			t.Fatalf("bbox difference = %v, want %v", box, []float64{2, 2, 0})
		}
	}
	view := payload["views"].([]any)[0].(map[string]any)
	if view["left_task_id"] != "task-a" || view["right_task_id"] != "task-b" ||
		view["width"] != float64(schema.ModelReview.Render.Size*2+4) || view["height"] != float64(schema.ModelReview.Render.Size) {
		t.Fatalf("view = %v", view)
	}
	decoded, err := png.Decode(bytes.NewReader(assets.uploaded[0]))
	if err != nil || decoded.Bounds().Dx() != schema.ModelReview.Render.Size*2+4 {
		t.Fatalf("side-by-side picture = %v, err=%v", decoded.Bounds(), err)
	}
	if result.Pictures[0].URL != result.ImageURLs[0] {
		t.Fatalf("picture = %+v urls = %v", result.Pictures[0], result.ImageURLs)
	}
}

func TestModelReviewCompareRequiresExactlyTwoModels(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, schema := reviewExecutor(t, gateway, assets, nil)
	result, err := executor.CompareModels(context.Background(),
		reviewCommand("compare_models", `{"models":["task-a","task-a","task-a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(schema.ModelReview.Texts.TooManyModels, "{count}", "3"); result.Error != want {
		t.Fatalf("refusal = %q, want %q", result.Error, want)
	}
	if len(assets.opens) != 0 {
		t.Fatal("an invalid comparison read the model")
	}
}

func TestModelReviewToolsReportUnavailableWithoutTheirGateways(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	executor, err := generationtool.New(generationtool.Config{
		Gateway: newReviewGateway(), References: &fakeReferences{}, Chat: app.Chat, TaskStates: app.TaskStates,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(context.Context, chat.ToolCommand) (chat.ToolResult, error){
		"render_model_views": executor.RenderModelViews,
		"compare_models":     executor.CompareModels,
	} {
		result, err := run(context.Background(), reviewCommand(name, `{}`))
		if err != nil || !result.NoSubmission || result.Error == "" {
			t.Fatalf("%s without gateways = %+v err=%v", name, result, err)
		}
	}
}

func reviewUsageItem(t *testing.T, payload map[string]any, check string) map[string]any {
	t.Helper()
	usage, ok := payload["usage_check"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no usage_check: %v", payload)
	}
	items, _ := usage["checks"].([]any)
	for _, item := range items {
		object, ok := item.(map[string]any)
		if ok && object["check"] == check {
			return object
		}
	}
	t.Fatalf("usage_check has no %q item: %v", check, usage)
	return nil
}

// TestModelReviewInspectReportsThePurposeCheckAndFiltersFixHints wires
// inspect_model end to end: the shipped Catalog's default purpose judges the
// saved GLB, only enabled Offerings the runtime can reach are named in a fix
// hint, and a frozen brief purpose with a triangle_budget overrides the
// default.
func TestModelReviewInspectReportsThePurposeCheckAndFiltersFixHints(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	gateway.offerings = map[generation.Capability][]generation.Offering{
		"resize": {{
			ID: "test/resize", Capability: "resize", Enabled: true,
			Parameters: []generation.Parameter{{
				Name: "origin_at", Type: "text", Default: json.RawMessage(`"bottom"`),
			}},
		}},
		"format-conversion": {{
			ID: "test/format-conversion", Capability: "format-conversion", Enabled: true,
			Parameters: []generation.Parameter{{
				Name: "pivot_to_center_bottom", Type: "boolean", Default: json.RawMessage(`true`),
			}},
		}},
		"remesh":    {{ID: "test/remesh", Capability: "remesh", Enabled: true}},
		"uv-unwrap": {{ID: "test/uv-unwrap", Capability: "uv-unwrap", Enabled: true}},
	}
	gateway.unavailable = map[generation.OfferingID]bool{"test/format-conversion": true}
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, schema := reviewExecutor(t, gateway, assets, nil)

	// Without a brief the Schema's default purpose is used and says so.
	result, err := executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	payload := reviewPayload(t, result)
	usage := payload["usage_check"].(map[string]any)
	if usage["purpose"] != "render" || usage["purpose_source"] != "default_profile" {
		t.Fatalf("usage purpose = %v/%v, want the default render purpose", usage["purpose"], usage["purpose_source"])
	}
	if note, _ := usage["note"].(string); !strings.Contains(note, "默认用途") || !strings.Contains(note, "展示/渲染") {
		t.Fatalf("usage note = %q", note)
	}
	if items, _ := usage["checks"].([]any); len(items) != len(catalog.ModelReviewUsageCheckIDs()) {
		t.Fatalf("checks = %d, want %d", len(items), len(catalog.ModelReviewUsageCheckIDs()))
	}
	for check, want := range map[string]string{
		"triangles":           "pass",
		"uv":                  "unknown",
		"watertight":          "unknown",
		"skin":                "unknown",
		"texture_size":        "pass",
		"pivot_bottom_center": "fail",
		"y_up":                "unknown",
		"plausible_scale":     "unknown",
		"debris":              "pass",
		"degenerate":          "pass",
	} {
		if got := reviewUsageItem(t, payload, check)["status"]; got != want {
			t.Errorf("%s status = %v, want %s", check, got, want)
		}
	}
	pivot := reviewUsageItem(t, payload, "pivot_bottom_center")
	hint, _ := pivot["fix_hint"].(string)
	if !strings.Contains(hint, "resize(origin_at=bottom)") {
		t.Fatalf("pivot fix hint = %q, want the enabled resize capability", hint)
	}
	if strings.Contains(hint, "format-conversion") {
		t.Fatalf("pivot fix hint = %q names an unavailable Offering", hint)
	}
	if got := reviewNumber(t, pivot, "budget", "maximum_ratio"); got != schema.ModelReview.Inspect.Production.PivotBottomCenterRatio {
		t.Fatalf("pivot maximum_ratio = %v", got)
	}
	if got := reviewNumber(t, pivot, "actual"); got <= schema.ModelReview.Inspect.Production.PivotBottomCenterRatio {
		t.Fatalf("pivot actual ratio = %v, want above the tolerance", got)
	}

	// An unavailable Offering and a disabled one both disappear from the hint.
	gateway.unavailable = map[generation.OfferingID]bool{"test/resize": true, "test/format-conversion": true}
	result, err = executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	hint, _ = reviewUsageItem(t, reviewPayload(t, result), "pivot_bottom_center")["fix_hint"].(string)
	if hint != schema.ModelReview.Inspect.Production.NoCapabilityText {
		t.Fatalf("fix hint = %q, want the no-capability text when every option is unavailable", hint)
	}
	gateway.unavailable = map[generation.OfferingID]bool{"test/format-conversion": true}
	gateway.offerings["resize"][0].Enabled = false
	result, err = executor.InspectModel(context.Background(), reviewCommand("inspect_model", `{"model":"task-a"}`))
	if err != nil || result.Error != "" {
		t.Fatalf("inspect = %+v err=%v", result, err)
	}
	hint, _ = reviewUsageItem(t, reviewPayload(t, result), "pivot_bottom_center")["fix_hint"].(string)
	if strings.Contains(hint, "resize") || hint != schema.ModelReview.Inspect.Production.NoCapabilityText {
		t.Fatalf("fix hint = %q names a disabled Offering", hint)
	}

	// A frozen brief selects its purpose and its triangle_budget overrides the
	// profile maximum; the fixes for the failed checks name the enabled tools.
	command := reviewCommand("inspect_model", `{"model":"task-a"}`)
	command.Options.ProductionBrief = chat.ProductionBrief{
		"purpose":         []byte(`"game_character"`),
		"triangle_budget": []byte(`100`),
	}
	result, err = executor.InspectModel(context.Background(), command)
	if err != nil || result.Error != "" {
		t.Fatalf("brief inspect = %+v err=%v", result, err)
	}
	payload = reviewPayload(t, result)
	usage = payload["usage_check"].(map[string]any)
	if usage["purpose"] != "game_character" || usage["purpose_source"] != "production_brief" {
		t.Fatalf("usage purpose = %v/%v, want the brief purpose", usage["purpose"], usage["purpose_source"])
	}
	if note, _ := usage["note"].(string); !strings.Contains(note, "游戏/实时·角色") || !strings.Contains(note, "100") {
		t.Fatalf("usage note = %q, want the brief purpose and the override", note)
	}
	triangles := reviewUsageItem(t, payload, "triangles")
	if triangles["status"] != "fail" {
		t.Fatalf("triangles = %v, want a failure below the character minimum", triangles)
	}
	if got, _ := triangles["budget"].(map[string]any)["maximum"].(float64); got != 100 {
		t.Fatalf("triangle maximum = %v, want the brief override 100", got)
	}
	if hint, _ := triangles["fix_hint"].(string); !strings.Contains(hint, "remesh") {
		t.Fatalf("triangle fix hint = %q, want remesh", hint)
	}
	uv := reviewUsageItem(t, payload, "uv")
	if uv["status"] != "fail" {
		t.Fatalf("uv = %v, want a failure for a quad without TEXCOORD_0", uv)
	}
	if hint, _ := uv["fix_hint"].(string); !strings.Contains(hint, "uv-unwrap") {
		t.Fatalf("uv fix hint = %q, want uv-unwrap", hint)
	}
	if got := reviewUsageItem(t, payload, "plausible_scale")["status"]; got != "pass" {
		t.Fatalf("scale status = %v, want pass for a 2 m model", got)
	}
}

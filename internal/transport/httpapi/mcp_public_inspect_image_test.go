package httpapi_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// gifPicture is a 1×1 GIF. The picture inspection decodes PNG, JPEG and WebP
// only, so a GIF is the readable-upload case the inspection refuses.
func gifPicture(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.RGBA{R: 0, G: 0, B: 0, A: 255}}
	picture := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	var buffer bytes.Buffer
	if err := gif.Encode(&buffer, picture, nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func mcpInspectionImage(t *testing.T, result toolResult) map[string]any {
	t.Helper()
	picture, _ := result.structured["image"].(map[string]any)
	if picture == nil {
		t.Fatalf("the result carries no image: %s", result.raw)
	}
	return picture
}

func mcpInspectionUsage(t *testing.T, result toolResult) map[string]any {
	t.Helper()
	usage, _ := result.structured["usage_check"].(map[string]any)
	if usage == nil {
		t.Fatalf("the result carries no usage_check: %s", result.raw)
	}
	return usage
}

func mcpInspectionCheck(t *testing.T, usage map[string]any, name string) map[string]any {
	t.Helper()
	for _, raw := range usage["checks"].([]any) {
		check := raw.(map[string]any)
		if check["check"] == name {
			return check
		}
	}
	return nil
}

// #332 step 7b: inspect_image reports one uploaded picture the way the
// platform tool does, through a read key, and both scopes see the read-only
// tool. It never spends a 3D step.
func TestMCPInspectImageReportsAnUploadedPicture(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	generateKey, readKey := f.issueKey("owner-a", "generate"), f.issueKey("owner-a", "read")
	imageID := f.upload(generateKey, testPNG(t, 64))
	stepsBefore := f.call(readKey, "get_status", nil).structured["total_used_steps"]

	result := f.call(readKey, "inspect_image", map[string]any{"image": imageID})
	if result.code != http.StatusOK || result.isError {
		t.Fatalf("inspect_image = %d %s", result.code, result.raw)
	}
	picture := mcpInspectionImage(t, result)
	if picture["source"] != "input_asset" || picture["input_asset_id"] != imageID ||
		picture["task_id"] != nil || picture["asset_id"] != nil ||
		picture["media_type"] != "image/png" || picture["size"].(float64) == 0 {
		t.Errorf("image = %v", picture)
	}
	report := result.structured["report"].(map[string]any)
	if report["format"] != "png" || report["width"].(float64) != 64 || report["height"].(float64) != 64 ||
		report["short_edge"].(float64) != 64 {
		t.Errorf("report = %v", report)
	}
	usage := mcpInspectionUsage(t, result)
	if usage["checklist"] != "delivery" || usage["purpose"] != "concept_art" ||
		usage["purpose_label"] != "概念设定图" || usage["purpose_source"] != "default_profile" {
		t.Errorf("usage_check = %v", usage)
	}
	if note, _ := usage["note"].(string); note == "" {
		t.Errorf("the default purpose carries no note: %v", usage)
	}
	shortEdge := mcpInspectionCheck(t, usage, "short_edge")
	if shortEdge == nil || shortEdge["status"] != "fail" || shortEdge["hint"] == nil || shortEdge["hint"] == "" {
		t.Errorf("short_edge = %v", shortEdge)
	}
	assertServedSchema(t, f, "inspect_image", result)
	if after := f.call(readKey, "get_status", nil).structured["total_used_steps"]; after != stepsBefore {
		t.Fatalf("inspection changed the used steps: %v -> %v", stepsBefore, after)
	}
	// Both scopes see the read-only tool.
	for _, key := range []string{readKey, generateKey} {
		listed := false
		for _, raw := range decodeToolResultRaw(t, f.post(key, "tools/list", nil))["tools"].([]any) {
			listed = listed || raw.(map[string]any)["name"] == "inspect_image"
		}
		if !listed {
			t.Errorf("the key does not list inspect_image")
		}
	}
	// The empty asset fields are written, not omitted.
	if _, present := picture["input_asset_id"]; !present {
		t.Errorf("input_asset_id is missing from %v", picture)
	}
}

// #332 step 7b: an explicit purpose judges this inspection only, and it cannot
// be combined with the 3d_input checklist.
func TestMCPInspectImagePurposeOverride(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	imageID := f.upload(key, testPNG(t, 64))

	result := f.call(key, "inspect_image", map[string]any{"image": imageID, "purpose": "illustration"})
	if result.code != http.StatusOK || result.isError {
		t.Fatalf("inspect_image with a purpose = %d %s", result.code, result.raw)
	}
	usage := mcpInspectionUsage(t, result)
	if usage["purpose"] != "illustration" || usage["purpose_label"] != "角色立绘 / 宣传插画" ||
		usage["purpose_source"] != "argument" || usage["note"] != nil {
		t.Errorf("usage_check = %v", usage)
	}
	if shortEdge := mcpInspectionCheck(t, usage, "short_edge"); shortEdge == nil || shortEdge["status"] != "fail" {
		t.Errorf("short_edge = %v", shortEdge)
	}
	assertServedSchema(t, f, "inspect_image", result)

	conflict := f.call(key, "inspect_image", map[string]any{
		"image": imageID, "checklist": "3d_input", "purpose": "illustration",
	})
	if conflict.code != http.StatusOK || !conflict.isError || conflict.problemCode() != "invalid_arguments" {
		t.Fatalf("purpose with 3d_input = %d %s", conflict.code, conflict.raw)
	}
	assertServedSchema(t, f, "inspect_image", conflict)

	unknown := f.call(key, "inspect_image", map[string]any{"image": imageID, "purpose": "no_such_purpose"})
	if unknown.code != http.StatusOK || !unknown.isError || unknown.problemCode() != "invalid_arguments" {
		t.Fatalf("unknown purpose = %d %s", unknown.code, unknown.raw)
	}
	if message := unknown.structured["error"].(map[string]any)["message"].(string); !bytes.Contains([]byte(message), []byte("illustration")) {
		t.Errorf("the refusal does not name the available purposes: %q", message)
	}
	assertServedSchema(t, f, "inspect_image", unknown)
}

// #332 step 7b: the 3d_input checklist runs the platform's deterministic
// checks and lists the visual items; MCP takes no verdict on them.
func TestMCPInspectImage3DInputListsTheVisualItems(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	imageID := f.upload(key, testPNG(t, 128))

	result := f.call(key, "inspect_image", map[string]any{"image": imageID, "checklist": "3d_input"})
	if result.code != http.StatusOK || result.isError {
		t.Fatalf("inspect_image 3d_input = %d %s", result.code, result.raw)
	}
	usage := mcpInspectionUsage(t, result)
	if usage["checklist"] != "3d_input" || usage["purpose"] != nil || usage["purpose_label"] != nil ||
		usage["purpose_source"] != nil || usage["note"] != nil {
		t.Errorf("usage_check = %v", usage)
	}
	for _, name := range []string{"nonblank", "background", "subject_size", "edge", "components"} {
		if mcpInspectionCheck(t, usage, name) == nil {
			t.Errorf("the 3d_input checks lack %q: %v", name, usage["checks"])
		}
	}
	items, _ := result.structured["visual_items"].([]any)
	if len(items) != 3 {
		t.Fatalf("visual_items = %v", result.structured["visual_items"])
	}
	want := map[string]bool{"pose": true, "perspective": true, "cast_shadow": true}
	for _, raw := range items {
		item := raw.(map[string]any)
		id, _ := item["id"].(string)
		if !want[id] || item["label"] == "" || item["instruction"] == "" || item["fix_hint"] == "" {
			t.Errorf("visual item = %v", item)
		}
		// Only the deterministic checks carry a verdict.
		if _, verdict := item["status"]; verdict {
			t.Errorf("MCP judged a visual item: %v", item)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Errorf("missing visual items: %v", want)
	}
	assertServedSchema(t, f, "inspect_image", result)
}

// #332 step 7b: a succeeded 2D Task's saved picture is inspectable by its task
// id (primary output or a named one) and by its picture ID, and the status
// block names the asset the task belongs to.
func TestMCPInspectImageReadsTaskPictures(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	task := f.succeededTask("owner-a", "", "inspect-image-task")
	if len(task.Assets) == 0 {
		t.Fatal("the succeeded task has no saved picture")
	}
	output := string(task.Assets[0].ID)

	primary := f.call(key, "inspect_image", map[string]any{"task_id": string(task.ID)})
	if primary.code != http.StatusOK || primary.isError {
		t.Fatalf("inspect_image by task = %d %s", primary.code, primary.raw)
	}
	picture := mcpInspectionImage(t, primary)
	if picture["source"] != "task_asset" || picture["task_id"] != string(task.ID) ||
		picture["asset_id"] != output || picture["input_asset_id"] != nil {
		t.Errorf("image = %v", picture)
	}
	if report := primary.structured["report"].(map[string]any); report["width"].(float64) <= 0 {
		t.Errorf("report = %v", report)
	}
	if asset, ok := primary.status(t)["asset"].(map[string]any); !ok || asset["id"] != task.SessionID {
		t.Errorf("the status block is not about the task's asset: %s", primary.raw)
	}
	assertServedSchema(t, f, "inspect_image", primary)

	named := f.call(key, "inspect_image", map[string]any{"task_id": string(task.ID), "asset_id": output})
	if named.code != http.StatusOK || named.isError || mcpInspectionImage(t, named)["asset_id"] != output {
		t.Fatalf("inspect_image by asset_id = %d %s", named.code, named.raw)
	}
	assertServedSchema(t, f, "inspect_image", named)

	byImage := f.call(key, "inspect_image", map[string]any{"image": output})
	if byImage.code != http.StatusOK || byImage.isError {
		t.Fatalf("inspect_image by picture ID = %d %s", byImage.code, byImage.raw)
	}
	if got := mcpInspectionImage(t, byImage); got["source"] != "task_asset" ||
		got["task_id"] != string(task.ID) || got["asset_id"] != output {
		t.Errorf("image = %v", got)
	}
	if asset, ok := byImage.status(t)["asset"].(map[string]any); !ok || asset["id"] != task.SessionID {
		t.Errorf("the task picture lost its asset: %s", byImage.raw)
	}
	assertServedSchema(t, f, "inspect_image", byImage)

	// Another person's task is not found, and nothing is inspected.
	other := f.succeededTask("owner-b", "", "inspect-image-other")
	foreign := f.call(key, "inspect_image", map[string]any{"task_id": string(other.ID)})
	if foreign.code != http.StatusOK || !foreign.isError || foreign.problemCode() != "task_not_found" {
		t.Fatalf("a foreign task = %d %s", foreign.code, foreign.raw)
	}
	assertServedSchema(t, f, "inspect_image", foreign)
	if unknown := f.call(key, "inspect_image", map[string]any{"image": "asset_missing"}); unknown.code != http.StatusOK ||
		!unknown.isError || unknown.problemCode() != "image_not_found" {
		t.Fatalf("an unknown picture = %d %s", unknown.code, unknown.raw)
	} else {
		assertServedSchema(t, f, "inspect_image", unknown)
	}
	if missing := f.call(key, "inspect_image", map[string]any{"task_id": string(task.ID), "asset_id": "asset_missing"}); missing.code != http.StatusOK ||
		!missing.isError || missing.problemCode() != "asset_not_found" {
		t.Fatalf("an unknown task asset = %d %s", missing.code, missing.raw)
	} else {
		assertServedSchema(t, f, "inspect_image", missing)
	}

	// A task output that is not a picture is refused with the asset_not_image
	// wording, and a task left without a picture is not ready.
	f.exec(`UPDATE generation_assets SET role = 'model' WHERE id = ?`, output)
	notImage := f.call(key, "inspect_image", map[string]any{"task_id": string(task.ID), "asset_id": output})
	if notImage.code != http.StatusOK || !notImage.isError || notImage.problemCode() != "asset_not_found" {
		t.Fatalf("a non-picture asset = %d %s", notImage.code, notImage.raw)
	}
	message := notImage.structured["error"].(map[string]any)["message"].(string)
	if !bytes.Contains([]byte(message), []byte("不是可检查的图片")) {
		t.Errorf("the refusal does not explain the asset is not a picture: %q", message)
	}
	assertServedSchema(t, f, "inspect_image", notImage)
	byPictureID := f.call(key, "inspect_image", map[string]any{"image": output})
	if byPictureID.code != http.StatusOK || !byPictureID.isError || byPictureID.problemCode() != "asset_not_found" {
		t.Fatalf("a non-picture pictured by ID = %d %s", byPictureID.code, byPictureID.raw)
	}
	assertServedSchema(t, f, "inspect_image", byPictureID)
	notReady := f.call(key, "inspect_image", map[string]any{"task_id": string(task.ID)})
	if notReady.code != http.StatusOK || !notReady.isError || notReady.problemCode() != "image_not_ready" {
		t.Fatalf("a task without a picture = %d %s", notReady.code, notReady.raw)
	}
	assertServedSchema(t, f, "inspect_image", notReady)
}

// #332 step 7b: a picture that cannot be decoded is a business error that says
// which formats are supported.
func TestMCPInspectImageRefusesUnreadablePictures(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	imageID := f.upload(key, gifPicture(t))

	result := f.call(key, "inspect_image", map[string]any{"image": imageID})
	if result.code != http.StatusOK || !result.isError || result.problemCode() != "image_unreadable" {
		t.Fatalf("a GIF = %d %s", result.code, result.raw)
	}
	message := result.structured["error"].(map[string]any)["message"].(string)
	if !bytes.Contains([]byte(message), []byte("PNG/JPEG/WebP")) {
		t.Errorf("the refusal does not name the supported formats: %q", message)
	}
	assertServedSchema(t, f, "inspect_image", result)
}

// #332 step 7b: the MCP pixel cap is the narrower of the two limits and its
// refusal carries the concrete bounds.
func TestMCPInspectImageRefusesPicturesOverThePixelCap(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil, func(application *catalog.ApplicationSchema) {
		application.MCPPublic.InspectImage.MaxPixels = 1
	})
	key := f.issueKey("owner-a", "generate")
	imageID := f.upload(key, testPNG(t, 2))

	result := f.call(key, "inspect_image", map[string]any{"image": imageID})
	if result.code != http.StatusOK || !result.isError || result.problemCode() != "image_too_large" {
		t.Fatalf("a picture over the pixel cap = %d %s", result.code, result.raw)
	}
	problem := result.structured["error"].(map[string]any)
	message := problem["message"].(string)
	if !bytes.Contains([]byte(message), []byte("1")) || !bytes.Contains([]byte(message), []byte("字节")) {
		t.Errorf("the refusal does not carry the bounds: %q", message)
	}
	assertServedSchema(t, f, "inspect_image", result)
}

// #332 step 7b: a caller names a picture exactly one way, and a purpose needs
// the delivery checklist.
func TestMCPInspectImageRefusesMalformedArguments(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	imageID := f.upload(f.issueKey("owner-a", "generate"), testPNG(t, 8))
	for name, arguments := range map[string]map[string]any{
		"neither source":     {},
		"both sources":       {"image": imageID, "task_id": "task_x"},
		"asset without task": {"asset_id": "asset_x"},
		"unknown checklist":  {"image": imageID, "checklist": "no_such_checklist"},
	} {
		result := f.call(key, "inspect_image", arguments)
		if result.code != http.StatusOK || !result.isError || result.problemCode() != "invalid_arguments" {
			t.Errorf("%s = %d %s", name, result.code, result.raw)
		}
		assertServedSchema(t, f, "inspect_image", result)
	}
}

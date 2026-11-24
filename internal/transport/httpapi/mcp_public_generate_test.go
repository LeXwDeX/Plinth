package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

// testPNG is a real picture: the services decode what they are given.
func testPNG(t *testing.T, side int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, side, side))
	for x := 0; x < side; x++ {
		for y := 0; y < side; y++ {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// upload sends a picture through upload_image and returns its ID.
func (f *mcpFixture) upload(key string, data []byte) string {
	f.t.Helper()
	result := f.call(key, "upload_image", map[string]any{"data": base64.StdEncoding.EncodeToString(data), "filename": "ref.png"})
	image, _ := result.structured["image"].(map[string]any)
	id, _ := image["id"].(string)
	if result.isError || id == "" {
		f.t.Fatalf("upload_image = %s", result.raw)
	}
	return id
}

// finish runs a Task of the owner until it ends and returns it.
func (f *mcpFixture) finish(owner, taskID string) generation.Task {
	f.t.Helper()
	for range 60 {
		if _, err := f.module.ReconcileOnce(context.Background(), generation.CoordinatorConfig{
			WorkerID: "mcp-generate-test", LeaseFor: time.Minute, BatchSize: 20,
		}); err != nil {
			f.t.Fatal(err)
		}
		task, err := f.module.Get(context.Background(), generation.OwnerID(owner), generation.TaskID(taskID))
		if err != nil {
			f.t.Fatal(err)
		}
		if task.State.Terminal() {
			return task
		}
		time.Sleep(12 * time.Millisecond)
	}
	f.t.Fatalf("task %s did not end", taskID)
	return generation.Task{}
}

var keySequence = struct {
	sync.Mutex
	n int
}{}

// idem is a fresh idempotency key.
func idem(label string) string {
	keySequence.Lock()
	defer keySequence.Unlock()
	keySequence.n++
	return fmt.Sprintf("%s-%04d-test-key", label, keySequence.n)
}

func taskID(t *testing.T, result toolResult) string {
	t.Helper()
	id, _ := result.structured["task_id"].(string)
	if result.isError || id == "" {
		t.Fatalf("no task started: %s", result.raw)
	}
	return id
}

func (f *mcpFixture) setMCPSteps(owner string, setting approvalpolicy.StepSetting) {
	f.t.Helper()
	version := int64(0)
	if stored, found, err := f.repository.AccountSettings(context.Background(), owner); err != nil {
		f.t.Fatal(err)
	} else if found {
		version = stored.Version
	}
	if _, err := f.repository.PutAccountSettings(context.Background(), owner, version, accountsettings.Settings{
		List: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20}, MCP: setting,
	}, time.Now().UTC()); err != nil {
		f.t.Fatal(err)
	}
}

// settle ends every task of the owner that is still running, so the person's
// in-flight limit is not what a test is about.
func (f *mcpFixture) settle(owner string) {
	f.t.Helper()
	f.exec(`UPDATE generation_tasks SET state = 'SUCCEEDED' WHERE owner_id = ? AND state NOT IN ('SUCCEEDED', 'FAILED', 'CANCELED')`, owner)
}

func (f *mcpFixture) assetRows(owner string) int {
	return f.countRows(`SELECT COUNT(*) FROM mcp_assets WHERE owner_id = ?`, owner)
}

// Every start tool answers with its declared output and the status block, a
// refusal included, and the served schema holds for both.
func assertServedSchema(t *testing.T, f *mcpFixture, tool string, result toolResult) {
	t.Helper()
	served, _, err := f.application.MCPPublic.ToolOutputSchema(tool)
	if err != nil {
		t.Fatal(err)
	}
	declared, err := toolschema.Parse(served, toolschema.Options{RootObject: true})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.structured)
	if err := declared.Validate(encoded); err != nil {
		t.Errorf("%s breaks its served output schema: %v\n%s", tool, err, encoded)
	}
	if len(result.text) != 2 || !strings.HasPrefix(result.text[1], "状态：") {
		t.Errorf("%s: text = %q", tool, result.text)
	}
}

func TestMCPAssetNamesMapToOrdinarySessions(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	keyA, keyB := f.issueKey("owner-a", "generate"), f.issueKey("owner-b", "generate")

	first := f.call(keyA, "generate_image", map[string]any{"prompt": "a barrel", "asset": "木桶", "idempotency_key": idem("barrel")})
	asset := first.structured["asset"].(map[string]any)
	if first.isError || asset["name"] != "木桶" || asset["title"] != "MCP · 木桶" || first.status(t)["asset"].(map[string]any)["id"] != asset["id"] {
		t.Fatalf("first call = %s", first.raw)
	}
	assertServedSchema(t, f, "generate_image", first)
	// The same name, however it is spelled, is the same session.
	for _, spelled := range []string{"木桶", "  木桶 ", "木桶"} {
		again := f.call(keyA, "generate_image", map[string]any{"prompt": "another", "asset": spelled, "idempotency_key": idem("barrel")})
		if again.isError || again.structured["asset"].(map[string]any)["id"] != asset["id"] {
			t.Fatalf("name %q = %s", spelled, again.raw)
		}
	}
	casedFirst := f.call(keyA, "generate_image", map[string]any{"prompt": "x", "asset": "Crate  Big", "idempotency_key": idem("crate")})
	casedSecond := f.call(keyA, "generate_image", map[string]any{"prompt": "x", "asset": " crate big", "idempotency_key": idem("crate")})
	if casedFirst.structured["asset"].(map[string]any)["id"] != casedSecond.structured["asset"].(map[string]any)["id"] ||
		casedFirst.structured["asset"].(map[string]any)["name"] != "Crate Big" {
		t.Fatalf("a name that differs by case and spacing is another asset: %s / %s", casedFirst.raw, casedSecond.raw)
	}
	// No name: the person's default asset, made once.
	defaultFirst := f.call(keyA, "generate_image", map[string]any{"prompt": "x", "idempotency_key": idem("default")})
	defaultSecond := f.call(keyA, "generate_image", map[string]any{"prompt": "y", "idempotency_key": idem("default")})
	def := defaultFirst.structured["asset"].(map[string]any)
	if def["name"] != nil || def["title"] != "MCP · 默认" || def["id"] == asset["id"] || def["id"] != defaultSecond.structured["asset"].(map[string]any)["id"] {
		t.Fatalf("default asset = %v", def)
	}
	if rows := f.assetRows("owner-a"); rows != 3 {
		t.Fatalf("owner-a has %d assets, want 3 (木桶, Crate Big, the default)", rows)
	}
	// Another person's same name is another session; each person sees only their own.
	theirs := f.call(keyB, "generate_image", map[string]any{"prompt": "x", "asset": "木桶", "idempotency_key": idem("theirs")})
	if theirs.structured["asset"].(map[string]any)["id"] == asset["id"] {
		t.Fatal("two people's assets of the same name share a session")
	}
	if foreign := f.call(keyB, "list_tasks", map[string]any{"asset_id": asset["id"]}); !foreign.isError || foreign.problemCode() != "asset_not_found" {
		t.Fatalf("another person listed an asset: %s", foreign.raw)
	}
	// The sessions are ordinary ones: in the person's own session list, with the
	// title, the default approval mode and no automatic retitling.
	sessions, err := f.repository.ListSessions(context.Background(), "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, session := range sessions {
		if session.ID == asset["id"] {
			found = true
			if session.Title != "MCP · 木桶" || session.TitleSource != "user" || session.ApprovalMode != f.application.Chat.ApprovalPolicy.DefaultMode || session.ProjectID != "" {
				t.Fatalf("session = %+v", session)
			}
		}
	}
	if !found {
		t.Fatal("the asset's session is not in the session list")
	}
	// A bad name is refused before any session is made.
	before := f.assetRows("owner-a")
	for name, bad := range map[string]string{"blank": "   ", "control": "a\u0007b", "too long": strings.Repeat("长", 61)} {
		result := f.call(keyA, "generate_image", map[string]any{"prompt": "x", "asset": bad, "idempotency_key": idem("bad")})
		if !result.isError || result.problemCode() != "asset_name_invalid" && result.problemCode() != "invalid_arguments" {
			t.Errorf("%s name = %s", name, result.raw)
		}
	}
	if f.assetRows("owner-a") != before {
		t.Fatal("a refused name made an asset")
	}
}

func TestMCPTheSameAssetNameCreatedAtOnceIsOneSession(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	var wait sync.WaitGroup
	ids := make([]string, 8)
	for index := range ids {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Barrel", "idempotency_key": idem("race")})
			if asset, ok := result.structured["asset"].(map[string]any); ok {
				ids[index], _ = asset["id"].(string)
			}
		}()
	}
	wait.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("sessions = %v; one name must be one session", ids)
		}
	}
	if rows := f.assetRows("owner-a"); rows != 1 {
		t.Fatalf("%d asset rows", rows)
	}
	if sessions := f.countRows(`SELECT COUNT(*) FROM go_chat_sessions WHERE owner_id = 'owner-a' AND title = 'MCP · Barrel'`); sessions != 1 {
		t.Fatalf("%d sessions were made; the others must not exist", sessions)
	}
}

func TestMCPAssetInProjectInheritsAndNeverMoves(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	now := time.Now().UTC()
	for _, p := range []project.Project{
		{ID: "project-mine", OwnerID: "owner-a", Name: "Mine", CreatedAt: now, UpdatedAt: now},
		{ID: "project-other", OwnerID: "owner-a", Name: "Other", CreatedAt: now, UpdatedAt: now},
		{ID: "project-theirs", OwnerID: "owner-b", Name: "Theirs", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := f.repository.ProjectRepository().CreateProject(context.Background(), p, 10); err != nil {
			t.Fatal(err)
		}
	}
	made := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Crate", "project": "project-mine", "idempotency_key": idem("project")})
	if made.isError {
		t.Fatalf("in a project = %s", made.raw)
	}
	assetID := made.structured["asset"].(map[string]any)["id"].(string)
	session, found, err := f.repository.Session(context.Background(), "owner-a", assetID)
	if err != nil || !found || session.ProjectID != "project-mine" || session.StyleLockSource != "project" {
		t.Fatalf("session = %+v found=%v err=%v; it inherits the project's style source", session, found, err)
	}
	// The asset is reused with the same project, or with none named; another
	// project (or none, for an asset that has one) is a business error.
	for _, args := range []map[string]any{{"project": "project-mine"}, {}} {
		args["prompt"], args["asset"], args["idempotency_key"] = "again", "Crate", idem("project")
		if again := f.call(key, "generate_image", args); again.isError || again.structured["asset"].(map[string]any)["id"] != assetID {
			t.Fatalf("reuse with %v = %s", args, again.raw)
		}
	}
	moved := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Crate", "project": "project-other", "idempotency_key": idem("project")})
	if !moved.isError || moved.problemCode() != "asset_project_mismatch" || moved.status(t)["asset"] != nil {
		t.Fatalf("another project = %s", moved.raw)
	}
	plain := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Plain", "idempotency_key": idem("project")})
	if plain.isError {
		t.Fatal(plain.raw)
	}
	if late := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Plain", "project": "project-mine", "idempotency_key": idem("project")}); late.problemCode() != "asset_project_mismatch" {
		t.Fatalf("a project for an asset that has none = %s", late.raw)
	}
	for name, id := range map[string]string{"foreign": "project-theirs", "unknown": "project-none"} {
		before := f.assetRows("owner-a")
		result := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Third-" + name, "project": id, "idempotency_key": idem("project")})
		if !result.isError || result.problemCode() != "project_not_found" || f.assetRows("owner-a") != before {
			t.Fatalf("%s project = %s", name, result.raw)
		}
	}
}

func TestMCPUploadImageStoresThroughTheSameIngestionAndFeedsGeneration(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	other := f.issueKey("owner-b", "generate")
	picture := testPNG(t, 96)

	result := f.call(key, "upload_image", map[string]any{"data": base64.StdEncoding.EncodeToString(picture), "filename": "../../etc/ref.png"})
	image, _ := result.structured["image"].(map[string]any)
	if result.isError || image["media_type"] != "image/png" || image["size"].(float64) != float64(len(picture)) || len(image["sha256"].(string)) != 64 {
		t.Fatalf("upload = %s", result.raw)
	}
	assertServedSchema(t, f, "upload_image", result)
	if result.status(t)["asset"] != nil {
		t.Fatal("an upload belongs to no asset")
	}
	id := image["id"].(string)
	if rows := f.countRows(`SELECT COUNT(*) FROM generation_input_assets WHERE owner_id = 'owner-a' AND id = ? AND original_filename = 'ref.png'`, id); rows != 1 {
		t.Fatal("the picture is not registered as the person's input asset, with the file's base name")
	}
	if log := f.countRows(`SELECT COUNT(*) FROM administration_audit_events WHERE action = 'upload_file' AND resource_id = ?`, id); log != 1 {
		t.Fatalf("the upload is not in the audit: %d", log)
	}

	for name, test := range map[string]struct {
		args map[string]any
		code string
	}{
		"bad base64":      {map[string]any{"data": "this is !! not base64"}, "upload_invalid"},
		"decodes to none": {map[string]any{"data": "===="}, "upload_invalid"},
		"too large": {map[string]any{"data": base64.StdEncoding.EncodeToString(make([]byte, f.application.MCPPublic.Upload.MaxBytes+1))},
			"upload_too_large"},
		"not a picture":   {map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("just some text, not a picture at all"))}, "upload_type_not_allowed"},
		"a model file":    {map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("glTF\x02\x00\x00\x00 padding to look binary"))}, "upload_type_not_allowed"},
		"no data":         {map[string]any{}, "invalid_arguments"},
		"stray argument":  {map[string]any{"data": "AAAA", "owner_id": "owner-b"}, "invalid_arguments"},
		"declared type":   {map[string]any{"data": "AAAA", "media_type": "image/png"}, "invalid_arguments"},
		"empty data text": {map[string]any{"data": ""}, "invalid_arguments"},
	} {
		got := f.call(key, "upload_image", test.args)
		if !got.isError || got.problemCode() != test.code {
			t.Errorf("%s = %s", name, got.raw)
			continue
		}
		assertServedSchema(t, f, "upload_image", got)
	}
	// The picture is the person's: another person cannot use its ID.
	foreign := f.call(other, "generate_image", map[string]any{"prompt": "x", "images": []any{id}, "idempotency_key": idem("foreign")})
	if !foreign.isError || foreign.problemCode() != "image_not_found" {
		t.Fatalf("another person used the picture: %s", foreign.raw)
	}
	// Used as an input of image-to-3D, with the service named in the answer.
	model := f.call(key, "create_model", map[string]any{"images": []any{id}, "asset": "Rock", "idempotency_key": idem("rock")})
	if model.isError {
		t.Fatalf("create_model = %s", model.raw)
	}
	assertServedSchema(t, f, "create_model", model)
	service := model.structured["service"].(map[string]any)
	if service["capability"] != "image-to-3d" || service["defaulted"] != true || service["id"] != "meshy/meshy-7.1/image-to-3d" ||
		model.structured["step_counted"] != true || model.structured["replayed"] != false {
		t.Fatalf("model start = %v", model.structured)
	}
	task, err := f.module.Get(context.Background(), "owner-a", generation.TaskID(taskID(t, model)))
	if err != nil || task.Capability != generation.ImageTo3D || string(task.Offering.ID) != "meshy/meshy-7.1/image-to-3d" {
		t.Fatalf("task = %+v err=%v", task, err)
	}
}

// A picture of a task of the caller's is a picture input too (2D then 3D).
func TestMCPGeneratedPictureFeedsTheNextStep(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	start := f.call(key, "generate_image", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": idem("2d")})
	task := f.finish("owner-a", taskID(t, start))
	if task.State != generation.TaskSucceeded || len(task.Assets) == 0 {
		t.Fatalf("2D task = %+v", task)
	}
	var pictureID string
	for _, asset := range task.Assets {
		if strings.HasPrefix(asset.MediaType, "image/") && asset.Role != "thumbnail" {
			pictureID = string(asset.ID)
			break
		}
	}
	if pictureID == "" {
		pictureID = string(task.Assets[0].ID)
	}
	next := f.call(key, "create_model", map[string]any{"images": []any{pictureID}, "asset": "Barrel", "idempotency_key": idem("3d")})
	if next.isError || next.structured["asset"].(map[string]any)["id"] != start.structured["asset"].(map[string]any)["id"] {
		t.Fatalf("2D to 3D in one asset = %s", next.raw)
	}
}

func TestMCPGenerationToolsStartTasksThroughTheKernelAndTheOwnerSeesThem(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key, other := f.issueKey("owner-a", "generate"), f.issueKey("owner-b", "generate")
	picture := f.upload(key, testPNG(t, 96))
	for name, test := range map[string]struct {
		tool       string
		args       map[string]any
		capability string
		counted    bool
	}{
		"text to image":    {"generate_image", map[string]any{"prompt": "a barrel"}, "text-to-image", false},
		"image to image":   {"generate_image", map[string]any{"prompt": "a barrel, rusty", "images": []any{picture}}, "image-to-image", false},
		"remove backgound": {"remove_background", map[string]any{"image": picture}, "background-removal", false},
		// Tripo's service has a native price, so the status block of the call that
		// starts it already says what a step costs.
		"text to 3d":  {"create_model", map[string]any{"prompt": "a barrel", "service": "tripo/tripo-v3.1-full/text-to-3d"}, "text-to-3d", true},
		"image to 3d": {"create_model", map[string]any{"images": []any{picture}}, "image-to-3d", true},
	} {
		args := test.args
		args["asset"], args["idempotency_key"] = "Tools", idem("tool")
		result := f.call(key, test.tool, args)
		if result.isError {
			t.Errorf("%s = %s", name, result.raw)
			continue
		}
		assertServedSchema(t, f, test.tool, result)
		id := taskID(t, result)
		service := result.structured["service"].(map[string]any)
		if service["capability"] != test.capability || result.structured["step_counted"] != test.counted ||
			result.structured["terminal"] != false || result.structured["poll_after_seconds"].(float64) != 5 ||
			service["provider"] == "" || service["model"] == "" {
			t.Errorf("%s = %v", name, result.structured)
		}
		// The status block of the very call that started a 3D step already says
		// what a step of that service costs in the provider's unit.
		if costs := result.status(t)["step_costs"].([]any); name == "text to 3d" && len(costs) == 0 {
			t.Errorf("%s: the status block names no step cost: %v", name, result.status(t))
		}
		// get_task of the same key sees it, with the asset; another person does not.
		read := f.call(key, "get_task", map[string]any{"task_id": id})
		if read.isError || read.structured["task"].(map[string]any)["asset_id"] != result.structured["asset"].(map[string]any)["id"] {
			t.Errorf("%s: get_task = %s", name, read.raw)
		}
		if hidden := f.call(other, "get_task", map[string]any{"task_id": id}); !hidden.isError || hidden.problemCode() != "task_not_found" {
			t.Errorf("%s: another person read the task: %s", name, hidden.raw)
		}
		// The Task was made by the kernel with a grant of source access_key bound to it.
		if grants := f.countRows(`SELECT COUNT(*) FROM execution_grants WHERE task_id = ? AND source = 'access_key' AND status = 'bound' AND access_key_id <> '' AND decided_by = 'policy:access_key'`, id); grants != 1 {
			t.Errorf("%s: the task has %d access-key grants", name, grants)
		}
		if audits := f.countRows(`SELECT COUNT(*) FROM administration_audit_events WHERE action = 'create_task' AND resource_id = ? AND detail_json LIKE '%"entry":"mcp"%' AND detail_json LIKE '%accessKeyId%'`, id); audits != 1 {
			t.Errorf("%s: the Task creation is not in the audit with its entry: %d", name, audits)
		}
	}
	// One asset holds all of them, and the 3D steps are counted in it.
	listed := f.call(key, "list_tasks", map[string]any{"asset_id": f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Tools", "idempotency_key": idem("tool")}).structured["asset"].(map[string]any)["id"]})
	if listed.isError || len(listed.structured["tasks"].([]any)) != 6 {
		t.Fatalf("the asset's tasks = %s", listed.raw)
	}
	if steps := listed.status(t)["asset"].(map[string]any)["used_steps"].(float64); steps != 2 {
		t.Fatalf("two 3D steps, not %v (2D is never counted)", steps)
	}
}

func TestMCPProcessModelTakesASucceededModelOfTheSameAsset(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key, other := f.issueKey("owner-a", "generate"), f.issueKey("owner-b", "generate")
	model := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": idem("model")})
	modelID := taskID(t, model)
	// The model must have succeeded before it can be processed.
	early := f.call(key, "process_model", map[string]any{"operation": "remesh", "source_task_id": modelID, "asset": "Barrel", "idempotency_key": idem("early")})
	if !early.isError || early.problemCode() != "source_not_ready" || f.countRows(`SELECT COUNT(*) FROM generation_tasks WHERE capability = 'remesh'`) != 0 {
		t.Fatalf("an unfinished model was processed: %s", early.raw)
	}
	if done := f.finish("owner-a", modelID); done.State != generation.TaskSucceeded {
		t.Fatalf("model = %+v", done)
	}
	processed := f.call(key, "process_model", map[string]any{"operation": "remesh", "source_task_id": modelID, "asset": "Barrel", "idempotency_key": idem("remesh")})
	if processed.isError {
		t.Fatalf("process_model = %s", processed.raw)
	}
	assertServedSchema(t, f, "process_model", processed)
	service := processed.structured["service"].(map[string]any)
	if service["capability"] != "remesh" || service["defaulted"] != true || processed.structured["step_counted"] != true ||
		processed.structured["asset"].(map[string]any)["id"] != model.structured["asset"].(map[string]any)["id"] {
		t.Fatalf("processing = %v", processed.structured)
	}
	// Not another asset's model, not another person's, not a made-up one.
	for name, test := range map[string]struct {
		key   string
		asset string
		id    string
	}{
		"another asset":  {key, "Crate", modelID},
		"the default":    {key, "", modelID},
		"another person": {other, "Barrel", modelID},
		"made up":        {key, "Barrel", "task_does_not_exist"},
	} {
		args := map[string]any{"operation": "remesh", "source_task_id": test.id, "idempotency_key": idem("source")}
		if test.asset != "" {
			args["asset"] = test.asset
		}
		before := f.countRows(`SELECT COUNT(*) FROM generation_tasks WHERE capability = 'remesh'`)
		result := f.call(test.key, "process_model", args)
		if !result.isError || result.problemCode() != "source_not_found" || f.countRows(`SELECT COUNT(*) FROM generation_tasks WHERE capability = 'remesh'`) != before {
			t.Errorf("%s = %s", name, result.raw)
		}
	}
	// An asset that does not exist is not made for a processing step.
	before := f.assetRows("owner-a")
	if none := f.call(key, "process_model", map[string]any{"operation": "remesh", "source_task_id": modelID, "asset": "Never", "idempotency_key": idem("never")}); none.problemCode() != "source_not_found" || f.assetRows("owner-a") != before {
		t.Fatalf("a processing step made an asset: %s", none.raw)
	}
	// A service that cannot take this model's provider is refused by name.
	mismatch := f.call(key, "process_model", map[string]any{
		"operation": "rigging", "source_task_id": modelID, "asset": "Barrel", "service": "tripo/processing/rigging", "idempotency_key": idem("rig"),
	})
	if !mismatch.isError || mismatch.problemCode() != "source_incompatible" {
		t.Fatalf("a service of another provider = %s", mismatch.raw)
	}
	wrong := f.call(key, "process_model", map[string]any{
		"operation": "rigging", "source_task_id": modelID, "asset": "Barrel", "service": "meshy/processing/remesh", "idempotency_key": idem("rig"),
	})
	if !wrong.isError || wrong.problemCode() != "service_mismatch" {
		t.Fatalf("a service of another capability = %s", wrong.raw)
	}
}

func TestMCPServiceChoiceIsExplicitAndNeverSubstituted(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	named := f.call(key, "generate_image", map[string]any{"prompt": "x", "service": "openai/gpt-image-2/text-to-image", "idempotency_key": idem("named")})
	service, _ := named.structured["service"].(map[string]any)
	if named.isError || service["id"] != "openai/gpt-image-2/text-to-image" || service["defaulted"] != false {
		t.Fatalf("named service = %s", named.raw)
	}
	unnamed := f.call(key, "generate_image", map[string]any{"prompt": "x", "idempotency_key": idem("default")})
	if unnamed.structured["service"].(map[string]any)["defaulted"] != true {
		t.Fatalf("default service = %s", unnamed.raw)
	}
	for name, test := range map[string]struct {
		tool string
		args map[string]any
		code string
	}{
		"unknown":          {"generate_image", map[string]any{"prompt": "x", "service": "no/such/service"}, "service_mismatch"},
		"disabled":         {"generate_image", map[string]any{"prompt": "x", "service": "openai/gpt-image-2.5-sunburst/text-to-image"}, "service_mismatch"},
		"another capacity": {"generate_image", map[string]any{"prompt": "x", "service": "meshy/meshy-7.1/text-to-3d"}, "service_mismatch"},
		"text for picture": {"generate_image", map[string]any{"prompt": "x", "images": []any{"asset_x"}, "service": "openai/gpt-image-2/text-to-image"}, "service_mismatch"},
	} {
		before := f.countRows(`SELECT COUNT(*) FROM generation_tasks`)
		test.args["idempotency_key"] = idem("refused")
		result := f.call(key, test.tool, test.args)
		if !result.isError || result.problemCode() != test.code || f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != before {
			t.Errorf("%s = %s", name, result.raw)
		}
	}
}

func TestMCPParametersAreValidatedByTheServicesOwnDeclaration(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	const service = "openai/gpt-image-2/text-to-image"
	call := func(args map[string]any) toolResult {
		args["prompt"], args["service"], args["idempotency_key"] = "x", service, idem("params")
		return f.call(key, "generate_image", args)
	}
	// What a caller may set is listed; what it may not is refused by name and
	// nothing is made: unknown, hidden (moderation), read-only, the prompt.
	for name, parameters := range map[string]map[string]any{
		"unknown":   {"no_such_parameter": 1},
		"hidden":    {"moderation": "low"},
		"prompt":    {"prompt": "sneaky"},
		"user only": {"n": 2},
	} {
		before := f.countRows(`SELECT COUNT(*) FROM generation_tasks`)
		result := call(map[string]any{"parameters": parameters})
		if !result.isError || result.problemCode() != "invalid_arguments" || !strings.Contains(result.text[0], "$.parameters.") ||
			f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != before {
			t.Errorf("%s = %s", name, result.raw)
		}
	}
	// A value the kernel's normalization refuses is the service's refusal.
	if bad := call(map[string]any{"parameters": map[string]any{"size": "1x1"}}); !bad.isError || bad.problemCode() != "input_refused" {
		t.Fatalf("an out of range value = %s", bad.raw)
	}
	if good := call(map[string]any{"parameters": map[string]any{"size": "1024x1024"}}); good.isError {
		t.Fatalf("a legal value = %s", good.raw)
	}
	// An advised parameter needs its reason, and a reason is only for one.
	if unexplained := call(map[string]any{"parameters": map[string]any{"quality": "medium"}}); !unexplained.isError ||
		!strings.Contains(unexplained.text[0], "$.parameter_reasons.quality") {
		t.Fatalf("an advised change without a reason = %s", unexplained.raw)
	}
	explained := call(map[string]any{"parameters": map[string]any{"quality": "medium"}, "parameter_reasons": map[string]any{"quality": "a draft for review"}})
	if explained.isError {
		t.Fatalf("an advised change with its reason = %s", explained.raw)
	}
	if stray := call(map[string]any{"parameter_reasons": map[string]any{"n": "why not"}}); !stray.isError || !strings.Contains(stray.text[0], "$.parameter_reasons.n") {
		t.Fatalf("a reason for a parameter that takes none = %s", stray.raw)
	}
	if text := call(map[string]any{"parameter_reasons": map[string]any{"quality": 5}}); !text.isError || !strings.Contains(text.text[0], "$.parameter_reasons.quality") {
		t.Fatalf("a reason that is not a text = %s", text.raw)
	}
	// A picture the service does not take, and a prompt it does not take.
	if pictures := f.call(key, "generate_image", map[string]any{"prompt": "x", "images": []any{f.upload(key, testPNG(t, 96))}, "service": service, "idempotency_key": idem("params")}); !pictures.isError ||
		pictures.problemCode() != "service_mismatch" {
		t.Fatalf("pictures for a text-to-image service = %s", pictures.raw)
	}
	// list_services offers exactly what a caller can set, and marks the advised ones.
	listed := f.call(key, "list_services", map[string]any{"capability": "text-to-image"})
	found := false
	for _, group := range listed.structured["groups"].([]any) {
		for _, capability := range group.(map[string]any)["capabilities"].([]any) {
			for _, raw := range capability.(map[string]any)["services"].([]any) {
				entry := raw.(map[string]any)
				if entry["id"] != service {
					continue
				}
				found = true
				names := map[string]map[string]any{}
				for _, parameter := range entry["parameters"].([]any) {
					names[parameter.(map[string]any)["name"].(string)] = parameter.(map[string]any)
				}
				for _, name := range []string{"moderation", "prompt"} {
					if _, listed := names[name]; listed {
						t.Errorf("list_services offers %s, which a caller cannot set", name)
					}
				}
				prompt := false
				for _, input := range entry["inputs"].([]any) {
					prompt = prompt || input.(map[string]any)["kind"] == "prompt"
				}
				if !prompt {
					t.Error("the prompt is not listed as an input")
				}
				if names["n"] != nil {
					t.Errorf("list_services offers n, which only the person's panel can set: %v", names)
				}
				if names["quality"]["advised"] != true || names["quality"]["advice"] == "" {
					t.Errorf("parameters = %v", names)
				}
			}
		}
	}
	if !found {
		t.Fatal("the service is not listed")
	}
}

func TestMCPIdempotencyReplaysTheSameTaskAndRefusesOtherInput(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	args := func() map[string]any {
		return map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": "replay-key-0001"}
	}
	first := f.call(key, "create_model", args())
	id := taskID(t, first)
	grants, tasks := f.countRows(`SELECT COUNT(*) FROM execution_grants`), f.countRows(`SELECT COUNT(*) FROM generation_tasks`)
	prepared := f.countRows(`SELECT COUNT(*) FROM generation_prepared_inputs`)
	for range 3 {
		again := f.call(key, "create_model", args())
		if again.isError || taskID(t, again) != id || again.structured["replayed"] != true || again.structured["service"].(map[string]any)["id"] != first.structured["service"].(map[string]any)["id"] {
			t.Fatalf("replay = %s", again.raw)
		}
		assertServedSchema(t, f, "create_model", again)
	}
	if f.countRows(`SELECT COUNT(*) FROM execution_grants`) != grants || f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != tasks ||
		f.countRows(`SELECT COUNT(*) FROM generation_prepared_inputs`) != prepared {
		t.Fatal("a replay made a grant, a Task or a frozen input")
	}
	if status := f.call(key, "get_status", nil).status(t); status["total_used_steps"].(float64) != 1 {
		t.Fatalf("a replay counted a step again: %v", status)
	}
	// The same key with another prompt, another asset, another tool or another service is a conflict.
	for name, change := range map[string]func(map[string]any){
		"prompt":  func(a map[string]any) { a["prompt"] = "a crate" },
		"asset":   func(a map[string]any) { a["asset"] = "Crate" },
		"service": func(a map[string]any) { a["service"] = "tripo/tripo-v3.1-full/text-to-3d" },
		"extra":   func(a map[string]any) { a["parameters"] = map[string]any{} },
	} {
		changed := args()
		change(changed)
		before := f.assetRows("owner-a")
		result := f.call(key, "create_model", changed)
		if !result.isError || result.problemCode() != "idempotency_conflict" || f.assetRows("owner-a") != before ||
			f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != tasks {
			t.Errorf("%s = %s", name, result.raw)
		}
	}
	if other := f.call(key, "generate_image", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": "replay-key-0001"}); other.problemCode() != "idempotency_conflict" {
		t.Fatalf("another tool under the key = %s", other.raw)
	}
	// Another person's same key is theirs; it neither replays nor conflicts.
	theirs := f.call(f.issueKey("owner-b", "generate"), "create_model", args())
	if theirs.isError || taskID(t, theirs) == id {
		t.Fatalf("another person's call = %s", theirs.raw)
	}
	// A key another entry made is the namespace's: the prefix keeps the caller's
	// key apart from the keys of the browser and the Agent.
	if got := f.countRows(`SELECT COUNT(*) FROM generation_tasks WHERE idempotency_key = 'replay-key-0001'`); got != 0 {
		t.Fatalf("the caller's key was used as the Task's key as it is")
	}
	if got := f.countRows(`SELECT COUNT(*) FROM generation_tasks WHERE idempotency_key = 'mcp:replay-key-0001' AND owner_id = 'owner-a'`); got != 1 {
		t.Fatalf("the Task's key is not the namespaced one: %d", got)
	}
}

func TestMCPTheSameCallAtOnceStartsOneTask(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	var wait sync.WaitGroup
	results := make([]toolResult, 6)
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index] = f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": "concurrent-key-01"})
		}()
	}
	wait.Wait()
	ids := map[string]bool{}
	for _, result := range results {
		if result.isError || result.rpcError != nil {
			t.Fatalf("a concurrent replay failed: %s\n%s", result.raw, f.serverLog())
		}
		ids[taskID(t, result)] = true
	}
	if len(ids) != 1 || f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != 1 {
		t.Fatalf("the same call at once started %d tasks", len(ids))
	}
	if status := f.call(key, "get_status", nil).status(t); status["total_used_steps"].(float64) != 1 {
		t.Fatalf("the steps = %v; the same call is one step", status["total_used_steps"])
	}
}

func TestMCPStepsAreTheOwnersMCPSettingPerAsset(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	model := func(asset string) toolResult {
		f.settle("owner-a")
		return f.call(key, "create_model", map[string]any{"prompt": "a thing", "asset": asset, "idempotency_key": idem("steps")})
	}
	// The default is the Agent's own management: no limit, the facts are given.
	for range 3 {
		if result := model("Free"); result.isError {
			t.Fatalf("agent mode = %s", result.raw)
		}
	}
	status := f.call(key, "get_status", nil).status(t)
	if status["step_mode"] != "agent" || status["step_cap"] != nil || status["total_used_steps"].(float64) != 3 {
		t.Fatalf("status = %v", status)
	}
	// Under the cap mode each asset has its own cap; 2D is never counted.
	f.setMCPSteps("owner-a", approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: 2})
	if fresh := model("Capped"); fresh.isError || fresh.status(t)["asset"].(map[string]any)["steps_left"].(float64) != 1 {
		t.Fatalf("the first step under a cap of 2 = %s", fresh.raw)
	}
	second := model("Capped")
	if second.isError || second.status(t)["asset"].(map[string]any)["steps_left"].(float64) != 0 {
		t.Fatalf("the last step = %s", second.raw)
	}
	tasks, grants := f.countRows(`SELECT COUNT(*) FROM generation_tasks`), f.countRows(`SELECT COUNT(*) FROM execution_grants WHERE status = 'denied'`)
	refused := model("Capped")
	if !refused.isError || refused.problemCode() != "step_cap_reached" || !strings.Contains(refused.text[0], "已用 2 步，上限 2 步") {
		t.Fatalf("the step at the cap = %s", refused.raw)
	}
	assertServedSchema(t, f, "create_model", refused)
	block := refused.status(t)
	if block["step_cap"].(float64) != 2 || block["asset"].(map[string]any)["used_steps"].(float64) != 2 || block["asset"].(map[string]any)["steps_left"].(float64) != 0 ||
		refused.structured["task_id"] != nil {
		t.Fatalf("status at the cap = %v", block)
	}
	if f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != tasks || f.countRows(`SELECT COUNT(*) FROM execution_grants WHERE status = 'denied'`) != grants+1 {
		t.Fatal("a refused step made a Task, or its denial was not recorded")
	}
	// 2D works at the cap, on the same asset; another asset has its own cap; the
	// refused call replays as the same refusal and takes nothing.
	for range 3 {
		f.settle("owner-a")
		if picture := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Capped", "idempotency_key": idem("2d")}); picture.isError {
			t.Fatalf("2D at the cap = %s", picture.raw)
		}
	}
	if other := model("Another"); other.isError {
		t.Fatalf("another asset under the same setting = %s", other.raw)
	}
	f.settle("owner-a")
	args := map[string]any{"prompt": "a thing", "asset": "Capped", "idempotency_key": "capped-replay-key-1"}
	first, again := f.call(key, "create_model", args), f.call(key, "create_model", args)
	if first.problemCode() != "step_cap_reached" || again.problemCode() != "step_cap_reached" {
		t.Fatalf("a refused call and its replay = %s / %s", first.raw, again.raw)
	}
	// The person raises the cap: the same retry goes through, once, as one step.
	f.setMCPSteps("owner-a", approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: 3})
	f.settle("owner-a")
	retried := f.call(key, "create_model", args)
	if retried.isError || retried.structured["replayed"] != false || retried.status(t)["asset"].(map[string]any)["used_steps"].(float64) != 3 {
		t.Fatalf("the retry after the cap was raised = %s", retried.raw)
	}
	if again := f.call(key, "create_model", args); again.isError || taskID(t, again) != taskID(t, retried) || again.structured["replayed"] != true {
		t.Fatalf("the retry's own replay = %s", again.raw)
	}
	// Unlimited never counts against a cap.
	f.setMCPSteps("owner-a", approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeUnlimited, Cap: 1})
	if unlimited := model("Capped"); unlimited.isError || unlimited.status(t)["step_cap"] != nil {
		t.Fatalf("unlimited = %s", unlimited.raw)
	}
}

func TestMCPAStepOnlyAPersonCouldApproveIsDeniedWithItsOwnReason(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	first := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": idem("charged")})
	id := taskID(t, first)
	if done := f.finish("owner-a", id); done.State != generation.TaskSucceeded {
		t.Fatalf("task = %+v", done)
	}
	// The earlier attempt was charged upstream but its result was not stored.
	f.exec(`UPDATE generation_tasks SET state = 'FAILED', failure_json = ? WHERE id = ?`,
		`{"class":"integrity_failure","retryable":false,"detail":"stored result failed its check"}`, id)
	tasks := f.countRows(`SELECT COUNT(*) FROM generation_tasks`)
	repeat := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": idem("charged")})
	if !repeat.isError || repeat.problemCode() != "needs_person" || !strings.Contains(repeat.text[0], id) || !strings.Contains(repeat.text[0], "网页界面") {
		t.Fatalf("a repeat of a charged attempt = %s", repeat.raw)
	}
	assertServedSchema(t, f, "create_model", repeat)
	if f.countRows(`SELECT COUNT(*) FROM generation_tasks`) != tasks ||
		f.countRows(`SELECT COUNT(*) FROM execution_grants WHERE decided_by = 'policy:access_key:needs_person' AND status = 'denied'`) != 1 {
		t.Fatal("the denied step made a Task, or its denial was not recorded as one that needs a person")
	}
	// The same kind in another asset, and 2D in this one, are not repeats of it.
	if elsewhere := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Crate", "idempotency_key": idem("charged")}); elsewhere.isError {
		t.Fatalf("another asset = %s", elsewhere.raw)
	}
	if picture := f.call(key, "generate_image", map[string]any{"prompt": "x", "asset": "Barrel", "idempotency_key": idem("charged")}); picture.isError {
		t.Fatalf("a 2D step of the asset = %s", picture.raw)
	}
	// The key's own retry is refused the same way and reads the same.
	again := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": "charged-replay-key-1"})
	retry := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": "charged-replay-key-1"})
	if again.problemCode() != "needs_person" || retry.problemCode() != "needs_person" {
		t.Fatalf("a retry = %s / %s", again.raw, retry.raw)
	}
}

func TestMCPGenerationToolsNeedTheGenerateScope(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	readOnly, generate := f.issueKey("owner-a", "read"), f.issueKey("owner-a", "generate")
	for _, tool := range []string{"upload_image", "generate_image", "remove_background", "create_model", "process_model"} {
		listed := false
		for _, raw := range decodeToolResultRaw(t, f.post(readOnly, "tools/list", nil))["tools"].([]any) {
			listed = listed || raw.(map[string]any)["name"] == tool
		}
		denied := f.post(readOnly, "tools/call", map[string]any{"name": tool, "arguments": map[string]any{}})
		if listed || denied.Code != http.StatusForbidden || denied.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope", scope="generate"` {
			t.Errorf("%s with a read key: listed=%v status=%d %v", tool, listed, denied.Code, denied.Header())
		}
	}
	if f.countRows(`SELECT COUNT(*) FROM generation_tasks`)+f.countRows(`SELECT COUNT(*) FROM mcp_assets`)+f.countRows(`SELECT COUNT(*) FROM execution_grants`) != 0 {
		t.Fatal("a refused call made something")
	}
	if ok := f.call(generate, "generate_image", map[string]any{"prompt": "x", "idempotency_key": idem("scope")}); ok.isError {
		t.Fatalf("a generate key = %s", ok.raw)
	}
	// A key must carry its own idempotency key and no stray argument.
	for name, args := range map[string]map[string]any{
		"no key":          {"prompt": "x"},
		"short key":       {"prompt": "x", "idempotency_key": "short"},
		"long key":        {"prompt": "x", "idempotency_key": strings.Repeat("k", 129)},
		"owner argument":  {"prompt": "x", "idempotency_key": idem("scope"), "owner_id": "owner-b"},
		"no prompt":       {"idempotency_key": idem("scope")},
		"model no input":  nil,
		"empty images":    {"prompt": "x", "images": []any{}, "idempotency_key": idem("scope")},
		"repeated images": {"prompt": "x", "images": []any{"a", "a"}, "idempotency_key": idem("scope")},
	} {
		tool := "generate_image"
		if name == "model no input" {
			tool, args = "create_model", map[string]any{"idempotency_key": idem("scope")}
		}
		if bad := f.call(generate, tool, args); !bad.isError || bad.problemCode() != "invalid_arguments" {
			t.Errorf("%s = %s", name, bad.raw)
		}
	}
}

func TestMCPTheInFlightCapIsABusinessErrorThatRetriesTheSameKey(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	var refused toolResult
	var refusedArgs map[string]any
	for index := 0; index < 30; index++ {
		args := map[string]any{"prompt": fmt.Sprintf("picture %d", index), "idempotency_key": idem("flood")}
		result := f.call(key, "generate_image", args)
		if result.isError {
			refused, refusedArgs = result, args
			break
		}
	}
	if refusedArgs == nil || refused.problemCode() != "too_many_in_flight" {
		t.Fatalf("the in-flight cap never answered: %s", refused.raw)
	}
	assertServedSchema(t, f, "generate_image", refused)
	wait, _ := refused.structured["error"].(map[string]any)["retry_after_seconds"].(float64)
	if wait <= 0 || !strings.Contains(refused.text[0], fmt.Sprint(int(wait))) || f.countRows(`SELECT COUNT(*) FROM generation_tasks WHERE state NOT IN ('SUCCEEDED','FAILED','CANCELED')`) < 1 {
		t.Fatalf("no retry hint: %s", refused.raw)
	}
	// When tasks have ended the same call, with the same key, goes through.
	for range 8 {
		f.exec(`UPDATE generation_tasks SET state = 'SUCCEEDED' WHERE owner_id = 'owner-a'`)
		time.Sleep(time.Millisecond)
	}
	retried := f.call(key, "generate_image", refusedArgs)
	if retried.isError || retried.structured["replayed"] != false {
		t.Fatalf("the retry after the wait = %s", retried.raw)
	}
}

func TestMCPNonAssetSessionsAreNotAssets(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "read")
	plain := f.succeededPlainTask("owner-a", "", "browser-task")
	read := f.call(key, "get_task", map[string]any{"task_id": string(plain.ID)})
	if read.isError || read.structured["task"].(map[string]any)["asset_id"] != nil || read.status(t)["asset"] != nil {
		t.Fatalf("a task of a browser session is the caller's, but it is no asset's: %s", read.raw)
	}
	if listed := f.call(key, "list_tasks", map[string]any{"asset_id": plain.SessionID}); !listed.isError || listed.problemCode() != "asset_not_found" {
		t.Fatalf("a plain session as an asset = %s", listed.raw)
	}
	if status := f.call(key, "get_status", nil); len(status.structured["assets"].([]any)) != 0 {
		t.Fatalf("get_status lists %v", status.structured["assets"])
	}
	// After the service names it, the very same session is an asset.
	f.markAsset("owner-a", plain.SessionID)
	if marked := f.call(key, "get_task", map[string]any{"task_id": string(plain.ID)}); marked.structured["task"].(map[string]any)["asset_id"] != plain.SessionID {
		t.Fatalf("a marked session = %s", marked.raw)
	}
}

func TestMCPEveryStartToolOutputHoldsItsDeclaredSchema(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil, func(application *catalog.ApplicationSchema) {})
	key := f.issueKey("owner-a", "generate")
	picture := f.upload(key, testPNG(t, 96))
	model := f.call(key, "create_model", map[string]any{"prompt": "a barrel", "asset": "Barrel", "idempotency_key": idem("shape")})
	f.finish("owner-a", taskID(t, model))
	for name, test := range map[string]struct {
		tool string
		args map[string]any
	}{
		"generate":            {"generate_image", map[string]any{"prompt": "x", "asset": "Shapes"}},
		"generate with image": {"generate_image", map[string]any{"prompt": "x", "images": []any{picture}}},
		"background":          {"remove_background", map[string]any{"image": picture}},
		"model":               {"create_model", map[string]any{"images": []any{picture}}},
		"process":             {"process_model", map[string]any{"operation": "remesh", "source_task_id": taskID(t, model), "asset": "Barrel"}},
		"refused process":     {"process_model", map[string]any{"operation": "remesh", "source_task_id": "task_none", "asset": "Barrel"}},
		"refused service":     {"generate_image", map[string]any{"prompt": "x", "service": "no/such/service"}},
	} {
		test.args["idempotency_key"] = idem("shape")
		result := f.call(key, test.tool, test.args)
		assertServedSchema(t, f, test.tool, result)
		if name == "process" && result.isError {
			t.Errorf("%s = %s", name, result.raw)
		}
	}
}

// Several pictures of one model are multiview: with no service named the
// server picks one that takes several views, never a single-picture service.
func TestMCPSeveralPicturesPickAMultiviewService(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	front, back := f.upload(key, testPNG(t, 96)), f.upload(key, testPNG(t, 97))
	result := f.call(key, "create_model", map[string]any{"images": []any{front, back}, "asset": "Views", "idempotency_key": idem("views")})
	if result.isError {
		t.Fatalf("multiview = %s", result.raw)
	}
	service := result.structured["service"].(map[string]any)
	if service["capability"] != "image-to-3d" || !strings.Contains(service["id"].(string), "multiview") || service["defaulted"] != true {
		t.Fatalf("service = %v; several pictures need a service that takes several views", service)
	}
	// One picture still goes to the default single-picture service.
	single := f.call(key, "create_model", map[string]any{"images": []any{front}, "asset": "Views", "idempotency_key": idem("views")})
	if single.isError || strings.Contains(single.structured["service"].(map[string]any)["id"].(string), "multiview") {
		t.Fatalf("one picture = %s", single.raw)
	}
}

// testMaskPNG is a PNG mask with a transparent square in the middle and opaque
// surroundings: the transparent pixels are the region to redraw (#332).
func testMaskPNG(t *testing.T, side int) []byte {
	t.Helper()
	mask := image.NewNRGBA(image.Rect(0, 0, side, side))
	inner := side / 4
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if x >= inner && x < side-inner && y >= inner && y < side-inner {
				continue // Transparent: redraw this pixel.
			}
			mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, mask); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// testJPEG is an opaque JPEG of the given side, for a file a mask parameter
// does not accept.
func testJPEG(t *testing.T, side int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, side, side))
	for x := 0; x < side; x++ {
		for y := 0; y < side; y++ {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// #332: an image-to-image call with a mask picks a mask-capable service, sends
// the mask in the Offering's mask parameter, and the result changes only the
// mask's transparent region.
func TestMCPGenerateImageMaskEditsOnlyTheMaskedRegion(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	reference := testPNG(t, 64)
	referenceID, maskID := f.upload(key, reference), f.upload(key, testMaskPNG(t, 64))

	// list_services names the mask input of the services that take one.
	listed := f.call(key, "list_services", map[string]any{"capability": "image-to-image"})
	foundMask := false
	for _, group := range listed.structured["groups"].([]any) {
		for _, capability := range group.(map[string]any)["capabilities"].([]any) {
			for _, raw := range capability.(map[string]any)["services"].([]any) {
				service := raw.(map[string]any)
				if service["id"] != "openai/gpt-image-2/image-to-image" {
					continue
				}
				for _, rawInput := range service["inputs"].([]any) {
					input := rawInput.(map[string]any)
					if input["name"] == "mask" && input["kind"] == "asset" && input["max_bytes"].(float64) > 0 {
						foundMask = true
					}
				}
			}
		}
	}
	if !foundMask {
		t.Fatalf("list_services does not name the mask input: %s", listed.raw)
	}

	result := f.call(key, "generate_image", map[string]any{
		"prompt": "a barrel, repaint the middle", "images": []any{referenceID}, "mask": maskID,
		"asset": "Masked", "idempotency_key": idem("mask"),
	})
	if result.isError {
		t.Fatalf("masked image-to-image = %s", result.raw)
	}
	service := result.structured["service"].(map[string]any)
	if service["id"] != "openai/gpt-image-2/image-to-image" || service["capability"] != "image-to-image" || service["defaulted"] != true {
		t.Fatalf("service = %v; a mask needs a mask-capable service", service)
	}
	task := f.finish("owner-a", taskID(t, result))
	if task.State != generation.TaskSucceeded {
		t.Fatalf("task = %+v", task)
	}
	saved := false
	for _, asset := range task.Assets {
		saved = saved || asset.Role == "image"
	}
	if !saved {
		t.Fatalf("the task saved no picture: %+v", task.Assets)
	}
	// The frozen input is what the provider wire is built from: the mask rides
	// the Offering's mask parameter beside the first reference picture.
	var frozen map[string]any
	if err := json.Unmarshal(task.Input, &frozen); err != nil {
		t.Fatalf("frozen input %s: %v", task.Input, err)
	}
	if frozen["mask"] != maskID {
		t.Fatalf("frozen input mask = %v, want %s (%s)", frozen["mask"], maskID, task.Input)
	}
	if references, _ := frozen["reference_assets"].([]any); len(references) == 0 || references[0] != referenceID {
		t.Fatalf("frozen input references = %v, want %s", frozen["reference_assets"], referenceID)
	}
}

// #332: a mask is checked before anything is prepared or paid for, and every
// refusal names the reason.
func TestMCPGenerateImageMaskIsRefusedBeforeStarting(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key, other := f.issueKey("owner-a", "generate"), f.issueKey("owner-b", "generate")
	reference := f.upload(key, testPNG(t, 64))
	small := f.upload(key, testMaskPNG(t, 32))
	opaque := f.upload(key, testPNG(t, 64))
	jpeg := f.upload(key, testJPEG(t, 64))
	foreign := f.upload(other, testMaskPNG(t, 64))
	mask := f.upload(key, testMaskPNG(t, 64))
	jpegReference := f.upload(key, testJPEG(t, 64))
	before := f.countRows(`SELECT COUNT(*) FROM generation_tasks`)
	for name, test := range map[string]struct {
		args map[string]any
		code string
	}{
		"size mismatch":          {map[string]any{"prompt": "x", "images": []any{reference}, "mask": small}, "mask_size_mismatch"},
		"opaque mask":            {map[string]any{"prompt": "x", "images": []any{reference}, "mask": opaque}, "mask_invalid"},
		"jpeg mask":              {map[string]any{"prompt": "x", "images": []any{reference}, "mask": jpeg}, "mask_invalid"},
		"jpeg first picture":     {map[string]any{"prompt": "x", "images": []any{jpegReference, reference}, "mask": mask}, "mask_format_mismatch"},
		"another owner's mask":   {map[string]any{"prompt": "x", "images": []any{reference}, "mask": foreign}, "image_not_found"},
		"no picture":             {map[string]any{"prompt": "x", "mask": small}, "mask_not_supported"},
		"service without a mask": {map[string]any{"prompt": "x", "images": []any{reference}, "mask": small, "service": "meshy/nano-banana-pro/image-to-image"}, "mask_not_supported"},
	} {
		args := test.args
		args["idempotency_key"] = idem("mask-refused")
		result := f.call(key, "generate_image", args)
		if !result.isError || result.problemCode() != test.code {
			t.Errorf("%s = %s (want %s)", name, result.raw, test.code)
		}
	}
	if after := f.countRows(`SELECT COUNT(*) FROM generation_tasks`); after != before {
		t.Fatalf("a refused mask started %d tasks", after-before)
	}
}

// #582: the image-edit wire takes a mask of the first picture's format only
// ("The image to edit and mask must be of the same format and size"). A
// first picture that is no PNG is refused before anything starts, with the
// sniffed formats and what to do; a PNG first picture beside a JPEG second one
// is taken, since the mask applies to the first.
func TestMCPGenerateImageMaskOfAnotherFormatIsRefused(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t, nil)
	key := f.issueKey("owner-a", "generate")
	mask := f.upload(key, testMaskPNG(t, 64))
	jpegReference := f.upload(key, testJPEG(t, 64))
	before := f.countRows(`SELECT COUNT(*) FROM generation_tasks`)
	result := f.call(key, "generate_image", map[string]any{
		"prompt": "x", "images": []any{jpegReference}, "mask": mask, "idempotency_key": idem("mask-format"),
	})
	if !result.isError || result.problemCode() != "mask_format_mismatch" {
		t.Fatalf("a JPEG first picture with a PNG mask = %s", result.raw)
	}
	want := strings.NewReplacer("{mask_format}", "PNG", "{image_format}", "JPEG").
		Replace(f.application.MCPPublic.Messages.MaskFormatMismatch)
	problem, _ := result.structured["error"].(map[string]any)
	if message, _ := problem["message"].(string); message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
	if after := f.countRows(`SELECT COUNT(*) FROM generation_tasks`); after != before {
		t.Fatalf("a refused mask started %d tasks", after-before)
	}
	png := f.upload(key, testPNG(t, 64))
	accepted := f.call(key, "generate_image", map[string]any{
		"prompt": "x", "images": []any{png, jpegReference}, "mask": mask, "idempotency_key": idem("mask-format-ok"),
	})
	if accepted.isError {
		t.Fatalf("a PNG first picture with a JPEG second one = %s", accepted.raw)
	}
}

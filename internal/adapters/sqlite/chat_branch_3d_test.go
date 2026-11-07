package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

var branchIDCounter atomic.Int64

func branchNewID() func(string) string {
	return func(kind string) string {
		return fmt.Sprintf("%s-%d", kind, branchIDCounter.Add(1))
	}
}

func insertBranchTask(t *testing.T, r *Repository, owner, session, task, modelAsset string) {
	t.Helper()
	insertBranchTaskEvidence(t, r, owner, session, task, modelAsset, true)
}

func insertBranchTaskEvidence(t *testing.T, r *Repository, owner, session, task, modelAsset string, createAsset bool) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO generation_tasks
			(id,owner_id,session_id,capability,offering_json,input_json,request_hash,idempotency_key,state,version,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{task, owner, session, "text-to-3d", `{"id":"text-to-3d"}`, `{}`, "hash-" + task, "idem-" + task, "SUCCEEDED", 1,
				"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"}},
		{`INSERT INTO generation_attempts
			(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			[]any{"attempt-" + task, task, 1, `{"id":"text-to-3d"}`, "SUCCEEDED", "submission-" + task,
				"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"}},
	}
	if createAsset {
		statements = append(statements, struct {
			query string
			args  []any
		}{`INSERT INTO generation_assets
			(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{modelAsset, task, "attempt-" + task, "model", 1, 0, "storage/" + modelAsset,
				"model/gltf-binary", 32, strings.Repeat("a", 64), "2026-01-01T00:00:00Z"}})
	}
	for _, statement := range statements {
		if _, err := r.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed task %s: %v", task, err)
		}
	}
}

func insertBranchInputAsset(t *testing.T, r *Repository, owner, id string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO generation_input_assets
		(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?)`, id, owner, "storage/"+id, "image/png", 16, strings.Repeat("b", 64),
		"2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed input asset %s: %v", id, err)
	}
}

func insertBranchPrepared(t *testing.T, r *Repository, owner, session, id string) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO generation_prepared_inputs
		(id,owner_id,session_id,input_hash,payload_json,created_at) VALUES (?,?,?,?,?,?)`,
		id, owner, session, "hash-"+id, `{}`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed prepared input %s: %v", id, err)
	}
}

func insertBranchScene(t *testing.T, r *Repository, owner, session, task, modelAsset string) {
	t.Helper()
	insertBranchSceneEvidence(t, r, owner, session, session, task, modelAsset, true)
}

func insertBranchSceneEvidence(t *testing.T, r *Repository, owner, sceneSession, taskSession, task, modelAsset string, createAsset bool) {
	t.Helper()
	insertBranchInputAsset(t, r, owner, "bitmap-asset")
	if task != "" {
		insertBranchTaskEvidence(t, r, owner, taskSession, task, modelAsset, createAsset)
	}
	insertBranchPrepared(t, r, owner, sceneSession, "prepared-a")
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO scene_revisions (session_id,owner_id,revision,updated_at) VALUES (?,?,?,?)`,
			[]any{sceneSession, owner, 3, "2026-01-02T00:00:00Z"}},
		{`INSERT INTO scene_objects
			(id,owner_id,session_id,revision,active_representation_id,
			 rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{"object-old", owner, sceneSession, 2, "rep-model", 10.0, 20.0, 300.0, 200.0, 0.5, 0.5,
				"2026-01-01T01:00:00Z", "2026-01-02T00:00:00Z"}},
		{`INSERT INTO scene_representations
			(id,object_id,kind,asset_id,source_task_id,source_representation_id,prepared_input_id,metadata_schema_revision,created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			[]any{"rep-bitmap", "object-old", "bitmap", "bitmap-asset", nil, nil, nil, "scene-v1",
				"2026-01-01T01:00:00Z"}},
		{`INSERT INTO scene_representations
			(id,object_id,kind,asset_id,source_task_id,source_representation_id,prepared_input_id,metadata_schema_revision,created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			[]any{"rep-model", "object-old", "model3d", modelAsset, task, "rep-bitmap", "prepared-a", "scene-v1",
				"2026-01-01T02:00:00Z"}},
	}
	for _, statement := range statements {
		if _, err := r.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed scene: %v", err)
		}
	}
}

func insertBranchCapture(t *testing.T, r *Repository, owner, session, id, task, modelAsset string) {
	t.Helper()
	insertBranchInputAsset(t, r, owner, "screenshot-"+id)
	if _, err := r.db.Exec(`INSERT INTO model_captures
		(id,owner_id,session_id,source_task_id,model_asset_id,screenshot_asset_id,
		 idempotency_key,command_hash,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, owner, session, task, modelAsset, "screenshot-"+id, "capture-key-"+id, "command-hash-"+id,
		`{"camera_orbit":"0 0 5","camera_target":"0 0 0","field_of_view":"45","environment":"studio","animation_time_ms":0,"renderer_version":"three"}`,
		640, 480, 128, strings.Repeat("c", 64), "2026-01-01T03:00:00Z"); err != nil {
		t.Fatalf("seed capture %s: %v", id, err)
	}
}

func branchRequest(owner, source, message, newSession string) chat.BranchRequest {
	return chat.BranchRequest{
		OwnerID: owner, SessionID: source, MessageID: message, NewSessionID: newSession,
		TitleTemplate: "{title} branch", At: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		NewID: branchNewID(), ActiveStates: []string{"queued", "running", "waiting_task"},
	}
}

func TestBranchCopiesCurrentSceneCapturesAndTaskRefs(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Parent"})
	if _, err := r.db.Exec(`INSERT INTO go_chat_messages
		(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at)
		VALUES ('message-1','parent','assistant','model','[]','[]','[{"kind":"image","capture_id":"capture-old","asset_id":"screenshot-capture-old"}]','','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	insertBranchScene(t, r, "owner", "parent", "task-parent", "model-asset")
	insertBranchCapture(t, r, "owner", "parent", "capture-old", "task-parent", "model-asset")

	branch, err := r.BranchSession(ctx, branchRequest("owner", "parent", "message-1", "branch"))
	if err != nil {
		t.Fatal(err)
	}
	if !branch.BranchSceneCopied || !branch.BranchCapturesCopied {
		t.Fatalf("branch flags scene=%v captures=%v", branch.BranchSceneCopied, branch.BranchCapturesCopied)
	}
	var revision int64
	if err := r.db.QueryRow(`SELECT revision FROM scene_revisions WHERE owner_id='owner' AND session_id='branch'`).Scan(&revision); err != nil || revision != 3 {
		t.Fatalf("branch scene revision = %d err=%v", revision, err)
	}
	var objectID, activeID string
	var objectRevision int64
	if err := r.db.QueryRow(`SELECT id,active_representation_id,revision FROM scene_objects WHERE owner_id='owner' AND session_id='branch'`).Scan(&objectID, &activeID, &objectRevision); err != nil {
		t.Fatal(err)
	}
	if objectID == "object-old" || objectRevision != 2 {
		t.Fatalf("branch object = %q revision=%d", objectID, objectRevision)
	}
	var bitmapNew, modelNew, modelSource, modelPrepared string
	var modelAsset, modelTask string
	if err := r.db.QueryRow(`SELECT id,asset_id,source_task_id,source_representation_id,prepared_input_id FROM scene_representations WHERE object_id=? AND kind='model3d'`, objectID).Scan(&modelNew, &modelAsset, &modelTask, &modelSource, &modelPrepared); err != nil {
		t.Fatal(err)
	}
	if err := r.db.QueryRow(`SELECT id FROM scene_representations WHERE object_id=? AND kind='bitmap'`, objectID).Scan(&bitmapNew); err != nil {
		t.Fatal(err)
	}
	if modelNew == "rep-model" || bitmapNew == "rep-bitmap" || activeID != modelNew {
		t.Fatalf("representation IDs not remapped: bitmap=%q model=%q active=%q", bitmapNew, modelNew, activeID)
	}
	if modelSource != bitmapNew {
		t.Fatalf("source_representation_id = %q, want %q", modelSource, bitmapNew)
	}
	if modelAsset != "model-asset" || modelTask != "task-parent" || modelPrepared != "prepared-a" {
		t.Fatalf("representation provenance changed: asset=%q task=%q prepared=%q", modelAsset, modelTask, modelPrepared)
	}
	var captureID, captureSession, captureKey string
	var captureTask, captureModel, captureShot string
	if err := r.db.QueryRow(`SELECT id,session_id,idempotency_key,source_task_id,model_asset_id,screenshot_asset_id
		FROM model_captures WHERE session_id='branch'`).Scan(&captureID, &captureSession, &captureKey, &captureTask, &captureModel, &captureShot); err != nil {
		t.Fatal(err)
	}
	if captureID == "capture-old" || captureSession != "branch" || captureKey == "capture-key-capture-old" ||
		captureTask != "task-parent" || captureModel != "model-asset" || captureShot != "screenshot-capture-old" {
		t.Fatalf("capture copy = %q %q %q %q %q %q", captureID, captureSession, captureKey, captureTask, captureModel, captureShot)
	}
	var attachments string
	if err := r.db.QueryRow(`SELECT attachments FROM go_chat_messages WHERE session_id='branch'`).Scan(&attachments); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(attachments, `"capture_id":"`+captureID+`"`) || strings.Contains(attachments, `"capture_id":"capture-old"`) {
		t.Fatalf("attachment capture_id was not remapped: %s", attachments)
	}
	// Parent rows are untouched.
	var parentObjects, parentCaptures int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM scene_objects WHERE session_id='parent'`).Scan(&parentObjects); err != nil || parentObjects != 1 {
		t.Fatalf("parent objects = %d err=%v", parentObjects, err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM model_captures WHERE session_id='parent'`).Scan(&parentCaptures); err != nil || parentCaptures != 1 {
		t.Fatalf("parent captures = %d err=%v", parentCaptures, err)
	}
	var refs int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM chat_branch_task_refs WHERE branch_session_id='branch' AND owner_id='owner' AND task_id='task-parent'`).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("branch task ref = %d err=%v", refs, err)
	}
	// A branch of the branch inherits the grandparent Task and copies the
	// branch's own current scene and capture with fresh IDs.
	var copiedMessageID string
	if err := r.db.QueryRow(`SELECT id FROM go_chat_messages WHERE session_id='branch'`).Scan(&copiedMessageID); err != nil {
		t.Fatal(err)
	}
	second, err := r.BranchSession(ctx, branchRequest("owner", "branch", copiedMessageID, "branch-2"))
	if err != nil {
		t.Fatal(err)
	}
	if !second.BranchSceneCopied || !second.BranchCapturesCopied {
		t.Fatalf("second branch flags scene=%v captures=%v", second.BranchSceneCopied, second.BranchCapturesCopied)
	}
	refs = 0
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM chat_branch_task_refs WHERE branch_session_id='branch-2' AND task_id='task-parent'`).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("grandparent ref in branch-2 = %d err=%v", refs, err)
	}
	var secondObjects, secondCaptures int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM scene_objects WHERE session_id='branch-2'`).Scan(&secondObjects); err != nil || secondObjects != 1 {
		t.Fatalf("branch-2 objects = %d err=%v", secondObjects, err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM model_captures WHERE session_id='branch-2'`).Scan(&secondCaptures); err != nil || secondCaptures != 1 {
		t.Fatalf("branch-2 captures = %d err=%v", secondCaptures, err)
	}
	var secondObjectID string
	if err := r.db.QueryRow(`SELECT id FROM scene_objects WHERE session_id='branch-2'`).Scan(&secondObjectID); err != nil || secondObjectID == objectID {
		t.Fatalf("branch-2 object id = %q err=%v", secondObjectID, err)
	}
}

func TestBranchRejectsUnsafe3DEvidenceWithoutCreatingABranch(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *Repository)
	}{
		{"missing asset", func(t *testing.T, r *Repository) {
			insertBranchSceneEvidence(t, r, "owner", "parent", "parent", "task-parent", "missing-asset", false)
		}},
		{"other owner task", func(t *testing.T, r *Repository) {
			insertBranchScene(t, r, "owner", "parent", "task-parent", "model-asset")
			if _, err := r.db.Exec(`UPDATE generation_tasks SET owner_id='other' WHERE id='task-parent'`); err != nil {
				t.Fatal(err)
			}
		}},
		{"sibling task", func(t *testing.T, r *Repository) {
			createApprovalSession(t, r, "sibling", "owner", chat.Session{Title: "Sibling"})
			insertBranchSceneEvidence(t, r, "owner", "parent", "sibling", "task-sibling", "sibling-model", true)
		}},
		{"missing prepared input", func(t *testing.T, r *Repository) {
			insertBranchScene(t, r, "owner", "parent", "task-parent", "model-asset")
			if _, err := r.db.Exec(`DELETE FROM generation_prepared_inputs WHERE id='prepared-a'`); err != nil {
				t.Fatal(err)
			}
		}},
		{"invalid active representation", func(t *testing.T, r *Repository) {
			insertBranchScene(t, r, "owner", "parent", "task-parent", "model-asset")
			if _, err := r.db.Exec(`UPDATE scene_objects SET active_representation_id='missing-rep' WHERE id='object-old'`); err != nil {
				t.Fatal(err)
			}
		}},
		{"sibling capture task", func(t *testing.T, r *Repository) {
			createApprovalSession(t, r, "sibling", "owner", chat.Session{Title: "Sibling"})
			insertBranchTask(t, r, "owner", "sibling", "task-sibling", "sibling-model")
			insertBranchCapture(t, r, "owner", "parent", "capture-bad", "task-sibling", "sibling-model")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := openApprovalRepo(t)
			createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Parent"})
			if _, err := r.db.Exec(`INSERT INTO go_chat_messages
				(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at)
				VALUES ('message-1','parent','user','hello','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			test.setup(t, r)
			_, err := r.BranchSession(context.Background(), branchRequest("owner", "parent", "message-1", "branch"))
			if !errors.Is(err, chat.ErrBranchScene) {
				t.Fatalf("unsafe evidence error = %v", err)
			}
			for _, query := range []string{
				`SELECT COUNT(*) FROM go_chat_sessions WHERE id='branch'`,
				`SELECT COUNT(*) FROM chat_branch_task_refs WHERE branch_session_id='branch'`,
			} {
				var count int
				if err := r.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s = %d err=%v", query, count, err)
				}
			}
		})
	}
}

func TestDeleteSessionRemoves3DEvidenceAndKeepsBranchRefsReadable(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Parent"})
	if _, err := r.db.Exec(`INSERT INTO go_chat_messages
		(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at)
		VALUES ('message-1','parent','user','hello','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	insertBranchScene(t, r, "owner", "parent", "task-parent", "model-asset")
	insertBranchCapture(t, r, "owner", "parent", "capture-old", "task-parent", "model-asset")
	if _, err := r.db.Exec(`INSERT INTO render_manifests
		(id,owner_id,session_id,scene_revision,frame_x,frame_y,frame_width,frame_height,
		 pixel_width,pixel_height,matrix_json,inputs_json,renderer_version,
		 output_asset_id,output_size_bytes,output_sha256,created_at)
		VALUES ('manifest','owner','parent',3,0,0,100,100,640,480,'{}','[]','three',
		 'render-asset',128,?, '2026-01-01T04:00:00Z')`, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BranchSession(ctx, branchRequest("owner", "parent", "message-1", "branch")); err != nil {
		t.Fatal(err)
	}
	// Immutable evidence cannot be deleted without the session deletion
	// control row.
	if _, err := r.db.Exec(`DELETE FROM scene_representations WHERE object_id='object-old'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("direct representation delete = %v", err)
	}
	if deleted, err := r.DeleteSession(ctx, "other", "parent"); err != nil || deleted {
		t.Fatalf("foreign delete = %v %v", deleted, err)
	}
	if deleted, err := r.DeleteSession(ctx, "owner", "parent"); err != nil || !deleted {
		t.Fatalf("delete parent = %v %v", deleted, err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM scene_revisions WHERE session_id='parent'`,
		`SELECT COUNT(*) FROM scene_objects WHERE session_id='parent'`,
		`SELECT COUNT(*) FROM model_captures WHERE session_id='parent'`,
		`SELECT COUNT(*) FROM render_manifests WHERE session_id='parent'`,
	} {
		var count int
		if err := r.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s = %d err=%v", query, count, err)
		}
	}
	var branchScene, branchCaptures int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM scene_objects WHERE session_id='branch'`).Scan(&branchScene); err != nil || branchScene != 1 {
		t.Fatalf("branch scene after parent delete = %d err=%v", branchScene, err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM model_captures WHERE session_id='branch'`).Scan(&branchCaptures); err != nil || branchCaptures != 1 {
		t.Fatalf("branch captures after parent delete = %d err=%v", branchCaptures, err)
	}
	// The child branch still exists and keeps the read-only Task ref even
	// though the parent session row (and its Task session binding) is gone.
	var taskSession sql.NullString
	if err := r.db.QueryRow(`SELECT session_id FROM generation_tasks WHERE id='task-parent'`).Scan(&taskSession); err != nil || taskSession.Valid {
		t.Fatalf("parent task session after delete = %v err=%v", taskSession, err)
	}
	refs, err := r.BranchTaskRefs(ctx, "owner", "branch")
	if err != nil || len(refs) != 1 || refs[0] != "task-parent" {
		t.Fatalf("branch refs after parent delete = %v err=%v", refs, err)
	}
	allowed, err := r.TaskInSession(ctx, "owner", "branch", "task-parent")
	if err != nil || !allowed {
		t.Fatalf("ancestor task readable after parent delete = %v err=%v", allowed, err)
	}
	if allowed, err := r.TaskInSession(ctx, "owner", "sibling", "task-parent"); err != nil || allowed {
		t.Fatalf("sibling read after parent delete = %v err=%v", allowed, err)
	}
	if deleted, err := r.DeleteSession(ctx, "owner", "branch"); err != nil || !deleted {
		t.Fatalf("delete branch = %v %v", deleted, err)
	}
	refs, err = r.BranchTaskRefs(ctx, "owner", "branch")
	if err != nil || len(refs) != 0 {
		t.Fatalf("branch refs after branch delete = %v err=%v", refs, err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM scene_revisions`,
		`SELECT COUNT(*) FROM scene_objects`,
		`SELECT COUNT(*) FROM scene_representations`,
		`SELECT COUNT(*) FROM model_captures`,
		`SELECT COUNT(*) FROM render_manifests`,
	} {
		var count int
		if err := r.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s = %d err=%v", query, count, err)
		}
	}
	var violations int
	rows, err := r.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("foreign_key_check violations = %d", violations)
	}
}

func TestMigration0082BackfillsBranchTaskRefsAcrossAncestors(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE go_schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	r := &Repository{db: db}
	all := migrations()
	branchRefs := -1
	for index, migration := range all {
		if migration.version == 82 {
			branchRefs = index
			break
		}
	}
	if branchRefs < 0 {
		t.Fatal("missing migration 0082")
	}
	if err := r.runMigrations(ctx, all[:branchRefs]); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at) VALUES ('grand','owner','owner','Grand','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, nil},
		{`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at,parent_session_id) VALUES ('parent','owner','owner','Parent','2026-01-02T00:00:00Z','2026-01-02T00:00:00Z','grand')`, nil},
		{`INSERT INTO go_chat_sessions (id,owner_id,owner_name,title,created_at,updated_at,parent_session_id) VALUES ('child','owner','owner','Child','2026-01-03T00:00:00Z','2026-01-03T00:00:00Z','parent')`, nil},
		{`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('grand-task','owner','text-to-3d','{}','{}','h','SUCCEEDED',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, nil},
		{`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('parent-task','owner','text-to-3d','{}','{}','h','SUCCEEDED',1,'2026-01-02T00:00:00Z','2026-01-02T00:00:00Z')`, nil},
		{`INSERT INTO generation_tasks (id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES ('child-task','owner','text-to-3d','{}','{}','h','SUCCEEDED',1,'2026-01-03T00:00:00Z','2026-01-03T00:00:00Z')`, nil},
		{`UPDATE generation_tasks SET session_id='grand' WHERE id='grand-task'`, nil},
		{`UPDATE generation_tasks SET session_id='parent' WHERE id='parent-task'`, nil},
		{`UPDATE generation_tasks SET session_id='child' WHERE id='child-task'`, nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed %s: %v", statement.query, err)
		}
	}
	if err := r.runMigrations(ctx, all[branchRefs:]); err != nil {
		t.Fatal(err)
	}
	rows := func(session string) map[string]string {
		t.Helper()
		result, err := r.BranchTaskRefRows(ctx, session, "owner")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := rows("parent"); len(got) != 1 || got["grand-task"] != "grand" {
		t.Fatalf("parent refs = %v", got)
	}
	if got := rows("child"); len(got) != 2 || got["grand-task"] != "grand" || got["parent-task"] != "parent" {
		t.Fatalf("child refs = %v", got)
	}
	if got := rows("grand"); len(got) != 0 {
		t.Fatalf("root session refs = %v", got)
	}
}

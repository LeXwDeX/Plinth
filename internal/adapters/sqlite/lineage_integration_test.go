package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/lineage"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// lineageTasks reads like the task kernel: owner-scoped listing and full get.
type lineageTasks struct{ *Repository }

func (l lineageTasks) GetScoped(ctx context.Context, scope generation.TaskListScope, id generation.TaskID) (generation.Task, error) {
	task, err := l.Repository.Get(ctx, scope.OwnerID(), id)
	if err != nil {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound, Operation: "get", Cause: err}
	}
	return task, nil
}

// The version chain reads real stored rows: frozen input JSON, offering JSON,
// capture records and byte hashes of input assets.
func TestLineageReadsStoredTasksCapturesAndInputAssets(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "lineage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	stamp := "2026-01-01T00:00:00Z"
	for _, row := range [][2]string{{"owner-a", "session-a"}, {"owner-b", "session-b"}} {
		if _, err = repo.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
			row[1], row[0], "n", "t", stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	image := generation.Offering{ID: "image", Parameters: []schema.Parameter{
		{Name: "prompt", Type: "text"},
		{Name: "reference_images", Type: "asset-array", Semantic: schema.SemanticReferenceImage},
	}}
	model := generation.Offering{ID: "model", Parameters: []schema.Parameter{
		{Name: "image", Type: "asset", Semantic: schema.SemanticReferenceImage},
	}}
	addTask := func(id, owner, session string, minute int, offering generation.Offering, input, assetID, role, media, hash string) {
		t.Helper()
		offeringJSON, _ := json.Marshal(offering)
		created := "2026-01-01T00:0" + string(rune('0'+minute)) + ":00Z"
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, owner, session, "text-to-image", string(offeringJSON), input, "h-"+id, "SUCCEEDED", 1, created, created); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			"att-"+id, id, 1, "{}", "SUCCEEDED", "key-"+id, created, created); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			assetID, id, "att-"+id, role, 1, 0, assetID+".bin", media, 4, hash, created); err != nil {
			t.Fatal(err)
		}
	}
	addTask("img-1", "owner-a", "session-a", 1, image, `{"prompt":"fox"}`, "img-1-a", "image", "image/png", "aa11")
	addTask("mdl-1", "owner-a", "session-a", 2, model, `{"image":"upload-1"}`, "mdl-1-a", "model", "model/gltf-binary", "bb22")
	addTask("img-2", "owner-a", "session-a", 3, image, `{"prompt":"fox 2","reference_images":["shot-1"]}`, "img-2-a", "image", "image/png", "cc33")
	addTask("foreign", "owner-b", "session-b", 4, model, `{"image":"img-1-a"}`, "foreign-a", "model", "model/gltf-binary", "dd44")
	for _, input := range []struct{ id, owner, hash string }{
		{"upload-1", "owner-a", "AA11"}, {"shot-1", "owner-a", "ee55"}, {"upload-foreign", "owner-b", "aa11"},
	} {
		if _, err = repo.db.ExecContext(ctx, `INSERT INTO generation_input_assets(id,owner_id,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?)`,
			input.id, input.owner, input.id+".png", "image/png", 4, input.hash, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = repo.db.ExecContext(ctx, `INSERT INTO model_captures
		(id,owner_id,session_id,source_task_id,model_asset_id,screenshot_asset_id,idempotency_key,command_hash,view_json,pixel_width,pixel_height,size_bytes,sha256,created_at)
		VALUES ('cap-1','owner-a','session-a','mdl-1','mdl-1-a','shot-1','k','h','{}',4,3,4,'ee55',?)`, stamp); err != nil {
		t.Fatal(err)
	}
	captures, err := repo.CaptureRepository(CaptureRepositoryConfig{Bytes: captureTestBytes{}, MaxBytes: 1, MaxPixels: 1})
	if err != nil {
		t.Fatal(err)
	}
	var _ lineage.Captures = captures
	service, err := lineage.New(lineageTasks{repo}, captures, repo, lineage.Config{MaxTasks: 100})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := service.Session(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	// The screenshot is a version of its own: the capture record is a node between the model and
	// the picture made from it.
	if len(graph.Nodes) != 4 {
		t.Fatalf("nodes = %+v", graph.Nodes)
	}
	shot, found := "", false
	for _, node := range graph.Nodes {
		if node.Kind == lineage.KindCapture {
			shot, found = node.ID, node.CaptureID == "cap-1" && node.PixelWidth == 4 && node.PixelHeight == 3
		}
	}
	if !found || shot != "capture:cap-1" {
		t.Fatalf("capture node = %q (%v) in %+v", shot, found, graph.Nodes)
	}
	want := map[string]lineage.Edge{
		"mdl-1":         {From: "img-1", To: "mdl-1", Kind: lineage.EdgeReference, Evidence: lineage.EvidenceSameBytes, AssetID: "img-1-a"},
		"capture:cap-1": {From: "mdl-1", To: "capture:cap-1", Kind: lineage.EdgeCapture, Evidence: lineage.EvidenceRecorded, AssetID: "mdl-1-a", CaptureID: "cap-1"},
		"img-2":         {From: "capture:cap-1", To: "img-2", Kind: lineage.EdgeReference, Evidence: lineage.EvidenceRecorded, AssetID: "shot-1", CaptureID: "cap-1"},
	}
	if len(graph.Edges) != len(want) {
		t.Fatalf("edges = %+v", graph.Edges)
	}
	for _, edge := range graph.Edges {
		if edge != want[edge.To] {
			t.Fatalf("edge = %+v, want %+v", edge, want[edge.To])
		}
	}
	// Another owner reads nothing of it, and cannot read it by guessing the session.
	foreign, err := service.Session(ctx, "owner-b", "session-a")
	if err != nil || len(foreign.Nodes) != 0 || len(foreign.Edges) != 0 {
		t.Fatalf("foreign graph = %+v err=%v", foreign, err)
	}
	own, err := service.Session(ctx, "owner-b", "session-b")
	if err != nil || len(own.Nodes) != 2 || own.Nodes[0].ID != "foreign" || own.Nodes[1].Kind != lineage.KindUnknown {
		t.Fatalf("owner-b graph = %+v err=%v", own, err)
	}
	// The foreign task referenced an asset it does not own: unknown, not linked.
	if edges := own.Edges; len(edges) != 1 || edges[0].Evidence != lineage.EvidenceUnknown {
		t.Fatalf("foreign edges = %+v", edges)
	}
}

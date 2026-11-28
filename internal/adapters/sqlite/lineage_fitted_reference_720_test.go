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

var _ lineage.InputDerivations = (*Repository)(nil)

// #720 on stored rows: image to image froze a fitted copy (a derived input
// asset, #276) of the chat's copy of version 1. The version chain follows the
// stored derivation to the copy and its bytes to version 1, with no upload node.
func TestLineageFollowsAStoredFittedCopyToItsVersion_720(t *testing.T) {
	repo, err := openTestRepository(filepath.Join(t.TempDir(), "lineage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	stamp := "2026-01-01T00:00:00Z"
	if _, err = repo.db.ExecContext(ctx, `INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
		"session-a", "owner-a", "n", "t", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	offering, _ := json.Marshal(generation.Offering{ID: "image", Parameters: []schema.Parameter{
		{Name: "prompt", Type: "text"},
		{Name: "reference_images", Type: "asset-array", Semantic: schema.SemanticReferenceImage},
	}})
	for _, row := range []struct{ id, minute, input, hash string }{
		{"img-1", "1", `{"prompt":"fox"}`, "aa11"},
		{"img-2", "2", `{"prompt":"redder","reference_images":["fit-copy"]}`, "bb22"},
	} {
		created := "2026-01-01T00:0" + row.minute + ":00Z"
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_tasks(id,owner_id,session_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			row.id, "owner-a", "session-a", "image-to-image", string(offering), row.input, "h-"+row.id, "SUCCEEDED", 1, created, created); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_attempts(id,task_id,ordinal,offering_json,state,submission_key,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			"att-"+row.id, row.id, 1, "{}", "SUCCEEDED", "key-"+row.id, created, created); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO generation_assets(id,task_id,attempt_id,role,required,ordinal,storage_key,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			row.id+"-a", row.id, "att-"+row.id, "image", 1, 0, row.id+".png", "image/png", 9, row.hash, created); err != nil {
			t.Fatal(err)
		}
	}
	// The chat's copy of img-1 (same bytes, asset_type image) and the fitter's compressed copy of it.
	if err := repo.CreateInputAsset(ctx, generation.InputAsset{ID: "copy-1", OwnerID: "owner-a", StorageKey: "copy-1.png",
		OriginalFilename: "reference.png", AssetType: "image", MediaType: "image/png", Size: 9, SHA256: "aa11"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateDerivedInputAsset(ctx, generation.InputAsset{ID: "fit-copy", OwnerID: "owner-a", StorageKey: "fit-copy.jpg",
		OriginalFilename: "reference.jpg", AssetType: "image", MediaType: "image/jpeg", Size: 4, SHA256: "cc33"},
		generation.InputAssetDerivation{SourceAssetID: "copy-1", Method: "lossy", SourceSize: 9}); err != nil {
		t.Fatal(err)
	}
	service, err := lineage.New(lineageTasks{repo}, nil, repo, lineage.Config{MaxTasks: 100})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := service.Session(ctx, "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Kind != lineage.KindTask {
			t.Fatalf("only the two versions are nodes: %+v", graph.Nodes)
		}
	}
	want := lineage.Edge{From: "img-1", To: "img-2", Kind: lineage.EdgeReference, Evidence: lineage.EvidenceSameBytes, AssetID: "img-1-a"}
	if len(graph.Edges) != 1 || graph.Edges[0] != want {
		t.Fatalf("edges = %+v, want %+v", graph.Edges, want)
	}
	// Another owner cannot follow it: the derivation read is owner scoped.
	if foreign, err := service.Session(ctx, "owner-b", "session-a"); err != nil || len(foreign.Nodes) != 0 {
		t.Fatalf("foreign graph = %+v err=%v", foreign, err)
	}
}

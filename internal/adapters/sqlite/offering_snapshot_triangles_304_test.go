package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #304: a Task row freezes the Offering it ran (offering_json), including
// output_triangles. The factors there were whole numbers before they could be
// decimals (Rodin's Quad is 2.2); a row written then must still read, and a row
// written now keeps the decimal.
func TestStoredOfferingSnapshotsReadWholeAndDecimalTriangleFactors(t *testing.T) {
	repo, err := openTestRepository(filepath.Join(t.TempDir(), "triangle-factors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	// What the previous release wrote for a finished Meshy Remesh: whole numbers.
	const legacy = `{"id":"meshy/processing/remesh","capability":"remesh","parameters":[` +
		`{"name":"topology","type":"select","default":"quad","options":[{"value":"quad"},{"value":"triangle"}]},` +
		`{"name":"target_polycount","type":"number","default":300000}],` +
		`"output_triangles":{"count_parameter":"target_polycount","topology_parameter":"topology",` +
		`"triangles_per_face":{"quad":2,"triangle":1}}}`
	// What this release writes for a Rodin task: the Offering as the repository encodes it.
	rodin := generation.Offering{ID: "hyper3d/rodin-gen-2.5/text-to-3d", Capability: generation.TextTo3D,
		Parameters: []generation.Parameter{
			{Name: "mesh_mode", Type: "select", Default: json.RawMessage(`"Raw"`),
				Options: []generation.ParameterOption{{Value: "Raw"}, {Value: "Quad"}}},
			{Name: "quality_override", Type: "number", Default: json.RawMessage(`2000000`)}},
		OutputTriangles: &generation.OutputTriangleEstimate{CountParameter: "quality_override", TopologyParameter: "mesh_mode",
			TrianglesPerFace: map[string]float64{"Raw": 1, "Quad": 2.2}}}
	encoded, err := json.Marshal(rodin)
	if err != nil {
		t.Fatal(err)
	}

	const stamp = "2026-11-16T00:00:00Z"
	for id, snapshot := range map[string]string{"legacy": legacy, "decimal": string(encoded)} {
		if _, err := repo.db.Exec(`INSERT INTO generation_tasks
			(id, owner_id, session_id, capability, offering_json, input_json, request_hash,
			 idempotency_key, state, progress, version, created_at, updated_at, finished_at)
			VALUES (?, 'owner-a', NULL, 'text-to-3d', ?, '{}', ?, ?, 'SUCCEEDED', 100, 1, ?, ?, ?)`,
			id, snapshot, "hash-"+id, "key-"+id, stamp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	read := func(id string) generation.Offering {
		task, err := repo.Get(context.Background(), "owner-a", generation.TaskID(id))
		if err != nil {
			t.Fatalf("a stored %s Offering no longer reads: %v", id, err)
		}
		return task.Offering
	}
	remesh := read("legacy")
	if remesh.OutputTriangles == nil || remesh.OutputTriangles.TrianglesPerFace["quad"] != 2 || remesh.OutputTriangles.TrianglesPerFace["triangle"] != 1 {
		t.Fatalf("legacy factors = %+v", remesh.OutputTriangles)
	}
	if triangles, ok := remesh.EstimateOutputTriangles(json.RawMessage(`{"target_polycount":160000}`)); !ok || triangles != 320000 {
		t.Fatalf("legacy estimate = %d, %v", triangles, ok)
	}
	stored := read("decimal")
	if stored.OutputTriangles == nil || stored.OutputTriangles.TrianglesPerFace["Quad"] != 2.2 || stored.OutputTriangles.TrianglesPerFace["Raw"] != 1 {
		t.Fatalf("decimal factors = %+v", stored.OutputTriangles)
	}
	if triangles, ok := stored.EstimateOutputTriangles(json.RawMessage(`{"mesh_mode":"Quad","quality_override":10000}`)); !ok || triangles != 22000 {
		t.Fatalf("decimal estimate = %d, %v", triangles, ok)
	}
}

package localparts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// namedExtrasModel is one node with two primitives in a named mesh; every
// source node and the mesh carry extras that the split must preserve.
func namedExtrasModel(t testing.TB) []byte {
	t.Helper()
	f := newFixtureGLB()
	first := f.addQuadPrimitive(0, 1, 0, 1, 0, -1)
	second := f.addQuadPrimitive(2, 3, 0, 1, 0, -1)
	mesh := f.addMesh("source-mesh", first, second)
	f.doc.Meshes[mesh].Extras = json.RawMessage(`{"mesh_tag":"mesh-value"}`)
	leaf := nodeDef{
		Name: "leaf", Extras: json.RawMessage(`{"node_tag":"leaf-value"}`),
		Mesh: intAddress(mesh),
	}
	stage := nodeDef{
		Name: "stage", Extras: json.RawMessage(`{"node_tag":"stage-value"}`),
		Children: []int{1},
	}
	f.doc.Nodes = []nodeDef{stage, leaf}
	f.doc.Scenes = []sceneDef{{Name: "scene", Nodes: []int{0}}}
	return f.bytes(t)
}

func TestSplitPreservesSourceMeshAndExtras(t *testing.T) {
	raw := namedExtrasModel(t)
	offering := loadTestOffering(t, "local/model-parts/split")
	source := sourceAsset(t, "extras-source", "source-task", raw)
	inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
	store := newMemoryStore()
	adapter := newTestAdapter(t, inputs, store)
	input := splitInputJSON(t, string(source.ID), schema.ModelSplitModePrimitive, 0, 32)
	observation, err := adapter.Start(context.Background(), generation.StartRequest{
		SessionID: testSession, OwnerID: testOwner, TaskID: "split-task", AttemptID: "attempt-1",
		Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"source_asset": {source}},
	})
	if err != nil {
		t.Fatalf("Start: %v (cause: %v)", err, errors.Unwrap(err))
	}
	if len(observation.Artifacts) != 3 {
		t.Fatalf("artifacts = %d, want two model parts and one manifest", len(observation.Artifacts))
	}
	manifest := decodeManifest(t, store, observation.Artifacts[len(observation.Artifacts)-1])
	if len(manifest.Parts) != 2 {
		t.Fatalf("manifest parts = %d, want 2", len(manifest.Parts))
	}
	for index := 0; index < 2; index++ {
		body := store.bodies[observation.Artifacts[index].StoredAsset.ID]
		document, _, err := parseGLB(body, modelinput.GeneratedPolicy().Limits)
		if err != nil {
			t.Fatalf("parse output %d: %v", index, err)
		}
		if len(document.Meshes) != 1 {
			t.Fatalf("output %d meshes = %d, want 1", index, len(document.Meshes))
		}
		mesh := document.Meshes[0]
		if mesh.Name != "source-mesh" {
			t.Fatalf("output %d mesh name = %q, want the source mesh name", index, mesh.Name)
		}
		if string(mesh.Extras) != `{"mesh_tag":"mesh-value"}` {
			t.Fatalf("output %d mesh extras = %s", index, mesh.Extras)
		}
		// buildGroup writes the leaf first, then its ancestors, then the part
		// root: leaf, stage, part root.
		if len(document.Nodes) != 3 {
			t.Fatalf("output %d nodes = %d, want 3", index, len(document.Nodes))
		}
		partName := manifest.Parts[index].Name
		if document.Nodes[0].Name != partName {
			t.Fatalf("output %d leaf node name = %q, want part name %q", index, document.Nodes[0].Name, partName)
		}
		if document.Nodes[2].Name != partName {
			t.Fatalf("output %d root node name = %q, want part name %q", index, document.Nodes[2].Name, partName)
		}
		if string(document.Nodes[0].Extras) != `{"node_tag":"leaf-value"}` {
			t.Fatalf("output %d leaf extras = %s", index, document.Nodes[0].Extras)
		}
		if string(document.Nodes[1].Extras) != `{"node_tag":"stage-value"}` {
			t.Fatalf("output %d ancestor extras = %s", index, document.Nodes[1].Extras)
		}
	}
}

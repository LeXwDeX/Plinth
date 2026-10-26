package modelinput_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func TestInstancedTrianglesRetainsDefinitionLimit(t *testing.T) {
	for _, tc := range []struct {
		name                string
		nodes, unreferenced int
		want                int64
	}{
		{"one", 1, 0, 1}, {"instances", 3, 0, 3}, {"unused_definition", 2, 1, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, bin := modelinputtest.Document(modelinputtest.Options{})
			nodes := make([]any, tc.nodes)
			roots := make([]any, tc.nodes)
			for i := range nodes {
				nodes[i] = map[string]any{"mesh": 0}
				roots[i] = i
			}
			doc["nodes"] = nodes
			doc["scenes"] = []any{map[string]any{"nodes": roots}}
			meshes := doc["meshes"].([]any)
			for i := 0; i < tc.unreferenced; i++ {
				meshes = append(meshes, meshes[0])
			}
			doc["meshes"] = meshes
			limits := policy()
			limits.Limits.MaxTriangles = int64(1 + tc.unreferenced)
			profile, err := modelinput.ValidateGLBBytes(context.Background(), modelinputtest.GLB(doc, bin), limits)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Triangles != int64(1+tc.unreferenced) || profile.InstancedTriangles != tc.want {
				t.Fatalf("definition/instance totals = %d/%d, want %d/%d", profile.Triangles, profile.InstancedTriangles, 1+tc.unreferenced, tc.want)
			}
		})
	}
}

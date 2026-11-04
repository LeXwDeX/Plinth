package localparts

import (
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestBuildManifestListsEveryNodePathOfAPart(t *testing.T) {
	box := bounds{}
	box.add(vec3{})
	target := &part{
		name:      "merged",
		triangles: 3,
		box:       box,
		pieces: []*piece{
			{node: nodeInstance{path: "stage/beta"}, triangles: 1, box: box},
			{node: nodeInstance{path: "stage/alpha"}, triangles: 1, box: box},
			{node: nodeInstance{path: "stage/alpha"}, triangles: 1, box: box},
		},
	}
	manifest, err := buildManifest([]*part{target}, schema.ModelSplitModeNode)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(manifest.Parts) != 1 {
		t.Fatalf("manifest parts = %d, want 1", len(manifest.Parts))
	}
	want := []string{"stage/alpha", "stage/beta"}
	if got := manifest.Parts[0].Source.Nodes; !reflect.DeepEqual(got, want) {
		t.Fatalf("part source nodes = %v, want %v", got, want)
	}
}

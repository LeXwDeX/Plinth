package chat

import (
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"testing"
)

func TestSplatToolUses3DScopeAndRuntimeAvailability(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, available := range []bool{false, true} {
		r, err := compileToolRegistry(c.Application().Chat, func(capability, domain string) bool {
			return available && capability == "image-to-splat" && domain == "model-3d"
		})
		if err != nil {
			t.Fatal(err)
		}
		tool := r.byName["create_gaussian_splat"]
		for _, scope := range []string{"2d", "3d", "auto"} {
			found := false
			for _, described := range r.describe(scope) {
				if described.Name == tool.name {
					found = true
				}
			}
			want := available && scope != "2d"
			if found != want || (r.permits(tool, false) && !r.scopeBlocks(tool, scope, false)) != want {
				t.Fatalf("available=%v scope=%s description=%v", available, scope, found)
			}
		}
	}
}

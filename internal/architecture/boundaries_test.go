package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const internalModule = "github.com/LeXwDeX/Plinth/internal/"

// These are dependency rules, not product configuration. Composition and
// persistence adapters may depend on the ports they implement. Declarations
// must remain independent of execution, and execution cannot import its host.
func dependencyAllowed(source, imported string) bool {
	if !strings.HasPrefix(imported, internalModule) {
		return true
	}
	target := strings.TrimPrefix(imported, internalModule)
	switch {
	case within(source, "schema"):
		return within(target, "schema")
	case within(source, "catalog"):
		return within(target, "catalog") || within(target, "schema")
	case within(source, "generation"), within(source, "chat"),
		within(source, "processing"), within(source, "skillrun"), within(source, "agents"):
		return !within(target, "app") && !within(target, "transport") &&
			!within(target, "providers") && !within(target, "adapters")
	case within(source, "providers"):
		return !within(target, "app") && !within(target, "transport") &&
			!within(target, "chat") && !within(target, "processing") &&
			!within(target, "skillrun") && !within(target, "agents")
	default:
		return true
	}
}

func within(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func rejectedImports(source, filename string, content any) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, content, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var rejected []string
	for _, spec := range parsed.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		if !dependencyAllowed(source, imported) {
			rejected = append(rejected, imported)
		}
	}
	return rejected, nil
}

func TestLayerRulesRejectReverseDependencies(t *testing.T) {
	for _, tc := range []struct {
		source, target string
		allowed        bool
	}{
		{"schema", "generation", false},
		{"catalog", "skillrun", false},
		{"catalog", "generation", false},
		{"catalog", "schema", true},
		{"chat/generationtool", "adapters/sqlite", false},
		{"processing", "providers/tripo", false},
		{"generation/future", "app", false},
		{"skillrun", "transport/httpapi", false},
		{"agents/openaicompatible", "app", false},
		{"providers/tripo", "chat", false},
		{"providers/tripo", "generation", true},
		{"providers/meshy", "providers/httpjson", true},
		{"adapters/catalogrecipes", "skillrun", true},
		{"app", "providers/tripo", true},
		{"chat", "providershelper", true},
	} {
		t.Run(tc.source+"->"+tc.target, func(t *testing.T) {
			if got := dependencyAllowed(tc.source, internalModule+tc.target); got != tc.allowed {
				t.Fatalf("dependency allowed = %v, want %v", got, tc.allowed)
			}
		})
	}
	// A path from another module is not a dependency on our implementation.
	if !dependencyAllowed("catalog", "example.com/other/internal/generation") {
		t.Fatal("lookalike module rejected")
	}
}

func TestImportGuardParsesImportsAndFailsClosed(t *testing.T) {
	content := `package catalog
// import "github.com/LeXwDeX/Plinth/internal/skillrun"
import (
    "context"
    _ "github.com/LeXwDeX/Plinth/internal/generation"
)
const example = "github.com/LeXwDeX/Plinth/internal/skillrun"
`
	rejected, err := rejectedImports("catalog", "fixture.go", content)
	if err != nil || len(rejected) != 1 || rejected[0] != internalModule+"generation" {
		t.Fatalf("unexpected import scan: %v, %v", rejected, err)
	}
	if _, err := rejectedImports("catalog", "broken.go", "package catalog\nimport ("); err == nil {
		t.Fatal("invalid imports accepted")
	}
}

func TestProductionDependencyBoundaries(t *testing.T) {
	root := filepath.Join("..")
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		source := filepath.ToSlash(relative)
		counts[strings.Split(source, "/")[0]]++
		rejected, err := rejectedImports(source, path, nil)
		if err != nil {
			return err
		}
		for _, imported := range rejected {
			t.Errorf("%s imports %s against the declared architecture boundary", path, imported)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, anchor := range []string{"schema", "catalog", "generation", "chat", "processing", "skillrun", "agents", "providers"} {
		if counts[anchor] == 0 {
			t.Fatalf("no production files scanned for %s", anchor)
		}
	}
}

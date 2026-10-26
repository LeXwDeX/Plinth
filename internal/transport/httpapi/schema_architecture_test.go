package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPublicHTTPTextCannotBypassProductSchema(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ast.Inspect(source, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok &&
				selector.Sel.Name == "SafeDetail" {
				t.Errorf("%s exposes diagnostic SafeDetail from HTTP transport", file)
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			function, ok := call.Fun.(*ast.Ident)
			if !ok || function.Name != "writeProblem" {
				return true
			}
			if literal, ok := call.Args[3].(*ast.BasicLit); ok &&
				literal.Kind == token.STRING {
				t.Errorf(
					"%s calls writeProblem with public literal %s; use Product Schema",
					file,
					literal.Value,
				)
			}
			return true
		})
	}
}

func TestProductionHTTPRoutesCannotBypassProductSchema(t *testing.T) {
	t.Parallel()
	var files []string
	for _, pattern := range []string{"*.go", "../../assets/input/*.go"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ast.Inspect(source, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && strings.HasPrefix(value, "/api/") {
				t.Errorf(
					"%s contains hardcoded public API route %q; use Product Schema",
					file,
					value,
				)
			}
			return true
		})
	}
}

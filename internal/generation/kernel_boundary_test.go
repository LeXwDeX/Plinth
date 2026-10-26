package generation_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const kernelModulePath = "github.com/LeXwDeX/Plinth"

// kernelForbiddenImports 声明内核生产代码禁止直接依赖的实现包前缀：Provider/
// Adapter 实现包与上层 transport/app。只对导入路径做段边界匹配，不扫描注释、
// 字符串字面量或 Provider 名称，避免误伤合法的元数据与日志文本。
var kernelForbiddenImports = []string{
	kernelModulePath + "/internal/providers",
	kernelModulePath + "/internal/adapters",
	kernelModulePath + "/internal/transport",
	kernelModulePath + "/internal/app",
}

func forbiddenKernelImports(
	fset *token.FileSet,
	filename string,
	source any,
) ([]string, error) {
	parsed, err := parser.ParseFile(fset, filename, source, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	violations := make([]string, 0)
	for _, spec := range parsed.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, forbidden := range kernelForbiddenImports {
			if imported == forbidden || strings.HasPrefix(imported, forbidden+"/") {
				violations = append(violations, imported)
				break
			}
		}
	}
	return violations, nil
}

func TestKernelBoundaryGuardClassifiesForbiddenAndLegalImports(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name: "provider implementation import is forbidden",
			source: `package generation

import _ "github.com/LeXwDeX/Plinth/internal/providers/tripo"
`,
			want: []string{"github.com/LeXwDeX/Plinth/internal/providers/tripo"},
		},
		{
			name: "provider registry root package is forbidden",
			source: `package generation

import _ "github.com/LeXwDeX/Plinth/internal/providers"
`,
			want: []string{"github.com/LeXwDeX/Plinth/internal/providers"},
		},
		{
			name: "adapter transport and composition root imports are forbidden",
			source: `package generation

import (
	_ "github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	_ "github.com/LeXwDeX/Plinth/internal/transport/httpapi"
	_ "github.com/LeXwDeX/Plinth/internal/app"
)
`,
			want: []string{
				"github.com/LeXwDeX/Plinth/internal/adapters/sqlite",
				"github.com/LeXwDeX/Plinth/internal/transport/httpapi",
				"github.com/LeXwDeX/Plinth/internal/app",
			},
		},
		{
			name: "standard library and kernel capability imports are legal",
			source: `package generation

import (
	"context"
	"encoding/json"

	"github.com/LeXwDeX/Plinth/internal/cachemanagement"
)
`,
		},
		{
			name: "lookalike module and package paths are legal",
			source: `package generation

import (
	_ "example.com/x/internal/providers"
	_ "github.com/other/art-ai/internal/providers"
	_ "github.com/LeXwDeX/Plinth/internal/providerstub"
	_ "github.com/LeXwDeX/Plinth/internal/adapterstub"
)
`,
		},
		{
			name: "provider paths in comments and strings are legal",
			source: `package generation

// github.com/LeXwDeX/Plinth/internal/providers/tripo is discussed here
import "context"

const providerLabel = "github.com/LeXwDeX/Plinth/internal/providers/tripo"
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := forbiddenKernelImports(
				token.NewFileSet(),
				"synthetic.go",
				tc.source,
			)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}
			if !slices.Equal(violations, tc.want) {
				t.Fatalf("violations = %v, want %v", violations, tc.want)
			}
		})
	}
}

func TestKernelBoundaryGuardFailsClosedOnUnparsableSource(t *testing.T) {
	violations, err := forbiddenKernelImports(
		token.NewFileSet(),
		"synthetic.go",
		"package generation\nimport (\n",
	)
	if err == nil || violations != nil {
		t.Fatalf("unparsable source returned %v, %v", violations, err)
	}
}

func TestKernelProductionFilesDoNotImportProviderImplementations(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		violations, err := forbiddenKernelImports(fset, file, nil)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		scanned[file] = true
		for _, imported := range violations {
			t.Errorf(
				"%s imports forbidden implementation package %q; the kernel may only consume its own interfaces",
				file,
				imported,
			)
		}
	}
	for _, anchor := range []string{"coordinator.go", "module.go"} {
		if !scanned[anchor] {
			t.Fatalf("boundary scan skipped %s; the guard is vacuous", anchor)
		}
	}
}

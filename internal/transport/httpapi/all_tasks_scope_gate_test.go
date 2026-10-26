package httpapi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllTasksScopeIsAlwaysPermissionGated：generation.AllTasks() 是任务内核里
// 唯一能跨 owner 读取的 scope，但它自身不做任何鉴权——鉴权责任完全落在调用方。
// 于是"新增一处 AllTasks() 调用却忘了先判权限"会静默变成越权读：既能编译通过，
// 运行期也不报错，测试与人工评审都很容易放过。
//
// 这条约定因此必须变成门禁。每处 AllTasks() 调用都要求同一个函数体内、其之前
// 不超过 20 行处出现一个显式权限判断。
//
// 例外只有 legacythumbnail 的后台缩略图任务：它不接受用户请求，没有"主体"可判。
func TestAllTasksScopeIsAlwaysPermissionGated(t *testing.T) {
	t.Parallel()
	// callSiteWindow 是"权限判断必须出现在调用之前多少行内"的容忍窗口，
	// 用来容纳 if/else-if 换行与中间注释，但不容忍整段无关逻辑。
	const callSiteWindow = 20

	permissionGates := []string{
		"PermissionTasksReadAny",
		"PermissionTasksReadOwn",
		"IsAdmin",
	}
	// backgroundScopes 是不接受用户请求的后台作业，天然无需按主体判权限。
	backgroundScopes := map[string]bool{
		"internal/legacythumbnail/service.go": true,
	}

	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	walkErr := filepath.WalkDir(
		moduleRoot,
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				// .opencode 下的 worktree 是同一仓库的独立检出，扫描它们
				// 会把别处的代码当成当前实现来判门禁。
				case ".git", ".opencode", "node_modules", "testdata",
					"web", "dist", "build":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, relErr := filepath.Rel(moduleRoot, path)
			if relErr != nil {
				return relErr
			}
			relative = filepath.ToSlash(relative)
			fset := token.NewFileSet()
			parsed, parseErr := parser.ParseFile(
				fset, path, nil, parser.SkipObjectResolution,
			)
			if parseErr != nil {
				return parseErr
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				for _, call := range allTasksCallPositions(fset, fn.Body) {
					checked++
					if backgroundScopes[relative] {
						continue
					}
					if !hasNearbyPermissionGate(
						fset, fn.Body, call,
						callSiteWindow, permissionGates,
					) {
						t.Errorf(
							"%s 的 %s() 在第 %d 行使用 generation.AllTasks()，"+
								"但其前 %d 行内没有显式权限判断（%s）；"+
								"AllTasks() 自身不鉴权，越权读不会有任何运行期报错",
							relative,
							fn.Name.Name,
							call,
							callSiteWindow,
							strings.Join(permissionGates, " / "),
						)
					}
				}
			}
			return nil
		},
	)
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	// 门禁本身必须非空：解析规则一旦失效，checked==0 会让这条门禁静默放行。
	if checked == 0 {
		t.Fatal("未发现任何 generation.AllTasks() 调用点；解析规则或目录范围已失效，门禁变成空转")
	}
}

// allTasksCallPositions 收集函数体内 generation.AllTasks() 的调用行号。
// 只认限定名 generation.AllTasks，避免把同名方法或其他包的 AllTasks 算进来。
func allTasksCallPositions(
	fset *token.FileSet,
	body *ast.BlockStmt,
) []int {
	positions := []int{}
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "AllTasks" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "generation" {
			return true
		}
		positions = append(positions, fset.Position(call.Pos()).Line)
		return true
	})
	return positions
}

// hasNearbyPermissionGate 判断调用点之前是否出现过显式权限判断。
func hasNearbyPermissionGate(
	fset *token.FileSet,
	body *ast.BlockStmt,
	callLine int,
	window int,
	gates []string,
) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		for _, gate := range gates {
			if selector.Sel.Name != gate {
				continue
			}
			line := fset.Position(selector.Pos()).Line
			if line < callLine && callLine-line <= window {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// TestAllTasksGateClassification 用内联源码证明门禁的判别力，避免这条守卫
// 变成"永远通过"的空转：既能放过真实受保护的调用，也必须拒绝漏判权限的调用。
// 真实扫描的 checked>0 只保证规则没失效，这里保证规则判得对。
func TestAllTasksGateClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "权限判断在前应当放行",
			source: `package p
func f(principal any) {
	if principal.Can(access.PermissionTasksReadAny) {
		scope = generation.AllTasks()
	}
}`,
			want: true,
		},
		{
			name: "管理员判断在前应当放行",
			source: `package p
func f(principal any) {
	if principal.IsAdmin() {
		scope = generation.AllTasks()
	}
}`,
			want: true,
		},
		{
			name: "完全没有权限判断必须拒绝",
			source: `package p
func f(principal any) {
	scope = generation.AllTasks()
	_ = scope
}`,
			want: false,
		},
		{
			name: "权限判断在调用之后不算数（窗口只认之前）",
			source: `package p
func f(principal any) {
	scope = generation.AllTasks()
	if principal.IsAdmin() {
		_ = scope
	}
}`,
			want: false,
		},
		{
			name: "权限判断距离过远必须拒绝",
			source: `package p
func f(principal any) {
	if principal.IsAdmin() {
		_ = 1
	}
	_ = 2
	_ = 3
	_ = 4
	_ = 5
	_ = 6
	_ = 7
	_ = 8
	_ = 9
	_ = 10
	_ = 11
	_ = 12
	_ = 13
	_ = 14
	_ = 15
	_ = 16
	_ = 17
	_ = 18
	_ = 19
	_ = 20
	_ = 21
	_ = 22
	scope = generation.AllTasks()
}`,
			want: false,
		},
		{
			name: "其他包的 AllTasks 不归本门禁管",
			source: `package p
func f() {
	scope = other.AllTasks()
}`,
			want: true,
		},
	}
	gates := []string{
		"PermissionTasksReadAny",
		"PermissionTasksReadOwn",
		"IsAdmin",
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(
				fset, "case.go", testCase.source, parser.SkipObjectResolution,
			)
			if err != nil {
				t.Fatal(err)
			}
			var body *ast.BlockStmt
			for _, decl := range parsed.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					body = fn.Body
				}
			}
			if body == nil {
				t.Fatal("用例源码里没有函数体")
			}
			calls := allTasksCallPositions(fset, body)
			if len(calls) == 0 {
				if testCase.want {
					return
				}
				t.Fatal("用例本应被判违规，却没有解析出调用点，规则失效")
			}
			for _, call := range calls {
				got := hasNearbyPermissionGate(
					fset, body, call, 20, gates,
				)
				if got != testCase.want {
					t.Errorf(
						"第 %d 行的 AllTasks() 判定为 %v，期望 %v",
						call, got, testCase.want,
					)
				}
			}
		})
	}
}

package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// composeEnvironmentKey 只匹配"注入容器的运行时环境变量"，不匹配 build args：
// GOPROXY 之类的构建参数由 go 工具链消费，本就不是应用配置，把它算进死配置
// 会让这个门禁从第一天起就是红的。
var composeEnvironmentKey = regexp.MustCompile(
	`(?m)^\s+([A-Z][A-Z0-9_]+):\s+"\$\{[A-Z0-9_]+`,
)

// runtimeEnvironmentSection 截出 environment: 块。块内成员缩进比 environment
// 自身多，块结束于第一个同级键。
func runtimeEnvironmentSection(compose string) string {
	lines := strings.Split(compose, "\n")
	start := -1
	indent := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "environment:") {
			start = index + 1
			indent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}
	if start < 0 {
		return ""
	}
	for index := start; index < len(lines); index++ {
		line := lines[index]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			return strings.Join(lines[start:index], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// TestComposeInjectsOnlyEnvironmentKeysTheRuntimeReads：compose 注入容器的
// 每个环境变量都必须被 Go 代码真实读取。死配置的危险不在于占位，而在于
// 运维会照着它改环境变量却毫无效果——DEV_MOCK_AUTH_BYPASS_USER_STATE 就是
// 这样长期存在、且一度被误当成"关闭用户状态校验"的开关。
func TestComposeInjectsOnlyEnvironmentKeysTheRuntimeReads(t *testing.T) {
	compose, err := os.ReadFile(
		filepath.Join("..", "..", "docker-compose.go-candidate.yml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	section := runtimeEnvironmentSection(string(compose))
	matches := composeEnvironmentKey.FindAllSubmatch([]byte(section), -1)
	if len(matches) == 0 {
		t.Fatal("未从 compose 解析出任何运行时环境变量，解析规则可能已失效")
	}
	read := runtimeEnvironmentReader(t)
	dead := []string{}
	for _, match := range matches {
		key := string(match[1])
		if !read(key) {
			dead = append(dead, key)
		}
	}
	if len(dead) > 0 {
		t.Fatalf(
			"compose 注入了无人读取的死配置：%s",
			strings.Join(dead, ", "),
		)
	}
}

// runtimeEnvironmentReader 扫 Go 源码，判断某个环境变量名是否真的被读取。
// 扫源码而不是扫"配置结构体字段"，因为同一份 Schema 字段可能由多个环境变量
// 供给——只认字面量才能发现"注入了但没人读"这类死配置。
func runtimeEnvironmentReader(t *testing.T) func(string) bool {
	t.Helper()
	cache := map[string]bool{}
	return func(key string) bool {
		if cached, ok := cache[key]; ok {
			return cached
		}
		found := false
		err := filepath.Walk(
			filepath.Join("..", "..", "internal"),
			func(path string, info os.FileInfo, err error) error {
				if err != nil || found || info.IsDir() ||
					!strings.HasSuffix(path, ".go") {
					return nil //nolint:nilerr // 探测式遍历，遇到不可读项继续
				}
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					return nil //nolint:nilerr // 同上
				}
				if strings.Contains(string(content), `"`+key+`"`) {
					found = true
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		cache[key] = found
		return found
	}
}

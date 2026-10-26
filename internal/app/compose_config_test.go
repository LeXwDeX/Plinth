package app

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestSelfDeployComposeContract 锁定自部署入口 docker-compose.yml 与
// .env.example（#251、#259）：不写死子网或容器 IP，端口与绑定地址来自
// .env，会话密钥与首个管理员密码缺失时 Compose 拒绝启动，构建自部署镜像
// 目标（不含历史迁移工具），不挂载 legacy 目录，也不引用内网主机。
func TestSelfDeployComposeContract(t *testing.T) {
	compose := readRepositoryFile(t, "docker-compose.yml")
	example := readRepositoryFile(t, ".env.example")

	for _, forbidden := range []string{
		"ipv4_address", "ipam", "subnet", "LEGACY_", "legacy",
		"192." + "168.", "yc" + "game", "runtime-with-migration-tools",
	} {
		for name, content := range map[string]string{
			"docker-compose.yml": compose,
			".env.example":       example,
		} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s 不得包含 %q", name, forbidden)
			}
		}
	}
	for _, required := range []string{
		"target: runtime\n",
		`"${ART_AI_BIND_IP:-127.0.0.1}:${ART_AI_PORT:-13333}:13333"`,
		"APP_ENV: production\n",
		"AUTH_MODE: session\n",
		"SESSION_SECRET: ${SESSION_SECRET:?",
		"ADMIN_PASSWORD: ${ADMIN_PASSWORD:?",
		"- art-ai-data:/app/data\n",
		"- art-ai-storage:/app/storage\n",
	} {
		if !strings.Contains(compose, required) {
			t.Errorf("docker-compose.yml 缺少 %q", required)
		}
	}
	// 示例值必须留空：任何非空占位都可能被原样带进部署。
	for _, secret := range []string{"SESSION_SECRET", "ADMIN_PASSWORD", "ASSET_SIGNING_KEY"} {
		if !regexp.MustCompile("(?m)^" + secret + "=$").MatchString(example) {
			t.Errorf(".env.example 的 %s 必须为空值", secret)
		}
	}

	// .env 经 env_file 原样进入容器：除 Compose 自身插值变量外，每个键都
	// 必须是 Product Schema 声明的运行设置环境变量，避免拼错后静默失效。
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, setting := range taskCatalog.Application().Runtime.Configuration.Settings {
		for _, source := range setting.Environment {
			declared[source.Name] = true
		}
	}
	composeOnly := map[string]bool{
		"COMPOSE_PROJECT_NAME": true,
		"ART_AI_BIND_IP":       true,
		"ART_AI_PORT":          true,
		"ART_AI_IMAGE_TAG":     true,
		"ART_AI_SOURCE_COMMIT": true,
		"GOPROXY":              true,
	}
	assignment := regexp.MustCompile(`(?m)^#? ?([A-Z][A-Z0-9_]*)=`)
	for _, match := range assignment.FindAllStringSubmatch(example, -1) {
		if name := match[1]; !declared[name] && !composeOnly[name] {
			t.Errorf(".env.example 的 %s 不是 Product Schema 运行设置", name)
		}
	}
	for name := range composeEnvironment(compose) {
		if !declared[name] {
			t.Errorf("docker-compose.yml environment 的 %s 不是 Product Schema 运行设置", name)
		}
	}
	for _, interpolated := range regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)`).FindAllStringSubmatch(compose, -1) {
		name := interpolated[1]
		if !regexp.MustCompile("(?m)^#? ?" + name + "=").MatchString(example) {
			t.Errorf("docker-compose.yml 插值变量 %s 未写入 .env.example", name)
		}
	}
}

func readRepositoryFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// composeEnvironment 提取唯一服务的 environment 映射：Compose 文件结构
// 固定（environment: 位于 4 空格缩进，条目更深），缩进回退即映射结束。
func composeEnvironment(raw string) map[string]string {
	environment := map[string]string{}
	inEnvironment := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inEnvironment {
			if trimmed == "environment:" {
				inEnvironment = true
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent <= 4 {
			break
		}
		name, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		environment[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return environment
}

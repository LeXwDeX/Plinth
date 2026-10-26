package etl

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var productionSettingOverrides = map[string]string{
	"application_environment":        "production",
	"address":                        "0.0.0.0:13333",
	"database_path":                  "/app/data/art-ai.db",
	"storage_root":                   "/app/storage",
	"web_assets_root":                "/app/web",
	"legacy_generated_images_root":   "/app/legacy-generated",
	"legacy_asset_cache_root":        "/app/legacy-cache",
	"legacy_thumbnail_cache_root":    "/app/legacy-thumbnails",
	"legacy_input_assets_root":       "/app/legacy-assets",
	"legacy_backend_startup_enabled": "false",
	"provider_mode":                  "live",
	"agent_mode":                     "live",
	"auth_mode":                      "session",
}

func runMapEnv(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("map-env", flag.ContinueOnError)
	flags.SetOutput(stderr)
	legacyEnvPath := flags.String(
		"legacy-env",
		"",
		"旧 Next.js 生产 .env 文件（必填）",
	)
	outputPath := flags.String(
		"output",
		"",
		"Go 生产 env 输出文件（必填）",
	)
	catalogPath := flags.String(
		"catalog",
		"",
		"Product Schema Catalog 文件（默认使用内嵌 Catalog）",
	)
	publicBaseURL := flags.String(
		"public-base-url",
		"",
		"生产公开 HTTPS 根 URL；同时覆盖 PUBLIC_BASE_URL 与 NEXT_PUBLIC_APP_URL",
	)
	if err := flags.Parse(args); err != nil {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if strings.TrimSpace(*legacyEnvPath) == "" ||
		strings.TrimSpace(*outputPath) == "" {
		fmt.Fprintln(
			stderr,
			"缺少必填参数 --legacy-env 或 --output。",
		)
		fmt.Fprint(stderr, usageText)
		return 2
	}

	source, err := parseDotEnvFile(*legacyEnvPath)
	if err != nil {
		fmt.Fprintf(stderr, "读取旧环境文件失败：%v\n", err)
		return 1
	}
	product, err := loadEnvMapCatalog(*catalogPath)
	if err != nil {
		fmt.Fprintf(stderr, "读取 Product Schema 失败：%v\n", err)
		return 1
	}
	preserved := map[string]string{}
	if _, statErr := os.Stat(*outputPath); statErr == nil {
		preserved, err = parseDotEnvFile(*outputPath)
		if err != nil {
			fmt.Fprintf(stderr, "读取现有 Go 环境文件失败：%v\n", err)
			return 1
		}
	} else if !os.IsNotExist(statErr) {
		fmt.Fprintf(stderr, "检查 Go 环境文件失败：%v\n", statErr)
		return 1
	}
	mapped, err := mapProductionEnvironment(
		product.Application(),
		source,
		preserved,
		strings.TrimSpace(*publicBaseURL),
	)
	if err != nil {
		fmt.Fprintf(stderr, "环境映射失败：%v\n", err)
		return 1
	}
	if err := writeDotEnvAtomic(*outputPath, mapped); err != nil {
		fmt.Fprintf(stderr, "写入 Go 环境文件失败：%v\n", err)
		return 1
	}
	fmt.Fprintf(
		stdout,
		"Go 生产环境文件已生成：%s（%d 个 Schema 变量，权限 0600）\n",
		*outputPath,
		len(mapped),
	)
	return 0
}

func loadEnvMapCatalog(path string) (*catalog.Catalog, error) {
	if strings.TrimSpace(path) == "" {
		return catalog.LoadDefault()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return catalog.Load(file)
}

func mapProductionEnvironment(
	schema catalog.ApplicationSchema,
	source map[string]string,
	preserved map[string]string,
	publicBaseURL string,
) (map[string]string, error) {
	settingNames := make([]string, 0, len(schema.Runtime.Configuration.Settings))
	for name := range schema.Runtime.Configuration.Settings {
		settingNames = append(settingNames, name)
	}
	sort.Strings(settingNames)

	result := make(map[string]string, len(settingNames))
	for _, name := range settingNames {
		setting := schema.Runtime.Configuration.Settings[name]
		if len(setting.Environment) == 0 {
			return nil, fmt.Errorf(
				"Product Schema 运行设置 %q 没有环境变量声明",
				name,
			)
		}
		value := resolveEnvironmentSetting(setting, source, "production")
		if override, exists := productionSettingOverrides[name]; exists {
			value = override
		}
		canonicalName := setting.Environment[0].Name
		if previous, exists := result[canonicalName]; exists &&
			previous != value {
			return nil, fmt.Errorf(
				"Product Schema 环境变量 %s 被多个运行设置映射为不同值",
				canonicalName,
			)
		}
		if setting.Required && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("缺少必需环境变量 %s", canonicalName)
		}
		if err := validateMappedAllowedValue(name, setting, value); err != nil {
			return nil, err
		}
		result[canonicalName] = value
	}

	if strings.TrimSpace(result["SESSION_SECRET"]) == "" {
		return nil, errors.New("缺少生产必需环境变量 SESSION_SECRET")
	}
	if len(result["SESSION_SECRET"]) < 32 {
		return nil, errors.New("SESSION_SECRET 至少需要 32 个字符")
	}

	if publicBaseURL != "" {
		result["PUBLIC_BASE_URL"] = publicBaseURL
		result["NEXT_PUBLIC_APP_URL"] = publicBaseURL
	} else if result["PUBLIC_BASE_URL"] == "" {
		result["PUBLIC_BASE_URL"] = result["NEXT_PUBLIC_APP_URL"]
	}
	if result["PUBLIC_BASE_URL"] != "" {
		if err := validateProductionPublicURL(result["PUBLIC_BASE_URL"]); err != nil {
			return nil, err
		}
		if result["ASSET_SIGNING_KEY"] == "" {
			result["ASSET_SIGNING_KEY"] = preserved["ASSET_SIGNING_KEY"]
		}
		if result["ASSET_SIGNING_KEY"] == "" {
			generated, err := generateSigningKey()
			if err != nil {
				return nil, fmt.Errorf("生成 ASSET_SIGNING_KEY: %w", err)
			}
			result["ASSET_SIGNING_KEY"] = generated
		}
	}
	if (result["PUBLIC_BASE_URL"] == "") !=
		(result["ASSET_SIGNING_KEY"] == "") {
		return nil, errors.New(
			"PUBLIC_BASE_URL 与 ASSET_SIGNING_KEY 必须同时配置",
		)
	}
	if key := result["ASSET_SIGNING_KEY"]; key != "" && len(key) < 32 {
		return nil, errors.New("ASSET_SIGNING_KEY 至少需要 32 个字符")
	}
	return result, nil
}

func resolveEnvironmentSetting(
	setting catalog.RuntimeSettingSchema,
	source map[string]string,
	environment string,
) string {
	for _, binding := range setting.Environment {
		value, exists := source[binding.Name]
		if !exists || strings.TrimSpace(value) == "" {
			continue
		}
		if binding.TrimSuffix != "" || binding.Append != "" {
			value = strings.TrimRight(value, "/")
			value = strings.TrimSuffix(value, binding.TrimSuffix)
			value += binding.Append
		}
		return value
	}
	if value, exists := setting.DefaultByEnvironment[environment]; exists {
		return value
	}
	return setting.Default
}

func validateMappedAllowedValue(
	name string,
	setting catalog.RuntimeSettingSchema,
	value string,
) error {
	if len(setting.AllowedValues) == 0 || value == "" {
		return nil
	}
	for _, allowed := range setting.AllowedValues {
		if value == allowed {
			return nil
		}
	}
	return fmt.Errorf(
		"运行设置 %s 的值不在 Product Schema allowed_values 中",
		name,
	)
}

func validateProductionPublicURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return errors.New(
			"生产 PUBLIC_BASE_URL 必须是无凭据、query 和 fragment 的 HTTPS URL",
		)
	}
	return nil
}

func generateSigningKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func parseDotEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	result := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export") &&
			len(line) > len("export") &&
			unicode.IsSpace(rune(line[len("export")])) {
			line = strings.TrimSpace(line[len("export"):])
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			return nil, fmt.Errorf("第 %d 行不是 KEY=VALUE", lineNumber)
		}
		key := strings.TrimSpace(line[:separator])
		if !validDotEnvKey(key) {
			return nil, fmt.Errorf("第 %d 行变量名无效", lineNumber)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("第 %d 行变量 %s 重复", lineNumber, key)
		}
		value, err := parseDotEnvValue(line[separator+1:])
		if err != nil {
			return nil, fmt.Errorf("第 %d 行变量 %s: %w", lineNumber, key, err)
		}
		result[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func validDotEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for index, character := range key {
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func parseDotEnvValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '\'':
		return parseQuotedDotEnvValue(raw, '\'', false)
	case '"':
		return parseQuotedDotEnvValue(raw, '"', true)
	default:
		for index, character := range raw {
			if character == '#' &&
				index > 0 &&
				unicode.IsSpace(rune(raw[index-1])) {
				raw = raw[:index]
				break
			}
		}
		return strings.TrimSpace(raw), nil
	}
}

func parseQuotedDotEnvValue(
	raw string,
	quote byte,
	interpretEscapes bool,
) (string, error) {
	var value strings.Builder
	escaped := false
	for index := 1; index < len(raw); index++ {
		character := raw[index]
		if escaped {
			switch {
			case !interpretEscapes && character == quote:
				value.WriteByte(character)
			case !interpretEscapes:
				value.WriteByte('\\')
				value.WriteByte(character)
			case character == 'n':
				value.WriteByte('\n')
			case character == 'r':
				value.WriteByte('\r')
			case character == 't':
				value.WriteByte('\t')
			default:
				value.WriteByte(character)
			}
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character != quote {
			value.WriteByte(character)
			continue
		}
		rest := strings.TrimSpace(raw[index+1:])
		if rest != "" && !strings.HasPrefix(rest, "#") {
			return "", errors.New("引号结束后存在非法内容")
		}
		return value.String(), nil
	}
	return "", errors.New("引号未闭合")
}

func writeDotEnvAtomic(path string, values map[string]string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	writer := bufio.NewWriter(file)
	if _, err := fmt.Fprintln(
		writer,
		"# 由 art-ai-etl map-env 生成；请勿提交。",
	); err != nil {
		return err
	}
	for _, key := range keys {
		encoded, err := encodeDotEnvValue(values[key])
		if err != nil {
			return fmt.Errorf("编码变量 %s: %w", key, err)
		}
		if _, err := fmt.Fprintf(writer, "%s=%s\n", key, encoded); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func encodeDotEnvValue(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\n\r") {
		return "", errors.New("值包含 env 文件无法安全表达的换行或 NUL")
	}
	return "'" + strings.ReplaceAll(value, "'", `\'`) + "'", nil
}

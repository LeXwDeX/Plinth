package etl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestMapEnvCreatesSchemaOwnedProductionEnvironment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	legacyEnv := filepath.Join(root, "legacy.env")
	output := filepath.Join(root, "go-production.env")
	const sessionSecret = "session-secret-with-at-least-32-characters"
	const providerSecret = "meshy-secret # preserved"
	const ssoSecret = "sso-secret-with-specials=$value"
	source := strings.Join([]string{
		"MESHY_API_BASE_URL=https://api.meshy.example/v1",
		`MESHY_API_KEY="` + providerSecret + `"`,
		"LLM_BASE_URL=http://llm.internal.test:8000/",
		"LLM_API_KEY=llm-secret",
		"LLM_MODEL=qwen-test",
		"MATTING_API_URL=http://matting.internal.test:8001",
		"MATTING_API_TOKEN=matting-secret",
		"TRIPO_API_BASE_URL=https://api.tripo.example/v2/openapi",
		"TRIPO_API_KEY=tripo-secret",
		"SESSION_SECRET=" + sessionSecret,
		"NEXT_PUBLIC_APP_URL=https://meshyai.example.com",
		"SSO_ADMIN_CENTER_URL=https://sso.example.com",
		"SSO_APP_ID=art-ai",
		`SSO_APP_SECRET='` + ssoSecret + `'`,
		"SSO_GLOBAL_COOKIE_NAME=AC_OAUTH2_SESSION_ID",
		"ADMIN_PASSWORD=admin-secret",
		"",
	}, "\n")
	if err := os.WriteFile(legacyEnv, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main([]string{
		"map-env",
		"--legacy-env", legacyEnv,
		"--output", output,
	}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf(
			"map-env exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("output mode=%#o want=%#o", got, os.FileMode(0o600))
	}
	values, err := parseDotEnvFile(output)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"APP_ENV":                        "production",
		"APP_ADDR":                       "0.0.0.0:13333",
		"DATABASE_PATH":                  "/app/data/art-ai.db",
		"STORAGE_ROOT":                   "/app/storage",
		"WEB_ASSETS_ROOT":                "/app/web",
		"LEGACY_GENERATED_IMAGES_ROOT":   "/app/legacy-generated",
		"ASSET_CACHE_DIR":                "/app/legacy-cache",
		"THUMBNAIL_CACHE_DIR":            "/app/legacy-thumbnails",
		"LEGACY_INPUT_ASSETS_ROOT":       "/app/legacy-assets",
		"LEGACY_BACKEND_STARTUP_ENABLED": "false",
		"PROVIDER_MODE":                  "live",
		"AGENT_MODE":                     "live",
		"AUTH_MODE":                      "session",
		"MESHY_BASE_URL":                 "https://api.meshy.example/v1",
		"MESHY_API_KEY":                  providerSecret,
		"LLM_BASE_URL":                   "http://llm.internal.test:8000/",
		"LLM_API_KEY":                    "llm-secret",
		"LLM_MODEL":                      "qwen-test",
		"MATTING_BASE_URL":               "http://matting.internal.test:8001",
		"MATTING_API_KEY":                "matting-secret",
		"TRIPO_BASE_URL":                 "https://api.tripo.example/v2/openapi",
		"TRIPO_API_KEY":                  "tripo-secret",
		"SESSION_SECRET":                 sessionSecret,
		"NEXT_PUBLIC_APP_URL":            "https://meshyai.example.com",
		"ADMIN_PASSWORD":                 "admin-secret",
	}
	for key, expected := range want {
		if got := values[key]; got != expected {
			t.Errorf("%s=%q want=%q", key, got, expected)
		}
	}

	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Retired settings may remain in an old source env, but must not be copied
	// into the password-only runtime generated from the current Schema.
	for _, name := range product.Application().Runtime.Configuration.RetiredEnvironment {
		if _, exists := values[name]; exists {
			t.Errorf("retired setting %q was copied to the runtime env", name)
		}
	}
	declared := map[string]struct{}{}
	for _, setting := range product.Application().Runtime.Configuration.Settings {
		for _, source := range setting.Environment {
			declared[source.Name] = struct{}{}
		}
	}
	for key := range values {
		if _, exists := declared[key]; !exists {
			t.Errorf("output key %q is absent from Product Schema", key)
		}
	}
	combinedLog := stdout.String() + stderr.String()
	for _, secret := range []string{
		sessionSecret,
		providerSecret,
		ssoSecret,
		"llm-secret",
		"matting-secret",
		"tripo-secret",
		"admin-secret",
	} {
		if strings.Contains(combinedLog, secret) {
			t.Errorf("secret leaked to command output: %q", secret)
		}
	}
}

func TestMapEnvRejectsMissingProductionSessionSecretWithoutPartialOutput(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	legacyEnv := filepath.Join(root, "legacy.env")
	output := filepath.Join(root, "go-production.env")
	if err := os.WriteFile(
		legacyEnv,
		[]byte("MESHY_API_KEY=do-not-log-this\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main([]string{
		"map-env",
		"--legacy-env", legacyEnv,
		"--output", output,
	}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatal("map-env unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "SESSION_SECRET") {
		t.Fatalf("stderr=%q does not list SESSION_SECRET", stderr.String())
	}
	if strings.Contains(
		stdout.String()+stderr.String(),
		"do-not-log-this",
	) {
		t.Fatal("secret leaked to command output")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("partial output exists: stat err=%v", err)
	}
}

func TestMapEnvAcceptsExplicitHTTPSPublicBaseForLegacyHTTPOrigin(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	legacyEnv := filepath.Join(root, "legacy.env")
	output := filepath.Join(root, "go-production.env")
	if err := os.WriteFile(
		legacyEnv,
		[]byte(strings.Join([]string{
			"SESSION_SECRET=session-secret-with-at-least-32-characters",
			"NEXT_PUBLIC_APP_URL=http://art.internal.test:13333",
			"",
		}, "\n")),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main([]string{
		"map-env",
		"--legacy-env", legacyEnv,
		"--output", output,
		"--public-base-url", "https://meshyai.example.com",
	}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf(
			"map-env exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
	values, err := parseDotEnvFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"PUBLIC_BASE_URL", "NEXT_PUBLIC_APP_URL"} {
		if got := values[key]; got != "https://meshyai.example.com" {
			t.Errorf("%s=%q", key, got)
		}
	}
}

func TestParseDotEnvRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "duplicate.env")
	if err := os.WriteFile(
		path,
		[]byte("SESSION_SECRET=first\nSESSION_SECRET=second\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDotEnvFile(path); err == nil {
		t.Fatal("parseDotEnvFile unexpectedly accepted duplicate keys")
	}
}

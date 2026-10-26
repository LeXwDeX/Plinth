package app

import (
	"os"
	"strings"
	"testing"
)

func TestConfigSourceDoesNotOwnEnvironmentBindingsOrDefaults(t *testing.T) {
	source, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"APP_ENV",
		"APP_ADDR",
		"DATABASE_PATH",
		"STORAGE_ROOT",
		"PROVIDER_MODE",
		"OPENAI_BASE_URL",
		"GEMINI_BASE_URL",
		"MESHY_API_BASE_URL",
		"TRIPO_API_BASE_URL",
		"MATTING_API_URL",
		"AUTH_TOKENS_JSON",
		"DEV_USER_ID",
		"https://api.openai.com",
		"127.0.0.1:13333",
		"./storage-go",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("config.go owns %q instead of Product Schema", forbidden)
		}
	}
}

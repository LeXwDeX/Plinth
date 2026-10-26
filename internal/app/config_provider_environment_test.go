package app

import (
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #252: a Provider is enabled by its own API key only, and a value read from a
// deprecated alias is reported for the startup warning.
func TestProviderEnvironmentContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	config, err := loadConfigFromSchema(schema, func(name string) string {
		switch name {
		case "LLM_API_KEY":
			return "agent-only-key"
		case "LLM_BASE_URL":
			return "http://gateway.example/v1"
		case "MESHY_API_BASE_URL":
			return "https://meshy.example"
		case "MATTING_API_TOKEN":
			return "matting-old-name"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"openai_api_key", "gemini_api_key"} {
		if value := configuredRuntimeSetting(config, schema, setting); value != "" {
			t.Fatalf("%s took the Agent key", setting)
		}
	}
	// LLM_BASE_URL is the Agent's chat model address only: it never
	// redirects an image connection, whose key would otherwise go to the
	// Agent's vendor. OpenAI falls back to its official URL; Gemini has no
	// default and stays unconfigured.
	if got := configuredRuntimeSetting(config, schema, "openai_base_url"); got != "https://api.openai.com/v1" {
		t.Fatalf("openai_base_url = %q; LLM_BASE_URL must not be its alias", got)
	}
	if got := configuredRuntimeSetting(config, schema, "gemini_base_url"); got != "" {
		t.Fatalf("gemini_base_url = %q; an empty GEMINI_BASE_URL must not read LLM_BASE_URL", got)
	}
	if configuredRuntimeSetting(config, schema, "meshy_base_url") != "https://meshy.example" ||
		configuredRuntimeSetting(config, schema, "matting_api_key") != "matting-old-name" {
		t.Fatal("deprecated aliases must keep working")
	}
	want := []string{
		"MATTING_API_TOKEN -> MATTING_API_KEY",
		"MESHY_API_BASE_URL -> MESHY_BASE_URL",
	}
	if !reflect.DeepEqual(config.DeprecatedEnvironment, want) {
		t.Fatalf("deprecated = %v; want %v", config.DeprecatedEnvironment, want)
	}
	// The canonical name wins and is not reported.
	config, err = loadConfigFromSchema(schema, func(name string) string {
		switch name {
		case "MESHY_BASE_URL":
			return "https://canonical.example"
		case "MESHY_API_BASE_URL":
			return "https://old.example"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if configuredRuntimeSetting(config, schema, "meshy_base_url") != "https://canonical.example" || len(config.DeprecatedEnvironment) != 0 {
		t.Fatalf("canonical = %q, deprecated = %v", configuredRuntimeSetting(config, schema, "meshy_base_url"), config.DeprecatedEnvironment)
	}
}

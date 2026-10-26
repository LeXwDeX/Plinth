package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// #322 Scope 2: the administrator's LLM settings and the environment contract
// of chat model connections. The default connection is the long-standing
// LLM_* variables; every extra connection reads only its own <ID>_* names.
func TestDefaultCatalogDeclaresLLMSettingsAndConnectionContract(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	settings := application.LLMSettings
	if settings == nil {
		t.Fatal("llm_settings missing")
	}
	connections := settings.Connections
	if connections.DefaultID != "LLM" || connections.ListSetting != "llm_connections" {
		t.Fatalf("connections = %#v", connections)
	}
	// The default connection's derived names are the existing variables, not
	// new aliases of them.
	for suffix, want := range map[string]string{
		connections.EnvironmentSuffixes.BaseURL:             "LLM_BASE_URL",
		connections.EnvironmentSuffixes.APIKey:              "LLM_API_KEY",
		connections.EnvironmentSuffixes.Model:               "LLM_MODEL",
		connections.EnvironmentSuffixes.ContextWindowTokens: "LLM_CONTEXT_WINDOW_TOKENS",
	} {
		if got := connections.DefaultID + suffix; got != want {
			t.Fatalf("default connection reads %s, want %s", got, want)
		}
	}
	list := application.Runtime.Configuration.Settings[connections.ListSetting]
	if len(list.Environment) == 0 || list.Environment[0].Name != "LLM_CONNECTIONS" || list.Secret {
		t.Fatalf("list setting = %#v", list)
	}
	if !connections.ValidID("QWEN") || !connections.ValidID("LLM_GLM") || connections.ValidID("qwen") ||
		connections.ValidID("") || connections.ValidID("1ABC") || connections.ValidID("A-B") {
		t.Fatal("id pattern accepts the wrong names")
	}
	if settings.Permission == "" || settings.AuditAction == "" || settings.TestAuditAction == "" {
		t.Fatalf("metadata = %#v", settings)
	}
	admin := false
	for _, role := range application.Authentication.Roles {
		if role.Key == application.Authentication.AdminRole {
			for _, permission := range role.Permissions {
				admin = admin || permission == settings.Permission
			}
		}
	}
	if !admin {
		t.Fatal("administrators lack the LLM settings permission")
	}
	navigation := false
	for _, item := range application.Administration.Navigation {
		navigation = navigation || item.Key == "llm" && item.Href == "/admin/llm"
	}
	if !navigation {
		t.Fatal("administration navigation lacks the LLM page")
	}
	if application.Runtime.HTTP.Routes.ChatSessionModel != "/api/v1/chat/sessions/{sessionID}/model" {
		t.Fatalf("chat session model route = %q", application.Runtime.HTTP.Routes.ChatSessionModel)
	}
	public, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	// The public projection carries the routes and copy, never the
	// environment contract or the probe wiring. (The copy names variables,
	// never their values.)
	for _, private := range []string{"environment_suffixes", "list_setting", "\"probe\"", "id_pattern"} {
		if strings.Contains(string(public), private) {
			t.Fatalf("public schema leaked %s", private)
		}
	}
	if !strings.Contains(string(public), `"llm_settings":{"routes":{"admin":"/api/admin/llm-settings"`) {
		t.Fatal("public schema lacks the LLM settings routes")
	}
}

func TestLLMSettingsValidationRejectsADefaultConnectionThatAliasesOtherNames(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*LLMSettingsSchema){
		"default id reads other variables": func(schema *LLMSettingsSchema) { schema.Connections.DefaultID = "OPENAI" },
		"unknown list setting":             func(schema *LLMSettingsSchema) { schema.Connections.ListSetting = "no_such_setting" },
		"empty suffix":                     func(schema *LLMSettingsSchema) { schema.Connections.EnvironmentSuffixes.APIKey = "" },
		"no probe timeout":                 func(schema *LLMSettingsSchema) { schema.Probe.TimeoutSeconds = 0 },
		"duplicate route":                  func(schema *LLMSettingsSchema) { schema.Routes.Test = schema.Routes.Publish },
		"no connections allowed":           func(schema *LLMSettingsSchema) { schema.Connections.MaxConnections = 0 },
	} {
		application := loaded.Application()
		cloned := *application.LLMSettings
		mutate(&cloned)
		application.LLMSettings = &cloned
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// An older Catalog without the section stays valid.
	if err := validateLLMSettings(ApplicationSchema{}); err != nil {
		t.Fatalf("catalog without llm_settings: %v", err)
	}
}

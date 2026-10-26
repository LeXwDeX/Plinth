package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// The open-source release signs in with an account and password only: the
// internal single sign-on contract is gone from the Schema, and the variables
// that configured it are retired so a leftover value is ignored (#249).
func TestDefaultCatalogDeclaresPasswordOnlyAuthentication(t *testing.T) {
	var raw struct {
		Application struct {
			Authentication map[string]json.RawMessage `json:"authentication"`
			Runtime        struct {
				HTTP struct {
					LegacyRoutes map[string]string `json:"legacy_routes"`
				} `json:"http"`
			} `json:"runtime"`
		} `json:"application"`
	}
	if err := json.Unmarshal(defaultCatalog, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw.Application.Authentication["sso"]; exists {
		t.Fatal("authentication.sso is still declared")
	}
	for key, route := range raw.Application.Runtime.HTTP.LegacyRoutes {
		if strings.Contains(key, "sso") || strings.Contains(route, "/sso") {
			t.Fatalf("legacy route %s = %q still serves single sign-on", key, route)
		}
	}

	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for name, setting := range application.Runtime.Configuration.Settings {
		if strings.HasPrefix(name, "sso_") {
			t.Fatalf("runtime setting %q is still declared", name)
		}
		for _, source := range setting.Environment {
			if strings.HasPrefix(source.Name, "SSO_") {
				t.Fatalf("runtime setting %q still reads %s", name, source.Name)
			}
		}
	}
	retired := map[string]bool{}
	for _, name := range application.Runtime.Configuration.RetiredEnvironment {
		retired[name] = true
	}
	for _, name := range []string{
		"SSO_ADMIN_CENTER_URL",
		"SSO_APP_ID",
		"SSO_APP_SECRET",
		"SSO_GLOBAL_COOKIE_NAME",
		"SSO_GLOBAL_COOKIE_DOMAIN",
		"SSO_DIRECTORY_EMAIL_DOMAIN",
		"SSO_ADMIN_ROLE_KEYS",
	} {
		if !retired[name] {
			t.Fatalf("%s is not listed in runtime.configuration.retired_environment", name)
		}
	}

	roles := make([]string, 0, len(application.Authentication.Roles))
	for _, role := range application.Authentication.Roles {
		roles = append(roles, role.Key)
	}
	if strings.Join(roles, ",") != "user,admin" ||
		application.Authentication.DefaultRole != "user" ||
		application.Authentication.AdminRole != "admin" {
		t.Fatalf("roles = %v default = %q admin = %q",
			roles, application.Authentication.DefaultRole, application.Authentication.AdminRole)
	}
	if application.Authentication.Login.RedirectQuery != "redirect" {
		t.Fatalf("login redirect query = %q", application.Authentication.Login.RedirectQuery)
	}
}

func TestRetiredEnvironmentMustNotShadowAnActiveSetting(t *testing.T) {
	schema := RuntimeConfigurationSchema{
		Settings: map[string]RuntimeSettingSchema{
			"session_secret": {
				Type:        "string",
				Environment: []EnvironmentSourceSchema{{Name: "SESSION_SECRET"}},
			},
		},
		RetiredEnvironment: []string{"SSO_APP_ID", "SESSION_SECRET"},
	}
	if err := validateRetiredEnvironment(schema); err == nil ||
		!strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Fatalf("err = %v, want the active variable named", err)
	}
	schema.RetiredEnvironment = []string{"SSO_APP_ID", "SSO_APP_ID"}
	if err := validateRetiredEnvironment(schema); err == nil {
		t.Fatal("duplicate retired name accepted")
	}
	schema.RetiredEnvironment = []string{" SSO_APP_ID"}
	if err := validateRetiredEnvironment(schema); err == nil {
		t.Fatal("padded retired name accepted")
	}
	schema.RetiredEnvironment = []string{"SSO_APP_ID"}
	if err := validateRetiredEnvironment(schema); err != nil {
		t.Fatal(err)
	}
}

package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// #233: the administrator's diagnostics view is declared once in the Schema:
// a route and the permission that guards it. It is server-side only.
func TestDefaultCatalogDeclaresDiagnosticsForAdministratorsOnly(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	diagnostics := application.Diagnostics
	if diagnostics == nil || diagnostics.Routes.Admin != "/api/admin/diagnostics" || diagnostics.Permission != "diagnostics.read" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	declared := false
	for _, permission := range application.Authentication.Permissions {
		declared = declared || permission.Key == diagnostics.Permission
	}
	if !declared {
		t.Fatal("the diagnostics permission is not declared")
	}
	for _, role := range application.Authentication.Roles {
		holds := slices.Contains(role.Permissions, diagnostics.Permission)
		if holds != (role.Key == application.Authentication.AdminRole) {
			t.Errorf("role %q holds the diagnostics permission = %v", role.Key, holds)
		}
	}
	// The browser never receives the section.
	encoded, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	if _, leaked := public["diagnostics"]; leaked || strings.Contains(string(encoded), "/api/admin/diagnostics") {
		t.Fatal("the public projection carries the diagnostics section")
	}
}

func TestDiagnosticsValidationRejectsBadRoutesAndUndeclaredPermissions(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DiagnosticsSchema){
		"empty route":           func(schema *DiagnosticsSchema) { schema.Routes.Admin = "" },
		"relative route":        func(schema *DiagnosticsSchema) { schema.Routes.Admin = "api/admin/diagnostics" },
		"route with a query":    func(schema *DiagnosticsSchema) { schema.Routes.Admin = "/api/admin/diagnostics?x=1" },
		"route with a variable": func(schema *DiagnosticsSchema) { schema.Routes.Admin = "/api/admin/{id}" },
		"empty permission":      func(schema *DiagnosticsSchema) { schema.Permission = "" },
		"unknown permission":    func(schema *DiagnosticsSchema) { schema.Permission = "diagnostics.write" },
	} {
		application := loaded.Application()
		cloned := *application.Diagnostics
		mutate(&cloned)
		application.Diagnostics = &cloned
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// An older Catalog without the section stays valid.
	if err := validateDiagnostics(ApplicationSchema{}); err != nil {
		t.Fatalf("catalog without diagnostics: %v", err)
	}
}

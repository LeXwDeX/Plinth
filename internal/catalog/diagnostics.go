package catalog

import (
	"fmt"
	"strings"
)

// DiagnosticsSchema declares the administrator's read-only diagnostics view
// (#233): runtime facts an operator needs to confirm what the stack is doing,
// never a configuration value or a secret. Today it reports whether the opt-in
// capture of upstream calls is on and what it holds. Server-side only: it is
// not part of the public projection, and /api/version stays as it was.
type DiagnosticsSchema struct {
	Routes DiagnosticsRoutes `json:"routes"`
	// Permission guards the route; it must be declared in
	// authentication.permissions.
	Permission string `json:"permission"`
}

type DiagnosticsRoutes struct {
	Admin string `json:"admin"`
}

func cloneDiagnostics(source *DiagnosticsSchema) *DiagnosticsSchema {
	if source == nil {
		return nil
	}
	result := *source
	return &result
}

func validateDiagnostics(application ApplicationSchema) error {
	schema := application.Diagnostics
	if schema == nil {
		return nil // Older external Catalog documents remain valid.
	}
	route := schema.Routes.Admin
	if !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") || strings.ContainsAny(route, "?#\\{}") {
		return fmt.Errorf("diagnostics: invalid route %q", route)
	}
	declared := false
	for _, permission := range application.Authentication.Permissions {
		if permission.Key == schema.Permission {
			declared = true
		}
	}
	if schema.Permission == "" || !declared {
		return fmt.Errorf("diagnostics: permission %q is not declared in authentication.permissions", schema.Permission)
	}
	return nil
}

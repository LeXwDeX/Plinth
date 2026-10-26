package catalog

import "testing"

// #317: signed-out client error reports are optional (0 keeps the route
// signed-in only) and, when enabled, never looser than the signed-in limits.
func TestAnonymousClientErrorLimitsAreBoundedBySignedInLimits(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	base := c.Application().Runtime
	if err := validateRuntime(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ClientErrorReportSchema){
		"negative global":       func(r *ClientErrorReportSchema) { r.AnonymousGlobalPerMinute = -1 },
		"body above signed-in":  func(r *ClientErrorReportSchema) { r.AnonymousMaxBodyBytes = r.MaxBodyBytes + 1 },
		"text above signed-in":  func(r *ClientErrorReportSchema) { r.AnonymousMaxTextCharacters = r.MaxTextCharacters + 1 },
		"client above global":   func(r *ClientErrorReportSchema) { r.AnonymousPerClientPerMinute = r.AnonymousGlobalPerMinute + 1 },
		"missing client bucket": func(r *ClientErrorReportSchema) { r.AnonymousPerClientPerMinute = 0 },
	} {
		runtime := base
		mutate(&runtime.HTTP.ClientErrorReport)
		if err := validateRuntime(runtime); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	disabled := base
	disabled.HTTP.ClientErrorReport = ClientErrorReportSchema{MaxBodyBytes: 1, PerOwnerPerMinute: 1, MaxTextCharacters: 1}
	if err := validateRuntime(disabled); err != nil {
		t.Fatalf("signed-in only Schema rejected: %v", err)
	}
}

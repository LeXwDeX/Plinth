package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #332: the default Catalog declares personal access keys with the product's
// limits, and the browser sees only what the key dialog needs.
func TestDefaultAccessKeysLoadAndProjectSafeMembers(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	keys := application.AccessKeys
	if keys == nil {
		t.Fatal("default Catalog has no application.access_keys")
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default access keys invalid: %v", err)
	}
	if keys.KeyPrefix != "plk" || keys.MaxKeysPerUser != 10 || keys.DefaultTTLDays != 90 || keys.MaxTTLDays != 365 {
		t.Fatalf("access keys = %+v", keys)
	}
	scopes := map[string]bool{}
	for _, scope := range keys.Scopes {
		scopes[scope.Value] = true
	}
	if len(scopes) != 2 || !scopes["read"] || !scopes["generate"] {
		t.Fatalf("scopes = %+v; the vocabulary is read and generate", keys.Scopes)
	}
	if keys.Audit.CreateAction == "" || keys.Audit.RevokeAction == "" || !strings.Contains(keys.Messages.LimitReached, "{max}") {
		t.Fatalf("audit/messages = %+v %+v", keys.Audit, keys.Messages)
	}

	public := application.Public().AccessKeys
	if public == nil || public.KeyPrefix != "plk" || len(public.Scopes) != 2 || public.MaxKeysPerUser != keys.MaxKeysPerUser {
		t.Fatalf("public access keys = %+v", public)
	}
	encoded, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	section, _ := json.Marshal(document["access_keys"])
	for _, internal := range []string{"last_used_update_seconds", "limit_reached", "create_action", "revoke_action", "resource_type"} {
		if strings.Contains(string(section), internal) {
			t.Fatalf("public access_keys leaks %q: %s", internal, section)
		}
	}
	// The management routes are Schema routes, projected like every other.
	routes := application.Public().Runtime.HTTP.Routes
	encodedRoutes, _ := json.Marshal(routes)
	if !strings.Contains(string(encodedRoutes), "/api/v1/account/access-keys/{id}") {
		t.Fatalf("public routes lack the access key routes: %s", encodedRoutes)
	}
}

func TestAccessKeysValidationRejectsIncompleteOrUnknownDeclarations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"empty prefix":          func(app *catalog.ApplicationSchema) { app.AccessKeys.KeyPrefix = "" },
		"prefix with separator": func(app *catalog.ApplicationSchema) { app.AccessKeys.KeyPrefix = "pl_k" },
		"prefix uppercase":      func(app *catalog.ApplicationSchema) { app.AccessKeys.KeyPrefix = "PLK" },
		"prefix too long":       func(app *catalog.ApplicationSchema) { app.AccessKeys.KeyPrefix = strings.Repeat("a", 17) },
		"no keys allowed":       func(app *catalog.ApplicationSchema) { app.AccessKeys.MaxKeysPerUser = 0 },
		"unbounded keys":        func(app *catalog.ApplicationSchema) { app.AccessKeys.MaxKeysPerUser = 101 },
		"no default lifetime":   func(app *catalog.ApplicationSchema) { app.AccessKeys.DefaultTTLDays = 0 },
		"default above maximum": func(app *catalog.ApplicationSchema) {
			app.AccessKeys.DefaultTTLDays, app.AccessKeys.MaxTTLDays = 100, 99
		},
		"maximum too long": func(app *catalog.ApplicationSchema) { app.AccessKeys.MaxTTLDays = 3651 },
		"empty name bound": func(app *catalog.ApplicationSchema) { app.AccessKeys.NameMaxLength = 0 },
		"no scopes":        func(app *catalog.ApplicationSchema) { app.AccessKeys.Scopes = nil },
		"unlabeled scope":  func(app *catalog.ApplicationSchema) { app.AccessKeys.Scopes[0].Label = "" },
		"duplicate scope": func(app *catalog.ApplicationSchema) {
			app.AccessKeys.Scopes = append(app.AccessKeys.Scopes, app.AccessKeys.Scopes[0])
		},
		"unknown scope":      func(app *catalog.ApplicationSchema) { app.AccessKeys.Scopes[0].Value = "admin" },
		"no last-use window": func(app *catalog.ApplicationSchema) { app.AccessKeys.LastUsedUpdateSeconds = 0 },
		"missing message":    func(app *catalog.ApplicationSchema) { app.AccessKeys.Messages.LimitReached = "" },
		"message without max": func(app *catalog.ApplicationSchema) {
			app.AccessKeys.Messages.LimitReached = "上限已满"
		},
		"missing audit resource type": func(app *catalog.ApplicationSchema) { app.AccessKeys.Audit.ResourceType = "" },
		"same create and revoke": func(app *catalog.ApplicationSchema) {
			app.AccessKeys.Audit.RevokeAction = app.AccessKeys.Audit.CreateAction
		},
		"undeclared create action": func(app *catalog.ApplicationSchema) { app.AccessKeys.Audit.CreateAction = "mint_key" },
		"undeclared revoke action": func(app *catalog.ApplicationSchema) { app.AccessKeys.Audit.RevokeAction = "burn_key" },
		"missing list route":       func(app *catalog.ApplicationSchema) { app.Runtime.HTTP.Routes.AccountAccessKeys = "" },
		"missing item route":       func(app *catalog.ApplicationSchema) { app.Runtime.HTTP.Routes.AccountAccessKey = "" },
		"list route with parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountAccessKeys = "/api/v1/account/{id}/access-keys"
		},
		"item route without parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountAccessKey = "/api/v1/account/access-key"
		},
		"item route with the wrong parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountAccessKey = "/api/v1/account/access-keys/{keyID}"
		},
		"route duplicates another": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.AccountAccessKeys = app.Runtime.HTTP.Routes.Tasks
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			application := defaultApplication(t)
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("invalid access keys declaration was accepted")
			}
		})
	}
}

func TestAccessKeysCloneDoesNotShareSlices(t *testing.T) {
	t.Parallel()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := source.Application()
	first.AccessKeys.Scopes[0].Value = "mutated"
	second := source.Application()
	if second.AccessKeys.Scopes[0].Value == "mutated" {
		t.Fatal("Catalog Application() shares access key scopes with a previous copy")
	}
}

// A Catalog written before the section existed loads unchanged: no key can be
// issued, nothing is projected, and a shared en bundle that translates the
// section does not make the document invalid.
func TestHistoricalCatalogWithoutAccessKeysStaysValid(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.AccessKeys = nil
	application.Runtime.HTTP.Routes.AccountAccessKeys = ""
	application.Runtime.HTTP.Routes.AccountAccessKey = ""
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("historical Catalog without access_keys rejected: %v", err)
	}
	if application.Public().AccessKeys != nil {
		t.Fatal("historical Catalog projected an access_keys section")
	}
}

// The English public schema carries the translated scope labels, and the
// server-side limit message has an English overlay.
func TestAccessKeysLabelsAreLocalized(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	localized, err := catalog.NewLocalizedPublicSchemas(application)
	if err != nil {
		t.Fatal(err)
	}
	english := localized.For("en").Schema.AccessKeys
	if english == nil {
		t.Fatal("english public schema lost access_keys")
	}
	labels := map[string]string{}
	for _, scope := range english.Scopes {
		labels[scope.Value] = scope.Label
	}
	if labels["read"] != "Read (query and download)" || labels["generate"] != "Generate (start generation and processing)" {
		t.Fatalf("english labels = %v", labels)
	}
}

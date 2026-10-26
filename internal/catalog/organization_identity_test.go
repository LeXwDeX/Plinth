package catalog

import (
	"strings"
	"testing"
)

// The default Product Schema ships with the open-source repository, so it
// must not carry one organization's domains, brand, hosts or server paths
// (#249). A deployment that needs its own identity sets it through runtime
// settings or a CATALOG_FILE of its own.
func TestDefaultCatalogCarriesNoOrganizationIdentifiers(t *testing.T) {
	lowered := strings.ToLower(string(defaultCatalog))
	// Assembled from pieces so the guard itself carries no identifier (#261).
	for _, marker := range []string{
		"yc" + "game",
		"yun" + "chan",
		"云" + "蟾",
		"192." + "168.",
		"33." + "110",
		"33." + "146",
		"/data/" + "appdata",
	} {
		if index := strings.Index(lowered, marker); index >= 0 {
			start := max(index-60, 0)
			end := min(index+60, len(lowered))
			t.Fatalf(
				"default catalog contains %q near %q",
				marker,
				string(defaultCatalog[start:end]),
			)
		}
	}
}

func TestDefaultCatalogLeavesDeploymentIdentityToExplicitSettings(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	settings := application.Runtime.Configuration.Settings
	if got := settings["admin_email"].Default; got != "admin@example.invalid" {
		t.Fatalf("admin_email default = %q", got)
	}
}

// The project name is the only brand the default Schema carries (#249).
func TestDefaultCatalogUsesProjectBrand(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	brand := application.Content.Brand
	if brand.Title != "Plinth" ||
		!strings.HasPrefix(brand.MetadataTitle, "Plinth") ||
		application.Authentication.Login.Title != "Plinth" ||
		application.Content.Service.Name != "Plinth Platform" {
		t.Fatalf("brand = %#v, login = %q, service = %q",
			brand, application.Authentication.Login.Title, application.Content.Service.Name)
	}
	metadata := application.Runtime.AssetDelivery.Proxy.Metadata
	if metadata.Artist != "Plinth" || metadata.Software != "Plinth" ||
		!strings.Contains(metadata.CopyrightTemplate, "Plinth") {
		t.Fatalf("asset metadata = %#v", metadata)
	}
}

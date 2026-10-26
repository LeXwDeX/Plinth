package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestDefaultProductSchemaOwnsBrowserRoutes(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	clientRoutes := application.Runtime.Frontend.ClientRoutes
	if clientRoutes.Home != "/" || clientRoutes.Login != "/login" {
		t.Fatalf("client routes = %#v", clientRoutes)
	}
	if application.Runtime.Frontend.WebAssetPrefix != "/assets/" {
		t.Fatalf(
			"web asset prefix = %q",
			application.Runtime.Frontend.WebAssetPrefix,
		)
	}
	if application.Runtime.Frontend.FaviconPath != "/favicon.ico" {
		t.Fatalf(
			"favicon path = %q",
			application.Runtime.Frontend.FaviconPath,
		)
	}
	hasAssetPrefix := false
	for _, path := range application.Authentication.PublicPaths {
		if path == application.Runtime.Frontend.WebAssetPrefix {
			hasAssetPrefix = true
			break
		}
	}
	if !hasAssetPrefix {
		t.Fatalf(
			"authentication public paths do not contain web asset prefix %q: %#v",
			application.Runtime.Frontend.WebAssetPrefix,
			application.Authentication.PublicPaths,
		)
	}

	keys := make(map[string]struct{})
	for _, item := range application.Administration.Navigation {
		if item.Key == "" {
			t.Fatalf("administration navigation = %#v", item)
		}
		if _, exists := keys[item.Key]; exists {
			t.Fatalf("duplicate administration navigation key %q", item.Key)
		}
		keys[item.Key] = struct{}{}
	}
}

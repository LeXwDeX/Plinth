package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAuthenticatedAssetCachePolicyCannotBeWeakenedBySchema(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*catalog.ApplicationSchema)
	}{
		{"task asset", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.ImmutableAssetCacheControl = "public, max-age=86400"
		}},
		{"private API", func(a *catalog.ApplicationSchema) { a.Runtime.HTTP.PrivateCacheControl = "public, max-age=86400" }},
		{"input asset", func(a *catalog.ApplicationSchema) { a.Runtime.InputAssets.BinaryCacheControl = "public, max-age=86400" }},
		{"legacy image", func(a *catalog.ApplicationSchema) {
			a.Runtime.AssetDelivery.LegacyGeneratedImage.CacheControl = "public, max-age=86400"
		}},
		{"asset proxy", func(a *catalog.ApplicationSchema) {
			a.Runtime.AssetDelivery.Proxy.Cache.Control = "public, max-age=86400"
		}},
		{"proxy CORS", func(a *catalog.ApplicationSchema) { a.Runtime.AssetDelivery.Proxy.Cache.CORSOrigin = "*" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := loaded.Application()
			test.change(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("accepted unsafe authenticated asset policy")
			}
		})
	}
}

// #225: thumbnails may be cached privately for a bounded time; build assets
// may be immutable; nothing may become public or shared-cacheable.
func TestThumbnailAndBuildAssetCachePoliciesStayPrivateAndBounded(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	defaults := loaded.Application().Runtime.HTTP
	if defaults.ThumbnailCacheControl != "private, max-age=3600" || len(defaults.ThumbnailAssetRoles) != 1 ||
		defaults.ThumbnailAssetRoles[0] != "thumbnail" || defaults.ImmutableAssetCacheControl != "private, max-age=31536000, immutable" ||
		defaults.SessionChangeClearSiteData != `"cache"` || defaults.PrivateCacheControl != "private, no-store" {
		t.Fatalf("default cache policies = %+v", defaults)
	}
	for _, test := range []struct {
		name   string
		change func(*catalog.HTTPRuntimeSchema)
	}{
		{"public thumbnail", func(h *catalog.HTTPRuntimeSchema) { h.ThumbnailCacheControl = "public, max-age=3600" }},
		{"shared thumbnail", func(h *catalog.HTTPRuntimeSchema) { h.ThumbnailCacheControl = "private, max-age=60, s-maxage=60" }},
		{"thumbnail without max-age", func(h *catalog.HTTPRuntimeSchema) { h.ThumbnailCacheControl = "private" }},
		{"thumbnail above the cap", func(h *catalog.HTTPRuntimeSchema) { h.ThumbnailCacheControl = "private, max-age=86401" }},
		{"cap above one day", func(h *catalog.HTTPRuntimeSchema) { h.ThumbnailCacheMaxAgeSeconds = 172800 }},
		{"no thumbnail roles", func(h *catalog.HTTPRuntimeSchema) { h.ThumbnailAssetRoles = nil }},
		{"public build assets", func(h *catalog.HTTPRuntimeSchema) {
			h.ImmutableAssetCacheControl = "public, max-age=31536000, immutable"
		}},
		{"cacheable but mutable build assets", func(h *catalog.HTTPRuntimeSchema) { h.ImmutableAssetCacheControl = "private, max-age=600" }},
		{"clear-site-data without cache", func(h *catalog.HTTPRuntimeSchema) { h.SessionChangeClearSiteData = `"cookies"` }},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := loaded.Application()
			test.change(&application.Runtime.HTTP)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("accepted unsafe cache policy")
			}
		})
	}
}

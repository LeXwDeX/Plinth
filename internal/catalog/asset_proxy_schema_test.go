package catalog_test

import (
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestDefaultCatalogOwnsLegacyAssetProxyContract(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	proxy := application.Runtime.AssetDelivery.Proxy

	if application.Runtime.HTTP.LegacyRoutes.MeshyProxyAsset !=
		"/api/meshy/proxy-asset" ||
		proxy.API.Query.URL != "url" ||
		proxy.API.Query.Filename != "filename" ||
		proxy.API.Query.TaskID != "taskId" ||
		proxy.API.Query.Size != "size" ||
		proxy.API.Fields.Error != "error" ||
		proxy.API.Statuses.Success != 200 ||
		proxy.API.Statuses.BadRequest != 400 ||
		proxy.API.Statuses.Forbidden != 403 ||
		proxy.API.Statuses.NotFound != 404 ||
		proxy.API.Statuses.BadGateway != 502 {
		t.Fatalf("asset proxy API = %#v", proxy.API)
	}
	if proxy.Security.HostSource != "offering_asset_hosts" ||
		!slices.Equal(proxy.Security.AllowedSchemes, []string{"http", "https"}) ||
		!slices.Contains(proxy.Security.AllowedHosts, "assets.meshy.ai") ||
		!slices.Contains(proxy.Security.AllowedHosts, "tripo3d.com") {
		t.Fatalf("asset proxy security = %#v", proxy.Security)
	}
	if proxy.Cache.RootSetting != "legacy_asset_cache_root" ||
		proxy.Cache.ShardCharacters != 2 ||
		proxy.Cache.MetadataSuffix != ".meta.json" ||
		proxy.Cache.MetadataFields.URL != "url" ||
		proxy.Cache.MetadataFields.ContentType != "contentType" ||
		proxy.Cache.MetadataFields.Size != "size" ||
		proxy.Cache.MetadataFields.CachedAt != "cachedAt" ||
		proxy.Cache.Control != "private, no-store" ||
		proxy.Cache.CORSOrigin != "" ||
		proxy.Cache.StatusHeader != "X-Cache" ||
		proxy.Cache.HitValue != "HIT" ||
		proxy.Cache.MissValue != "MISS" ||
		proxy.Cache.RefreshedValue != "REFRESHED" {
		t.Fatalf("asset proxy cache = %#v", proxy.Cache)
	}
	if proxy.HTTP.RequestTimeoutMilliseconds != 300000 ||
		proxy.HTTP.ResponseMaxBytes != 1073741824 ||
		proxy.HTTP.MaximumRedirects != 5 {
		t.Fatalf("asset proxy HTTP = %#v", proxy.HTTP)
	}
	if proxy.Thumbnail.RootSetting != "legacy_thumbnail_cache_root" ||
		proxy.Thumbnail.RequestValue != "thumb" ||
		proxy.Thumbnail.MaximumDimension != 256 ||
		proxy.Thumbnail.OutputMediaType != "image/png" ||
		proxy.Thumbnail.Header != "X-Thumbnail" ||
		proxy.Thumbnail.HeaderValue != "thumb-256" {
		t.Fatalf("asset proxy thumbnail = %#v", proxy.Thumbnail)
	}
	if !slices.Equal(proxy.Metadata.SupportedMediaTypes, []string{
		"image/jpeg",
		"image/png",
		"image/webp",
		"image/tiff",
	}) ||
		proxy.Metadata.CopyrightTemplate == "" ||
		proxy.Metadata.Artist == "" ||
		proxy.Metadata.Software != "Plinth" {
		t.Fatalf("asset proxy metadata = %#v", proxy.Metadata)
	}
	if proxy.Audit.Action != "download_asset" ||
		proxy.Audit.ResourceType != "asset" ||
		proxy.Audit.ResourceIDMaximumCharacters != 200 ||
		proxy.Audit.DetailFields.Filename != "filename" ||
		proxy.Audit.DetailFields.Size != "size" ||
		proxy.Messages.MissingURL != "缺少 url 参数" ||
		proxy.Messages.InvalidURL != "无效的 URL" ||
		proxy.Messages.ForbiddenHost != "不允许访问的域名" ||
		proxy.Messages.FetchFailed != "代理资源获取失败" {
		t.Fatalf("asset proxy product contract = %#v %#v %#v",
			proxy.Audit, proxy.Messages, proxy.Metadata)
	}

	application.Runtime.AssetDelivery.Proxy.API.Query.URL = ""
	if err := catalog.ValidateApplicationSchema(application); err == nil {
		t.Fatal("缺少 URL 查询字段的资产代理 Product Schema 被接受")
	}
}

func TestApplicationReturnsDefensiveLegacyAssetProxyClone(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Runtime.AssetDelivery.Proxy.Security.AllowedSchemes[0] = "changed"
	first.Runtime.AssetDelivery.Proxy.Security.AllowedHosts[0] = "changed"
	first.Runtime.AssetDelivery.Proxy.Metadata.SupportedMediaTypes[0] =
		"changed"
	first.Runtime.AssetDelivery.Proxy.MediaTypeExtensions["image/png"] =
		".changed"

	second := taskCatalog.Application()
	if second.Runtime.AssetDelivery.Proxy.Security.AllowedSchemes[0] !=
		"http" ||
		second.Runtime.AssetDelivery.Proxy.Security.AllowedHosts[0] ==
			"changed" ||
		second.Runtime.AssetDelivery.Proxy.Metadata.SupportedMediaTypes[0] !=
			"image/jpeg" ||
		second.Runtime.AssetDelivery.Proxy.MediaTypeExtensions["image/png"] !=
			".png" {
		t.Fatal("资产代理 Product Schema clone 泄漏了调用方修改")
	}
}

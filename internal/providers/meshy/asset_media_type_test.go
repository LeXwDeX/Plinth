package meshy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
)

// Meshy 的 CDN 按宿主机 mime.types 标注下载内容，OBJ 会得到
// application/x-tgif（TGIF 图像类型）。该标签不携带模型格式信息，Adapter
// 保留自己声明的精确类型；通用 octet-stream 交给内核保留声明，具体的
// text/html 等响应仍原样返回并参与物化契约校验（#191）。
func TestOpenAssetKeepsDeclaredModelTypeForMeshyCDNLabel(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		claimMedia  string
		wantMedia   string
	}{
		{"obj mislabeled as tgif", "application/x-tgif", "model/obj", "model/obj"},
		{"generic octet stream stays for the kernel", "application/octet-stream", "model/fbx", "application/octet-stream"},
		{"specific error page is not masked", "text/html; charset=utf-8", "model/gltf-binary", "text/html; charset=utf-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte("asset-bytes"))
			}))
			defer server.Close()
			parsed, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			adapter := newMeshyAdapter(t, meshy.Config{
				BaseURL:         server.URL,
				APIKey:          "test-key",
				HTTPClient:      server.Client(),
				AllowHTTPAssets: true,
			})
			body, metadata, err := adapter.OpenAsset(context.Background(), generation.OpenAssetRequest{
				Offering: generation.Offering{ProviderID: "meshy", AssetHosts: []string{parsed.Hostname()}},
				Claim: generation.ArtifactClaim{
					Role:      "model",
					Variant:   "obj",
					Locator:   server.URL + "/model.obj",
					MediaType: tc.claimMedia,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			content, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "asset-bytes" {
				t.Fatalf("body = %q", content)
			}
			if metadata.MediaType != tc.wantMedia {
				t.Fatalf("media_type = %q, want %q", metadata.MediaType, tc.wantMedia)
			}
		})
	}
}

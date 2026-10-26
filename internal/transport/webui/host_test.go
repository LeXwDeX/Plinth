package webui_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/transport/webui"
)

func TestHostServesSchemaDrivenWebShellAndDelegatesAPI(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	api := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/health" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!doctype html>
<html lang="{{.Locale}}">
<head>
  <title>{{.Title}}</title>
  <meta name="description" content="{{.Description}}">
  <link rel="icon" href="{{.Favicon}}">
</head>
<body>
  <div id="root"></div>
  <script>window.__ART_AI_PRODUCT_SCHEMA__={{.Schema}};</script>
</body>
</html>`),
		},
	}

	handler, err := webui.New(webui.Config{
		Assets:      fs.FS(assets),
		Application: application,
		API:         api,
	})
	if err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	pageRequest := httptest.NewRequest(
		http.MethodGet,
		"/admin/users",
		nil,
	)
	handler.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, body = %s", page.Code, page.Body)
	}
	body := page.Body.String()
	for _, expected := range []string{
		`<html lang="zh-CN">`,
		`<title>Plinth - 多模型创作平台</title>`,
		`<link rel="icon" href="/favicon.ico">`,
		`window.__ART_AI_PRODUCT_SCHEMA__=`,
		`"revision":"` + application.Revision + `"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("page body does not contain %q: %s", expected, body)
		}
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		response.Body.String() != `{"status":"ok"}` {
		t.Fatalf(
			"API response = %d %q",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestHostServesVersionedBrowserAssetsWithSchemaCachePolicy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<html><body>{{.Title}}</body></html>`),
		},
		"assets/app-deadbeef.js": &fstest.MapFile{
			Data: []byte(`console.log("go-web")`),
		},
	}
	handler, err := webui.New(webui.Config{
		Assets:      fs.FS(assets),
		Application: application,
		API:         http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/assets/app-deadbeef.js",
		nil,
	)
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("asset status = %d, body = %s", response.Code, response.Body)
	}
	if response.Body.String() != `console.log("go-web")` {
		t.Fatalf("asset body = %q", response.Body.String())
	}
	if response.Header().Get("Cache-Control") !=
		application.Runtime.HTTP.ImmutableAssetCacheControl {
		t.Fatalf(
			"asset Cache-Control = %q",
			response.Header().Get("Cache-Control"),
		)
	}
}

func TestHostServesPublicBrowserFilesWithoutShadowingTheAPI(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<html><body>{{.Title}}</body></html>`),
		},
		"gpt-skills/example.webp": &fstest.MapFile{
			Data: []byte("browser-preview"),
		},
	}
	api := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := webui.New(webui.Config{
		Assets:      fs.FS(assets),
		Application: application,
		API:         api,
	})
	if err != nil {
		t.Fatal(err)
	}

	publicFile := httptest.NewRecorder()
	publicFileRequest := httptest.NewRequest(
		http.MethodGet,
		"/gpt-skills/example.webp",
		nil,
	)
	handler.ServeHTTP(publicFile, publicFileRequest)
	if publicFile.Code != http.StatusOK ||
		publicFile.Body.String() != "browser-preview" {
		t.Fatalf(
			"public file response = %d %q",
			publicFile.Code,
			publicFile.Body.String(),
		)
	}
	if publicFile.Header().Get("Cache-Control") !=
		application.Runtime.HTTP.PrivateCacheControl {
		t.Fatalf(
			"public file Cache-Control = %q",
			publicFile.Header().Get("Cache-Control"),
		)
	}

	apiResponse := httptest.NewRecorder()
	apiRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/not-a-static-file",
		nil,
	)
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusTeapot {
		t.Fatalf("API status = %d", apiResponse.Code)
	}
}

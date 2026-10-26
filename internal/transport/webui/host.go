// Package webui hosts the browser application and the Go HTTP interface behind
// one net/http Handler.
package webui

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/transport/httplocale"
)

type Config struct {
	Assets      fs.FS
	Application catalog.ApplicationSchema
	API         http.Handler
}

type Host struct {
	application catalog.ApplicationSchema
	schemas     *catalog.LocalizedPublicSchemas
	api         http.Handler
	assets      fs.FS
	files       http.Handler
	shell       *template.Template
	clientPaths map[string]struct{}
}

type shellData struct {
	Locale      string
	Title       string
	Description string
	Favicon     string
	Schema      template.JS
}

func New(config Config) (http.Handler, error) {
	if config.Assets == nil {
		return nil, errors.New("webui: 缺少浏览器资源")
	}
	if config.API == nil {
		return nil, errors.New("webui: 缺少 Go HTTP handler")
	}
	if err := catalog.ValidateApplicationSchema(config.Application); err != nil {
		return nil, fmt.Errorf("webui: Product Schema 无效: %w", err)
	}
	document, err := fs.ReadFile(config.Assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("webui: 读取 index.html: %w", err)
	}
	shell, err := template.New("web-shell").Parse(string(document))
	if err != nil {
		return nil, fmt.Errorf("webui: 解析 index.html: %w", err)
	}
	schemas, err := catalog.NewLocalizedPublicSchemas(config.Application)
	if err != nil {
		return nil, fmt.Errorf("webui: 公开 Schema 投影: %w", err)
	}
	return &Host{
		application: config.Application,
		schemas:     schemas,
		api:         config.API,
		assets:      config.Assets,
		files:       http.FileServer(http.FS(config.Assets)),
		shell:       shell,
		clientPaths: projectClientPaths(config.Application),
	}, nil
}

func (h *Host) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet &&
		request.Method != http.MethodHead {
		h.api.ServeHTTP(writer, request)
		return
	}
	if h.serveStaticFile(writer, request) {
		return
	}
	if _, exists := h.clientPaths[request.URL.Path]; !exists {
		h.api.ServeHTTP(writer, request)
		return
	}
	h.serveShell(writer, request)
}

func (h *Host) serveStaticFile(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	name := strings.TrimPrefix(request.URL.Path, "/")
	if name == "" || name == "index.html" {
		return false
	}
	info, err := fs.Stat(h.assets, name)
	if err != nil || info.IsDir() {
		return false
	}
	cacheControl := h.application.Runtime.HTTP.PrivateCacheControl
	if strings.HasPrefix(
		request.URL.Path,
		h.application.Runtime.Frontend.WebAssetPrefix,
	) {
		cacheControl = h.application.Runtime.HTTP.ImmutableAssetCacheControl
	}
	writer.Header().Set("Cache-Control", cacheControl)
	h.files.ServeHTTP(writer, request)
	return true
}

func (h *Host) serveShell(
	writer http.ResponseWriter,
	request *http.Request,
) {
	// 首屏与 /api/v1/schema 用同一份按请求语言选出的公开投影；html lang、
	// 标题与描述都跟随该语言。
	projection := h.schemas.For(httplocale.FromRequest(h.application, request))
	var document bytes.Buffer
	if err := h.shell.Execute(&document, shellData{
		Locale:      projection.Locale,
		Title:       projection.Schema.Content.Brand.MetadataTitle,
		Description: projection.Schema.Content.Brand.MetadataDescription,
		Favicon:     h.application.Runtime.Frontend.FaviconPath,
		Schema:      template.JS(projection.JSON),
	}); err != nil {
		http.Error(
			writer,
			h.application.Content.Messages["error.internal"],
			http.StatusInternalServerError,
		)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	httplocale.MarkResponse(writer.Header(), projection.Locale)
	writer.Header().Set(
		"Cache-Control",
		h.application.Runtime.HTTP.PrivateCacheControl,
	)
	// SEC-04：SPA shell 与 API 共用同一套 Schema 声明的安全响应头。
	// shell 额外把当次内联注入脚本的哈希来源并入声明的 script-src，使浏览器在
	// 不放开 unsafe-inline 的前提下接受 Product Schema 引导脚本。
	scriptSource, hasScript := inlineProductSchemaScriptSource(document.Bytes())
	for name, value := range h.application.Runtime.HTTP.SecurityHeaders {
		if hasScript && strings.EqualFold(name, "Content-Security-Policy") {
			value = applyInlineScriptSource(value, scriptSource)
		}
		writer.Header().Set(name, value)
	}
	if request.Method == http.MethodHead {
		return
	}
	_, _ = writer.Write(document.Bytes())
}

func projectClientPaths(
	application catalog.ApplicationSchema,
) map[string]struct{} {
	paths := make(map[string]struct{})
	paths[application.Runtime.Frontend.ClientRoutes.Home] = struct{}{}
	paths[application.Runtime.Frontend.ClientRoutes.Login] = struct{}{}
	for _, item := range application.Content.Navigation {
		paths[item.Href] = struct{}{}
	}
	for _, item := range application.Administration.Navigation {
		paths[item.Href] = struct{}{}
	}
	return paths
}

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// TestSchemaETagTracksPublicContentInsteadOfRevision 守住缓存失效的正确性。
//
// 线上曾出现：把 content.navigation 从 13 项收敛到 2 项后，浏览器仍长期显示旧
// 导航。根因是 ETag 取自手写的 revision 字符串——改了公开内容却没改版本号，
// 服务端对旧 If-None-Match 回 304，客户端就一直用部署前的 Schema。
//
// 本用例让 ETag 由公开投影内容派生：内容变则 ETag 必变，与 revision 无关。
func TestSchemaETagTracksPublicContentInsteadOfRevision(t *testing.T) {
	t.Parallel()
	// 改一个公开字段，但**不动 revision**——这正是线上漏掉的那一步。
	mutate := func(schema *catalog.ApplicationSchema) {
		schema.Content.Navigation = append(
			schema.Content.Navigation,
			catalog.NavigationPresentation{
				Key:       "probe",
				Href:      "/probe",
				Label:     "Probe",
				Icon:      "sparkles",
				Placement: "main",
				Order:     999,
			},
		)
	}

	base := schemaETagFor(t, nil)
	after := schemaETagFor(t, mutate)

	if base == after {
		t.Fatalf(
			"改了 content.navigation 却得到相同 ETag %s；"+
				"客户端会收到 304 并继续使用过期 Schema",
			base,
		)
	}
	if !strings.HasPrefix(base, `"sha256-`) {
		t.Errorf("ETag 未由内容派生，实际为 %s", base)
	}
	if !strings.Contains(base, httpapiETagQuoted(base)) {
		t.Errorf("ETag 缺少外层引号，实际为 %s", base)
	}

	// 旧 ETag 必须真的被打掉，否则"ETag 变了"本身没有实际意义。
	if code := schemaRequestCode(t, mutate, base); code != http.StatusOK {
		t.Errorf(
			"内容已变却对旧 ETag %s 返回 %d，期望 200"+
				"（304 会让客户端保留过期 Schema）",
			base, code,
		)
	}
	// 内容未变时仍须正常协商缓存，否则等于把 Schema 端点变成不可缓存。
	if code := schemaRequestCode(t, nil, base); code != http.StatusNotModified {
		t.Errorf("内容未变时对同 ETag 返回 %d，期望 304", code)
	}
}

// schemaETagFor 起一个夹具并取回 /api/v1/schema 的 ETag。
// mutate 为 nil 时使用默认目录。
func schemaETagFor(t *testing.T, mutate func(*catalog.ApplicationSchema)) string {
	t.Helper()
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t, []string{"admin"}, mutate, nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/schema", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("schema 状态码 = %d", response.Code)
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("schema 响应缺少 ETag")
	}
	return etag
}

// schemaRequestCode 带指定 If-None-Match 请求 schema，返回状态码。
func schemaRequestCode(
	t *testing.T,
	mutate func(*catalog.ApplicationSchema),
	ifNoneMatch string,
) int {
	t.Helper()
	_, handler, _ := newHTTPHarnessWithAuthentication(
		t, []string{"admin"}, mutate, nil,
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/schema", nil)
	request.Header.Set("If-None-Match", ifNoneMatch)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

// httpapiETagQuoted 校验 ETag 首尾是引号，ETag 语法要求带引号。
func httpapiETagQuoted(etag string) string {
	if strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`) {
		return ""
	}
	return etag
}

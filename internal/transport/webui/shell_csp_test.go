package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// shellCSPDirective 返回响应头里某个 CSP 指令的来源列表。
func shellCSPDirective(t *testing.T, policy string, name string) string {
	t.Helper()
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) > 0 && strings.EqualFold(fields[0], name) {
			return strings.Join(fields[1:], " ")
		}
	}
	return ""
}

// browserExecutedScriptText 独立复刻浏览器口径：取 <script> 与 </script> 之间
// 的原文。故意不复用生产实现，使本断言能真正校验哈希算对了。
func browserExecutedScriptText(t *testing.T, document string) string {
	t.Helper()
	marker := strings.Index(document, productSchemaScriptMarker)
	if marker < 0 {
		t.Fatalf("shell 未注入 %s", productSchemaScriptMarker)
	}
	open := strings.LastIndex(document[:marker], "<script")
	if open < 0 {
		t.Fatal("注入脚本缺少 <script> 开始标签")
	}
	begin := strings.Index(document[open:], ">")
	if begin < 0 {
		t.Fatal("注入脚本的开始标签未闭合")
	}
	begin += open + 1
	end := strings.Index(document[begin:], "</script")
	if end < 0 {
		t.Fatal("注入脚本缺少 </script> 结束标签")
	}
	return document[begin : begin+end]
}

// TestShellCSPAllowsOnlyTheInjectedProductSchemaScript 是浏览器可启动门禁。
// 站点在真实浏览器里全空白过一次：Schema 注入脚本是内联的，而 Schema 声明的
// CSP 没有 script-src，浏览器按 default-src 拦掉它，main.tsx 随即抛
// "Go Web Host did not inject Product Schema"。本断言同时守住两件事：
// 哈希必须与实际渲染出的脚本文本匹配，且不得靠放开 unsafe-inline 通过。
func TestShellCSPAllowsOnlyTheInjectedProductSchemaScript(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()

	// 门禁必须覆盖生产真实模板，测试内联副本会让模板改动绕过断言。
	handler, err := New(Config{
		Assets:      os.DirFS("../../../web"),
		Application: application,
		API:         http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("shell 状态码 = %d", page.Code)
	}

	body := page.Body.String()
	policy := page.Header().Get("Content-Security-Policy")
	scriptSrc := shellCSPDirective(t, policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("CSP 未声明 script-src，内联注入脚本会被 default-src 拦死：%q", policy)
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Fatalf("script-src 不得放开 unsafe-inline：%q", scriptSrc)
	}
	// model-viewer 的 Meshopt 解码器需要 WebAssembly.instantiate。
	// 只放行 Wasm 编译，不放开 JavaScript eval 或任意内联脚本。
	for _, source := range []struct {
		name   string
		policy string
	}{
		{name: "shell 响应", policy: policy},
		{name: "Schema 声明", policy: application.Runtime.HTTP.SecurityHeaders["Content-Security-Policy"]},
	} {
		fields := strings.Fields(shellCSPDirective(t, source.policy, "script-src"))
		if !slices.Contains(fields, "'self'") || !slices.Contains(fields, "'wasm-unsafe-eval'") {
			t.Fatalf("%s 的 script-src 缺少同源脚本或 WebAssembly 许可：%q", source.name, fields)
		}
		for _, forbidden := range []string{"'unsafe-eval'", "'unsafe-inline'", "*", "https:"} {
			if slices.Contains(fields, forbidden) {
				t.Fatalf("%s 的 script-src 意外放开 %s：%q", source.name, forbidden, fields)
			}
		}
	}

	digest := sha256.Sum256([]byte(browserExecutedScriptText(t, body)))
	// hash-source 语法要求整体带单引号；写成裸值浏览器会整条忽略。
	want := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	if !strings.Contains(scriptSrc, want) {
		t.Fatalf("script-src 缺少与实际注入脚本匹配的哈希来源 %s：%q", want, scriptSrc)
	}
	for _, field := range strings.Fields(scriptSrc) {
		if strings.HasPrefix(field, "sha256-") {
			t.Fatalf("哈希来源必须带单引号：%q", scriptSrc)
		}
	}

	// 其余指令必须原样保留，代码只补哈希，不得吞掉 Schema 声明的其它策略。
	for _, directive := range []string{
		"default-src 'self'",
		"img-src 'self' data: blob:",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"base-uri 'self'",
	} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("CSP 丢失声明指令 %q：%q", directive, policy)
		}
	}
	// 连接来源必须精确匹配；前缀包含检查会漏掉追加的 *、https: 或 data:。
	for _, source := range []struct {
		name   string
		policy string
	}{
		{name: "shell 响应", policy: policy},
		{name: "Schema 声明", policy: application.Runtime.HTTP.SecurityHeaders["Content-Security-Policy"]},
	} {
		if got, want := shellCSPDirective(t, source.policy, "connect-src"), "'self' blob:"; got != want {
			t.Fatalf("%s 的 connect-src = %q，期望 %q", source.name, got, want)
		}
	}
	// 前端 bundle 运行时会注入 <style>，style-src 必须放行，否则界面无样式。
	if styleSrc := shellCSPDirective(t, policy, "style-src"); !strings.Contains(styleSrc, "'unsafe-inline'") {
		t.Fatalf("style-src 需放行运行时注入的样式：%q", styleSrc)
	}
}

// TestShellCSPStaysClosedWithoutInjectedScript 保证无注入脚本时 fail-closed：
// 不新建策略指令，也不凭空放行内联脚本。
func TestShellCSPStaysClosedWithoutInjectedScript(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	handler, err := New(Config{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte(`<!doctype html><html><body><div id="root"></div></body></html>`),
			},
		},
		Application: application,
		API:         http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))

	policy := page.Header().Get("Content-Security-Policy")
	if scriptSrc := shellCSPDirective(t, policy, "script-src"); strings.Contains(scriptSrc, "sha256-") {
		t.Fatalf("无注入脚本时不应产生脚本哈希：%q", scriptSrc)
	}
	if got, want := policy, application.Runtime.HTTP.SecurityHeaders["Content-Security-Policy"]; got != want {
		t.Fatalf("无注入脚本时 CSP 应与 Schema 声明一致：\n实际 %q\n声明 %q", got, want)
	}
}

// TestRealWebShellScriptIsCoveredByCSP 用生产真实 web/index.html 收口 CSP
// 门禁。同文件里的另一条用例走内存夹具，只能证明"哈希算法与注入机制正确"；
// 若真实 shell 的注入脚本结构变化（例如换了标记、移出 <script>、变成外链），
// 夹具仍会绿，而线上就是白屏。本用例直接对真实 shell 断言：注入脚本能定位、
// 哈希与浏览器口径一致、且真实 shell 未引入需要放开策略的第二个内联脚本。
func TestRealWebShellScriptIsCoveredByCSP(t *testing.T) {
	shell, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "index.html"))
	if err != nil {
		t.Fatalf("读取生产真实 shell 失败，CSP 门禁变成空转: %v", err)
	}
	rendered := strings.ReplaceAll(string(shell), "{{.Schema}}", `{"application":{}}`)
	source, ok := inlineProductSchemaScriptSource([]byte(rendered))
	if !ok {
		t.Fatalf(
			"生产真实 web/index.html 已无法定位注入脚本（标记 %q），"+
				"CSP 哈希将缺失、线上会白屏",
			productSchemaScriptMarker,
		)
	}

	// 独立复刻浏览器口径算一遍，确认生产实现算出的哈希与真实 shell 一致。
	executed := browserExecutedScriptText(t, rendered)
	digest := sha256.Sum256([]byte(executed))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	if source != want {
		t.Errorf("真实 shell 的脚本哈希不匹配：\n得到 %s\n期望 %s", source, want)
	}

	// 真实 shell 只能有一个内联脚本：多一个就意味着还有一段代码不在声明范围内，
	// 再放开一次策略就等于把 CSP 逐步退化成 unsafe-inline。外链脚本不计入——
	// 它的来源由 script-src 的 'self' 覆盖，本就不需要放开任何东西。
	inline, external := classifyShellScripts(rendered)
	if len(inline) != 1 {
		t.Errorf(
			"生产真实 shell 有 %d 个内联 <script>，应恰好 1 个；"+
				"新增内联脚本需要重新评估 CSP 策略",
			len(inline),
		)
	}

	// 外链脚本必须是同源相对路径。若将来引入 CDN 脚本，必须在 Schema 的
	// script-src 里显式声明该来源，而不是让浏览器默默放行。
	policy := shellCSPDirective(t, declaredScriptSrc(t), "script-src")
	for _, src := range external {
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
			t.Errorf(
				"真实 shell 引入了跨源脚本 %q；"+
					"须在 Schema 的 script-src 显式声明该来源（当前 %q）",
				src, policy,
			)
		}
	}
}

// declaredScriptSrc 返回 Schema 声明的 CSP 策略原文（不含按响应补的哈希）。
func declaredScriptSrc(t *testing.T) string {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	headers := product.Application().Runtime.HTTP.SecurityHeaders
	policy, ok := headers["Content-Security-Policy"]
	if !ok {
		t.Fatal("Schema 未声明 Content-Security-Policy")
	}
	return policy
}

// classifyShellScripts 按有无 src 属性区分内联与外链脚本。取开始标签内的
// src 值；属性顺序不固定，故在标签内检索而非假定 src 是第一个属性。
func classifyShellScripts(document string) (inline []string, external []string) {
	for index := 0; ; {
		open := strings.Index(document[index:], "<script")
		if open < 0 {
			return
		}
		open += index
		end := strings.Index(document[open:], ">")
		if end < 0 {
			return
		}
		tag := document[open : open+end]
		index = open + end + 1
		if src, found := scriptTagSrc(tag); found {
			external = append(external, src)
			continue
		}
		close := strings.Index(document[index:], "</script")
		if close < 0 {
			return
		}
		inline = append(inline, document[index:index+close])
		index += close + len("</script")
	}
}

// scriptTagSrc 从 <script ...> 开始标签里取 src 属性值。属性名边界必须校验：
// data-src、x-src 这类名字里也含 "src"，直接子串匹配会把它们误认成外链脚本。
func scriptTagSrc(tag string) (string, bool) {
	remainder := tag
	for {
		position := strings.Index(remainder, "src")
		if position < 0 {
			return "", false
		}
		remainder = remainder[position+len("src"):]
		if position > 0 && isAttributeNameByte(tag[position-1]) {
			continue
		}
		trimmed := strings.TrimLeft(remainder, " \t\r\n")
		if !strings.HasPrefix(trimmed, "=") {
			continue
		}
		trimmed = strings.TrimLeft(trimmed[1:], " \t\r\n")
		quote := trimmed[:1]
		if quote != "\"" && quote != "'" {
			return "", false
		}
		close := strings.Index(trimmed[1:], quote)
		if close < 0 {
			return "", false
		}
		return trimmed[1 : 1+close], true
	}
}

// isAttributeNameByte 判断字节是否可能构成属性名的一部分。HTML 属性名可含
// 字母、数字、连字符与下划线；命中即说明当前 "src" 只是更长的属性名片段。
func isAttributeNameByte(char byte) bool {
	switch {
	case char >= 'a' && char <= 'z',
		char >= 'A' && char <= 'Z',
		char >= '0' && char <= '9',
		char == '-', char == '_':
		return true
	}
	return false
}

// TestClassifyShellScriptsDistinguishesInlineFromExternal 证明内联/外链
// 区分规则本身判得对。真实 shell 恰好是"1 内联 + 1 外链"，若计数规则把外链
// 也算成内联，或反过来，两种错法都会让上面的守门断言失去意义。
func TestClassifyShellScriptsDistinguishesInlineFromExternal(t *testing.T) {
	cases := []struct {
		name         string
		document     string
		wantInline   int
		wantExternal []string
	}{
		{
			name:         "生产形态：1 内联 + 1 外链",
			document:     "<script>window.a=1</script><script type=\"module\" src=\"/src/main.tsx\"></script>",
			wantInline:   1,
			wantExternal: []string{"/src/main.tsx"},
		},
		{
			name:         "外链在前内联在后",
			document:     "<script src=\"/a.js\"></script><script>window.a=1</script>",
			wantInline:   1,
			wantExternal: []string{"/a.js"},
		},
		{
			name:         "src 不是首属性仍须识别为外链",
			document:     "<script type=\"module\" crossorigin src='/a.js'></script>",
			wantInline:   0,
			wantExternal: []string{"/a.js"},
		},
		{
			name:         "含 src 字样的属性不得误判为外链",
			document:     "<script data-src='not-a-src'>window.a=1</script>",
			wantInline:   1,
			wantExternal: nil,
		},
		{
			name:         "两个内联脚本必须都被数出来",
			document:     "<script>window.a=1</script><script>window.b=2</script>",
			wantInline:   2,
			wantExternal: nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			inline, external := classifyShellScripts(testCase.document)
			if len(inline) != testCase.wantInline {
				t.Errorf("内联脚本数 = %d，期望 %d", len(inline), testCase.wantInline)
			}
			if len(external) != len(testCase.wantExternal) {
				t.Fatalf("外链脚本 = %v，期望 %v", external, testCase.wantExternal)
			}
			for index, want := range testCase.wantExternal {
				if external[index] != want {
					t.Errorf("外链脚本[%d] = %q，期望 %q", index, external[index], want)
				}
			}
		})
	}
}

package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// productSchemaScriptMarker 是 Go Web Host 注入 Product Schema 的内联脚本标记。
// 浏览器端 web/src/main.tsx 以同一全局名读取，两端是同一份契约。
const productSchemaScriptMarker = "window.__ART_AI_PRODUCT_SCHEMA__="

// inlineProductSchemaScriptSource 返回浏览器实际执行的那段内联脚本的 CSP
// 哈希来源表达式。哈希对象是 <script> 与 </script> 之间的原文（含缩进换行），
// 与浏览器口径一致；找不到标记时返回 false，调用方保持 fail-closed（不放宽策略）。
//
// 注意 CSP 语法：hash-source 必须整体带单引号（'sha256-…'），写成裸值会被浏览器
// 当作非法来源整条忽略，表现为"头里有哈希但内联脚本仍被拦"。
func inlineProductSchemaScriptSource(document []byte) (string, bool) {
	html := string(document)
	marker := strings.Index(html, productSchemaScriptMarker)
	if marker < 0 {
		return "", false
	}
	open := strings.LastIndex(html[:marker], "<script")
	if open < 0 {
		return "", false
	}
	begin := strings.Index(html[open:], ">")
	if begin < 0 {
		return "", false
	}
	begin += open + 1
	end := strings.Index(html[begin:], "</script")
	if end < 0 {
		return "", false
	}
	digest := sha256.Sum256([]byte(html[begin : begin+end]))
	return "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'", true
}

// applyInlineScriptSource 把按响应计算出的脚本哈希来源并入 Schema 声明的
// script-src。声明的来源列表原样保留（策略真源仍在 Product Schema），此处只补
// 当次响应必需的哈希；Schema 未声明 script-src 时不新建指令，避免代码里藏策略。
func applyInlineScriptSource(policy string, source string) string {
	directives := strings.Split(policy, ";")
	declared := false
	for index, directive := range directives {
		fields := strings.Fields(directive)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "script-src") {
			continue
		}
		directives[index] = "script-src " + strings.Join(fields[1:], " ") + " " + source
		declared = true
	}
	if !declared {
		return policy
	}
	return strings.Join(directives, ";")
}

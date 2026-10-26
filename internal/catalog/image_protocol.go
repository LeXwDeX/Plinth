package catalog

import (
	"encoding/json"
	"fmt"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var wirePathPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*(\.(?:[a-zA-Z_][a-zA-Z_0-9]*|[0-9]+))*(\.\[\])?$`)

// multipartFieldPattern 限制 multipart 表单字段名：扁平字段或单层数组，
// 不允许 JSON 路径，避免同一 wire 契约出现两种解释。
var multipartFieldPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*(\[\])?$`)

func validateImageProtocol(offering schemacontract.Offering) error {
	if len(offering.ProviderOptions) == 0 {
		return nil
	}
	var protocol schemacontract.ImageProtocol
	if err := json.Unmarshal(offering.ProviderOptions, &protocol); err != nil {
		return err
	}
	// Other protocols retain their own schema validation.
	if len(protocol.RequestTemplate) == 0 {
		return nil
	}
	switch protocol.RequestEncoding {
	case "", "json", "multipart":
	default:
		return fmt.Errorf("图片请求编码 %q 无效", protocol.RequestEncoding)
	}
	endpoint, err := url.Parse(protocol.Endpoint)
	if err != nil || endpoint.IsAbs() || endpoint.Host != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.HasPrefix(protocol.Endpoint, "/") || strings.HasPrefix(protocol.Endpoint, "../") || path.Clean(protocol.Endpoint) != protocol.Endpoint || protocol.Endpoint == "." || protocol.Endpoint == "" {
		return fmt.Errorf("图片接口必须是安全的相对路径")
	}
	var object map[string]any
	if err := json.Unmarshal(protocol.RequestTemplate, &object); err != nil || object == nil {
		return fmt.Errorf("图片请求模板必须是对象")
	}
	if protocol.AuthHeader != "" && protocol.AuthHeader != "Authorization" && protocol.AuthHeader != "x-goog-api-key" {
		return fmt.Errorf("图片认证 Header 无效")
	}
	if protocol.Response.ImagesPath != "" && (protocol.MaxResponseBytes <= 0 || protocol.Response.Role == "" || !strings.HasPrefix(protocol.Response.DefaultMediaType, "image/") || (protocol.Response.Base64Field == "" && protocol.Response.URLField == "")) {
		return fmt.Errorf("图片响应声明不完整")
	}
	for _, parameter := range offering.Parameters {
		name := parameter.WireName
		if name == "" {
			name = parameter.Name
		}
		if protocol.RequestEncoding == "multipart" {
			if !multipartFieldPattern.MatchString(name) {
				return fmt.Errorf("参数 %s 的 multipart 字段名无效", parameter.Name)
			}
			if parameter.Type == "asset" || parameter.Type == "asset-array" {
				if len(parameter.WireTemplate) > 0 {
					return fmt.Errorf("参数 %s 的 multipart 资产参数不支持 wire_template", parameter.Name)
				}
			} else if len(parameter.WireTemplate) > 0 {
				var value any
				if err := json.Unmarshal(parameter.WireTemplate, &value); err != nil {
					return fmt.Errorf("参数 %s 的 wire_template 无效", parameter.Name)
				}
				if _, scalar := value.(map[string]any); scalar {
					return fmt.Errorf("参数 %s 的 multipart wire_template 必须是标量", parameter.Name)
				}
				if _, list := value.([]any); list {
					return fmt.Errorf("参数 %s 的 multipart wire_template 必须是标量", parameter.Name)
				}
			}
		} else if !wirePathPattern.MatchString(name) {
			return fmt.Errorf("参数 %s 的 wire_name 无效", parameter.Name)
		}
		if (parameter.Type == "asset" || parameter.Type == "asset-array") && parameter.MaxBytes <= 0 {
			return fmt.Errorf("参数 %s 缺少资产大小限制", parameter.Name)
		}
		if len(parameter.WireTemplate) > 0 && !json.Valid(parameter.WireTemplate) {
			return fmt.Errorf("参数 %s 的 wire_template 无效", parameter.Name)
		}
	}
	if protocol.RequestEncoding == "multipart" {
		for field, value := range object {
			if !multipartFieldPattern.MatchString(field) {
				return fmt.Errorf("multipart 请求模板字段 %q 无效", field)
			}
			switch value.(type) {
			case map[string]any, []any:
				return fmt.Errorf("multipart 请求模板字段 %q 必须是标量", field)
			}
		}
	}
	return nil
}

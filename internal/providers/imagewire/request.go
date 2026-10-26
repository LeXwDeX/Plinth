// Package imagewire projects validated Offering parameters into JSON wire bodies.
package imagewire

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/uploadname"
)

type Protocol = generation.ImageProtocol

// EncodeMultipart 按 Offering 声明生成 multipart/form-data 请求体。
// 标量参数写为表单字段，资产参数写为文件分片；编码方式只来自 Schema，
// 不按模型或能力分支。返回的 reader 为管道，本地资产错误会中断写入并
// 通过 CloseWithError 传播，使 httpjson 在上游提交前失败。
// 每个文件分片的文件名在返回前按资产已核验的媒体类型确定；无可靠扩展名
// 的资产在此同步拒绝（错误包裹 uploadname.ErrNoReliableExtension），
// 调用方因此在任何派发许可或上游请求之前得知原因。
func EncodeMultipart(
	ctx context.Context,
	request generation.StartRequest,
	resolver generation.InputAssetResolver,
) (io.Reader, string, error) {
	var protocol Protocol
	if err := json.Unmarshal(request.Offering.ProviderOptions, &protocol); err != nil {
		return nil, "", err
	}
	if len(protocol.RequestTemplate) == 0 {
		return nil, "", fmt.Errorf("图片请求缺少当前协议模板")
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return nil, "", err
	}
	if err := checkUploadFilenames(request, input); err != nil {
		return nil, "", err
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	contentType := form.FormDataContentType()
	go func() {
		writeErr := writeMultipartForm(ctx, form, request, input, resolver)
		if closeErr := form.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = writer.CloseWithError(writeErr)
	}()
	return reader, contentType, nil
}

func writeMultipartForm(
	ctx context.Context,
	form *multipart.Writer,
	request generation.StartRequest,
	input map[string]any,
	resolver generation.InputAssetResolver,
) error {
	var template map[string]any
	if err := json.Unmarshal(request.Offering.ProviderOptions, &struct {
		RequestTemplate *map[string]any `json:"request_template"`
	}{RequestTemplate: &template}); err != nil {
		return err
	}
	fields := make([]string, 0, len(template))
	for field := range template {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		value := expand(template[field], map[string]any{"$model": request.Offering.ModelID})
		text, err := formScalar(value)
		if err != nil {
			return fmt.Errorf("请求模板字段 %s: %w", field, err)
		}
		if err := form.WriteField(field, text); err != nil {
			return err
		}
	}
	for _, p := range request.Offering.Parameters {
		value, exists := input[p.Name]
		if !exists || p.Name == request.Offering.ExtraConsentParameter() || generation.ParameterOmitted(p, input) {
			continue
		}
		field := p.WireName
		if field == "" {
			field = p.Name
		}
		if p.Type == "asset" || p.Type == "asset-array" {
			assets := request.InputAssets[p.Name]
			if resolver == nil || len(assets) == 0 {
				return fmt.Errorf("参数 %s 缺少已解析资产", p.Name)
			}
			if p.MaxBytes <= 0 {
				return fmt.Errorf("参数 %s 缺少资产大小约束", p.Name)
			}
			if p.Type == "asset" && len(assets) > 1 {
				assets = assets[:1]
			}
			for _, asset := range assets {
				if p.AlphaFlatten != nil {
					data, err := readAsset(ctx, resolver, asset, p)
					if err != nil {
						return err
					}
					data, flat, err := flattenAlpha(data, asset, p, input)
					if err != nil {
						return err
					}
					if err := writeAssetPart(form, field, p, flat, bytes.NewReader(data)); err != nil {
						return err
					}
					continue
				}
				reader, err := resolver.Open(ctx, asset)
				if err != nil {
					return err
				}
				err = writeAssetPart(form, field, p, asset, reader)
				closeErr := reader.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
			continue
		}
		if mapped, ok := p.WireValues[fmt.Sprint(value)]; ok {
			if err := json.Unmarshal(mapped, &value); err != nil {
				return err
			}
		} else if len(p.WireTemplate) > 0 {
			var template any
			if err := json.Unmarshal(p.WireTemplate, &template); err != nil {
				return err
			}
			value = expand(template, map[string]any{"$value": value})
		}
		text, err := formScalar(value)
		if err != nil {
			return fmt.Errorf("参数 %s: %w", p.Name, err)
		}
		if err := form.WriteField(field, text); err != nil {
			return err
		}
	}
	return nil
}

// checkUploadFilenames names every file part the form will carry before any
// byte is written. Alpha flattening may turn a stored picture into PNG, so the
// written part is named again from the asset it actually carries.
func checkUploadFilenames(request generation.StartRequest, input map[string]any) error {
	for _, p := range request.Offering.Parameters {
		if p.Type != "asset" && p.Type != "asset-array" {
			continue
		}
		if _, exists := input[p.Name]; !exists || p.Name == request.Offering.ExtraConsentParameter() || generation.ParameterOmitted(p, input) {
			continue
		}
		assets := request.InputAssets[p.Name]
		if p.Type == "asset" && len(assets) > 1 {
			assets = assets[:1]
		}
		for _, asset := range assets {
			if _, err := uploadFilename(p, asset); err != nil {
				return err
			}
		}
	}
	return nil
}

func uploadFilename(p generation.Parameter, asset generation.InputAsset) (string, error) {
	name, err := uploadname.Filename(asset, p.Extensions)
	if err != nil {
		return "", fmt.Errorf("参数 %s: %w", p.Name, err)
	}
	return name, nil
}

func writeAssetPart(
	form *multipart.Writer,
	field string,
	p generation.Parameter,
	asset generation.InputAsset,
	body io.Reader,
) error {
	filename, err := uploadFilename(p, asset)
	if err != nil {
		return err
	}
	maxBytes := p.MaxBytes
	header := make(textproto.MIMEHeader)
	header.Set(
		"Content-Disposition",
		fmt.Sprintf(
			`form-data; name="%s"; filename="%s"`,
			escapeQuotes(field),
			escapeQuotes(filename),
		),
	)
	if asset.MediaType != "" {
		header.Set("Content-Type", asset.MediaType)
	}
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	written, err := io.Copy(part, io.LimitReader(body, maxBytes+1))
	if err != nil {
		return err
	}
	if written > maxBytes {
		return fmt.Errorf("参数 %s 资产超过大小限制", p.Name)
	}
	return nil
}

// readAsset reads one resolved source whole, bounded by the parameter's declared size.
func readAsset(ctx context.Context, resolver generation.InputAssetResolver, asset generation.InputAsset, p generation.Parameter) ([]byte, error) {
	reader, err := resolver.Open(ctx, asset)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, p.MaxBytes+1))
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > p.MaxBytes {
		return nil, fmt.Errorf("参数 %s 资产超过大小限制", p.Name)
	}
	return data, nil
}

func formScalar(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(typed), nil
	default:
		return "", fmt.Errorf("multipart 字段必须是标量")
	}
}

func escapeQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`).Replace(value)
}

func Build(ctx context.Context, request generation.StartRequest, resolver generation.InputAssetResolver) (map[string]any, error) {
	var protocol Protocol
	if err := json.Unmarshal(request.Offering.ProviderOptions, &protocol); err != nil {
		return nil, err
	}
	if len(protocol.RequestTemplate) == 0 {
		return nil, fmt.Errorf("图片请求缺少当前协议模板")
	}
	var root any = map[string]any{}
	if len(protocol.RequestTemplate) > 0 {
		if err := json.Unmarshal(protocol.RequestTemplate, &root); err != nil {
			return nil, err
		}
		root = expand(root, map[string]any{"$model": request.Offering.ModelID})
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return nil, err
	}
	for _, p := range request.Offering.Parameters {
		value, exists := input[p.Name]
		if !exists || p.Name == request.Offering.ExtraConsentParameter() || generation.ParameterOmitted(p, input) {
			continue
		}
		path := p.WireName
		if path == "" {
			path = p.Name
		}
		if p.Type == "asset" || p.Type == "asset-array" {
			assets := request.InputAssets[p.Name]
			if resolver == nil || len(assets) == 0 {
				return nil, fmt.Errorf("参数 %s 缺少已解析资产", p.Name)
			}
			values := make([]any, 0, len(assets))
			for _, asset := range assets {
				if p.MaxBytes <= 0 {
					return nil, fmt.Errorf("参数 %s 缺少资产大小约束", p.Name)
				}
				data, err := readAsset(ctx, resolver, asset, p)
				if err != nil {
					return nil, err
				}
				data, asset, err := flattenAlpha(data, asset, p, input)
				if err != nil {
					return nil, err
				}
				encoded := base64.StdEncoding.EncodeToString(data)
				vars := map[string]any{"$base64": encoded, "$media_type": asset.MediaType, "$data_uri": "data:" + asset.MediaType + ";base64," + encoded}
				var item any = vars["$data_uri"]
				if len(p.WireTemplate) > 0 {
					if err := json.Unmarshal(p.WireTemplate, &item); err != nil {
						return nil, err
					}
					item = expand(item, vars)
				}
				values = append(values, item)
			}
			value = values
			if p.Type == "asset" {
				value = values[0]
			}
		} else if mapped, ok := p.WireValues[fmt.Sprint(value)]; ok {
			if err := json.Unmarshal(mapped, &value); err != nil {
				return nil, err
			}
		} else if len(p.WireTemplate) > 0 {
			var template any
			if err := json.Unmarshal(p.WireTemplate, &template); err != nil {
				return nil, err
			}
			value = expand(template, map[string]any{"$value": value})
		}
		var err error
		root, err = set(root, strings.Split(path, "."), value)
		if err != nil {
			return nil, fmt.Errorf("参数 %s: %w", p.Name, err)
		}
	}
	body, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("请求模板必须为对象")
	}
	return body, nil
}

func expand(value any, vars map[string]any) any {
	switch x := value.(type) {
	case string:
		if replacement, ok := vars[x]; ok {
			return replacement
		}
	case map[string]any:
		for k, v := range x {
			x[k] = expand(v, vars)
		}
	case []any:
		for i, v := range x {
			x[i] = expand(v, vars)
		}
	}
	return value
}

func set(node any, path []string, value any) (any, error) {
	if len(path) == 0 {
		return value, nil
	}
	key := path[0]
	if key == "[]" {
		if len(path) != 1 {
			return nil, fmt.Errorf("追加路径必须位于末尾")
		}
		list, _ := node.([]any)
		if values, ok := value.([]any); ok {
			return append(list, values...), nil
		}
		return append(list, value), nil
	}
	if index, err := strconv.Atoi(key); err == nil {
		if index < 0 || index > 64 {
			return nil, fmt.Errorf("数组索引无效")
		}
		list, ok := node.([]any)
		if node != nil && !ok {
			return nil, fmt.Errorf("路径要求数组")
		}
		for len(list) <= index {
			list = append(list, nil)
		}
		child, err := set(list[index], path[1:], value)
		if err != nil {
			return nil, err
		}
		list[index] = child
		return list, nil
	}
	object, ok := node.(map[string]any)
	if node != nil && !ok {
		return nil, fmt.Errorf("路径要求对象")
	}
	if object == nil {
		object = map[string]any{}
	}
	child, err := set(object[key], path[1:], value)
	if err != nil {
		return nil, err
	}
	object[key] = child
	return object, nil
}

// Select traverses explicit object paths; * visits every array element.
func Select(node any, path string) []any {
	if path == "" {
		return []any{node}
	}
	parts := strings.SplitN(path, ".", 2)
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	if parts[0] == "*" {
		var result []any
		if list, ok := node.([]any); ok {
			for _, item := range list {
				result = append(result, Select(item, rest)...)
			}
		}
		return result
	}
	if object, ok := node.(map[string]any); ok {
		if child, exists := object[parts[0]]; exists {
			return Select(child, rest)
		}
	}
	return nil
}

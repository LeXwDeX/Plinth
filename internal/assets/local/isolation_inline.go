package local

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/url"
	"strings"
)

const (
	maxInlineResourceBytes = 64 << 20
	maxInlineResourceDepth = 4
	maxInlineHeaderBytes   = 4 << 10
)

type inlineInspectionBudget struct {
	remaining         int64
	depth             int
	document          any
	buffer            int64
	work              *inspectionWorkBudget
	onArchiveDocument func(any, string) error
}

func newInlineInspectionBudget() *inlineInspectionBudget {
	return &inlineInspectionBudget{remaining: maxInlineResourceBytes, work: newInspectionWorkBudget()}
}

// newInlineInspectionBudgetForSize gives a top-level model a work budget that
// scales with its size; the inline data URI limit stays fixed.
func newInlineInspectionBudgetForSize(size int64) *inlineInspectionBudget {
	return &inlineInspectionBudget{remaining: maxInlineResourceBytes, work: newInspectionWorkBudgetForSize(size)}
}

func (budget *inlineInspectionBudget) inspectionWork() *inspectionWorkBudget {
	if budget.work == nil {
		budget.work = newInspectionWorkBudget()
	}
	return budget.work
}

func isInlineDataURI(value string) bool {
	return len(value) >= 5 && strings.EqualFold(value[:5], "data:")
}

func decodeInlineDataURI(raw string, budget *inlineInspectionBudget) ([]byte, string, error) {
	if !isInlineDataURI(raw) {
		return nil, "", errors.New("内嵌资源 URI 无效")
	}
	separator := strings.IndexByte(raw, ',')
	if separator < 5 || separator-5 > maxInlineHeaderBytes {
		return nil, "", errors.New("内嵌资源头部无效")
	}
	header, payload := raw[5:separator], raw[separator+1:]
	if containsExternalAddress([]byte(header)) {
		return nil, "", errors.New("内嵌资源媒体头部含外部地址")
	}
	base64Encoded := false
	if strings.HasSuffix(strings.ToLower(header), ";base64") {
		base64Encoded = true
		header = header[:len(header)-7]
	}
	if header == "" {
		header = "text/plain"
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return nil, "", errors.New("内嵌资源媒体类型无效")
	}
	var size int64
	if base64Encoded {
		if len(payload)%4 != 0 || strings.ContainsAny(payload, " \t\r\n") {
			return nil, "", errors.New("内嵌资源 base64 编码无效")
		}
		size = int64(len(payload)/4) * 3
		if strings.HasSuffix(payload, "=") {
			size--
		}
		if strings.HasSuffix(payload, "==") {
			size--
		}
	} else {
		for index := 0; index < len(payload); index++ {
			if payload[index] == '%' {
				if index+2 >= len(payload) || !isURIHex(payload[index+1]) || !isURIHex(payload[index+2]) {
					return nil, "", errors.New("内嵌资源 percent 编码无效")
				}
				index += 2
			}
			size++
			if size > maxInlineResourceBytes || size > budget.remaining {
				return nil, "", errors.New("内嵌资源展开大小超出限制")
			}
		}
	}
	if size <= 0 || size > maxInlineResourceBytes || size > budget.remaining {
		return nil, "", errors.New("内嵌资源展开大小超出限制")
	}
	// Reserve the exact decoded size before DecodeString/PathUnescape allocate.
	budget.remaining -= size
	var data []byte
	if base64Encoded {
		data, err = base64.StdEncoding.Strict().DecodeString(payload)
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil || int64(len(data)) != size {
		return nil, "", errors.New("内嵌资源编码无效")
	}
	return data, strings.ToLower(mediaType), nil
}

func isURIHex(value byte) bool {
	return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') || (value >= 'A' && value <= 'F')
}

func inspectInlineResource(raw, kind, declaredMIME string, declaredBytes int64, budget *inlineInspectionBudget) error {
	if budget.depth >= maxInlineResourceDepth {
		return errors.New("内嵌资源递归超出限制")
	}
	budget.depth++
	defer func() { budget.depth-- }()
	data, mediaType, err := decodeInlineDataURI(raw, budget)
	if err != nil {
		return err
	}
	if declaredMIME != "" && strings.ToLower(declaredMIME) != mediaType {
		return errors.New("内嵌资源媒体类型冲突")
	}
	if kind == "image" && !strings.HasPrefix(mediaType, "image/") {
		return errors.New("内嵌图片媒体类型无效")
	}
	if kind == "buffer" && mediaType != "application/octet-stream" && mediaType != "application/gltf-buffer" {
		return errors.New("内嵌缓冲区媒体类型无效")
	}
	if declaredBytes >= 0 && declaredBytes != int64(len(data)) {
		return errors.New("内嵌缓冲区长度与声明不一致")
	}
	var fields []inspectionField
	if kind == "buffer" {
		fields, err = gltfNumericFields(budget.document, budget.buffer, int64(len(data)))
		if err != nil {
			return err
		}
		images, err := gltfBufferImages(budget.document, budget.buffer, int64(len(data)))
		if err != nil {
			return err
		}
		if err := validateImageNumericRanges(images, fields); err != nil {
			return err
		}
		if len(images) != 0 {
			// The decoded buffer already consumed the shared inline budget.
			// Its image views are checked inside that same buffer, including
			// compressed metadata, gaps and remaining unclassified bytes.
			return inspectGLBBIN(bytes.NewReader(data), int64(len(data)), images, fields, newPlannedInspectorWithWork(budget.inspectionWork()))
		}
	}
	return inspectInlineBytes(data, mediaType, budget, fields)
}

func inspectInlineBytes(data []byte, mediaType string, budget *inlineInspectionBudget, fields []inspectionField) error {
	magic := data[:min(8, len(data))]
	switch mediaType {
	case "image/png":
		if !bytes.Equal(magic, []byte("\x89PNG\r\n\x1a\n")) {
			return errors.New("内嵌 PNG 媒体声明与字节不一致")
		}
	case "image/jpeg":
		if !bytes.HasPrefix(magic, []byte{0xff, 0xd8}) {
			return errors.New("内嵌 JPEG 媒体声明与字节不一致")
		}
	case "model/gltf-binary":
		if !bytes.HasPrefix(magic, []byte("glTF")) {
			return errors.New("内嵌 GLB 媒体声明与字节不一致")
		}
	case "model/gltf+json":
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
			return errors.New("内嵌 glTF 媒体声明与字节不一致")
		}
	}
	if format := rasterFormat(magic, "", mediaType); format != "" {
		if strings.HasPrefix(mediaType, "image/") && mediaType != "image/"+format && !(format == "jpeg" && mediaType == "image/jpg") {
			return errors.New("内嵌图片媒体声明与字节不一致")
		}
		return inspectRasterAddressesWithWork(bytes.NewReader(data), int64(len(data)), format, budget.inspectionWork())
	}
	if bytes.HasPrefix(magic, []byte("glTF")) {
		return inspectGLBStreamBounded(bytes.NewReader(data), int64(len(data)), nil, "", budget)
	}
	if mediaType == "model/gltf+json" {
		return inspectGLTFJSONBounded(bytes.NewReader(data), int64(len(data)), nil, "", budget)
	}
	if bytes.HasPrefix(magic, []byte("PK\x03\x04")) || bytes.HasPrefix(magic, []byte("PK\x05\x06")) {
		return errors.New("内嵌资源不能包含未检查的压缩包")
	}
	return inspectPlannedBytesWithWork(bytes.NewReader(data), inspectionPlan{fields: fields}, budget.inspectionWork())
}

// Only successfully decoded and inspected inline-resource string tokens can
// skip the outer JSON wire scan. Their complete decoded bytes were inspected;
// a second percent pass over pixel data would recreate the binary false match.
func inspectedInlineJSONSpans(data []byte) ([]inspectionSpan, error) {
	var spans []inspectionSpan
	for offset := 0; offset < len(data); offset++ {
		if data[offset] != '"' {
			continue
		}
		start := offset
		for offset++; offset < len(data); offset++ {
			if data[offset] == '\\' {
				offset++
				continue
			}
			if data[offset] == '"' {
				break
			}
		}
		if offset >= len(data) {
			return nil, errors.New("glTF 字符串不完整")
		}
		var value string
		if err := json.Unmarshal(data[start:offset+1], &value); err != nil {
			return nil, errors.New("glTF 字符串无效")
		}
		if isInlineDataURI(value) {
			next := offset + 1
			for next < len(data) && (data[next] == ' ' || data[next] == '\t' || data[next] == '\r' || data[next] == '\n') {
				next++
			}
			if next < len(data) && data[next] == ':' {
				continue // JSON object keys retain the complete wire scan.
			}
			if len(spans) >= maxArchiveFiles {
				return nil, errors.New("glTF 内嵌资源数量超出限制")
			}
			spans = append(spans, inspectionSpan{int64(start + 1), int64(offset)})
		}
	}
	return spans, nil
}

// A map decoder discards earlier duplicate fields. The original asset is
// delivered unchanged, so those discarded values must never escape inspection.
func validateUniqueJSONFields(data []byte) error {
	type frame struct {
		object, key bool
		keys        map[string]struct{}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var frames []frame
	nodes, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if len(frames) != 0 || roots != 1 {
				return errors.New("glTF JSON 结构不完整")
			}
			return nil
		}
		if err != nil {
			return errors.New("glTF JSON 无效")
		}
		nodes++
		if nodes > 1<<20 {
			return errors.New("glTF JSON 检查节点超出限制")
		}
		if delimiter, ok := token.(json.Delim); ok && (delimiter == '}' || delimiter == ']') {
			if len(frames) == 0 {
				return errors.New("glTF JSON 结构无效")
			}
			frames = frames[:len(frames)-1]
			continue
		}
		if len(frames) == 0 {
			roots++
		} else if top := &frames[len(frames)-1]; top.object {
			if top.key {
				key, ok := token.(string)
				if !ok {
					return errors.New("glTF JSON 字段名无效")
				}
				if _, present := top.keys[key]; present {
					return errors.New("glTF JSON 含重复字段")
				}
				if len(top.keys) >= maxArchiveFiles {
					return errors.New("glTF JSON 字段数量超出限制")
				}
				top.keys[key] = struct{}{}
				top.key = false
				continue
			}
			top.key = true
		}
		if delimiter, ok := token.(json.Delim); ok {
			if len(frames) >= 64 || (delimiter != '{' && delimiter != '[') {
				return errors.New("glTF JSON 嵌套超出限制")
			}
			value := frame{}
			if delimiter == '{' {
				value.object, value.key, value.keys = true, true, make(map[string]struct{})
			}
			frames = append(frames, value)
		}
	}
}

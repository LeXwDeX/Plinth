package local

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// maxGLTFMetadataAddressBytes bounds an asset.generator/copyright value that
// may carry a tool or credit address.
const maxGLTFMetadataAddressBytes = 256

// gltfMetadataAddressAllowed reports whether a top-level asset.generator or
// asset.copyright string may name an address (#189). glTF 2.0 defines both as
// descriptive text ("tool that generated this model", "copyright message"),
// never a resource the model loads; only uri fields reference resources. A
// plain address such as "https://github.com/mikedh/trimesh" is therefore not
// an uncached dependency. Query, fragment, credential, percent or escape
// forms, data URIs and long values keep the full address check, so a signed
// or private link is still refused.
func gltfMetadataAddressAllowed(value string) bool {
	return len(value) <= maxGLTFMetadataAddressBytes && !isInlineDataURI(value) &&
		!strings.ContainsAny(value, "?#@%\\&")
}

func gltfMetadataField(parentKind, key string) bool {
	return parentKind == "asset" && (key == "generator" || key == "copyright")
}

// gltfAssetMetadataSpans locates the raw JSON bytes of the top-level
// asset.generator/asset.copyright string values that gltfMetadataAddressAllowed
// accepts, so the wire scan skips exactly those values and nothing else.
// Duplicate fields are refused earlier by validateUniqueJSONFields.
func gltfAssetMetadataSpans(data []byte) ([]inspectionSpan, error) {
	type frame struct {
		object, keyNext bool
		key             string
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var stack []frame
	var spans []inspectionSpan
	valueDone := func() {
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].keyNext = true
		}
	}
	for {
		before := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			return spans, nil
		}
		if err != nil {
			return nil, errors.New("glTF JSON 无效")
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				stack = append(stack, frame{object: true, keyNext: true})
			case '[':
				stack = append(stack, frame{})
			default:
				if len(stack) == 0 {
					return nil, errors.New("glTF JSON 无效")
				}
				stack = stack[:len(stack)-1]
				valueDone()
			}
			continue
		}
		if top := len(stack) - 1; top >= 0 && stack[top].object && stack[top].keyNext {
			key, _ := token.(string)
			stack[top].key, stack[top].keyNext = key, false
			continue
		}
		if value, ok := token.(string); ok && len(stack) == 2 && stack[0].key == "asset" && stack[1].object &&
			gltfMetadataField("asset", stack[1].key) && gltfMetadataAddressAllowed(value) {
			end := decoder.InputOffset()
			start := before + int64(bytes.IndexByte(data[before:end], '"'))
			if start < before || end-1 <= start || data[end-1] != '"' {
				return nil, errors.New("glTF JSON 无效")
			}
			spans = append(spans, inspectionSpan{start + 1, end - 1})
		}
		valueDone()
	}
}

// inspectedGLTFJSONSpans are the JSON chunk bytes exempt from the wire address
// scan: validated inline resources and permitted asset metadata.
func inspectedGLTFJSONSpans(data []byte) ([]inspectionSpan, error) {
	spans, err := inspectedInlineJSONSpans(data)
	if err != nil {
		return nil, err
	}
	metadata, err := gltfAssetMetadataSpans(data)
	if err != nil {
		return nil, err
	}
	return append(spans, metadata...), nil
}

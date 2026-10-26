package modelinput

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type validator struct {
	ctx                       context.Context
	policy                    Policy
	err                       error
	remaining, elements       int64
	bin                       []byte
	views                     []view
	accessors                 []accessor
	materialUV                [][]int64
	materialBaseColorTextures []int
	imageKeys                 map[string]bool
	checkedAttributes         map[string]bool
	texturedMeshes            []bool
	meshTriangles             []int64
	pixels                    int64
	profile                   Profile
}

func (v *validator) fail(kind Kind, reason string) {
	if v.err == nil {
		v.err = failure(kind, reason)
	}
}
func (v *validator) work(n int64) {
	if v.err != nil {
		return
	}
	if v.ctx.Err() != nil {
		v.fail(Canceled, "context_canceled")
		return
	}
	if n < 0 || n > v.remaining {
		v.fail(Limit, "work_limit")
		return
	}
	v.remaining -= n
}
func (v *validator) parseJSON(raw []byte) map[string]any {
	if !utf8.Valid(raw) || !validSurrogates(raw) {
		v.fail(Invalid, "invalid_json_unicode")
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value := v.jsonValue(decoder, 1)
	if v.err != nil {
		return nil
	}
	if _, err := decoder.Token(); err != io.EOF {
		v.fail(Invalid, "json_trailing_content")
	}
	return v.object(value)
}
func (v *validator) jsonValue(d *json.Decoder, depth int64) any {
	if v.err != nil {
		return nil
	}
	v.work(1)
	v.elements++
	if depth > v.policy.Limits.MaxDepth {
		v.fail(Limit, "json_depth_limit")
		return nil
	}
	if v.elements > v.policy.Limits.MaxElements {
		v.fail(Limit, "element_limit")
		return nil
	}
	token, err := d.Token()
	if err != nil {
		v.fail(Invalid, "invalid_json")
		return nil
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := map[string]any{}
			for d.More() && v.err == nil {
				key, err := d.Token()
				if err != nil {
					v.fail(Invalid, "invalid_json")
					return nil
				}
				name, ok := key.(string)
				if !ok {
					v.fail(Invalid, "invalid_json")
					return nil
				}
				if dataURI(name) {
					v.fail(Unsupported, "data_uri")
					return nil
				}
				if _, ok := o[name]; ok {
					v.fail(Invalid, "duplicate_json_field")
					return nil
				}
				v.work(1)
				v.elements++
				if v.elements > v.policy.Limits.MaxElements {
					v.fail(Limit, "element_limit")
					return nil
				}
				o[name] = v.jsonValue(d, depth+1)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				v.fail(Invalid, "invalid_json")
			}
			return o
		case '[':
			a := []any{}
			for d.More() && v.err == nil {
				a = append(a, v.jsonValue(d, depth+1))
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				v.fail(Invalid, "invalid_json")
			}
			return a
		default:
			v.fail(Invalid, "invalid_json")
		}
	case string:
		if dataURI(t) {
			v.fail(Unsupported, "data_uri")
		}
	case json.Number:
		if len(t.String()) > 128 {
			v.fail(Invalid, "invalid_number")
		} else if n, e := t.Float64(); e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			v.fail(Invalid, "nonfinite_number")
		}
	}
	return token
}

// Reject inline resources in all decoded metadata before the shared isolation
// checker can decode them with a separate budget. This profile embeds images
// through BIN buffer views only.
func dataURI(value string) bool {
	return len(value) >= 5 && strings.EqualFold(value[:5], "data:")
}

func (v *validator) object(value any) map[string]any {
	if o, ok := value.(map[string]any); ok {
		return o
	}
	v.fail(Invalid, "expected_object")
	return map[string]any{}
}
func (v *validator) array(o map[string]any, key string, required bool) []any {
	raw, ok := o[key]
	if !ok && !required {
		return nil
	}
	if a, ok := raw.([]any); ok {
		return a
	}
	v.fail(Invalid, "expected_array")
	return nil
}
func (v *validator) fields(o map[string]any, keys ...string) {
	allowed := map[string]bool{"name": true, "extras": true}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range o {
		if key == "extensions" || key == "extensionsUsed" || key == "extensionsRequired" {
			v.fail(Unsupported, "extensions")
		} else if !allowed[key] {
			v.fail(Unsupported, "unsupported_field")
		}
	}
	if name, ok := o["name"]; ok {
		if _, ok := name.(string); !ok {
			v.fail(Invalid, "invalid_name")
		}
	}
}
func (v *validator) integer(o map[string]any, key string, fallback int64, required bool) int64 {
	raw, ok := o[key]
	if !ok && !required {
		return fallback
	}
	n, ok := raw.(json.Number)
	if !ok {
		v.fail(Invalid, "expected_integer")
		return 0
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil || f < 0 || f > math.MaxInt32 || math.Trunc(f) != f {
		v.fail(Invalid, "invalid_integer")
		return 0
	}
	return int64(f)
}
func (v *validator) numeric(raw any) float64 {
	n, ok := raw.(json.Number)
	if !ok {
		v.fail(Invalid, "expected_number")
		return 0
	}
	f, e := n.Float64()
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.IsInf(float64(float32(f)), 0) {
		v.fail(Invalid, "nonfinite_number")
		return 0
	}
	return f
}
func (v *validator) floats(o map[string]any, key string, count int, required bool) []float64 {
	values := v.array(o, key, required)
	if values == nil && !required {
		return nil
	}
	if len(values) != count {
		v.fail(Invalid, "invalid_vector")
		return nil
	}
	result := make([]float64, len(values))
	for i, value := range values {
		result[i] = v.numeric(value)
	}
	return result
}
func (v *validator) boolean(o map[string]any, key string, fallback bool) bool {
	raw, ok := o[key]
	if !ok {
		return fallback
	}
	b, ok := raw.(bool)
	if !ok {
		v.fail(Invalid, "expected_boolean")
	}
	return b
}
func (v *validator) text(o map[string]any, key, fallback string, required bool) string {
	raw, ok := o[key]
	if !ok && !required {
		return fallback
	}
	s, ok := raw.(string)
	if !ok {
		v.fail(Invalid, "expected_string")
	}
	return s
}
func (v *validator) index(o map[string]any, key string, size int, required bool) int {
	if _, ok := o[key]; !ok && !required {
		return -1
	}
	n := v.integer(o, key, 0, true)
	if n >= int64(size) {
		v.fail(Invalid, "invalid_reference")
		return -1
	}
	return int(n)
}

// encoding/json deliberately repairs invalid UTF-8 and lone surrogates. A model
// profile must reject those malformed wire strings instead of changing them.
func validSurrogates(raw []byte) bool {
	quoted := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return true
}

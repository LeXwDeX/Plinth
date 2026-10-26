// Package modelconvert changes the container of a validated static model without
// changing its geometry, materials, metadata, or logical buffer bytes.
package modelconvert

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
)

// Limits are supplied by the caller's frozen product policy. Input and output
// count wire bytes, including a glTF's base64; MaxWork covers conversion and the
// shared model-input validator. In particular, callers must supply their asset
// JSON ceiling for glTF wire bytes. No product defaults live here.
type Limits struct{ MaxInputBytes, MaxOutputBytes, MaxWork int64 }

type operation struct {
	ctx       context.Context
	policy    modelinput.Policy
	remaining int64
}

func fail(kind modelinput.Kind, reason string) error {
	return &modelinput.Error{Kind: kind, Reason: reason}
}
func begin(ctx context.Context, raw []byte, policy modelinput.Policy, limits Limits) (*operation, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	// uint32 is the GLB container's address space, not a product size default.
	if limits.MaxInputBytes <= 0 || limits.MaxOutputBytes <= 0 || limits.MaxWork <= 0 || limits.MaxInputBytes > math.MaxUint32 || limits.MaxOutputBytes > math.MaxUint32 {
		return nil, fail(modelinput.InvalidPolicy, "conversion_limits")
	}
	o := &operation{ctx: ctx, policy: policy, remaining: limits.MaxWork}
	if err := o.work(0); err != nil {
		return nil, err
	}
	if int64(len(raw)) > limits.MaxInputBytes {
		return nil, fail(modelinput.Limit, "conversion_input_bytes")
	}
	return o, nil
}
func (o *operation) work(n int64) error {
	if o.ctx.Err() != nil {
		return fail(modelinput.Canceled, "context_canceled")
	}
	if n < 0 || n > o.remaining {
		return fail(modelinput.Limit, "conversion_work")
	}
	o.remaining -= n
	return nil
}
func (o *operation) validate(raw []byte) error {
	if err := o.work(0); err != nil {
		return err
	}
	if o.remaining <= 0 {
		return fail(modelinput.Limit, "conversion_work")
	}
	p := o.policy
	if p.Limits.MaxWork > o.remaining {
		p.Limits.MaxWork = o.remaining
	}
	_, err := modelinput.ValidateGLBBytes(o.ctx, raw, p)
	return err
}

// GLBToGLTF embeds the one logical BIN in a standard base64 buffer URI. Image
// references remain untouched. Validation completes before any output escapes.
func GLBToGLTF(ctx context.Context, raw []byte, policy modelinput.Policy, limits Limits) ([]byte, error) {
	o, err := begin(ctx, raw, policy, limits)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > policy.Limits.MaxBytes {
		return nil, fail(modelinput.Limit, "byte_limit")
	}
	j, bin, err := glbParts(raw, policy)
	if err != nil {
		return nil, err
	}
	doc, err := o.parse(j, false)
	if err != nil {
		return nil, err
	}
	buffer, err := bufferNode(doc)
	if err != nil {
		return nil, err
	}
	if buffer.field("uri") != nil {
		return nil, fail(modelinput.Unsupported, "uri")
	}
	length, err := bufferLength(j, buffer)
	if err != nil {
		return nil, err
	}
	if length > int64(len(bin)) || int64(len(bin))-length > 3 {
		return nil, fail(modelinput.Invalid, "buffer_length")
	}
	encodedLength := (length + 2) / 3 * 4
	insertLength := int64(len(`,"uri":"`)+len(bufferPrefix)+len(`"`)) + encodedLength
	size := int64(len(j)) + insertLength
	if size > limits.MaxOutputBytes {
		return nil, fail(modelinput.Limit, "conversion_output_bytes")
	}
	if err = o.work(size + length + encodedLength); err != nil {
		return nil, err
	}
	out := make([]byte, 0, int(size))
	at := buffer.end - 1
	out = append(out, j[:at]...)
	out = append(out, `,"uri":"`...)
	out = append(out, bufferPrefix...)
	// Encode in small blocks so cancellation remains observable during large BINs.
	const block = 3 * 8192
	for start := int64(0); start < length; {
		if err = o.work(0); err != nil {
			return nil, err
		}
		end := min(start+block, length)
		out = base64.StdEncoding.AppendEncode(out, bin[start:end])
		start = end
	}
	out = append(out, '"')
	out = append(out, j[at:]...)
	if err = o.validate(raw); err != nil {
		return nil, err
	}
	if err = o.work(0); err != nil {
		return nil, err
	}
	return out, nil
}

const bufferPrefix = "data:application/octet-stream;base64,"

// GLTFToGLB accepts only the closed single-buffer container representation. The
// caller additionally enforces Task provenance/owner; this function always
// revalidates actual bytes and never follows a URI or reads a dependency.
func GLTFToGLB(ctx context.Context, raw []byte, policy modelinput.Policy, limits Limits) ([]byte, error) {
	o, err := begin(ctx, raw, policy, limits)
	if err != nil {
		return nil, err
	}
	doc, err := o.parse(raw, true)
	if err != nil {
		return nil, err
	}
	buffer, err := bufferNode(doc)
	if err != nil {
		return nil, err
	}
	uri := buffer.field("uri")
	if uri == nil || uri.kind != '"' {
		return nil, fail(modelinput.Unsupported, "buffer_uri")
	}
	if !strings.HasPrefix(uri.text, bufferPrefix) {
		return nil, fail(modelinput.Unsupported, "buffer_uri")
	}
	encoded := uri.text[len(bufferPrefix):]
	// Strict canonical base64 disallows whitespace and hidden nonzero pad bits.
	if len(encoded) == 0 || len(encoded)%4 != 0 {
		return nil, fail(modelinput.Invalid, "buffer_base64")
	}
	decodedLength := int64(len(encoded)/4) * 3
	if encoded[len(encoded)-1] == '=' {
		decodedLength--
		if encoded[len(encoded)-2] == '=' {
			decodedLength--
		}
	}
	if decodedLength > policy.Limits.MaxBytes {
		return nil, fail(modelinput.Limit, "decoded_buffer_bytes")
	}
	length, err := bufferLength(raw, buffer)
	if err != nil {
		return nil, err
	}
	if decodedLength != length {
		return nil, fail(modelinput.Invalid, "buffer_length")
	}
	start, end := uriMemberRange(buffer)
	jsonLength := int64(len(raw) - (end - start))
	paddedJSON := (jsonLength + 3) / 4 * 4
	paddedBIN := (decodedLength + 3) / 4 * 4
	size := 28 + paddedJSON + paddedBIN
	if paddedJSON > policy.Limits.MaxJSONBytes || size > policy.Limits.MaxBytes {
		return nil, fail(modelinput.Limit, "byte_limit")
	}
	if size > limits.MaxOutputBytes {
		return nil, fail(modelinput.Limit, "conversion_output_bytes")
	}
	if err = o.work(int64(len(encoded)) + decodedLength + size); err != nil {
		return nil, err
	}
	out := make([]byte, int(size))
	copy(out, "glTF")
	binary.LittleEndian.PutUint32(out[4:], 2)
	binary.LittleEndian.PutUint32(out[8:], uint32(size))
	binary.LittleEndian.PutUint32(out[12:], uint32(paddedJSON))
	copy(out[16:], "JSON")
	at := 20
	at += copy(out[at:], raw[:start])
	at += copy(out[at:], raw[end:])
	for int64(at) < 20+paddedJSON {
		out[at] = ' '
		at++
	}
	binary.LittleEndian.PutUint32(out[at:], uint32(paddedBIN))
	copy(out[at+4:], "BIN\x00")
	bin := out[at+8 : at+8+int(decodedLength)]
	const block = 4 * 8192
	written := 0
	for pos := 0; pos < len(encoded); {
		if err = o.work(0); err != nil {
			return nil, err
		}
		end := min(pos+block, len(encoded))
		chunk := encoded[pos:end]
		if strings.ContainsAny(chunk, "\r\n") {
			return nil, fail(modelinput.Invalid, "buffer_base64")
		}
		n, decodeErr := base64.StdEncoding.Strict().Decode(bin[written:], []byte(chunk))
		if decodeErr != nil {
			return nil, fail(modelinput.Invalid, "buffer_base64")
		}
		if end < len(encoded) && strings.ContainsRune(chunk, '=') {
			return nil, fail(modelinput.Invalid, "buffer_base64")
		}
		written += n
		pos = end
	}
	if int64(written) != decodedLength {
		return nil, fail(modelinput.Invalid, "buffer_base64")
	}
	if err = o.validate(out); err != nil {
		return nil, err
	}
	if err = o.work(0); err != nil {
		return nil, err
	}
	return out, nil
}

func glbParts(raw []byte, policy modelinput.Policy) ([]byte, []byte, error) {
	if len(raw) < 28 || len(raw)%4 != 0 || !bytes.Equal(raw[:4], []byte("glTF")) || binary.LittleEndian.Uint32(raw[4:]) != 2 || int64(binary.LittleEndian.Uint32(raw[8:])) != int64(len(raw)) {
		return nil, nil, fail(modelinput.Invalid, "glb_header")
	}
	j := int64(binary.LittleEndian.Uint32(raw[12:]))
	if j <= 0 || j%4 != 0 || j > int64(len(raw))-28 || !bytes.Equal(raw[16:20], []byte("JSON")) {
		return nil, nil, fail(modelinput.Invalid, "json_chunk")
	}
	if j > policy.Limits.MaxJSONBytes {
		return nil, nil, fail(modelinput.Limit, "json_byte_limit")
	}
	b := 20 + j
	if !bytes.Equal(raw[b+4:b+8], []byte("BIN\x00")) || int64(binary.LittleEndian.Uint32(raw[b:])) != int64(len(raw))-b-8 {
		return nil, nil, fail(modelinput.Invalid, "bin_chunk")
	}
	return bytes.TrimRight(raw[20:b], " "), raw[b+8:], nil
}
func bufferLength(raw []byte, buffer *node) (int64, error) {
	n := buffer.field("byteLength")
	if n == nil {
		return 0, fail(modelinput.Invalid, "buffer_length")
	}
	length, err := strconv.ParseInt(string(raw[n.start:n.end]), 10, 64)
	if err != nil || length <= 0 {
		return 0, fail(modelinput.Invalid, "buffer_length")
	}
	return length, nil
}
func uriMemberRange(buffer *node) (int, int) {
	for i, m := range buffer.members {
		if m.key != "uri" {
			continue
		}
		if i > 0 {
			return m.separator, m.value.end
		}
		if len(buffer.members) > 1 {
			return m.keyStart, buffer.members[1].keyStart
		}
		return m.keyStart, m.value.end
	}
	panic("URI member was checked")
}

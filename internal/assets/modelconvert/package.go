package modelconvert

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"slices"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
)

// Package member names. The glTF references the others by these relative URIs.
const (
	PackageDocument  = "model.gltf"
	PackageBuffer    = "model.bin"
	packageTextures  = "textures/"
	packageEntrySlop = 256 // local header, descriptor and central record per member
)

// PackageResult describes what GLBToGLTFPackage wrote.
type PackageResult struct {
	// Bytes is the ZIP size; BufferBytes the external BIN; Images the number
	// of texture files moved out of the BIN.
	Bytes, BufferBytes int64
	Images             int
}

// GLBToGLTFPackage writes a glTF 2.0 package ZIP: model.gltf, model.bin and
// one file per embedded image, all referenced by relative URIs. Nothing is
// base64 encoded. The source is validated with policy before the first byte
// is written; geometry and image bytes are copied unchanged, so the only
// allocation that scales with the model is the caller's source buffer.
// Limits.MaxOutputBytes bounds the ZIP itself. On error the writer may hold a
// partial archive; the caller must discard it.
func GLBToGLTFPackage(ctx context.Context, raw []byte, policy modelinput.Policy, limits Limits, out io.Writer) (PackageResult, error) {
	var result PackageResult
	o, err := begin(ctx, raw, policy, limits)
	if err != nil {
		return result, err
	}
	if int64(len(raw)) > policy.Limits.MaxBytes {
		return result, fail(modelinput.Limit, "byte_limit")
	}
	if err = o.validate(raw); err != nil {
		return result, err
	}
	j, bin, err := glbParts(raw, policy)
	if err != nil {
		return result, err
	}
	tree, err := o.parse(j, false)
	if err != nil {
		return result, err
	}
	doc := editable(j, tree)
	plan, err := planPackage(doc, bin)
	if err != nil {
		return result, err
	}
	var document bytes.Buffer
	document.Grow(len(j) + 64*len(plan.images))
	if err = doc.emit(&document); err != nil {
		return result, err
	}
	total := int64(document.Len()) + plan.binLength
	for _, image := range plan.images {
		total += image.end - image.start
	}
	entries := int64(2 + len(plan.images))
	if total+packageEntrySlop*(entries+1) > limits.MaxOutputBytes {
		return result, fail(modelinput.Limit, "conversion_output_bytes")
	}
	if err = o.work(2*total + int64(len(raw))); err != nil {
		return result, err
	}

	// Every member header carries its final CRC-32 and sizes, so no member
	// uses a trailing data descriptor (general purpose flag bit 3). Streaming
	// readers such as Java's ZipInputStream reject a STORED member with one.
	var compressed bytes.Buffer
	deflater, err := flate.NewWriter(&compressed, flate.DefaultCompression)
	if err != nil {
		return result, outputError(err)
	}
	if _, err = deflater.Write(document.Bytes()); err == nil {
		err = deflater.Close()
	}
	if err != nil {
		return result, outputError(err)
	}
	binCRC := crc32.NewIEEE()
	if err = plan.writeBuffer(o, binCRC, bin); err != nil {
		return result, err
	}

	counter := &limitedWriter{writer: out, remaining: limits.MaxOutputBytes}
	archive := zip.NewWriter(counter)
	if err = writeMember(o, archive, memberHeader(PackageDocument, zip.Deflate, crc32.ChecksumIEEE(document.Bytes()), int64(compressed.Len()), int64(document.Len())), func(w io.Writer) error {
		_, err := w.Write(compressed.Bytes())
		return err
	}); err != nil {
		return result, err
	}
	if err = writeMember(o, archive, memberHeader(PackageBuffer, zip.Store, binCRC.Sum32(), plan.binLength, plan.binLength), func(w io.Writer) error {
		return plan.writeBuffer(o, w, bin)
	}); err != nil {
		return result, err
	}
	for _, image := range plan.images {
		data := bin[image.start:image.end]
		if err = writeMember(o, archive, memberHeader(image.name, zip.Store, crc32.ChecksumIEEE(data), int64(len(data)), int64(len(data))), func(w io.Writer) error {
			return copyBlocks(o, w, data)
		}); err != nil {
			return result, err
		}
	}
	if err = archive.Close(); err != nil {
		return result, outputError(err)
	}
	if err = o.work(0); err != nil {
		return result, err
	}
	result.Bytes, result.BufferBytes, result.Images = counter.written, plan.binLength, len(plan.images)
	return result, nil
}

var errOutputLimit = errors.New("conversion output limit")

type limitedWriter struct {
	writer             io.Writer
	remaining, written int64
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errOutputLimit
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	w.written += int64(n)
	return n, err
}

func outputError(err error) error {
	var domain *modelinput.Error
	switch {
	case errors.As(err, &domain):
		return err
	case errors.Is(err, errOutputLimit):
		return fail(modelinput.Limit, "conversion_output_bytes")
	default:
		return &modelinput.Error{Kind: modelinput.IO, Reason: "conversion_output_write"}
	}
}

// memberHeader describes a member whose CRC-32 and sizes are known before it
// is written. Flag bit 3 (data descriptor) stays clear. The fixed MS-DOS time
// 1980-01-01 00:00 keeps the archive deterministic.
func memberHeader(name string, method uint16, crc uint32, compressed, uncompressed int64) *zip.FileHeader {
	return &zip.FileHeader{
		Name: name, Method: method, CRC32: crc,
		CompressedSize64: uint64(compressed), UncompressedSize64: uint64(uncompressed),
		CreatorVersion: 20, ReaderVersion: 20,
		ModifiedDate: packageDOSDate,
	}
}

// packageDOSDate is 1980-01-01 in MS-DOS date encoding.
const packageDOSDate = 1<<5 | 1

var errMemberSize = errors.New("package member size differs from its header")

type countedWriter struct {
	writer io.Writer
	count  int64
}

func (w *countedWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.count += int64(n)
	return n, err
}

// writeMember writes the raw member bytes after its complete header and
// proves the written length equals the declared compressed size.
func writeMember(o *operation, archive *zip.Writer, header *zip.FileHeader, body func(io.Writer) error) error {
	if err := o.work(0); err != nil {
		return err
	}
	entry, err := archive.CreateRaw(header)
	if err != nil {
		return outputError(err)
	}
	counted := &countedWriter{writer: entry}
	if err := body(counted); err != nil {
		return outputError(err)
	}
	if uint64(counted.count) != header.CompressedSize64 {
		return outputError(errMemberSize)
	}
	return nil
}

// copyBlocks keeps cancellation observable while large members stream.
func copyBlocks(o *operation, w io.Writer, data []byte) error {
	const block = 1 << 20
	for start := 0; start < len(data); start += block {
		if err := o.work(0); err != nil {
			return err
		}
		if _, err := w.Write(data[start:min(start+block, len(data))]); err != nil {
			return err
		}
	}
	return nil
}

type packagedImage struct {
	name       string
	start, end int64
}

type keptView struct {
	start, end, offset int64
}

type packagePlan struct {
	views     []keptView
	images    []packagedImage
	binLength int64
}

func (p packagePlan) writeBuffer(o *operation, w io.Writer, bin []byte) error {
	var zeros [4]byte
	at := int64(0)
	for _, view := range p.views {
		if pad := view.offset - at; pad > 0 {
			if _, err := w.Write(zeros[:pad]); err != nil {
				return err
			}
		}
		if err := copyBlocks(o, w, bin[view.start:view.end]); err != nil {
			return err
		}
		at = view.offset + view.end - view.start
	}
	if pad := p.binLength - at; pad > 0 {
		_, err := w.Write(zeros[:pad])
		return err
	}
	return nil
}

// planPackage moves every image-only bufferView out of the BIN into its own
// file, compacts the remaining views (each keeps its offset modulo 4, so every
// accessor keeps its component alignment) and rewrites the references. The
// source was fully validated, so indices and ranges are already in bounds.
func planPackage(doc *jvalue, bin []byte) (packagePlan, error) {
	var plan packagePlan
	views := doc.field("bufferViews")
	if views == nil || views.arr == nil {
		return plan, fail(modelinput.Unsupported, "buffer_views")
	}
	imageUse := map[int]bool{}
	dataUse := map[int]bool{}
	images := doc.field("images")
	if images != nil {
		for _, image := range images.arr {
			if index, ok := image.field("bufferView").integer(); ok {
				imageUse[index] = true
			}
		}
	}
	accessors := doc.field("accessors")
	if accessors != nil {
		for _, accessor := range accessors.arr {
			if index, ok := accessor.field("bufferView").integer(); ok {
				dataUse[index] = true
			}
			if sparse := accessor.field("sparse"); sparse != nil {
				for _, part := range []string{"indices", "values"} {
					if index, ok := sparse.field(part).field("bufferView").integer(); ok {
						dataUse[index] = true
					}
				}
			}
		}
	}
	remap := make([]int, len(views.arr))
	kept := make([]*jvalue, 0, len(views.arr))
	ranges := make([][2]int64, len(views.arr))
	names := map[int]string{}
	for index, view := range views.arr {
		offset, okOffset := view.field("byteOffset").sourceInteger()
		length, ok := view.field("byteLength").integer()
		if !ok || !okOffset || length <= 0 || int64(offset)+int64(length) > int64(len(bin)) {
			return plan, fail(modelinput.Invalid, "buffer_view_range")
		}
		ranges[index] = [2]int64{int64(offset), int64(offset + length)}
		if imageUse[index] && !dataUse[index] {
			remap[index] = -1
			continue
		}
		remap[index] = len(kept)
		at := plan.binLength
		if at%4 != int64(offset)%4 {
			at += (int64(offset)%4 - at%4 + 4) % 4
		}
		plan.views = append(plan.views, keptView{start: int64(offset), end: int64(offset + length), offset: at})
		plan.binLength = at + int64(length)
		view.set("byteOffset", literal(strconv.FormatInt(at, 10)))
		kept = append(kept, view)
	}
	if len(kept) == 0 || plan.binLength == 0 {
		return plan, fail(modelinput.Unsupported, "empty_geometry_buffer")
	}
	plan.binLength = (plan.binLength + 3) / 4 * 4
	views.arr = kept
	if images != nil {
		for _, image := range images.arr {
			index, ok := image.field("bufferView").integer()
			if !ok {
				continue
			}
			if index >= len(ranges) {
				return plan, fail(modelinput.Invalid, "image_buffer_view")
			}
			name, exists := names[index]
			if !exists {
				extension := ".png"
				if mime, _ := image.field("mimeType").text(); mime == "image/jpeg" {
					extension = ".jpg"
				}
				name = packageTextures + strconv.Itoa(len(plan.images)) + extension
				names[index] = name
				plan.images = append(plan.images, packagedImage{name: name, start: ranges[index][0], end: ranges[index][1]})
			}
			image.remove("bufferView")
			image.set("uri", stringLiteral(name))
		}
	}
	if accessors != nil {
		for _, accessor := range accessors.arr {
			accessor.remapView(remap)
			if sparse := accessor.field("sparse"); sparse != nil {
				sparse.field("indices").remapView(remap)
				sparse.field("values").remapView(remap)
			}
		}
	}
	buffers := doc.field("buffers")
	if buffers == nil || len(buffers.arr) != 1 {
		return plan, fail(modelinput.Unsupported, "buffer_count")
	}
	buffers.arr[0].set("byteLength", literal(strconv.FormatInt(plan.binLength, 10)))
	buffers.arr[0].set("uri", stringLiteral(PackageBuffer))
	return plan, nil
}

// jvalue is an editable copy of the span-preserving parse tree. Untouched
// scalars are written back byte for byte, so numbers keep their spelling.
type jvalue struct {
	raw    []byte
	keys   []string
	values map[string]*jvalue
	arr    []*jvalue
	object bool
}

func editable(raw []byte, n *node) *jvalue {
	switch n.kind {
	case '{':
		v := &jvalue{object: true, values: make(map[string]*jvalue, len(n.members))}
		for _, m := range n.members {
			v.keys = append(v.keys, m.key)
			v.values[m.key] = editable(raw, m.value)
		}
		return v
	case '[':
		v := &jvalue{arr: make([]*jvalue, 0, len(n.items))}
		for _, item := range n.items {
			v.arr = append(v.arr, editable(raw, item))
		}
		return v
	default:
		return &jvalue{raw: raw[n.start:n.end]}
	}
}

func literal(text string) *jvalue { return &jvalue{raw: []byte(text)} }
func stringLiteral(text string) *jvalue {
	encoded, _ := json.Marshal(text)
	return &jvalue{raw: encoded}
}

func (v *jvalue) field(key string) *jvalue {
	if v == nil || !v.object {
		return nil
	}
	return v.values[key]
}

func (v *jvalue) set(key string, value *jvalue) {
	if _, exists := v.values[key]; !exists {
		v.keys = append(v.keys, key)
	}
	v.values[key] = value
}

func (v *jvalue) remove(key string) {
	if _, exists := v.values[key]; !exists {
		return
	}
	delete(v.values, key)
	v.keys = slices.DeleteFunc(v.keys, func(candidate string) bool { return candidate == key })
}

func (v *jvalue) remapView(remap []int) {
	index, ok := v.field("bufferView").integer()
	if !ok || index >= len(remap) || remap[index] < 0 {
		return
	}
	v.set("bufferView", literal(strconv.Itoa(remap[index])))
}

func (v *jvalue) integer() (int, bool) {
	if v == nil || v.object || v.arr != nil || len(v.raw) == 0 {
		return 0, false
	}
	value, err := strconv.ParseInt(string(v.raw), 10, 64)
	if err != nil || value < 0 || value > 1<<32 {
		return 0, false
	}
	return int(value), true
}

// sourceInteger reads an offset that may be absent (the glTF default is 0).
func (v *jvalue) sourceInteger() (int, bool) {
	if v == nil {
		return 0, true
	}
	return v.integer()
}

func (v *jvalue) text() (string, bool) {
	if v == nil || len(v.raw) == 0 || v.raw[0] != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(v.raw, &s) == nil
}

func (v *jvalue) emit(out *bytes.Buffer) error {
	switch {
	case v.object:
		out.WriteByte('{')
		for index, key := range v.keys {
			if index > 0 {
				out.WriteByte(',')
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return fail(modelinput.Invalid, "json_key")
			}
			out.Write(encoded)
			out.WriteByte(':')
			if err := v.values[key].emit(out); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case v.arr != nil:
		out.WriteByte('[')
		for index, item := range v.arr {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := item.emit(out); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		out.Write(v.raw)
	}
	return nil
}

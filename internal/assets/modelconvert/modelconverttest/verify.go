// Package modelconverttest checks a glTF package ZIP against its source GLB.
package modelconverttest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path"
	"reflect"
	"strings"
)

type gltfDocument struct {
	Buffers []struct {
		URI        string `json:"uri"`
		ByteLength int64  `json:"byteLength"`
	} `json:"buffers"`
	BufferViews []struct {
		Buffer     int   `json:"buffer"`
		ByteOffset int64 `json:"byteOffset"`
		ByteLength int64 `json:"byteLength"`
		ByteStride int64 `json:"byteStride"`
	} `json:"bufferViews"`
	Accessors []map[string]any `json:"accessors"`
	Images    []map[string]any `json:"images"`
}

var componentBytes = map[float64]int64{5120: 1, 5121: 1, 5122: 2, 5123: 2, 5125: 4, 5126: 4}
var typeComponents = map[string]int64{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4}

// VerifyPackage proves the package is a loadable glTF: the document references
// the BIN and every image by a relative member URI, nothing is base64, the
// buffer length equals the BIN member, every accessor reads exactly the bytes
// it read in the source, and every image is the source image byte for byte.
func VerifyPackage(archive, source []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	if err := verifyLocalHeaders(archive, reader.File); err != nil {
		return err
	}
	members := map[string][]byte{}
	for _, file := range reader.File {
		if strings.HasSuffix(file.Name, "/") || path.Clean(file.Name) != file.Name || strings.HasPrefix(file.Name, "../") {
			return fmt.Errorf("unexpected member name %q", file.Name)
		}
		body, err := file.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			return fmt.Errorf("member %s: %w", file.Name, err)
		}
		members[file.Name] = data
	}
	documentBytes, ok := members["model.gltf"]
	if !ok {
		return fmt.Errorf("model.gltf missing")
	}
	if bytes.Contains(documentBytes, []byte("data:")) {
		return fmt.Errorf("model.gltf embeds a data URI")
	}
	var output gltfDocument
	if err := json.Unmarshal(documentBytes, &output); err != nil {
		return fmt.Errorf("model.gltf: %w", err)
	}
	sourceJSON, sourceBIN, err := glbChunks(source)
	if err != nil {
		return err
	}
	var input gltfDocument
	if err := json.Unmarshal(sourceJSON, &input); err != nil {
		return fmt.Errorf("source JSON: %w", err)
	}
	if len(output.Buffers) != 1 || output.Buffers[0].URI != "model.bin" {
		return fmt.Errorf("buffers = %+v", output.Buffers)
	}
	bin, ok := members["model.bin"]
	if !ok || int64(len(bin)) != output.Buffers[0].ByteLength {
		return fmt.Errorf("model.bin length %d differs from byteLength %d", len(bin), output.Buffers[0].ByteLength)
	}
	referenced := map[string]bool{"model.gltf": true, "model.bin": true}
	if len(output.Images) != len(input.Images) {
		return fmt.Errorf("image count %d, source %d", len(output.Images), len(input.Images))
	}
	for index, image := range output.Images {
		uri, _ := image["uri"].(string)
		if _, has := image["bufferView"]; has || uri == "" || strings.Contains(uri, ":") || strings.HasPrefix(uri, "/") {
			return fmt.Errorf("image %d is not a relative file reference: %v", index, image)
		}
		data, ok := members[uri]
		if !ok {
			return fmt.Errorf("image %d references missing member %q", index, uri)
		}
		referenced[uri] = true
		sourceView, _ := input.Images[index]["bufferView"].(float64)
		view := input.BufferViews[int(sourceView)]
		if !bytes.Equal(data, sourceBIN[view.ByteOffset:view.ByteOffset+view.ByteLength]) {
			return fmt.Errorf("image %d bytes differ from the source", index)
		}
	}
	for name := range members {
		if !referenced[name] {
			return fmt.Errorf("member %q is not referenced", name)
		}
	}
	for index, view := range output.BufferViews {
		if view.Buffer != 0 || view.ByteOffset < 0 || view.ByteLength <= 0 || view.ByteOffset+view.ByteLength > int64(len(bin)) {
			return fmt.Errorf("bufferView %d out of range", index)
		}
	}
	if len(output.Accessors) != len(input.Accessors) {
		return fmt.Errorf("accessor count changed")
	}
	for index, accessor := range output.Accessors {
		want, got := maps.Clone(input.Accessors[index]), maps.Clone(accessor)
		delete(want, "bufferView")
		delete(got, "bufferView")
		if !reflect.DeepEqual(want, got) {
			return fmt.Errorf("accessor %d fields changed: %v vs %v", index, want, got)
		}
		sourceBytes, err := accessorBytes(input, input.Accessors[index], sourceBIN)
		if err != nil {
			return err
		}
		outputBytes, err := accessorBytes(output, accessor, bin)
		if err != nil {
			return err
		}
		if !bytes.Equal(sourceBytes, outputBytes) {
			return fmt.Errorf("accessor %d bytes differ", index)
		}
	}
	return nil
}

func accessorBytes(document gltfDocument, accessor map[string]any, bin []byte) ([]byte, error) {
	viewIndex, ok := accessor["bufferView"].(float64)
	if !ok {
		return nil, fmt.Errorf("accessor without bufferView")
	}
	view := document.BufferViews[int(viewIndex)]
	offset, _ := accessor["byteOffset"].(float64)
	width := componentBytes[accessor["componentType"].(float64)] * typeComponents[accessor["type"].(string)]
	stride := view.ByteStride
	if stride == 0 {
		stride = width
	}
	count := int64(accessor["count"].(float64))
	var out []byte
	for item := int64(0); item < count; item++ {
		start := view.ByteOffset + int64(offset) + item*stride
		if start+width > int64(len(bin)) || start+width > view.ByteOffset+view.ByteLength {
			return nil, fmt.Errorf("accessor reads outside its view")
		}
		out = append(out, bin[start:start+width]...)
	}
	return out, nil
}

func glbChunks(raw []byte) ([]byte, []byte, error) {
	if len(raw) < 28 || string(raw[:4]) != "glTF" {
		return nil, nil, fmt.Errorf("source is not a GLB")
	}
	jsonLength := int(binary.LittleEndian.Uint32(raw[12:]))
	binStart := 20 + jsonLength
	if binStart+8 > len(raw) {
		return nil, nil, fmt.Errorf("source GLB truncated")
	}
	return raw[20:binStart], raw[binStart+8:], nil
}

// verifyLocalHeaders walks the archive the way a streaming reader does: each
// local header must carry the final CRC-32 and sizes with flag bit 3 (data
// descriptor) clear, and the next record must follow the member data directly.
// Java's ZipInputStream, for one, rejects a STORED member with a descriptor.
func verifyLocalHeaders(archive []byte, files []*zip.File) error {
	at := 0
	for _, file := range files {
		if file.Flags&0x8 != 0 {
			return fmt.Errorf("%s: central flags %#x set the data-descriptor bit", file.Name, file.Flags)
		}
		if at+30 > len(archive) || binary.LittleEndian.Uint32(archive[at:]) != 0x04034b50 {
			return fmt.Errorf("%s: no local header at offset %d", file.Name, at)
		}
		header := archive[at : at+30]
		flags := binary.LittleEndian.Uint16(header[6:])
		method := binary.LittleEndian.Uint16(header[8:])
		crc := binary.LittleEndian.Uint32(header[14:])
		compressed := int(binary.LittleEndian.Uint32(header[18:]))
		uncompressed := binary.LittleEndian.Uint32(header[22:])
		nameLength := int(binary.LittleEndian.Uint16(header[26:]))
		extraLength := int(binary.LittleEndian.Uint16(header[28:]))
		if flags&0x8 != 0 || method != file.Method || crc != file.CRC32 || uint64(compressed) != file.CompressedSize64 || uint64(uncompressed) != file.UncompressedSize64 {
			return fmt.Errorf("%s: local header flags=%#x method=%d crc=%#x sizes=%d/%d differ from the final values", file.Name, flags, method, crc, compressed, uncompressed)
		}
		if string(archive[at+30:at+30+nameLength]) != file.Name {
			return fmt.Errorf("%s: local header name differs", file.Name)
		}
		at += 30 + nameLength + extraLength + compressed
	}
	if at+4 > len(archive) || binary.LittleEndian.Uint32(archive[at:]) != 0x02014b50 {
		return fmt.Errorf("central directory does not follow the last member")
	}
	return nil
}

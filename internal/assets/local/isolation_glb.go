package local

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// A GLB can contain a complete PNG/JPEG inside a BIN bufferView. Inspect that
// image through the same verified raster boundary as a standalone asset.
// Unclassified BIN bytes retain the generic address check.
func inspectGLBStream(reader io.Reader, size int64, archiveNames map[string]struct{}, base string) error {
	return inspectGLBStreamBounded(reader, size, archiveNames, base, newInlineInspectionBudgetForSize(size))
}

func inspectGLBStreamBounded(reader io.Reader, size int64, archiveNames map[string]struct{}, base string, budget *inlineInspectionBudget) error {
	if size < 20 || size%4 != 0 {
		return errors.New("GLB 头部或长度无效")
	}
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	if !bytes.Equal(header[:4], []byte("glTF")) || binary.LittleEndian.Uint32(header[4:8]) != 2 ||
		int64(binary.LittleEndian.Uint32(header[8:12])) != size {
		return errors.New("GLB 结构无效")
	}
	inspector := newPlannedInspectorWithWork(budget.inspectionWork())
	if err := inspector.inspect(header[:], inspectionPlan{numeric: []inspectionSpan{{4, 12}}}); err != nil {
		return err
	}
	var document any
	seenJSON, seenBIN := false, false
	for offset := int64(12); offset < size; {
		if size-offset < 8 {
			return errors.New("GLB 块头不完整")
		}
		var chunk [8]byte
		if _, err := io.ReadFull(reader, chunk[:]); err != nil {
			return err
		}
		length := int64(binary.LittleEndian.Uint32(chunk[:4]))
		offset += 8
		if length%4 != 0 || length > size-offset {
			return errors.New("GLB 块长度无效")
		}
		if err := inspector.inspect(chunk[:], inspectionPlan{numeric: []inspectionSpan{{0, 4}}}); err != nil {
			return err
		}
		switch string(chunk[4:]) {
		case "JSON":
			if seenJSON || offset != 20 || length == 0 || length > maxModelJSONBytes {
				return errors.New("GLB JSON 块无效或超出限制")
			}
			data := make([]byte, int(length))
			if _, err := io.ReadFull(reader, data); err != nil {
				return err
			}
			var err error
			document, err = decodeGLTFDocumentBounded(data, archiveNames, base, budget)
			if err != nil {
				return err
			}
			spans, err := inspectedGLTFJSONSpans(data)
			if err != nil {
				return err
			}
			if err := inspector.inspect(data, inspectionPlan{allowed: spans}); err != nil {
				return err
			}
			seenJSON = true
		case "BIN\x00":
			if !seenJSON || seenBIN {
				return errors.New("GLB BIN 块顺序无效")
			}
			images, err := embeddedGLBImages(document, length)
			if err != nil {
				return err
			}
			fields, err := gltfNumericFields(document, 0, length)
			if err != nil {
				return err
			}
			if err := validateImageNumericRanges(images, fields); err != nil {
				return err
			}
			draco, err := glbDracoRanges(document, length, images, fields)
			if err != nil {
				return err
			}
			if err := inspectGLBBINWithDraco(reader, length, images, fields, draco, inspector); err != nil {
				return err
			}
			seenBIN = true
		default:
			if !seenJSON {
				return errors.New("GLB 首块必须是 JSON")
			}
			if err := inspectUnplannedSection(reader, length, inspector); err != nil {
				return err
			}
		}
		offset += length
	}
	if !seenJSON {
		return errors.New("GLB 缺少 JSON 块")
	}
	if !seenBIN {
		if _, err := glbDracoRanges(document, 0, nil, nil); err != nil {
			return err
		}
	}
	var extra [1]byte
	if count, err := reader.Read(extra[:]); count != 0 || err != io.EOF {
		if err != nil && err != io.EOF {
			return err
		}
		return errors.New("GLB 内容超出声明长度")
	}
	return nil
}

func decodeGLTFDocumentBounded(data []byte, archiveNames map[string]struct{}, base string, budget *inlineInspectionBudget) (any, error) {
	if err := validateUniqueJSONFields(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("glTF JSON 无效")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("glTF JSON 含额外内容")
	}
	previousDocument, previousBuffer := budget.document, budget.buffer
	budget.document, budget.buffer = document, 0
	defer func() { budget.document, budget.buffer = previousDocument, previousBuffer }()
	if err := checkGLTFURIsBounded(document, archiveNames, base, budget, "document"); err != nil {
		return nil, err
	}
	if archiveNames != nil && budget.onArchiveDocument != nil {
		if err := budget.onArchiveDocument(document, base); err != nil {
			return nil, err
		}
	}
	return document, nil
}

type gltfImageRange struct {
	start, end int64
	format     string
}

func gltfInteger(object map[string]any, key string, fallback int64) (int64, error) {
	value, present := object[key]
	if !present {
		return fallback, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("glTF 字节范围必须是整数")
	}
	result, err := number.Int64()
	if err != nil || result < 0 {
		return 0, errors.New("glTF 字节范围无效")
	}
	return result, nil
}

func gltfArray(document map[string]any, key string) ([]any, error) {
	value, present := document[key]
	if !present {
		return nil, nil
	}
	array, ok := value.([]any)
	if !ok || len(array) > maxArchiveFiles {
		return nil, errors.New("glTF 字节范围数量无效")
	}
	return array, nil
}

func embeddedGLBImages(value any, binLength int64) ([]gltfImageRange, error) {
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("glTF JSON 顶层必须是对象")
	}
	buffers, err := gltfArray(document, "buffers")
	if err != nil {
		return nil, err
	}
	views, err := gltfArray(document, "bufferViews")
	if err != nil {
		return nil, err
	}
	images, err := gltfArray(document, "images")
	if err != nil {
		return nil, err
	}
	if len(buffers) == 0 {
		if len(views) != 0 || len(images) != 0 {
			return nil, errors.New("GLB 缺少嵌入缓冲区")
		}
		return nil, nil
	}
	buffer, ok := buffers[0].(map[string]any)
	if !ok {
		return nil, errors.New("GLB 缓冲区无效")
	}
	if _, hasURI := buffer["uri"]; hasURI {
		return nil, errors.New("GLB BIN 缓冲区不能含 URI")
	}
	byteLength, err := gltfInteger(buffer, "byteLength", -1)
	if err != nil || byteLength < 0 || byteLength > binLength || binLength-byteLength > 3 {
		return nil, errors.New("GLB 缓冲区长度无效")
	}
	return gltfBufferImages(document, 0, byteLength)
}

// BIN and decoded data-URI buffers use the same image declarations. Only the
// GLB wrapper owns the BIN padding and absent-URI requirements above.
func gltfBufferImages(value any, targetBuffer, bufferLength int64) ([]gltfImageRange, error) {
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("glTF JSON 顶层必须是对象")
	}
	buffers, err := gltfArray(document, "buffers")
	if err != nil {
		return nil, err
	}
	views, err := gltfArray(document, "bufferViews")
	if err != nil {
		return nil, err
	}
	images, err := gltfArray(document, "images")
	if err != nil {
		return nil, err
	}
	if targetBuffer < 0 || targetBuffer >= int64(len(buffers)) {
		return nil, errors.New("glTF 缓冲区索引无效")
	}
	buffer, ok := buffers[targetBuffer].(map[string]any)
	if !ok {
		return nil, errors.New("glTF 缓冲区声明无效")
	}
	byteLength, err := gltfInteger(buffer, "byteLength", -1)
	if err != nil || byteLength < 0 || byteLength != bufferLength {
		return nil, errors.New("glTF 缓冲区长度无效")
	}
	ranges := make([]gltfImageRange, 0, len(images))
	for _, entry := range images {
		image, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("glTF 图片声明无效")
		}
		if _, present := image["bufferView"]; !present {
			continue
		}
		if _, present := image["uri"]; present {
			return nil, errors.New("glTF 图片来源冲突")
		}
		index, err := gltfInteger(image, "bufferView", -1)
		if err != nil || index < 0 || index >= int64(len(views)) {
			return nil, errors.New("glTF 图片缓冲区索引无效")
		}
		view, ok := views[index].(map[string]any)
		if !ok {
			return nil, errors.New("glTF 图片字节范围无效")
		}
		bufferIndex, err := gltfInteger(view, "buffer", -1)
		if err != nil || bufferIndex < 0 || bufferIndex >= int64(len(buffers)) {
			return nil, errors.New("glTF 图片缓冲区索引无效")
		}
		if bufferIndex != targetBuffer {
			// Each buffer is inspected by its own declared source. Its image
			// bytes cannot grant a waiver to another buffer's numeric fields.
			continue
		}
		start, err := gltfInteger(view, "byteOffset", 0)
		if err != nil {
			return nil, err
		}
		length, err := gltfInteger(view, "byteLength", -1)
		if err != nil || length <= 0 || start > byteLength || length > byteLength-start {
			return nil, errors.New("glTF 图片字节范围越界")
		}
		mimeType, ok := image["mimeType"].(string)
		if !ok || !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
			return nil, errors.New("glTF 嵌入图片缺少媒体类型")
		}
		format := strings.ToLower(mimeType)
		switch format {
		case "image/png":
			format = "png"
		case "image/jpeg":
			format = "jpeg"
		}
		if length > maxRasterInspect {
			return nil, errors.New("glTF 嵌入图片超出检查上限")
		}
		// Unsupported image codecs still have a declared field boundary and
		// cannot overlap numeric accessors. Their bytes receive no waiver.
		ranges = append(ranges, gltfImageRange{start, start + length, format})
	}
	sort.Slice(ranges, func(left, right int) bool { return ranges[left].start < ranges[right].start })
	for index := 1; index < len(ranges); index++ {
		if ranges[index].start < ranges[index-1].end {
			if ranges[index] == ranges[index-1] {
				continue
			}
			return nil, errors.New("glTF 嵌入图片字节范围冲突")
		}
	}
	return ranges, nil
}

func validateImageNumericRanges(images []gltfImageRange, fields []inspectionField) error {
	for _, image := range images {
		for _, field := range fields {
			if field.start < image.end && field.end() > image.start {
				return errors.New("glTF 图片与数值字段范围冲突")
			}
		}
	}
	return nil
}

func inspectGLBBIN(reader io.Reader, length int64, images []gltfImageRange, fields []inspectionField, inspector *plannedInspector) error {
	return inspectGLBBINWithDraco(reader, length, images, fields, nil, inspector)
}

func inspectGLBBINWithDraco(reader io.Reader, length int64, images []gltfImageRange, fields []inspectionField, draco []dracoRange, inspector *plannedInspector) error {
	plan := inspectionPlan{fields: fields, sections: int64(len(images)+len(draco)) + 1}
	if err := inspector.prepare(&plan, length); err != nil {
		return err
	}
	var offset int64
	for len(images) > 0 || len(draco) > 0 {
		if len(draco) > 0 && (len(images) == 0 || draco[0].start < images[0].start) {
			r := draco[0]
			draco = draco[1:]
			if err := inspectGLBPlannedSection(reader, r.start-offset, offset, plan, inspector, nil); err != nil {
				return err
			}
			if err := inspector.work.reserve(r.work); err != nil {
				return err
			}
			data := make([]byte, int(r.end-r.start))
			if _, err := io.ReadFull(reader, data); err != nil {
				return err
			}
			if err := validateDraco(data, r, inspector.work); err != nil {
				return err
			}
			compressedPlan := inspectionPlan{numeric: []inspectionSpan{{r.start, r.end}}}
			if err := inspectGLBPlannedSection(bytes.NewReader(data), r.end-r.start, r.start, compressedPlan, inspector, nil); err != nil {
				return err
			}
			offset = r.end
			continue
		}
		image := images[0]
		images = images[1:]
		if image.end <= offset {
			continue // multiple materials can share one image bufferView
		}
		if err := inspectGLBPlannedSection(reader, image.start-offset, offset, plan, inspector, nil); err != nil {
			return err
		}
		data := make([]byte, int(image.end-image.start))
		if _, err := io.ReadFull(reader, data); err != nil {
			return err
		}
		var imagePlan inspectionPlan
		if image.format == "png" || image.format == "jpeg" {
			// The padding bytes stay in data and are still address-scanned
			// below; only the raster validation reads the image itself.
			raster := data[:len(data)-glbImageAlignmentPadding(data, image.format, image.end)]
			var err error
			imagePlan, err = validateRasterBytesWithWork(raster, image.format, inspector.work)
			if err != nil {
				return err
			}
		} else {
			if rasterFormat(data[:min(8, len(data))], "", "") != "" {
				return errors.New("glTF 嵌入图片媒体声明与字节不一致")
			}
			if err := scanExternalAddresses(bytes.NewReader(data)); err != nil {
				return err
			}
		}
		if err := inspector.inspect(data, imagePlan); err != nil {
			return err
		}
		offset = image.end
	}
	return inspectGLBPlannedSection(reader, length-offset, offset, plan, inspector, nil)
}

func inspectUnplannedSection(reader io.Reader, length int64, inspector *plannedInspector) error {
	return inspectGLBPlannedSection(reader, length, 0, inspectionPlan{}, inspector, newPlannedInspector())
}

func inspectGLBPlannedSection(reader io.Reader, length, offset int64, plan inspectionPlan, inspector, boundary *plannedInspector) error {
	if length < 0 {
		return errors.New("内容检查范围冲突")
	}
	buffer := make([]byte, min(int64(isolationChunkBytes), length))
	for length > 0 {
		count := int(min(int64(len(buffer)), length))
		if _, err := io.ReadFull(reader, buffer[:count]); err != nil {
			return err
		}
		if boundary != nil {
			// Unknown chunk bodies begin a new field, even when the preceding
			// chunk-type byte is a letter. Keep the enclosing scan as well.
			if err := boundary.inspectPart(buffer[:count], plan, offset); err != nil {
				return err
			}
		}
		if err := inspector.inspectPart(buffer[:count], plan, offset); err != nil {
			return err
		}
		length -= int64(count)
		offset += int64(count)
	}
	return nil
}

// glbImageAlignmentPadding is the number of trailing bytes of an embedded
// image view that are only 4-byte alignment padding (#189): one to three
// space or zero bytes after the image's own end marker (PNG IEND chunk, JPEG
// EOI), with the view ending on a 4-byte boundary of the BIN chunk. Exporters
// such as trimesh count that padding in the image bufferView. Anything else is
// 0, so the raster check sees and refuses any other trailer.
func glbImageAlignmentPadding(data []byte, format string, viewEnd int64) int {
	if viewEnd%4 != 0 {
		return 0
	}
	padding := 0
	for padding < 3 && padding < len(data) && (data[len(data)-1-padding] == ' ' || data[len(data)-1-padding] == 0) {
		padding++
	}
	if padding == 0 {
		return 0
	}
	image := data[:len(data)-padding]
	switch format {
	case "png":
		if bytes.HasSuffix(image, []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}) {
			return padding
		}
	case "jpeg":
		if bytes.HasSuffix(image, []byte{0xff, 0xd9}) {
			return padding
		}
	}
	return 0
}

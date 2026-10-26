package local

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
)

type archiveBufferLayout struct {
	images []gltfImageRange
	fields []inspectionField
}

// References bind to one member of this already framed ZIP. Models are fully
// checked first, so member order cannot turn a cached buffer into unknown data.
// The second pass checks each referenced buffer with the same typed boundary as
// a GLB BIN. No entire member or unreferenced byte receives a waiver.
type archiveGLTFBuffers struct {
	names    map[string]struct{}
	entries  map[string]*zip.File
	layouts  map[string]*archiveBufferLayout
	bindings int
	ranges   int
}

func newArchiveGLTFBuffers(entries []*zip.File, names map[string]struct{}) *archiveGLTFBuffers {
	result := &archiveGLTFBuffers{names: names, entries: make(map[string]*zip.File, len(entries)), layouts: make(map[string]*archiveBufferLayout)}
	for _, entry := range entries {
		result.entries[path.Clean(entry.Name)] = entry
	}
	return result
}

func (buffers *archiveGLTFBuffers) addModel(value any, base string) error {
	document, ok := value.(map[string]any)
	if !ok {
		return errors.New("glTF JSON 顶层必须是对象")
	}
	declared, err := gltfArray(document, "buffers")
	if err != nil {
		return err
	}
	for index, item := range declared {
		buffer, ok := item.(map[string]any)
		if !ok {
			return errors.New("glTF 缓冲区声明无效")
		}
		raw, present := buffer["uri"]
		if !present {
			continue // The GLB parser owns its embedded BIN source.
		}
		uri, ok := raw.(string)
		if !ok {
			return errors.New("glTF 缓冲区 URI 无效")
		}
		if isInlineDataURI(uri) {
			continue // Its decoded bytes were checked before this callback.
		}
		if !safeModelURI(uri, buffers.names, base) {
			return errors.New("glTF 含未缓存的外部依赖")
		}
		parsed, err := url.Parse(uri)
		if err != nil {
			return errors.New("glTF 缓冲区 URI 无效")
		}
		name := path.Clean(path.Join(path.Dir(base), parsed.Path))
		entry := buffers.entries[name]
		if entry == nil || isZIPDirectory(&entry.FileHeader) {
			return errors.New("glTF 缓冲区没有唯一文件来源")
		}
		buffers.bindings++
		if buffers.bindings > maxArchiveFiles {
			return errors.New("模型压缩包缓冲区引用数量超出限制")
		}
		length, err := gltfInteger(buffer, "byteLength", -1)
		if err != nil || length <= 0 || uint64(length) != entry.UncompressedSize64 {
			return errors.New("glTF 缓冲区长度与缓存成员不一致")
		}
		images, err := gltfBufferImages(document, int64(index), length)
		if err != nil {
			return err
		}
		fields, err := gltfNumericFields(document, int64(index), length)
		if err != nil {
			return err
		}
		count := len(images) + len(fields)
		if count > maxArchiveFiles-buffers.ranges {
			return errors.New("模型压缩包缓冲区字段数量超出限制")
		}
		buffers.ranges += count
		layout := buffers.layouts[name]
		if layout == nil {
			layout = &archiveBufferLayout{}
			buffers.layouts[name] = layout
		}
		layout.images = append(layout.images, images...)
		layout.fields = append(layout.fields, fields...)
	}
	return nil
}

func (buffers *archiveGLTFBuffers) finalize() error {
	// Merge once after all registrations. Revalidating every accumulated
	// layout per reference would make many shared references quadratic.
	for _, layout := range buffers.layouts {
		sort.Slice(layout.images, func(left, right int) bool {
			a, b := layout.images[left], layout.images[right]
			if a.start != b.start {
				return a.start < b.start
			}
			if a.end != b.end {
				return a.end < b.end
			}
			return a.format < b.format
		})
		images := layout.images[:0]
		for _, image := range layout.images {
			if len(images) > 0 && image.start < images[len(images)-1].end {
				if image == images[len(images)-1] {
					continue
				}
				return errors.New("glTF 缓存图片字节范围冲突")
			}
			images = append(images, image)
		}
		layout.images = images
		if err := validateImageNumericRanges(layout.images, layout.fields); err != nil {
			return err
		}
	}
	return nil
}

// An entry is peeked once and read at most twice: once for its own wire format
// and once as a referenced buffer. Charge all reads, including peeks, against
// three times the already bounded archive expansion. Buffers stay streaming.
type archiveInspectionWork struct{ remaining int64 }

type archiveInspectionReader struct {
	reader io.Reader
	work   *archiveInspectionWork
}

func (reader archiveInspectionReader) Read(body []byte) (int, error) {
	if len(body) == 0 {
		return 0, nil
	}
	if reader.work.remaining <= 0 {
		return 0, errors.New("模型压缩包内容检查工作量超出限制")
	}
	body = body[:min(int64(len(body)), reader.work.remaining)]
	count, err := reader.reader.Read(body)
	reader.work.remaining -= int64(count)
	return count, err
}

func openArchiveInspectionEntry(entry *zip.File, work *archiveInspectionWork) (io.ReadCloser, *bufio.Reader, error) {
	body, err := entry.Open()
	if err != nil {
		return nil, nil, err
	}
	return body, bufio.NewReader(archiveInspectionReader{body, work}), nil
}

func closeArchiveInspectionEntry(body io.ReadCloser, inspection error) error {
	closeErr := body.Close()
	if inspection != nil {
		return inspection
	}
	return closeErr
}

func archiveModelMember(magic []byte, name string) bool {
	if isArchiveXML(name) {
		return false
	}
	if isUSDCFormat(magic, name) {
		return true
	}
	if bytes.HasPrefix(magic, []byte("glTF")) || strings.HasSuffix(strings.ToLower(name), ".glb") {
		return true
	}
	return rasterFormat(magic, name, "") == "" && strings.HasSuffix(strings.ToLower(name), ".gltf")
}

func archiveMemberHasOwnContract(magic []byte, name string) bool {
	return isArchiveXML(name) || archiveModelMember(magic, name) ||
		isBinaryFBXMagic(magic) || rasterFormat(magic, name, "") != "" || isSTLFormat(name, "")
}

func inspectArchiveModelMember(entry *zip.File, names map[string]struct{}, budget *inlineInspectionBudget, work *archiveInspectionWork) (bool, error) {
	body, reader, err := openArchiveInspectionEntry(entry, work)
	if err != nil {
		return false, err
	}
	magic, _ := reader.Peek(8)
	if !archiveModelMember(magic, entry.Name) {
		return false, closeArchiveInspectionEntry(body, nil)
	}
	err = inspectArchiveMemberReader(reader, entry, names, budget, nil)
	return true, closeArchiveInspectionEntry(body, err)
}

func inspectArchiveBufferMember(entry *zip.File, names map[string]struct{}, budget *inlineInspectionBudget, work *archiveInspectionWork, layout *archiveBufferLayout, ownChecked bool) error {
	body, reader, err := openArchiveInspectionEntry(entry, work)
	if err != nil {
		return err
	}
	magic, _ := reader.Peek(8)
	if layout != nil && !ownChecked && archiveMemberHasOwnContract(magic, entry.Name) {
		// A PNG/XML/STL/FBX member must still satisfy its own format contract.
		// A second model cannot reinterpret its metadata as geometry.
		err = inspectArchiveMemberReader(reader, entry, names, budget, nil)
		if err := closeArchiveInspectionEntry(body, err); err != nil {
			return err
		}
		body, reader, err = openArchiveInspectionEntry(entry, work)
		if err != nil {
			return err
		}
	}
	err = inspectArchiveMemberReader(reader, entry, names, budget, layout)
	return closeArchiveInspectionEntry(body, err)
}

func inspectArchiveMemberReader(reader *bufio.Reader, entry *zip.File, names map[string]struct{}, budget *inlineInspectionBudget, layout *archiveBufferLayout) error {
	magic, _ := reader.Peek(8)
	if bytes.HasPrefix(magic, []byte("PK\x03\x04")) {
		return errors.New("模型压缩包不能包含未检查的嵌套压缩包")
	}
	limited := &io.LimitedReader{R: reader, N: int64(entry.UncompressedSize64) + 1}
	var err error
	if layout != nil {
		err = inspectGLBBIN(limited, int64(entry.UncompressedSize64), layout.images, layout.fields, newPlannedInspectorWithWork(budget.inspectionWork()))
	} else if isArchiveXML(entry.Name) {
		err = scanArchiveXML(limited, int64(entry.UncompressedSize64))
	} else if isUSDCFormat(magic, entry.Name) {
		err = inspectUSDC(limited, int64(entry.UncompressedSize64), names, entry.Name, budget.inspectionWork())
	} else if isBinaryFBXMagic(magic) {
		err = inspectBinaryFBX(limited, int64(entry.UncompressedSize64), budget.inspectionWork())
	} else if bytes.HasPrefix(magic, []byte("glTF")) || strings.HasSuffix(strings.ToLower(entry.Name), ".glb") {
		err = inspectGLBStreamBounded(limited, int64(entry.UncompressedSize64), names, entry.Name, budget)
	} else if format := rasterFormat(magic, entry.Name, ""); format != "" {
		err = inspectRasterAddressesWithWork(limited, int64(entry.UncompressedSize64), format, budget.inspectionWork())
	} else if strings.HasSuffix(strings.ToLower(entry.Name), ".gltf") {
		err = inspectGLTFJSONBounded(limited, int64(entry.UncompressedSize64), names, entry.Name, budget)
	} else if isSTLFormat(entry.Name, "") {
		err = inspectSTLAddresses(bufio.NewReader(limited), int64(entry.UncompressedSize64))
	} else {
		err = scanExternalAddresses(limited)
	}
	if err != nil {
		return err
	}
	var extra [1]byte
	count, err := limited.Read(extra[:])
	if count != 0 || err != io.EOF || limited.N != 1 {
		if err != nil && err != io.EOF {
			return err // Includes a CRC failure reported only at EOF.
		}
		return errors.New("模型压缩包成员内容与声明大小不一致")
	}
	return nil
}

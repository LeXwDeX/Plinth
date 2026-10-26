package local

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
	"github.com/LeXwDeX/Plinth/internal/rigcheckreport"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	isolationChunkBytes = 64 << 10
	isolationOverlap    = 4 << 10
	maxModelJSONBytes   = 32 << 20
	maxArchiveXMLBytes  = 256 << 20
	maxArchiveFiles     = 10000
	maxArchiveBytes     = 1 << 30
	// Matches the largest base64 image response (max_response_bytes, 256 MiB);
	// 8K PBR textures can exceed 64 MiB. A URL-downloaded texture is bounded
	// only by its role's max_bytes (1 GiB) and is rejected above this limit.
	// Images over the decode budget are walked, not decoded.
	maxRasterInspect = 256 << 20
)

// Network authorities include IP addresses and local hostnames. Require a URI
// boundary before // so an OBJ face such as 1//1 remains valid geometry.
const networkAddressBoundary = `[^\p{L}\p{N}_./\\:+-]`
const protocolRelativeAddress = `//(?:[\p{L}\p{N}._~!$&'()*+,;=:%-]+@)?(?:[\p{L}\p{N}][\p{L}\p{N}._-]*|\[[a-z0-9:.%_-]+\])(?::[0-9]+)?(?:[/?:#\s]|$)`

var externalAddress = regexp.MustCompile(`(?i)(https?://|(?:^|` + networkAddressBoundary + `)` + protocolRelativeAddress + `)`)
var continuedExternalAddress = regexp.MustCompile(`(?i)(https?://|` + networkAddressBoundary + protocolRelativeAddress + `)`)
var absoluteAddress = regexp.MustCompile(`(?i)https?://`)
var percentByte = regexp.MustCompile(`(?i)%([0-9a-f]{2})`)
var escapedASCII = regexp.MustCompile(`(?i)\\u([0-9a-f]{4})`)

// inspectDeliveredAsset runs before a generated blob is committed and again
// before an old READY blob is handed to a caller. The latter prevents legacy
// content from bypassing a newer isolation rule.
func inspectDeliveredAsset(file *os.File, role, variant, mediaType string) error {
	format, err := inspectDeliveredAssetFormat(file, role, variant, mediaType)
	if err != nil {
		return labelInspectionFailure(format, err)
	}
	return nil
}

// inspectDeliveredAssetFormat reports the wire family that rejected the bytes
// so the typed diagnostic can name it without exposing content.
func inspectDeliveredAssetFormat(file *os.File, role, variant, mediaType string) (string, error) {
	if role == schema.GaussianSplatRole || variant == schema.GaussianSplatVariant || normalizeMediaType(mediaType) == schema.GaussianSplatMediaType {
		return "gaussian_splat", deliveryProfileError()
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if normalizeMediaType(mediaType) == rigcheckreport.MediaType {
		if info.Size() <= 0 || info.Size() > rigcheckreport.MaxBytes {
			return "rig_check_report", rigcheckreport.ErrInvalid
		}
		err := rigcheckreport.ValidateReader(file, rigcheckreport.MaxBytes)
		if err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
		return "rig_check_report", err
	}
	if mediaType == printreport.MediaType {
		err := inspectPrintReport(file, info.Size())
		if err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
		return "print_report", err
	}
	var magic [8]byte
	_, _ = io.ReadFull(file, magic[:])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if normalizeMediaType(mediaType) == "model/gltf-binary" && !bytes.Equal(magic[:4], []byte("glTF")) {
		return "glb", errors.New("GLB media type requires a binary glTF container")
	}
	// A slot declared as STL must hold an STL mesh. Its numeric records are
	// never reinterpreted as a ZIP footer or another container's magic, so
	// HTML, archives or other models in an STL slot are rejected here.
	if isSTLFormat(variant, mediaType) {
		if err := inspectSTL(file, info.Size()); err != nil {
			return "stl", err
		}
		_, err = file.Seek(0, io.SeekStart)
		return "stl", err
	}
	// A Blender container is opaque; its random compressed tail must not be
	// mistaken for a ZIP footer.
	blend := isBlendFormat(variant, mediaType) && blendMagicKnown(magic[:])
	archive := false
	if !blend {
		archive, err = looksLikeZIP(file, info.Size(), magic[:], variant, mediaType)
		if err != nil {
			return "archive", err
		}
	}
	if archive {
		if err := inspectArchive(file, info.Size()); err != nil {
			return "archive", err
		}
		// A slot declared as 3MF must also be a 3MF package, not any ZIP.
		if is3MFFormat(variant, mediaType) {
			if err := inspect3MFIdentity(file, info.Size()); err != nil {
				return "3mf", err
			}
		}
		// A slot declared as an OBJ archive must hold an OBJ, not any ZIP.
		if isOBJArchiveFormat(variant, mediaType) {
			if err := inspectOBJArchiveIdentity(file, info.Size()); err != nil {
				return "archive", err
			}
		}
		_, err = file.Seek(0, io.SeekStart)
		return "archive", err
	}
	format := "text"
	switch {
	case isUSDCFormat(magic[:], variant):
		format, err = "usdc", inspectUSDC(file, info.Size(), nil, "", newInspectionWorkBudgetForSize(info.Size()))
	case isBinaryFBXMagic(magic[:]):
		format, err = "fbx", inspectBinaryFBX(file, info.Size(), newInspectionWorkBudgetForSize(info.Size()))
	case bytes.Equal(magic[:4], []byte("glTF")):
		format, err = "glb", inspectGLB(file, info.Size(), nil, "")
	case isGLTFJSON(role, variant, mediaType, magic[:]):
		format, err = "gltf", inspectGLTFJSON(file, info.Size(), nil, "")
	case rasterFormat(magic[:], variant, mediaType) != "":
		format, err = "raster", inspectRasterAddresses(file, info.Size(), rasterFormat(magic[:], variant, mediaType))
	case isBlendFormat(variant, mediaType):
		format, err = "blend", inspectBlend(file, info.Size(), magic[:])
	default:
		err = scanExternalAddresses(file)
	}
	if err != nil {
		return format, err
	}
	_, err = file.Seek(0, io.SeekStart)
	return format, err
}

// ValidateDeliveredFile applies the same byte boundary to older server-side
// caches that predate the content-addressed store. The caller retains file
// ownership; a successful check resets its offset to the beginning.
func ValidateDeliveredFile(file *os.File, role, variant, mediaType string) error {
	return inspectDeliveredAsset(file, role, variant, mediaType)
}

// ValidateDeliveredGLBBytes applies the existing GLB delivery isolation check
// to an immutable in-memory container. It does not resolve external resources.
func ValidateDeliveredGLBBytes(raw []byte) error {
	if len(raw) < 4 || !bytes.Equal(raw[:4], []byte("glTF")) {
		return labelInspectionFailure("glb", errors.New("GLB media type requires a binary glTF container"))
	}
	reader, size := bytes.NewReader(raw), int64(len(raw))
	var magic [8]byte
	copy(magic[:], raw)
	archive, err := looksLikeZIP(reader, size, magic[:], "glb", "model/gltf-binary")
	if err != nil {
		return labelInspectionFailure("archive", err)
	}
	if archive {
		if err := inspectArchive(reader, size); err != nil {
			return labelInspectionFailure("archive", err)
		}
	}
	if err := inspectGLB(reader, size, nil, ""); err != nil {
		return labelInspectionFailure("glb", err)
	}
	return nil
}

func scanExternalAddresses(reader io.Reader) error {
	buffer := make([]byte, isolationChunkBytes)
	scanner := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
	for {
		count, err := reader.Read(buffer)
		if count > 0 && scanner.contains(buffer[:count]) {
			return errors.New("模型内容含外部地址")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type addressScanner struct {
	initial, continuation *regexp.Regexp
	previous              []byte
	seen                  int64
}

func (scanner *addressScanner) contains(data []byte) bool {
	window := make([]byte, 0, len(scanner.previous)+len(data))
	window = append(window, scanner.previous...)
	window = append(window, data...)
	pattern := scanner.initial
	// The overlap has already been checked with its preceding context. Its
	// truncated beginning is not a new URI boundary, including after decoding.
	if scanner.seen > int64(len(scanner.previous)) {
		pattern = scanner.continuation
	}
	scanner.seen += int64(len(data))
	scanner.previous = append(scanner.previous[:0], window[max(0, len(window)-isolationOverlap):]...)
	return containsAddress(window, pattern)
}

func rasterFormat(magic []byte, variant, mediaType string) string {
	variant, mediaType = strings.ToLower(variant), strings.ToLower(mediaType)
	if bytes.HasPrefix(magic, []byte("\x89PNG\r\n\x1a\n")) || mediaType == "image/png" || variant == "png" || strings.HasSuffix(variant, ".png") {
		return "png"
	}
	if bytes.HasPrefix(magic, []byte{0xff, 0xd8}) || mediaType == "image/jpeg" || variant == "jpg" || variant == "jpeg" || strings.HasSuffix(variant, ".jpg") || strings.HasSuffix(variant, ".jpeg") {
		return "jpeg"
	}
	return ""
}

func inspectRasterAddresses(reader io.Reader, size int64, format string) error {
	if size < 1 || size > maxRasterInspect {
		return rejectInspection("raster_inspection_size", errors.New("图片大小超出内容检查范围"))
	}
	data, err := readRasterBytes(reader, size)
	if err != nil {
		return err
	}
	work := newInspectionWorkBudgetForSize(size)
	plan, err := validateRasterBytesWithWork(data, format, work)
	if err != nil {
		return err
	}
	return inspectPlannedBytesWithWork(bytes.NewReader(data), plan, work)
}

func containsExternalAddress(data []byte) bool {
	return containsAddress(data, externalAddress)
}

func containsAddress(data []byte, pattern *regexp.Regexp) bool {
	// Every recognized URI needs a slash, either literal or introduced by
	// percent, JSON or entity decoding. UTF-16 ASCII projection cannot invent
	// these bytes. Inspect the complete caller-provided window, including its
	// carry, before allocating projections. The stream caller above advances
	// seen and retains carry even when this necessary syntax is absent.
	if bytes.IndexByte(data, '/') < 0 && bytes.IndexByte(data, '%') < 0 &&
		bytes.IndexByte(data, '\\') < 0 && bytes.IndexByte(data, '&') < 0 {
		return false
	}
	if bytes.Contains(data, []byte(addressPatternMarker(pattern))) && pattern.Match(data) {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 &&
		(containsEncodedAddress(decodeUTF16ASCII(data, true, pattern), pattern) ||
			containsEncodedAddress(decodeUTF16ASCII(data, false, pattern), pattern)) {
		return true
	}
	if bytes.IndexAny(data, `%\&`) < 0 {
		return false
	}
	return containsEncodedAddress(string(data), pattern)
}

func containsEncodedAddress(text string, pattern *regexp.Regexp) bool {
	for pass := 0; pass < 3; pass++ {
		if strings.Contains(text, addressPatternMarker(pattern)) && pattern.MatchString(text) {
			return true
		}
		decoded := html.UnescapeString(text)
		decoded = strings.ReplaceAll(decoded, `\/`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002f`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002F`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u003a`, `:`)
		decoded = strings.ReplaceAll(decoded, `\u003A`, `:`)
		decoded = escapedASCII.ReplaceAllStringFunc(decoded, func(token string) string {
			code, err := strconv.ParseUint(token[2:], 16, 16)
			if err != nil || code < 0x20 || code > 0x7e {
				return token
			}
			return string(rune(code))
		})
		decoded = percentByte.ReplaceAllStringFunc(decoded, func(token string) string {
			value, err := url.QueryUnescape(token)
			if err != nil {
				return token
			}
			return value
		})
		if decoded == text {
			return false
		}
		text = decoded
	}
	return strings.Contains(text, addressPatternMarker(pattern)) && pattern.MatchString(text)
}

// These literals are necessary, not sufficient, for the known address patterns.
// Apply the filter to each decoded representation, never instead of decoding.
// An unknown pattern uses the empty marker, which leaves its regex unrestricted.
func addressPatternMarker(pattern *regexp.Regexp) string {
	switch pattern {
	case absoluteAddress:
		return "://"
	case externalAddress, continuedExternalAddress:
		return "//"
	default:
		return ""
	}
}

func decodeUTF16ASCII(data []byte, littleEndian bool, pattern *regexp.Regexp) string {
	var out strings.Builder
	for start := 0; start < 2; start++ {
		out.Reset()
		for offset := start; offset+1 < len(data); offset += 2 {
			first, second := data[offset], data[offset+1]
			if !littleEndian {
				first, second = second, first
			}
			if second == 0 && first >= 0x20 && first <= 0x7e {
				out.WriteByte(first)
			} else {
				out.WriteByte(' ')
			}
		}
		if containsEncodedAddress(out.String(), pattern) {
			return out.String()
		}
	}
	return out.String()
}

func isGLTFJSON(role, variant, mediaType string, magic []byte) bool {
	if mediaType == "model/gltf+json" || strings.EqualFold(variant, "gltf") || strings.HasSuffix(strings.ToLower(variant), ".gltf") {
		return true
	}
	trimmed := bytes.TrimSpace(magic)
	return (role == "model" || role == "converted_model") && len(trimmed) > 0 && trimmed[0] == '{'
}

func inspectGLB(reader io.ReaderAt, size int64, archiveNames map[string]struct{}, base string) error {
	return inspectGLBStream(io.NewSectionReader(reader, 0, size), size, archiveNames, base)
}

func inspectGLTFJSON(reader io.Reader, size int64, archiveNames map[string]struct{}, base string) error {
	return inspectGLTFJSONBounded(reader, size, archiveNames, base, newInlineInspectionBudgetForSize(size))
}

func inspectGLTFJSONBounded(reader io.Reader, size int64, archiveNames map[string]struct{}, base string, budget *inlineInspectionBudget) error {
	if size <= 0 || size > maxModelJSONBytes {
		return errors.New("glTF JSON 超出限制")
	}
	data, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil || int64(len(data)) != size {
		return errors.New("glTF JSON 内容不完整")
	}
	if _, err := decodeGLTFDocumentBounded(data, archiveNames, base, budget); err != nil {
		return err
	}
	spans, err := inspectedGLTFJSONSpans(data)
	if err != nil {
		return err
	}
	return inspectPlannedBytes(bytes.NewReader(data), inspectionPlan{allowed: spans})
}

func checkGLTFURIsBounded(node any, archiveNames map[string]struct{}, base string, budget *inlineInspectionBudget, kind string) error {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if isInlineDataURI(key) {
				if err := inspectInlineResource(key, "", "", -1, budget); err != nil {
					return err
				}
			}
			if containsExternalAddress([]byte(key)) {
				return errors.New("glTF 字段名含外部地址")
			}
			if text, isText := child.(string); isText && gltfMetadataField(kind, key) && gltfMetadataAddressAllowed(text) {
				continue
			}
			if strings.EqualFold(key, "uri") {
				uri, ok := child.(string)
				if !ok {
					return errors.New("glTF 含未缓存的外部依赖")
				}
				if isInlineDataURI(uri) {
					declaredMIME := ""
					if rawMIME, present := value["mimeType"]; present {
						var valid bool
						declaredMIME, valid = rawMIME.(string)
						if !valid {
							return errors.New("glTF 图片媒体声明无效")
						}
					}
					declaredBytes := int64(-1)
					if kind == "buffer" {
						var err error
						declaredBytes, err = gltfInteger(value, "byteLength", -1)
						if err != nil || declaredBytes <= 0 {
							return errors.New("glTF 缓冲区长度无效")
						}
					}
					if err := inspectInlineResource(uri, kind, declaredMIME, declaredBytes, budget); err != nil {
						return err
					}
					continue
				}
				if !safeModelURI(uri, archiveNames, base) {
					return errors.New("glTF 含未缓存的外部依赖")
				}
			}
			childKind := ""
			if kind == "document" && key == "asset" {
				childKind = "asset"
			} else if kind == "document" && key == "images" {
				childKind = "image"
			} else if kind == "document" && key == "buffers" {
				childKind = "buffer"
			}
			if err := checkGLTFURIsBounded(child, archiveNames, base, budget, childKind); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range value {
			previousBuffer := budget.buffer
			if kind == "buffer" {
				budget.buffer = int64(index)
			}
			if err := checkGLTFURIsBounded(child, archiveNames, base, budget, kind); err != nil {
				return err
			}
			budget.buffer = previousBuffer
		}
	case string:
		if isInlineDataURI(value) {
			return inspectInlineResource(value, "", "", -1, budget)
		}
		if containsExternalAddress([]byte(value)) {
			return errors.New("glTF 含外部地址")
		}
	}
	return nil
}

func safeModelURI(uri string, archiveNames map[string]struct{}, base string) bool {
	if containsExternalAddress([]byte(uri)) || strings.HasPrefix(uri, "/") || strings.Contains(uri, "\\") {
		return false
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if archiveNames == nil || uri == "" {
		return false
	}
	name := path.Clean(path.Join(path.Dir(base), parsed.Path))
	if name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	_, found := archiveNames[name]
	return found
}

func inspectArchive(file io.ReaderAt, size int64) error {
	return inspectArchiveWithXMLPart(file, size, "")
}

// A 3MF content-type declaration may identify its model XML independently of
// the member suffix. Empty preserves the existing extension/magic contracts.
func inspectArchiveWithXMLPart(file io.ReaderAt, size int64, xmlPart string) error {
	if err := validateZIPFraming(file, size); err != nil {
		return fmt.Errorf("模型压缩包结构无效: %w", err)
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return fmt.Errorf("模型压缩包无效: %w", err)
	}
	if len(archive.File) > maxArchiveFiles {
		return errors.New("模型压缩包文件数量超出限制")
	}
	archive.RegisterDecompressor(zip.Deflate, inspectZIPDeflate)
	if containsExternalAddress([]byte(archive.Comment)) {
		return errors.New("模型压缩包注释含外部地址")
	}
	names := make(map[string]struct{}, len(archive.File))
	var total uint64
	for _, entry := range archive.File {
		if containsExternalAddress([]byte(entry.Comment)) || containsExternalAddress(entry.Extra) {
			return errors.New("模型压缩包元数据含外部地址")
		}
		name := path.Clean(entry.Name)
		if name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || containsExternalAddress([]byte(name)) {
			return errors.New("模型压缩包包含不安全路径")
		}
		if entry.UncompressedSize64 > maxArchiveBytes || total > maxArchiveBytes-entry.UncompressedSize64 {
			return errors.New("模型压缩包展开大小超出限制")
		}
		total += entry.UncompressedSize64
		if _, duplicate := names[name]; duplicate {
			return errors.New("模型压缩包包含重复的规范化成员名")
		}
		names[name] = struct{}{}
		if !isZIPDirectory(&entry.FileHeader) && strings.HasSuffix(strings.ToLower(entry.Name), ".zip") {
			return errors.New("模型压缩包不能嵌套未检查的压缩包")
		}
	}
	// Dependencies must resolve to a file. Keep every name in the framing and
	// duplicate checks above, including directory entries and their metadata.
	fileNames := make(map[string]struct{}, len(names))
	for _, entry := range archive.File {
		if !isZIPDirectory(&entry.FileHeader) {
			fileNames[path.Clean(entry.Name)] = struct{}{}
		}
	}
	buffers := newArchiveGLTFBuffers(archive.File, fileNames)
	// Members are inspected uncompressed, so scale with the larger size.
	budget := newInlineInspectionBudgetForSize(max(size, int64(total)))
	budget.onArchiveDocument = buffers.addModel
	work := &archiveInspectionWork{remaining: 3 * maxArchiveBytes}
	models := make(map[*zip.File]bool)
	for _, entry := range archive.File {
		if isZIPDirectory(&entry.FileHeader) {
			continue
		}
		if xmlPart != "" && path.Clean(entry.Name) == xmlPart {
			continue
		}
		checked, err := inspectArchiveModelMember(entry, fileNames, budget, work)
		if err != nil {
			return err
		}
		models[entry] = checked
	}
	if err := buffers.finalize(); err != nil {
		return err
	}
	for _, entry := range archive.File {
		if isZIPDirectory(&entry.FileHeader) {
			continue
		}
		layout := buffers.layouts[path.Clean(entry.Name)]
		if models[entry] && layout == nil {
			continue
		}
		if xmlPart != "" && path.Clean(entry.Name) == xmlPart {
			if err := inspectDeclaredXMLMember(entry, work); err != nil {
				return err
			}
			if layout == nil {
				continue
			}
		}
		if err := inspectArchiveBufferMember(entry, fileNames, budget, work, layout, models[entry]); err != nil {
			return err
		}
	}
	return nil
}

func isArchiveXML(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") ||
		strings.HasSuffix(name, ".model")
}

// 3MF and OPC use HTTP URIs as immutable XML vocabulary identifiers. Only
// namespace declarations and relationship Type may contain such identifiers;
// Target, metadata and ordinary text still obey the no-external-address rule.
func scanArchiveXML(reader io.Reader, size int64) error {
	if size > maxArchiveXMLBytes {
		return errors.New("模型 XML 超出检查上限")
	}
	decoder := xml.NewDecoder(io.LimitReader(reader, size+1))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("模型 XML 无效: %w", err)
		}
		switch node := token.(type) {
		case xml.StartElement:
			for _, attr := range node.Attr {
				value := strings.TrimSpace(attr.Value)
				if strings.EqualFold(attr.Name.Local, "TargetMode") && strings.EqualFold(value, "External") {
					return errors.New("模型 XML 引用外部资源")
				}
				isNamespace := attr.Name.Space == "xmlns" ||
					(attr.Name.Space == "" && attr.Name.Local == "xmlns")
				if (isNamespace || attr.Name.Local == "Type") && standardSpecificationURI(value) {
					continue
				}
				if containsExternalAddress([]byte(value)) {
					return errors.New("模型 XML 含外部地址")
				}
			}
		case xml.CharData:
			if containsExternalAddress(node) {
				return errors.New("模型 XML 含外部地址")
			}
		case xml.Comment:
			if containsExternalAddress(node) {
				return errors.New("模型 XML 注释含外部地址")
			}
		case xml.Directive:
			return errors.New("模型 XML 不允许 DTD 声明")
		case xml.ProcInst:
			if containsExternalAddress(node.Inst) {
				return errors.New("模型 XML 处理指令含外部地址")
			}
		}
	}
}

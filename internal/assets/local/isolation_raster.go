package local

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	maxRasterDecodeBytes = 512 << 20
	maxRasterTextBytes   = 16 << 20
	rasterDecodeOverhead = (4 << 20) + maxRasterTextBytes
	maxRasterSections    = maxArchiveFiles / 3 // Non-IDAT PNG chunks and JPEG sections.
	// Four units per descriptor visit cover the initial walk, strict payload
	// reader and decoder visits. CRC bytes are prepaid by rasterWalkWork.
	pngChunkWork int64 = 16
	// maxRasterPaddingBytes bounds the alignment padding tolerated after an
	// image's end marker (#330). See rasterPadding.
	maxRasterPaddingBytes = 64
)

// validateRasterBytes proves that data is a complete PNG or JPEG within the
// structure and work budgets, then admits the whole image as delivered. An
// image within the decode memory budget is also fully decoded; one over it
// (an 8K texture) is admitted after the framing/CRC walk alone, so its pixel
// data is only checked at display time. Metadata (XMP, EXIF, C2PA, text
// chunks) is cached unchanged and is not address-scanned; malformed,
// truncated or over-work-budget images are rejected.
func validateRasterBytes(data []byte, format string) (inspectionPlan, error) {
	return validateRasterBytesWithWork(data, format, newInspectionWorkBudget())
}

func validateRasterBytesWithWork(data []byte, format string, work *inspectionWorkBudget) (inspectionPlan, error) {
	if len(data) == 0 || len(data) > maxRasterInspect {
		return inspectionPlan{}, rejectInspection("raster_inspection_size", errors.New("图片大小超出内容检查范围"))
	}
	format = strings.ToLower(format)
	if format == "jpg" {
		format = "jpeg"
	}
	if format != "png" && format != "jpeg" {
		return inspectionPlan{}, rejectInspection("raster_unsupported_format", errors.New("图片格式没有可验证的数值字段"))
	}
	padding, err := rasterPadding(data, format)
	if err != nil {
		return inspectionPlan{}, err
	}
	if err := reserveRasterWalkWork(int64(len(data)), work); err != nil {
		return inspectionPlan{}, err
	}
	image := data[:len(data)-padding]
	switch format {
	case "png":
		_, err = validatePNGBytes(image, work)
	case "jpeg":
		_, err = validateJPEGBytes(image, work)
	}
	if err != nil {
		return inspectionPlan{}, err
	}
	// Upstream images are cached as delivered, metadata (XMP, EXIF, C2PA,
	// text chunks) included; the API and UI decide what to show. A complete
	// structure walk (plus a full decode when the image fits the decode
	// budget) proves the bytes are an image, and the page shows it from a
	// same-origin URL, so text inside it is never fetched (decided by Lex,
	// 2026-10-09; structure-only for large textures 2026-10-10). Tolerated
	// alignment padding is not part of the image: it stays outside the
	// allowed span, so the ordinary address scan still reads it.
	return inspectionPlan{allowed: []inspectionSpan{{0, int64(len(image))}}}, nil
}

var (
	pngEndChunk   = []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	jpegEndMarker = []byte{0xff, 0xd9}
)

// rasterPadding is the number of alignment padding bytes after the image's
// end marker (PNG IEND chunk, JPEG EOI) that the check tolerates (#330): at
// most maxRasterPaddingBytes bytes, each NUL or space. Exporters pad embedded
// images to their container's alignment (a glTF bufferView to 4 bytes, #189)
// and converters such as Meshy Convert copy that padding into FBX and USDZ
// textures; decoders stop at the end marker and ignore it.
//
// Any other byte after the end marker, or more padding than the bound, is
// refused as raster_trailing_data: arbitrary trailing bytes could carry the
// end record of another container (a ZIP end-of-central-directory whose
// directory hides in a PNG text chunk), making the file a polyglot. An image
// without any end marker is left to the structure check, which refuses it.
func rasterPadding(data []byte, format string) (int, error) {
	marker := pngEndChunk
	if format == "jpeg" {
		marker = jpegEndMarker
	}
	if bytes.HasSuffix(data, marker) {
		return 0, nil
	}
	padding := 0
	for padding <= maxRasterPaddingBytes && padding < len(data) && (data[len(data)-1-padding] == ' ' || data[len(data)-1-padding] == 0) {
		padding++
	}
	if padding > 0 && padding <= maxRasterPaddingBytes && bytes.HasSuffix(data[:len(data)-padding], marker) {
		return padding, nil
	}
	if bytes.Contains(data, marker) {
		return 0, rejectInspection("raster_trailing_data", errors.New("图片结束标记之后有多余数据"))
	}
	return 0, nil
}

// Check the decoder's allocation bound before Decode, independently of the
// archive byte limit. Go's PNG reader allocates up to 8 bytes per pixel; Adam7
// also allocates seven pass images whose total pixels equal the full image.
// Reserve 16 bytes per pixel for both allocations, including dead passes that
// the GC may not have reclaimed, and 24 bytes per row-width for row buffers.
// JPEG's maximum sampling factor is 4, so its MCU padding can reach 32 pixels.
// Four progressive coefficient grids cost at most 16 bytes per padded pixel
// (64 int32 per 8x8 block). Padded image planes and color conversion cost at
// most 8 more bytes; the strict entropy verifier's bitsets fit in one more.
// The fixed allowance also reserves the entire metadata expansion limit,
// although decoded metadata is streamed rather than held as a second image.
// These bounds match image/png/reader.go and image/jpeg/{scan,reader,dct}.go.
func checkRasterDecodeBudget(width, height, encodedBytes int, format string) error {
	if width <= 0 || height <= 0 || encodedBytes < 0 || encodedBytes > maxRasterInspect {
		return rejectInspection("raster_invalid_dimensions", errors.New("图片尺寸无效"))
	}
	w, h := int64(width), int64(height)
	bytesPerPixel := int64(16)
	if format == "jpeg" {
		// Check before addition, including on platforms with a narrower int.
		if w > maxRasterDecodeBytes || h > maxRasterDecodeBytes {
			return rejectInspection("raster_decode_budget", errors.New("图片解码内存超出限制"))
		}
		w, h = ((w+31)/32)*32, ((h+31)/32)*32
		bytesPerPixel = 25
	}
	remaining := int64(maxRasterDecodeBytes-rasterDecodeOverhead) - int64(encodedBytes)
	if w > remaining/24 {
		return rejectInspection("raster_decode_budget", errors.New("图片解码内存超出限制"))
	}
	remaining -= w * 24
	// Division guards both products. Malicious dimensions never reach Decode.
	if w > remaining/bytesPerPixel || h > (remaining/bytesPerPixel)/w {
		return rejectInspection("raster_decode_budget", errors.New("图片解码内存超出限制"))
	}
	return nil
}

// rasterFullDecode reports whether the standard decoder fits the decode
// memory budget. An image over it (an 8K texture, for example) is cached as
// delivered after the structure walk alone: no pixel stream is inflated and
// no image is allocated, so its dimensions cost no memory (Lex, 2026-10-10).
// Invalid dimensions are still an error.
func rasterFullDecode(width, height, encodedBytes int, format string) (bool, error) {
	err := checkRasterDecodeBudget(width, height, encodedBytes, format)
	var rejection *inspectionRejection
	if errors.As(err, &rejection) && rejection.reason == "raster_decode_budget" {
		return false, nil
	}
	return err == nil, err
}

type rasterPNGFrame struct {
	width, height    int
	depth, color     byte
	interlace        byte
	paletteEntries   int
	seenPalette      bool
	seenTransparency bool
	seenPixels       bool
	closedPixels     bool
	// structureOnly skips pixel inflation and decoding for an image over the
	// decode budget.
	structureOnly bool
}

func validatePNGBytes(data []byte, work *inspectionWorkBudget) (inspectionPlan, error) {
	if len(data) < 45 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 结构无效"))
	}
	var plan inspectionPlan
	var frame rasterPNGFrame
	var pixels inspectionSpan // Complete contiguous, validated IDAT chunk envelope.
	var pixelBytes int64
	pixelNumericIndex := -1
	textRemaining := int64(maxRasterTextBytes)
	textReserved := false
	seen := make(map[string]bool)
	paletteNames := make(map[string]bool)
	chunks, otherChunks := 0, 0
	for offset := 8; offset < len(data); {
		if work == nil {
			return inspectionPlan{}, inspectionWorkError()
		}
		if err := work.reserve(pngChunkWork); err != nil {
			return inspectionPlan{}, err
		}
		chunks++
		if len(data)-offset < 12 {
			return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 块数量或长度无效"))
		}
		length := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length > uint64(len(data)-offset-12) {
			return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 块长度无效"))
		}
		start, end := offset+8, offset+8+int(length)
		// Only bounded non-IDAT metadata needs a retained string key. The
		// common pixel path must not allocate a string for every fragment.
		kind := "IDAT"
		if !bytes.Equal(data[offset+4:offset+8], []byte("IDAT")) {
			kind = string(data[offset+4 : offset+8])
		}
		if !validPNGChunkType(data[offset+4 : offset+8]) {
			return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 块类型无效"))
		}
		if kind != "IDAT" {
			otherChunks++
			if otherChunks > maxRasterSections {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 非像素块数量超出限制"))
			}
		}
		if binary.BigEndian.Uint32(data[end:end+4]) != crc32.ChecksumIEEE(data[offset+4:end]) {
			if kind == "caBX" {
				return inspectionPlan{}, rejectInspection("png_metadata_checksum", errors.New("PNG 来源元数据校验失败"))
			}
			return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 块校验无效"))
		}
		if chunks == 1 && kind != "IHDR" {
			return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 缺少首个尺寸块"))
		}
		payload := data[start:end]
		numeric := false
		var fieldPlan inspectionPlan
		if frame.seenPixels && kind != "IDAT" && kind != "IEND" {
			frame.closedPixels = true
		}
		switch kind {
		case "IHDR":
			if chunks != 1 || len(payload) != 13 {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 尺寸块无效"))
			}
			frame.width = int(binary.BigEndian.Uint32(payload[:4]))
			frame.height = int(binary.BigEndian.Uint32(payload[4:8]))
			frame.depth, frame.color, frame.interlace = payload[8], payload[9], payload[12]
			if payload[10] != 0 || payload[11] != 0 || frame.interlace > 1 || !validPNGWorkDepth(frame.depth, frame.color) {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 像素格式无效"))
			}
			full, err := rasterFullDecode(frame.width, frame.height, len(data), "png")
			if err != nil {
				return inspectionPlan{}, err
			}
			frame.structureOnly = !full
			numeric = true // The complete IHDR profile was verified before any decode.
		case "PLTE":
			if frame.seenPalette || frame.seenPixels || frame.seenTransparency || len(payload) == 0 || len(payload)%3 != 0 || len(payload) > 768 ||
				(frame.color != 2 && frame.color != 3 && frame.color != 6) {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 调色板无效"))
			}
			frame.paletteEntries, frame.seenPalette = len(payload)/3, true
			if frame.color == 3 && frame.paletteEntries > 1<<frame.depth {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 调色板超出位深"))
			}
			numeric = true
		case "tRNS":
			if frame.seenTransparency || frame.seenPixels || !validPNGTransparency(payload, frame) {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 透明度块无效"))
			}
			frame.seenTransparency = true
			numeric = true
		case "IDAT":
			if frame.closedPixels || (frame.color == 3 && !frame.seenPalette) {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 像素块顺序无效"))
			}
			// Every complete chunk has passed its bounds, type, CRC and
			// ordering checks. Coalesce the contiguous numeric envelope so
			// descriptor memory does not depend on the encoder's IDAT split.
			if !frame.seenPixels {
				pixels.start = int64(offset)
				pixelNumericIndex = len(plan.numeric)
				plan.numeric = append(plan.numeric, inspectionSpan{pixels.start, int64(end + 4)})
			} else {
				plan.numeric[pixelNumericIndex].end = int64(end + 4)
			}
			pixels.end = int64(end + 4)
			pixelBytes += int64(len(payload))
			frame.seenPixels = true
		case "IEND":
			if !frame.seenPixels || len(payload) != 0 || end+4 != len(data) {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 结束块或尾部无效"))
			}
		case "caBX":
			// Content provenance (C2PA) is cached as delivered; the chunk CRC
			// was checked above.
		case "sPLT":
			var err error
			fieldPlan, err = inspectPNGSuggestedPalette(payload, frame, paletteNames)
			if err != nil {
				return inspectionPlan{}, err
			}
		case "tEXt", "zTXt", "iTXt", "iCCP":
			if !textReserved && pngTextIsCompressed(kind, payload) {
				// All compressed text shares this same expansion limit. Prepay
				// once before the first inflate, rather than charging an unused
				// maximum for every metadata-free texture.
				if err := work.reserve(maxRasterTextBytes); err != nil {
					return inspectionPlan{}, err
				}
				textReserved = true
			}
			var err error
			fieldPlan, err = inspectPNGTextChunk(kind, payload, &textRemaining)
			if err != nil {
				return inspectionPlan{}, err
			}
		default:
			var err error
			numeric, err = validPNGNumericChunk(kind, payload, frame)
			if err != nil || (kind[0]&0x20 == 0 && !numeric) {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 辅助块无效或含未知关键块"))
			}
			if numeric {
				if seen[kind] {
					return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 数值块重复"))
				}
				seen[kind] = true
			}
		}
		for _, span := range fieldPlan.numeric {
			plan.numeric = append(plan.numeric, inspectionSpan{int64(start) + span.start, int64(start) + span.end})
		}
		if kind != "IDAT" {
			// IDAT framing and data already belong to the validated envelope.
			// Other lengths/CRCs receive their own bounded numeric spans.
			plan.numeric = append(plan.numeric, inspectionSpan{int64(offset), int64(offset + 4)}, inspectionSpan{int64(end), int64(end + 4)})
			if numeric && end > start {
				plan.numeric = append(plan.numeric, inspectionSpan{int64(start), int64(end)})
			}
		}
		if len(plan.numeric) > maxArchiveFiles {
			return inspectionPlan{}, rejectInspection("png_metadata_limit", errors.New("PNG 数值字段数量超出限制"))
		}
		offset = end + 4
		if kind == "IEND" && frame.structureOnly {
			// Every chunk's framing and CRC was checked above.
			return plan, nil
		}
		if kind == "IEND" {
			// Reuse the frame from this single complete chunk/CRC walk.
			// Reserve the actual allocation and both expanded-row passes
			// before the strict IDAT check or the standard pixel decoder.
			cost, err := pngFrameWork(frame)
			if err != nil {
				return inspectionPlan{}, err
			}
			decode, err := reserveRasterDecodeWork(len(data), cost, work)
			if err != nil {
				return inspectionPlan{}, err
			}
			if !decode {
				// The model's other images used the shared budget; this
				// one is admitted after its structure walk, like an 8K one.
				return plan, nil
			}
			config, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil || config.Width != frame.width || config.Height != frame.height {
				return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 图片配置无效"))
			}
			if err := validatePNGPixelStream(data, pixels, pixelBytes, frame); err != nil {
				return inspectionPlan{}, err
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				return inspectionPlan{}, rejectInspection("png_invalid_pixels", errors.New("PNG 像素数据无效"))
			}
			return plan, nil
		}
	}
	return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 缺少结束块"))
}

func validPNGChunkType(kind []byte) bool {
	for _, value := range kind {
		if (value < 'A' || value > 'Z') && (value < 'a' || value > 'z') {
			return false
		}
	}
	return len(kind) == 4 && kind[2] >= 'A' && kind[2] <= 'Z'
}

func validPNGTransparency(payload []byte, frame rasterPNGFrame) bool {
	switch frame.color {
	case 0:
		return len(payload) == 2 && uint32(binary.BigEndian.Uint16(payload)) < uint32(1)<<frame.depth
	case 2:
		if len(payload) != 6 {
			return false
		}
		for offset := 0; offset < 6; offset += 2 {
			if uint32(binary.BigEndian.Uint16(payload[offset:])) >= uint32(1)<<frame.depth {
				return false
			}
		}
		return true
	case 3:
		return frame.seenPalette && len(payload) > 0 && len(payload) <= frame.paletteEntries
	}
	return false
}

func validPNGNumericChunk(kind string, payload []byte, frame rasterPNGFrame) (bool, error) {
	valid := true
	switch kind {
	case "gAMA":
		valid = !frame.seenPixels && !frame.seenPalette && len(payload) == 4 && binary.BigEndian.Uint32(payload) != 0
	case "cHRM":
		valid = !frame.seenPixels && !frame.seenPalette && len(payload) == 32
		if valid {
			for offset := 0; offset < 32; offset += 8 {
				x, y := uint64(binary.BigEndian.Uint32(payload[offset:])), uint64(binary.BigEndian.Uint32(payload[offset+4:]))
				valid = valid && x+y <= 100000
			}
		}
	case "sRGB":
		valid = !frame.seenPixels && !frame.seenPalette && len(payload) == 1 && payload[0] <= 3
	case "pHYs":
		valid = !frame.seenPixels && len(payload) == 9 && payload[8] <= 1
	case "sBIT":
		counts := map[byte]int{0: 1, 2: 3, 3: 3, 4: 2, 6: 4}
		valid = !frame.seenPixels && !frame.seenPalette && len(payload) == counts[frame.color]
		depth := frame.depth
		if frame.color == 3 {
			depth = 8
		}
		for _, value := range payload {
			valid = valid && value != 0 && value <= depth
		}
	case "hIST":
		valid = !frame.seenPixels && frame.seenPalette && len(payload) == 2*frame.paletteEntries
	case "bKGD":
		valid = !frame.seenPixels
		switch frame.color {
		case 0, 4:
			valid = valid && len(payload) == 2 && uint32(binary.BigEndian.Uint16(payload)) < uint32(1)<<frame.depth
		case 2, 6:
			valid = valid && len(payload) == 6
			if valid {
				for offset := 0; offset < 6; offset += 2 {
					valid = valid && uint32(binary.BigEndian.Uint16(payload[offset:])) < uint32(1)<<frame.depth
				}
			}
		case 3:
			valid = valid && frame.seenPalette && len(payload) == 1 && int(payload[0]) < frame.paletteEntries
		default:
			valid = false
		}
	case "tIME":
		valid = len(payload) == 7 && payload[2] >= 1 && payload[2] <= 12 && payload[3] >= 1 && payload[3] <= 31 &&
			payload[4] <= 23 && payload[5] <= 59 && payload[6] <= 60
	default:
		return false, nil
	}
	if !valid {
		return false, errors.New("invalid PNG numeric field")
	}
	return true, nil
}

func pngTextIsCompressed(kind string, payload []byte) bool {
	if kind == "zTXt" || kind == "iCCP" {
		return true
	}
	if kind != "iTXt" {
		return false
	}
	keywordEnd := bytes.IndexByte(payload, 0)
	return keywordEnd >= 0 && keywordEnd+2 < len(payload) && payload[keywordEnd+1] == 1
}

func inspectPNGTextChunk(kind string, payload []byte, remaining *int64) (inspectionPlan, error) {
	encodedLength := len(payload)
	keywordEnd := bytes.IndexByte(payload, 0)
	if keywordEnd < 0 || !validPNGKeyword(payload[:keywordEnd]) {
		return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 元数据关键字无效"))
	}
	payload = payload[keywordEnd+1:]
	switch kind {
	case "tEXt":
		return inspectionPlan{}, nil
	case "zTXt", "iCCP":
		if len(payload) < 2 || payload[0] != 0 {
			return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 元数据压缩方式无效"))
		}
		payload = payload[1:]
	case "iTXt":
		if len(payload) < 4 || payload[0] > 1 || payload[1] != 0 {
			return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 国际文本块无效"))
		}
		compressed := payload[0] == 1
		payload = payload[2:]
		languageEnd := bytes.IndexByte(payload, 0)
		if languageEnd < 0 || !validPNGLanguageTag(payload[:languageEnd]) {
			return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 国际文本语言无效"))
		}
		payload = payload[languageEnd+1:]
		translatedEnd := bytes.IndexByte(payload, 0)
		if translatedEnd < 0 || !utf8.Valid(payload[:translatedEnd]) {
			return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 国际文本译名无效"))
		}
		payload = payload[translatedEnd+1:]
		if !compressed {
			if !utf8.Valid(payload) {
				return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 国际文本内容无效"))
			}
			return inspectionPlan{}, nil
		}
	default:
		return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 文本块类型无效"))
	}
	if remaining == nil || *remaining < 0 || *remaining > maxRasterTextBytes {
		return inspectionPlan{}, rejectInspection("png_metadata_limit", errors.New("PNG 元数据展开预算无效"))
	}
	source := bytes.NewReader(payload)
	decoded, err := zlib.NewReader(source)
	if err != nil {
		return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 元数据压缩流无效"))
	}
	defer decoded.Close()
	var text io.Reader = decoded
	if kind == "iTXt" {
		text = &pngUTF8Reader{source: decoded}
	}
	limited := &io.LimitedReader{R: text, N: *remaining + 1}
	// Inflate the whole stream to prove it is valid and within the shared
	// expansion limit; the text itself is cached as delivered.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return inspectionPlan{}, rejectInspection("png_invalid_metadata", errors.New("PNG 元数据压缩流无效"))
	}
	consumed := *remaining + 1 - limited.N
	if consumed > *remaining || source.Len() != 0 {
		return inspectionPlan{}, rejectInspection("png_metadata_limit", errors.New("PNG 元数据展开大小或尾部无效"))
	}
	*remaining -= consumed
	// Only the fully consumed zlib representation is numeric. The caller
	// discards this plan today (the whole image is admitted as delivered);
	// it still documents which bytes the structure walk verified.
	return inspectionPlan{numeric: []inspectionSpan{{int64(encodedLength - len(payload)), int64(encodedLength)}}}, nil
}

// Go's PNG decoder accepts unused trailing IDAT chunks. Require all IDAT
// payload bytes to belong to the single checked zlib stream before admitting
// the image as delivered. The reader implements ReadByte to prevent zlib from
// buffering and hiding bytes after its checksum.
func validatePNGPixelStream(data []byte, chunks inspectionSpan, payloadBytes int64, frame rasterPNGFrame) error {
	reader := &rasterIDATReader{data: data, offset: chunks.start, end: chunks.end, bytesRemaining: payloadBytes}
	decoded, err := zlib.NewReader(reader)
	if err != nil {
		return rejectInspection("png_invalid_pixels", errors.New("PNG 像素压缩流无效"))
	}
	defer decoded.Close()
	channels := map[byte]int64{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[frame.color]
	if channels == 0 || frame.depth == 0 {
		return rejectInspection("png_invalid_pixels", errors.New("PNG 像素格式无效"))
	}
	rowBytes := func(width int64) int64 { return 1 + (width*channels*int64(frame.depth)+7)/8 }
	w, h := int64(frame.width), int64(frame.height)
	expected := rowBytes(w) * h
	if frame.interlace == 1 {
		expected = 0
		passes := [7][4]int64{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
		for _, pass := range passes {
			if w <= pass[0] || h <= pass[1] {
				continue
			}
			pw, ph := (w-pass[0]+pass[2]-1)/pass[2], (h-pass[1]+pass[3]-1)/pass[3]
			expected += rowBytes(pw) * ph
		}
	}
	limited := &io.LimitedReader{R: decoded, N: expected + 1}
	var buffer [isolationChunkBytes]byte
	count, err := io.CopyBuffer(io.Discard, limited, buffer[:])
	if err != nil || count != expected || reader.remaining() != 0 {
		return rejectInspection("png_invalid_pixels", errors.New("PNG 像素压缩流大小或尾部无效"))
	}
	return nil
}

// The first walk validated this complete IDAT envelope. Only payload bytes
// reach zlib; length/type/CRC framing is skipped without per-chunk descriptors.
// bytesRemaining includes payload in chunks zlib has not visited, including
// after empty chunks, so an early zlib EOF cannot hide trailing compressed data.
type rasterIDATReader struct {
	data           []byte
	offset, end    int64
	payload        []byte
	bytesRemaining int64
}

func (reader *rasterIDATReader) Read(body []byte) (int, error) {
	if len(body) == 0 {
		return 0, nil
	}
	for len(reader.payload) == 0 {
		if reader.offset == reader.end {
			return 0, io.EOF
		}
		if reader.offset < 0 || reader.end > int64(len(reader.data)) || reader.end-reader.offset < 12 {
			return 0, io.ErrUnexpectedEOF
		}
		header := reader.data[reader.offset : reader.offset+8]
		length := int64(binary.BigEndian.Uint32(header[:4]))
		if string(header[4:]) != "IDAT" || length > reader.end-reader.offset-12 {
			return 0, io.ErrUnexpectedEOF
		}
		start := reader.offset + 8
		reader.payload = reader.data[start : start+length]
		reader.offset += length + 12
	}
	count := copy(body, reader.payload)
	reader.payload = reader.payload[count:]
	reader.bytesRemaining -= int64(count)
	return count, nil
}

func (reader *rasterIDATReader) ReadByte() (byte, error) {
	var value [1]byte
	_, err := reader.Read(value[:])
	return value[0], err
}

func (reader *rasterIDATReader) remaining() int64 { return reader.bytesRemaining }

// Keep the concrete decoder entry point here so all raster formats share the
// same pre-decode allocation guard. JPEG's verified parser lives separately.
func decodeValidatedJPEG(data []byte, width, height int) error {
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != width || config.Height != height {
		return rejectInspection("jpeg_invalid_structure", errors.New("JPEG 图片配置无效"))
	}
	if err := checkRasterDecodeBudget(config.Width, config.Height, len(data), "jpeg"); err != nil {
		return err
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		return rejectInspection("jpeg_invalid_pixels", errors.New("JPEG 像素数据无效"))
	}
	return nil
}

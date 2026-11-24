package local

import (
	"bytes"
	"encoding/binary"
	"errors"

	"golang.org/x/image/webp"
)

// WebP delivery check (#576). Tripo's renders (rendered_image) arrive as WebP.
// Before this check a WebP fell through to the generic byte scan for network
// addresses, which reads compressed pixels and metadata as text and refused a
// real 210 KB render as content_rejected. A WebP is now proved the way PNG and
// JPEG are: a complete RIFF walk with no trailing bytes, then a full decode
// when the image fits the decode budget. Like the other rasters, metadata
// chunks (EXIF, XMP, ICC) are cached as delivered and not address-scanned: the
// page shows the picture from a same-origin URL, so text inside it is never
// fetched. Animation, unknown chunks and anything the standard decoder cannot
// read are refused.

var webpRIFF, webpForm = []byte("RIFF"), []byte("WEBP")

// isWebPHeader reports whether the first twelve bytes are a RIFF WEBP header.
func isWebPHeader(header []byte) bool {
	return len(header) >= 12 && bytes.Equal(header[:4], webpRIFF) && bytes.Equal(header[8:12], webpForm)
}

func webpInvalid(detail string) error {
	return rejectInspection("raster_invalid_structure", errors.New("WebP "+detail))
}

// validateWebPBytes walks every chunk of a WebP file. It accepts the simple
// formats (one VP8 or VP8L chunk) and the extended format (VP8X first, then
// ICCP, ALPH, one image chunk, EXIF and XMP in that order), decodes the image
// within the shared work and memory budgets, and admits the whole file.
func validateWebPBytes(data []byte, work *inspectionWorkBudget) (inspectionPlan, error) {
	if len(data) < 20 || !isWebPHeader(data) {
		return inspectionPlan{}, webpInvalid("文件头无效")
	}
	// The RIFF size covers everything after its own field. Bytes beyond it
	// could carry the end record of another container, so they are refused
	// as trailing data rather than tolerated (see rasterPadding).
	declared := int64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if declared > int64(len(data)) {
		return inspectionPlan{}, webpInvalid("内容不完整")
	}
	if declared < int64(len(data)) {
		return inspectionPlan{}, rejectInspection("raster_trailing_data", errors.New("图片结束标记之后有多余数据"))
	}
	// Extended-format chunk order (WebP container specification). The
	// simple formats are a single image chunk.
	order := map[string]int{"VP8X": 0, "ICCP": 1, "ALPH": 2, "VP8 ": 3, "VP8L": 3, "EXIF": 4, "XMP ": 5}
	offset, chunks, last := 12, 0, -1
	extended, image, alpha := false, "", false
	for offset < len(data) {
		if len(data)-offset < 8 {
			return inspectionPlan{}, webpInvalid("块头不完整")
		}
		chunks++
		if chunks > maxRasterSections {
			return inspectionPlan{}, webpInvalid("块数量超出限制")
		}
		kind := string(data[offset : offset+4])
		size := int64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		end := int64(offset) + 8 + size
		padded := end + size%2
		if padded > int64(len(data)) {
			return inspectionPlan{}, webpInvalid("块长度无效")
		}
		if size%2 == 1 && data[end] != 0 {
			return inspectionPlan{}, webpInvalid("块填充无效")
		}
		position, known := order[kind]
		switch {
		case kind == "ANIM" || kind == "ANMF":
			return inspectionPlan{}, rejectInspection("raster_unsupported_format", errors.New("不支持动画 WebP"))
		case !known:
			return inspectionPlan{}, webpInvalid("含未知块")
		case chunks == 1:
			extended = kind == "VP8X"
			if !extended && position != 3 {
				return inspectionPlan{}, webpInvalid("首块无效")
			}
			if extended && size != 10 {
				return inspectionPlan{}, webpInvalid("扩展头无效")
			}
			if extended && data[offset+8]&0x02 != 0 {
				// The VP8X animation flag: frames live in ANMF chunks.
				return inspectionPlan{}, rejectInspection("raster_unsupported_format", errors.New("不支持动画 WebP"))
			}
		case !extended:
			return inspectionPlan{}, webpInvalid("简单格式只能有一个图像块")
		case position <= last:
			return inspectionPlan{}, webpInvalid("块顺序无效或重复")
		}
		last = position
		switch kind {
		case "VP8 ", "VP8L":
			image = kind
		case "ALPH":
			alpha = true
		}
		offset = int(padded)
	}
	if image == "" || (alpha && image != "VP8 ") {
		return inspectionPlan{}, webpInvalid("缺少图像块或透明度块无效")
	}
	config, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return inspectionPlan{}, webpInvalid("图片配置无效")
	}
	plan := inspectionPlan{allowed: []inspectionSpan{{0, int64(len(data))}}}
	full, err := rasterFullDecode(config.Width, config.Height, len(data), "webp")
	if err != nil {
		return inspectionPlan{}, err
	}
	if !full {
		// Over the decode memory budget (an 8K texture): admitted after the
		// chunk walk alone, as PNG and JPEG are.
		return plan, nil
	}
	// The decoder keeps at most four bytes per pixel for the image plus its
	// working rows; reserve that with both decoder passes.
	decode, err := reserveRasterDecodeWork(len(data), 4*int64(config.Width)*int64(config.Height), work)
	if err != nil {
		return inspectionPlan{}, err
	}
	if !decode {
		return plan, nil
	}
	decoded, err := webp.Decode(bytes.NewReader(data))
	if err != nil || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return inspectionPlan{}, webpInvalid("像素数据无效")
	}
	return plan, nil
}

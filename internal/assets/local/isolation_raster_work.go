package local

import (
	"bytes"
	"errors"
	"io"
)

func inspectRasterAddressesWithWork(reader io.Reader, size int64, format string, work *inspectionWorkBudget) error {
	if size < 1 || size > maxRasterInspect {
		return rejectInspection("raster_inspection_size", errors.New("图片大小超出内容检查范围"))
	}
	minimum, err := rasterWalkWork(size)
	if err != nil || work == nil || work.available() < minimum {
		return inspectionWorkError()
	}
	data, err := readRasterBytes(reader, size)
	if err != nil {
		return err
	}
	plan, err := validateRasterBytesWithWork(data, format, work)
	if err != nil {
		return err
	}
	return inspectPlannedBytesWithWork(bytes.NewReader(data), plan, work)
}

// readRasterBytes reads exactly size bytes and proves nothing follows. The
// buffer is allocated once at the declared size (at most maxRasterInspect),
// so a 256 MiB texture does not also pay io.ReadAll's growth copies, which
// can hold about twice the image in memory while the last copy runs.
func readRasterBytes(reader io.Reader, size int64) ([]byte, error) {
	if size < 1 || size > maxRasterInspect {
		return nil, rejectInspection("raster_inspection_size", errors.New("图片大小超出内容检查范围"))
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, rejectInspection("raster_invalid_structure", errors.New("图片内容不完整"))
		}
		return nil, err
	}
	var extra [1]byte
	count, err := io.ReadFull(reader, extra[:])
	if count != 0 {
		return nil, rejectInspection("raster_invalid_structure", errors.New("图片内容不完整"))
	}
	if err != nil && err != io.EOF {
		return nil, err // Includes a ZIP CRC failure reported only at EOF.
	}
	return data, nil
}

// rasterWorkCodecBytes covers the bounded codec buffers. rasterEnvelopeWork is
// the complete cost of an image that fits the decode budget: the framing/CRC
// walk plus the two decoder passes (DecodeConfig and the standard library's
// full Decode), which validatePNGBytes/validateJPEGBytes reserve only once the
// walk has shown the image fits. Pixel expansion and metadata work are
// reserved separately. The independent worst-case image memory guard remains
// unchanged.
//
// The fixed part is a per-image allowance, so it bounds how many images one
// file may carry (budget / allowance, about 8000 under the 512 MiB floor). It
// stands for decoder setup, which measures in microseconds per image (a 4x4
// PNG validates in about 15 us, a small JPEG in about 5 us), a small multiple
// of the 64 KiB framing/CRC walk. The earlier 4 MiB allowance capped a file at
// about 128 images and rejected a real Tripo Smart Segmentation GLB with 127
// parts and 381 embedded textures (4.4 MB in total, #94). Per-byte costs,
// decode reservations and the linear file-size budget are unchanged.
const rasterWorkCodecBytes = 64 << 10

func rasterEnvelopeWork(size int64) (int64, error) {
	walk, err := rasterWalkWork(size)
	if err != nil {
		return 0, err
	}
	return walk + 2*size, nil
}

// rasterWalkWork is the framing/CRC walk every image gets. The two decoder
// passes are reserved only when the image fits the decode budget, so an
// image cached after the structure walk alone costs one pass.
func rasterWalkWork(size int64) (int64, error) {
	if size <= 0 || size > maxRasterInspect {
		return 0, errors.New("图片检查字节大小无效")
	}
	return rasterWorkCodecBytes + size, nil
}

func reserveRasterWalkWork(size int64, work *inspectionWorkBudget) error {
	if work == nil {
		return inspectionWorkError()
	}
	cost, err := rasterWalkWork(size)
	if err != nil {
		return err
	}
	return work.reserve(cost)
}

// reserveRasterDecodeWork reserves both decoder passes and the pixel work
// together. It reports false and reserves nothing when the shared budget
// cannot cover them, for example a model's fourth 4K texture; the caller then
// caches the image after its structure walk, as for an image over the decode
// memory budget (decided by Lex, 2026-10-10). The reservation is marked
// reclaimable so a later mandatory structural check can yield this optional
// decode instead of rejecting the whole asset.
func reserveRasterDecodeWork(size int, pixelWork int64, work *inspectionWorkBudget) (bool, error) {
	if work == nil || pixelWork < 0 {
		return false, inspectionWorkError()
	}
	cost := 2*int64(size) + pixelWork
	if cost > work.remaining {
		return false, nil
	}
	work.remaining -= cost
	work.reclaimable += cost
	return true, nil
}

func validPNGWorkDepth(depth, color byte) bool {
	if color == 0 || color == 3 {
		return depth == 1 || depth == 2 || depth == 4 || depth == 8 || color == 0 && depth == 16
	}
	return (color == 2 || color == 4 || color == 6) && (depth == 8 || depth == 16)
}

func pngFrameWork(frame rasterPNGFrame) (int64, error) {
	w, h := int64(frame.width), int64(frame.height)
	channels := map[byte]int64{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[frame.color]
	output := int64(4)
	if frame.depth == 16 {
		output = 8
	}
	if frame.color == 3 {
		output = 1
	}
	if frame.color == 0 && !frame.seenTransparency {
		output = 1
		if frame.depth == 16 {
			output = 2
		}
	}
	var cost int64
	if err := addInspectionWork(&cost, w*h, output); err != nil {
		return 0, err
	}
	passes := [][4]int64{{0, 0, 1, 1}}
	if frame.interlace == 1 {
		passes = [][4]int64{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
	}
	for _, pass := range passes {
		if w <= pass[0] || h <= pass[1] {
			continue
		}
		pw, ph := (w-pass[0]+pass[2]-1)/pass[2], (h-pass[1]+pass[3]-1)/pass[3]
		row := 1 + (pw*channels*int64(frame.depth)+7)/8
		// The exact-IDAT validator inflates once; the standard decoder reads
		// and filters those rows once. Charge both complete decoded passes.
		if err := addInspectionWork(&cost, row*ph, 2); err != nil {
			return 0, err
		}
		if err := addInspectionWork(&cost, row, 2); err != nil {
			return 0, err
		}
		if frame.interlace == 1 {
			// Go allocates a pass image then merges it into the full image.
			if err := addInspectionWork(&cost, pw*ph, 2*output); err != nil {
				return 0, err
			}
		}
	}
	return cost, nil
}

type jpegRasterProfile struct {
	frame       rasterJPEGFrame
	jfif, adobe bool
	transform   byte
	blockVisits int64
}

func (profile *jpegRasterProfile) addScan(payload []byte) error {
	frame := &profile.frame
	if len(payload) < 1 {
		return rasterProfileError("jpeg")
	}
	n := int(payload[0])
	if n == 0 || n > frame.count || len(payload) != 1+2*n+3 {
		return rasterProfileError("jpeg")
	}
	var seen [4]bool
	mxx, myy := int64((frame.width+8*frame.maxH-1)/(8*frame.maxH)), int64((frame.height+8*frame.maxV-1)/(8*frame.maxV))
	for i := 0; i < n; i++ {
		component := -1
		for j := 0; j < frame.count; j++ {
			if frame.components[j].id == payload[1+2*i] {
				component = j
				break
			}
		}
		if component < 0 || seen[component] {
			return rasterProfileError("jpeg")
		}
		seen[component] = true
		entry := frame.components[component]
		if frame.structureOnly {
			continue // No entropy checker runs on this image.
		}
		// Both entropy checkers visit this same bounded block envelope.
		if err := addInspectionWork(&profile.blockVisits, mxx*myy, int64(entry.h*entry.v)*8); err != nil {
			return err
		}
	}
	return nil
}

func (profile *jpegRasterProfile) work() (int64, error) {
	frame := profile.frame
	w, h := int64(frame.width), int64(frame.height)
	first := frame.components[0]
	if first.h != frame.maxH || first.v != frame.maxV {
		return 0, rasterProfileError("jpeg")
	}
	mx, my := (w+int64(8*first.h)-1)/int64(8*first.h), (h+int64(8*first.v)-1)/int64(8*first.v)
	pw, ph := mx*int64(8*first.h), my*int64(8*first.v)
	cost := profile.blockVisits
	if err := addInspectionWork(&cost, pw*ph, 1); err != nil {
		return 0, err
	}
	if frame.count > 1 {
		cb, cr := frame.components[1], frame.components[2]
		if cb.h != cr.h || cb.v != cr.v {
			return 0, rasterProfileError("jpeg")
		}
		hr, vr := first.h/cb.h, first.v/cb.v
		if !(hr == 1 && (vr == 1 || vr == 2) || (hr == 2 || hr == 4) && (vr == 1 || vr == 2)) {
			return 0, rasterProfileError("jpeg")
		}
		if err := addInspectionWork(&cost, (pw/int64(hr))*(ph/int64(vr)), 2); err != nil {
			return 0, err
		}
		if frame.count == 4 {
			if !profile.adobe {
				return 0, rasterProfileError("jpeg")
			}
			black := frame.components[3]
			if err := addInspectionWork(&cost, mx*my, int64(64*black.h*black.v)); err != nil {
				return 0, err
			}
			if err := addInspectionWork(&cost, w*h, 4); err != nil {
				return 0, err
			}
		} else if !profile.jfif && (profile.adobe && profile.transform == 0 || first.id == 'R' && cb.id == 'G' && cr.id == 'B') {
			if err := addInspectionWork(&cost, w*h, 4); err != nil {
				return 0, err
			}
		}
	}
	if frame.progressive {
		for _, component := range frame.components[:frame.count] {
			// 64 int32 coefficients in Go's decoder plus one uint64 in the
			// strict entropy checker. Baseline JPEG has neither whole image.
			if err := addInspectionWork(&cost, mx*my, int64(component.h*component.v)*(256+8)); err != nil {
				return 0, err
			}
		}
	}
	return cost, nil
}

func rasterProfileError(format string) error {
	return rejectInspection(format+"_invalid_structure", errors.New("图片解码预算字节结构无效"))
}

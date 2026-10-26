package local

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestRasterWorkUsesVerifiedPNGShapes(t *testing.T) {
	for _, item := range []struct {
		name                    string
		depth, color, interlace byte
		transparent             bool
	}{
		{"gray1", 1, 0, 0, false}, {"gray8", 8, 0, 0, false}, {"gray8_trns", 8, 0, 0, true},
		{"gray16", 16, 0, 0, false}, {"gray16_trns", 16, 0, 0, true},
		{"rgb8", 8, 2, 0, false}, {"rgb8_trns", 8, 2, 0, true}, {"rgb16", 16, 2, 0, false},
		{"palette1", 1, 3, 0, false}, {"palette8_trns", 8, 3, 0, true},
		{"grayalpha8", 8, 4, 0, false}, {"grayalpha16", 16, 4, 0, false},
		{"rgba8", 8, 6, 0, false}, {"rgba16", 16, 6, 0, false}, {"adam7", 8, 6, 1, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			const w, h = 17, 13
			channels := map[byte]int{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[item.color]
			passes := [][4]int{{0, 0, 1, 1}}
			if item.interlace == 1 {
				passes = [][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
			}
			var rows []byte
			for _, pass := range passes {
				if w <= pass[0] || h <= pass[1] {
					continue
				}
				pw := (w - pass[0] + pass[2] - 1) / pass[2]
				for y := pass[1]; y < h; y += pass[3] {
					rows = append(rows, make([]byte, 1+(pw*channels*int(item.depth)+7)/8)...)
				}
			}
			body := rasterRawPNG(t, w, h, item.depth, item.color, item.interlace, rows)
			var prefix []byte
			if item.color == 3 {
				prefix = append(prefix, rasterPNGChunk("PLTE", []byte{0, 0, 0, 255, 255, 255})...)
			}
			if item.transparent {
				payload := []byte{0, 0}
				if item.color == 2 {
					payload = make([]byte, 6)
				}
				if item.color == 3 {
					payload = []byte{0, 255}
				}
				prefix = append(prefix, rasterPNGChunk("tRNS", payload)...)
			}
			body = append(append(append([]byte(nil), body[:33]...), prefix...), body[33:]...)
			decoded, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatal("valid standard fixture rejected", err)
			}
			work := newInspectionWorkBudget()
			if _, err := validateRasterBytesWithWork(body, "png", work); err != nil {
				t.Fatal(err)
			}
			allocated := int64(rasterWorkTestPixelBytes(decoded))
			envelope, err := rasterEnvelopeWork(int64(len(body)))
			if err != nil {
				t.Fatal(err)
			}
			if maxInspectionWork-work.remaining < envelope+allocated+2*int64(len(rows)) {
				t.Fatal("actual allocation and both expanded row passes were not charged")
			}
			if item.interlace == 1 && maxInspectionWork-work.remaining < envelope+3*allocated+2*int64(len(rows)) {
				t.Fatal("Adam7 pass allocation/merge costs were omitted")
			}
		})
	}
}

func rasterWorkTestPixelBytes(img image.Image) int {
	switch value := img.(type) {
	case *image.Gray:
		return len(value.Pix)
	case *image.Gray16:
		return len(value.Pix)
	case *image.Paletted:
		return len(value.Pix)
	case *image.RGBA:
		return len(value.Pix)
	case *image.NRGBA:
		return len(value.Pix)
	case *image.RGBA64:
		return len(value.Pix)
	case *image.NRGBA64:
		return len(value.Pix)
	case *image.YCbCr:
		return len(value.Y) + len(value.Cb) + len(value.Cr)
	case *image.CMYK:
		return len(value.Pix)
	}
	panic("unexpected decoder image type")
}

func TestRasterWorkUsesJPEGSamplingProgressiveAndLateColorTransform(t *testing.T) {
	for _, sampling := range []byte{0x11, 0x12, 0x21, 0x22, 0x41, 0x42} {
		t.Run(fmt.Sprintf("sampling_%x", sampling), func(t *testing.T) {
			body := rasterWorkTestJPEG(sampling, 3, -1, false)
			decoded, err := jpeg.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatal("independent JPEG decoder rejected fixture", err)
			}
			work := newInspectionWorkBudget()
			if _, err := validateRasterBytesWithWork(body, "jpeg", work); err != nil {
				t.Fatal(err)
			}
			envelope, err := rasterEnvelopeWork(int64(len(body)))
			if err != nil {
				t.Fatal(err)
			}
			if maxInspectionWork-work.remaining < envelope+int64(rasterWorkTestPixelBytes(decoded)) {
				t.Fatal("actual subsampled planes were not charged")
			}
		})
	}
	for _, item := range []struct {
		name string
		body []byte
	}{
		{"gray", rasterWorkTestJPEG(0x11, 1, -1, false)},
		{"rgb_late_app14", rasterWorkTestJPEG(0x11, 3, 0, false)},
		{"jfif_overrides_rgb", rasterWorkTestJPEG(0x11, 3, 0, true)},
		{"cmyk", rasterWorkTestJPEG(0x11, 4, 0, false)},
		{"ycck", rasterWorkTestJPEG(0x11, 4, 2, false)},
		{"progressive", rasterProgressiveJPEG(8, 8)},
	} {
		t.Run(item.name, func(t *testing.T) {
			decoded, err := jpeg.Decode(bytes.NewReader(item.body))
			if err != nil {
				t.Fatal(err)
			}
			work := newInspectionWorkBudget()
			if _, err := validateRasterBytesWithWork(item.body, "jpeg", work); err != nil {
				t.Fatal(err)
			}
			envelope, err := rasterEnvelopeWork(int64(len(item.body)))
			if err != nil {
				t.Fatal(err)
			}
			cost := maxInspectionWork - work.remaining - envelope
			if cost < int64(rasterWorkTestPixelBytes(decoded)) {
				t.Fatal("decoded output allocation omitted")
			}
			if item.name == "rgb_late_app14" && cost < 7*8*8 {
				t.Fatal("late color transform allocation omitted")
			}
			if item.name == "progressive" && cost < 8*8+256+8 {
				t.Fatal("progressive coefficient/bitmap allocation omitted")
			}
		})
	}
}

// Independent tiny baseline wire writer: all DCT coefficients are zero.
// Each block uses DC=0 and AC=EOB, both one-bit codes, plus standard pad bits.
func rasterWorkTestJPEG(sampling byte, count, transform int, jfif bool) []byte {
	h, v := int(sampling>>4), int(sampling&15)
	if count == 1 {
		h, v = 1, 1
	}
	out := []byte{0xff, 0xd8}
	out = append(out, rasterJPEGTestSegment(0xdb, append([]byte{0}, bytes.Repeat([]byte{1}, 64)...))...)
	frame := []byte{8, byte(8 * v >> 8), byte(8 * v), byte(8 * h >> 8), byte(8 * h), byte(count)}
	for i := 0; i < count; i++ {
		hv := byte(0x11)
		if i == 0 && count > 1 {
			hv = sampling
		}
		frame = append(frame, byte(i+1), hv, 0)
	}
	out = append(out, rasterJPEGTestSegment(0xc0, frame)...)
	huff := append([]byte{0, 1}, make([]byte, 15)...)
	huff = append(huff, 0)
	huff = append(huff, 0x10, 1)
	huff = append(huff, make([]byte, 15)...)
	huff = append(huff, 0)
	out = append(out, rasterJPEGTestSegment(0xc4, huff)...)
	scan := []byte{byte(count)}
	for i := 0; i < count; i++ {
		scan = append(scan, byte(i+1), 0)
	}
	scan = append(scan, 0, 63, 0)
	out = append(out, rasterJPEGTestSegment(0xda, scan)...)
	blocks := 1
	if count > 1 {
		blocks = h*v + count - 1
	}
	for bits := 2 * blocks; bits > 0; {
		left := min(8, bits)
		value := byte(0)
		if left < 8 {
			value = byte((1 << (8 - left)) - 1)
		}
		out = append(out, value)
		if value == 0xff {
			out = append(out, 0)
		}
		bits -= left
	}
	if transform >= 0 {
		app := append([]byte("Adobe"), make([]byte, 7)...)
		app[11] = byte(transform)
		out = append(out, rasterJPEGTestSegment(0xee, app)...)
	}
	if jfif {
		out = append(out, rasterJPEGTestSegment(0xe0, append([]byte("JFIF\x00"), make([]byte, 9)...))...)
	}
	return append(out, 0xff, 0xd9)
}

func TestRasterWorkRejectsMalformedProfilesAndKeepsSharedBudget(t *testing.T) {
	body := inlineTestPNG(t, "")
	work := newInspectionWorkBudget()
	if _, err := validateRasterBytesWithWork(body, "png", work); err != nil {
		t.Fatal(err)
	}
	used := maxInspectionWork - work.remaining
	walk, err := rasterWalkWork(int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	// The second image's decode does not fit: it is cached after its
	// structure walk and reserves no decode work.
	shared := &inspectionWorkBudget{remaining: 2*used - 1}
	if _, err := validateRasterBytesWithWork(body, "png", shared); err != nil {
		t.Fatal(err)
	}
	before := shared.remaining
	if _, err := validateRasterBytesWithWork(body, "png", shared); err != nil {
		t.Fatalf("image over the remaining decode work rejected: %v", err)
	}
	if before-shared.remaining >= used {
		t.Fatal("structure-only fallback reserved decode work")
	}
	// The next image's structure walk does not fit beside the first image's
	// optional decode; it reclaims that decode instead of rejecting.
	shared = &inspectionWorkBudget{remaining: used + walk - 1}
	if _, err := validateRasterBytesWithWork(body, "png", shared); err != nil {
		t.Fatal(err)
	}
	decode := shared.reclaimable
	if decode <= 0 {
		t.Fatal("first image must have run its optional decode")
	}
	if _, err := validateRasterBytesWithWork(body, "png", shared); err != nil {
		t.Fatalf("structure walk must reclaim optional decode work: %v", err)
	}
	if shared.reclaimable >= decode {
		t.Fatal("reclaimed work must leave the optional pool")
	}
	// No room for the walk itself and no optional work to reclaim still rejects.
	_, err = validateRasterBytesWithWork(body, "png", &inspectionWorkBudget{remaining: walk - 1})
	fbxTestRequireWorkRejection(t, err)
	if _, err := validateRasterBytesWithWork(body, "png", nil); err == nil {
		t.Fatal("missing shared work budget accepted")
	}
	badCRC := bytes.Clone(body)
	badCRC[29] ^= 1
	badDepth := bytes.Clone(body)
	badDepth[24] = 7
	binary.BigEndian.PutUint32(badDepth[29:33], crc32.ChecksumIEEE(badDepth[12:29]))
	// A non-padding byte after IEND is refused; NUL/space padding is tolerated (#330).
	for _, bad := range [][]byte{body[:33], append(bytes.Clone(body), 'x'), badCRC, badDepth} {
		if _, err := validateRasterBytesWithWork(bad, "png", newInspectionWorkBudget()); err == nil {
			t.Fatal("invalid PNG profile accepted")
		}
	}
	jpegBody := rasterWorkTestJPEG(0x22, 3, -1, false)
	for _, bad := range [][]byte{jpegBody[:len(jpegBody)-2], append(bytes.Clone(jpegBody), 'x'), rasterWorkTestJPEG(0x11, 4, -1, false)} {
		if _, err := validateRasterBytesWithWork(bad, "jpeg", newInspectionWorkBudget()); err == nil {
			t.Fatal("invalid/incomplete JPEG profile accepted")
		}
	}
	// Metadata text is cached as delivered; only structure and budgets decide.
	if _, err := validateRasterBytesWithWork(inlineTestPNG(t, "//private.example/path"), "png", newInspectionWorkBudget()); err != nil {
		t.Fatalf("valid PNG with address-like metadata rejected: %v", err)
	}
}

func TestRasterWorkSharedInlineAndGLBConsumers(t *testing.T) {
	body := inlineTestPNG(t, "")
	work := newInspectionWorkBudget()
	if _, err := validateRasterBytesWithWork(body, "png", work); err != nil {
		t.Fatal(err)
	}
	used := maxInspectionWork - work.remaining
	walk, err := rasterWalkWork(int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	// A second image without room for its walk reclaims the first image's
	// optional decode; only a walk that cannot fit even then is rejected.
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(body)
	t.Run("inline", func(t *testing.T) {
		budget := newInlineInspectionBudget()
		budget.work.remaining = used + walk - 1
		if err := inspectInlineResource(uri, "image", "image/png", -1, budget); err != nil {
			t.Fatal(err)
		}
		if budget.work.reclaimable <= 0 {
			t.Fatal("first inline image must have run its optional decode")
		}
		if err := inspectInlineResource(uri, "image", "image/png", -1, budget); err != nil {
			t.Fatalf("inline walk must reclaim optional decode work: %v", err)
		}
		empty := newInlineInspectionBudget()
		empty.work.remaining = walk - 1
		fbxTestRequireWorkRejection(t, inspectInlineResource(uri, "image", "image/png", -1, empty))
	})
	t.Run("glb_images", func(t *testing.T) {
		shared := &inspectionWorkBudget{remaining: used + walk - 1}
		ranges := []gltfImageRange{{start: 0, end: int64(len(body)), format: "png"}}
		if err := inspectGLBBIN(bytes.NewReader(body), int64(len(body)), ranges, nil, newPlannedInspectorWithWork(shared)); err != nil {
			t.Fatal(err)
		}
		if shared.reclaimable <= 0 {
			t.Fatal("first GLB image must have run its optional decode")
		}
		if err := inspectGLBBIN(bytes.NewReader(body), int64(len(body)), ranges, nil, newPlannedInspectorWithWork(shared)); err != nil {
			t.Fatalf("GLB image walk must reclaim optional decode work: %v", err)
		}
		fbxTestRequireWorkRejection(t, inspectGLBBIN(bytes.NewReader(body), int64(len(body)), ranges, nil, newPlannedInspectorWithWork(&inspectionWorkBudget{remaining: walk - 1})))
	})
}

func TestRasterWorkMetadataReservationsPrecedeParsing(t *testing.T) {
	for _, kind := range []string{"tEXt", "zTXt", "iTXt", "iCCP", "caBX"} {
		t.Run(kind, func(t *testing.T) {
			text := []byte("local caption")
			if kind == "caBX" {
				text = rasterC2PAIdentifiers("https://www.microsoft.com/pkiops/docs/repository.htm")
			}
			body := rasterPNGText(t, kind, text)
			work := newInspectionWorkBudget()
			if _, err := validateRasterBytesWithWork(body, "png", work); err != nil {
				t.Fatal(err)
			}
			envelope, err := rasterEnvelopeWork(int64(len(body)))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "tEXt" || kind == "caBX" {
				// Uncompressed text and opaque provenance manifests are cached
				// as delivered and are never parsed or inflated.
				if maxInspectionWork-work.remaining >= envelope+maxRasterTextBytes {
					t.Fatalf("%s must not reserve an unused expansion workspace", kind)
				}
			} else {
				walk, err := rasterWalkWork(int64(len(body)))
				if err != nil {
					t.Fatal(err)
				}
				limited := &inspectionWorkBudget{remaining: walk + maxRasterTextBytes - 1}
				fbxTestRequireWorkRejection(t, func() error {
					_, err := validateRasterBytesWithWork(body, "png", limited)
					return err
				}())
			}
		})
	}
	// All compressed text chunks of one image share one expansion limit and
	// therefore one prepaid reservation; opaque caBX manifests reserve none.
	text := rasterPNGChunk("zTXt", append([]byte("Comment\x00\x00"), rasterZlib(t, []byte("local caption"))...))
	manifest := rasterPNGChunk("caBX", rasterC2PAIdentifiers("https://www.microsoft.com/pkiops/docs/repository.htm"))
	body := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255}, text, text, manifest, manifest)
	full := newInspectionWorkBudget()
	if _, err := validateRasterBytesWithWork(body, "png", full); err != nil {
		t.Fatal(err)
	}
	used := maxInspectionWork - full.remaining
	single := newInspectionWorkBudget()
	if _, err := validateRasterBytesWithWork(rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255}, text), "png", single); err != nil {
		t.Fatal(err)
	}
	oneText := maxInspectionWork - single.remaining
	if used >= oneText+maxRasterTextBytes {
		t.Fatalf("repeated text chunks or manifests reserved additional expansion work: %d vs %d", used, oneText)
	}
	limited := &inspectionWorkBudget{remaining: used}
	if _, err := validateRasterBytesWithWork(body, "png", limited); err != nil {
		t.Fatalf("exact shared reservation rejected: %v", err)
	}
	walk, err := rasterWalkWork(int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	limited = &inspectionWorkBudget{remaining: walk + maxRasterTextBytes - 1}
	_, err = validateRasterBytesWithWork(body, "png", limited)
	fbxTestRequireWorkRejection(t, err)
}

func TestRasterWorkSharedReadRejectsBeforePayloadAllocation(t *testing.T) {
	body := inlineTestPNG(t, "")
	// The structure walk is the minimum every image needs before its bytes
	// are read; decoder passes are reserved later, only for a full decode.
	walk, err := rasterWalkWork(int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	reader := &fbxTestReadCounter{reader: bytes.NewReader(body), step: 1}
	fbxTestRequireWorkRejection(t, inspectRasterAddressesWithWork(reader, int64(len(body)), "png", &inspectionWorkBudget{remaining: walk - 1}))
	if reader.readBytes != 0 {
		t.Fatal("exhausted shared budget consumed the raster payload")
	}
}

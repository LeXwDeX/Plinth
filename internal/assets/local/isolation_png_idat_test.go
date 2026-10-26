package local

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/adler32"
	"hash/crc32"
	"image/png"
	"io"
	"testing"
)

func fragmentedPNGFixture(t *testing.T, emptyChunks int, oneByteChunks bool) []byte {
	t.Helper()
	base := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255})
	start, end := rasterPNGChunkLocation(t, base, "IDAT")
	payload := base[start+8 : end]
	body := append([]byte(nil), base[:start]...)
	for range emptyChunks {
		body = append(body, rasterPNGChunk("IDAT", nil)...)
	}
	if oneByteChunks {
		for _, value := range payload {
			body = append(body, rasterPNGChunk("IDAT", []byte{value})...)
			body = append(body, rasterPNGChunk("IDAT", nil)...)
		}
	} else {
		body = append(body, rasterPNGChunk("IDAT", payload)...)
	}
	body = append(body, rasterPNGChunk("IDAT", nil)...)
	return append(body, base[end+4:]...)
}

func TestPNGManyEmptyAndSingleByteIDATChunksAreValid(t *testing.T) {
	for _, oneByte := range []bool{false, true} {
		body := fragmentedPNGFixture(t, maxRasterSections+1, oneByte)
		if _, err := png.Decode(bytes.NewReader(body)); err != nil {
			t.Fatalf("standard decoder rejected valid fixture: %v", err)
		}
		plan, err := validateRasterBytes(body, "png")
		if err != nil {
			t.Fatalf("valid fragmented PNG rejected: %v", err)
		}
		rasterRequireWholeImagePlan(t, plan, body)
	}
}

func pngIDATEnvelope(t *testing.T, body []byte) (inspectionSpan, int64, int64) {
	t.Helper()
	var span inspectionSpan
	var payload, chunks int64
	for offset := 8; offset < len(body); {
		length := int(binary.BigEndian.Uint32(body[offset : offset+4]))
		end := offset + 12 + length
		chunks++
		if string(body[offset+4:offset+8]) == "IDAT" {
			if span.start == 0 {
				span.start = int64(offset)
			}
			span.end = int64(end)
			payload += int64(length)
		}
		offset = end
	}
	return span, payload, chunks
}

func TestPNGIDATReaderSkipsFramingAndEmptyChunksWithExactRemaining(t *testing.T) {
	body := fragmentedPNGFixture(t, maxRasterSections+1, true)
	span, payload, _ := pngIDATEnvelope(t, body)
	reader := &rasterIDATReader{data: body, offset: span.start, end: span.end, bytesRemaining: payload}
	if n, err := reader.Read(nil); n != 0 || err != nil || reader.remaining() != payload {
		t.Fatal("empty read changed reader")
	}
	var got []byte
	for reader.remaining() > 0 {
		if len(got)%2 == 0 {
			value, err := reader.ReadByte()
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, value)
		} else {
			var pair [2]byte
			n, err := reader.Read(pair[:])
			if err != nil || n == 0 {
				t.Fatalf("no reader progress %d %v", n, err)
			}
			got = append(got, pair[:n]...)
		}
		if reader.remaining() != payload-int64(len(got)) {
			t.Fatal("remaining excludes unread later chunks")
		}
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("empty tail did not reach EOF: %v", err)
	}
	base := rasterRawPNG(t, 1, 1, 8, 6, 0, []byte{0, 255, 0, 0, 255})
	start, end := rasterPNGChunkLocation(t, base, "IDAT")
	if !bytes.Equal(got, base[start+8:end]) {
		t.Fatal("reader included PNG framing or dropped payload")
	}
}

func TestPNGFragmentationRetainsCRCOrderingAndPixelTailChecks(t *testing.T) {
	valid := fragmentedPNGFixture(t, maxRasterSections+1, true)
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
		reason string
	}{
		{"late-empty-crc", func(body []byte) []byte { body[len(body)-13] ^= 1; return body }, "png_invalid_structure"},
		{"metadata-between-idat", func(body []byte) []byte {
			at := 33 + 24
			out := append([]byte(nil), body[:at]...)
			out = append(out, rasterPNGChunk("tEXt", []byte("Comment\x00ok"))...)
			return append(out, body[at:]...)
		}, "png_invalid_structure"},
		{"byte-after-zlib", func(body []byte) []byte {
			at := len(body) - 12
			out := append([]byte(nil), body[:at]...)
			out = append(out, rasterPNGChunk("IDAT", []byte{0})...)
			return append(out, body[at:]...)
		}, "png_invalid_pixels"},
		{"second-zlib-stream", func(body []byte) []byte {
			at := len(body) - 12
			out := append([]byte(nil), body[:at]...)
			out = append(out, rasterPNGChunk("IDAT", rasterZlib(t, []byte{0, 255, 0, 0, 255}))...)
			return append(out, body[at:]...)
		}, "png_invalid_pixels"},
		// NUL/space alignment padding after IEND is tolerated (#330); other bytes are not.
		{"tail-after-iend", func(body []byte) []byte { return append(body, 'x') }, "raster_trailing_data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.change(bytes.Clone(valid))
			_, err := validateRasterBytes(body, "png")
			var rejected *inspectionRejection
			if !errors.As(err, &rejected) || rejected.reason != tc.reason {
				t.Fatalf("expected %s, got %v", tc.reason, err)
			}
		})
	}
}

func TestPNGChunkFramingExhaustionIsHardAndShared(t *testing.T) {
	body := fragmentedPNGFixture(t, maxRasterSections+1, true)
	_, _, chunks := pngIDATEnvelope(t, body)
	walk, err := rasterWalkWork(int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	framing := chunks * pngChunkWork
	limited := &inspectionWorkBudget{remaining: walk + framing - 1}
	_, err = validateRasterBytesWithWork(body, "png", limited)
	fbxTestRequireWorkRejection(t, err)
	work := newInspectionWorkBudget()
	if _, err = validateRasterBytesWithWork(body, "png", work); err != nil {
		t.Fatal(err)
	}
	used := maxInspectionWork - work.remaining
	if used <= walk+framing {
		t.Fatal("valid tiny image did not pay for pixel decode")
	}
	shared := &inspectionWorkBudget{remaining: used + walk + framing - 1}
	if _, err = validateRasterBytesWithWork(body, "png", shared); err != nil {
		t.Fatal(err)
	}
	// The second image's framing walk no longer fits next to the first image's
	// optional pixel decode. Reclaiming that decode keeps the walk complete:
	// exhaustion of optional work must not reject a structurally valid image.
	afterFirst := shared.reclaimable
	if afterFirst <= 0 {
		t.Fatal("first image must have run its optional decode")
	}
	if _, err = validateRasterBytesWithWork(body, "png", shared); err != nil {
		t.Fatalf("framing walk must reclaim optional decode work: %v", err)
	}
	if shared.reclaimable >= afterFirst {
		t.Fatal("the second walk must reclaim the optional decode it displaced")
	}
	// A structure-only 8K image must also pay every framing visit: the walk's
	// exhaustion never becomes permission to skip the remainder of its chunks.
	large := bytes.Clone(body)
	binary.BigEndian.PutUint32(large[16:20], 8192)
	binary.BigEndian.PutUint32(large[20:24], 8192)
	binary.BigEndian.PutUint32(large[29:33], crc32.ChecksumIEEE(large[12:29]))
	_, err = validateRasterBytesWithWork(large, "png", &inspectionWorkBudget{remaining: walk + framing - 1})
	fbxTestRequireWorkRejection(t, err)
}

func TestPNGIDATNumericDescriptorsStayConstant(t *testing.T) {
	var previous inspectionPlan
	for _, empty := range []int{0, maxRasterSections + 1, 10 * maxRasterSections} {
		body := fragmentedPNGFixture(t, empty, true)
		work := newInspectionWorkBudget()
		if err := reserveRasterWalkWork(int64(len(body)), work); err != nil {
			t.Fatal(err)
		}
		plan, err := validatePNGBytes(body, work)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.numeric) != 6 {
			t.Fatalf("IDAT fragmentation retained per-chunk descriptors: %d", len(plan.numeric))
		}
		span, _, _ := pngIDATEnvelope(t, body)
		matches := 0
		for _, numeric := range plan.numeric {
			if numeric == span {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("validated complete IDAT interval not coalesced: %v", plan.numeric)
		}
		if len(previous.numeric) > 0 && cap(plan.numeric) != cap(previous.numeric) {
			t.Fatal("descriptor capacity grew with IDAT count")
		}
		previous = plan
	}
}

func TestPNGFragmentedIDATUsesNormalAssetAndFBXDelivery(t *testing.T) {
	body := fragmentedPNGFixture(t, maxRasterSections+1, true)
	rasterCheckDeliveryPaths(t, body, "png", false)
	fbx := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Content"), properties: [][]byte{fbxTestBytes('R', body)}})
	assertIsolationBoundary(t, fbx, "fbx", "application/octet-stream", true)
}

func singleByteIDATFixture(t *testing.T) []byte {
	t.Helper()
	rows := []byte{0, 255, 0, 0, 255}
	base := rasterRawPNG(t, 1, 1, 8, 6, 0, rows)
	// A legal zlib stream with empty nonfinal stored-deflate blocks expands
	// to one pixel, while providing >3333 nonempty one-byte IDAT payloads.
	stream := []byte{0x78, 0x01}
	for range maxRasterSections + 1 {
		stream = append(stream, 0, 0, 0, 0xff, 0xff)
	}
	stream = append(stream, 1, 5, 0, 0xfa, 0xff)
	stream = append(stream, rows...)
	stream = binary.BigEndian.AppendUint32(stream, adler32.Checksum(rows))
	body := append([]byte(nil), base[:33]...)
	for _, value := range stream {
		body = append(body, rasterPNGChunk("IDAT", []byte{value})...)
	}
	return append(body, rasterPNGChunk("IEND", nil)...)
}

func TestPNGManyNonEmptySingleByteIDATChunksAreValid(t *testing.T) {
	body := singleByteIDATFixture(t)
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("standard PNG decoder rejected fixture: %v", err)
	}
	span, payload, chunks := pngIDATEnvelope(t, body)
	if payload <= maxRasterSections || chunks <= maxRasterSections {
		t.Fatal("fixture does not cross the former nonempty IDAT descriptor limit")
	}
	plan, err := validatePNGBytes(body, newInspectionWorkBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.numeric) != 6 {
		t.Fatal("nonempty IDAT fragments retained per-payload descriptors")
	}
	reader := &rasterIDATReader{data: body, offset: span.start, end: span.end, bytesRemaining: payload}
	decoded, err := zlib.NewReader(reader)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := io.ReadAll(decoded)
	decoded.Close()
	if err != nil || !bytes.Equal(rows, []byte{0, 255, 0, 0, 255}) || reader.remaining() != 0 {
		t.Fatalf("one-byte IDAT stream changed: %v", err)
	}
}

func TestPNGIDATFragmentCountDoesNotGrowParserAllocations(t *testing.T) {
	allocations := func(empty int) float64 {
		body := fragmentedPNGFixture(t, empty, true)
		// The established 8K profile skips decode allocations, isolating the
		// framing walk and descriptor storage from the standard PNG decoder.
		binary.BigEndian.PutUint32(body[16:20], 8192)
		binary.BigEndian.PutUint32(body[20:24], 8192)
		binary.BigEndian.PutUint32(body[29:33], crc32.ChecksumIEEE(body[12:29]))
		return testing.AllocsPerRun(5, func() {
			if _, err := validatePNGBytes(body, newInspectionWorkBudget()); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := allocations(0), allocations(10*maxRasterSections)
	if large > small+2 {
		t.Fatalf("parser allocations grow with IDAT count: small=%v large=%v", small, large)
	}
}

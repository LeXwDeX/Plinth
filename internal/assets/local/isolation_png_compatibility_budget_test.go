package local

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"testing"
)

// After-only implementation-boundary tests. The exact pre-fix public suite
// excludes this file, because that revision does not expose inspection plans.
func TestPNGCompatibilityBudgetCompressedFields(t *testing.T) {
	for _, kind := range []string{"zTXt", "iTXt"} {
		t.Run(kind, func(t *testing.T) {
			plain := []byte("caption")
			wire := pngCompatibilityBudgetZlib(t, plain)
			prefix := []byte("Caption\x00\x00")
			if kind == "iTXt" {
				prefix = []byte("Caption\x00\x01\x00es-419\x00Caption\x00")
			}
			payload := append(bytes.Clone(prefix), wire...)
			remaining := int64(2 * len(plain))
			for index := range 2 {
				plan, err := inspectPNGTextChunk(kind, payload, &remaining)
				if err != nil || remaining != int64((1-index)*len(plain)) {
					t.Fatal("compressed fields did not consume one shared decoded-byte budget")
				}
				if len(plan.allowed) != 0 || len(plan.numeric) != 1 || plan.numeric[0] != (inspectionSpan{int64(len(prefix)), int64(len(payload))}) {
					t.Fatal("compressed representation must exempt only its validated encoded range")
				}
			}
			plan, err := inspectPNGTextChunk(kind, payload, &remaining)
			if err == nil || remaining != 0 || len(plan.numeric) != 0 || len(plan.allowed) != 0 {
				t.Fatal("exhausted metadata budget reset or granted an unchecked span")
			}
		})
	}
	for _, item := range []struct {
		name      string
		remaining *int64
	}{
		{"nil", nil}, {"negative", new(int64(-1))}, {"above_maximum", new(int64(maxRasterTextBytes + 1))},
	} {
		t.Run(item.name, func(t *testing.T) {
			payload := append([]byte("Caption\x00\x00"), pngCompatibilityBudgetZlib(t, []byte("caption"))...)
			if _, err := inspectPNGTextChunk("zTXt", payload, item.remaining); err == nil {
				t.Fatal("invalid shared metadata reserve reached decoding")
			}
		})
	}
}

func TestPNGCompatibilityBudgetFailedStreamsNeverGrantSpans(t *testing.T) {
	good := pngCompatibilityBudgetZlib(t, []byte("caption"))
	for name, wire := range map[string][]byte{
		"checksum":     append(bytes.Clone(good[:len(good)-1]), good[len(good)-1]^1),
		"truncated":    bytes.Clone(good[:len(good)-1]),
		"trailing":     append(bytes.Clone(good), 0),
		"concatenated": append(bytes.Clone(good), good...),
	} {
		t.Run(name, func(t *testing.T) {
			remaining := int64(maxRasterTextBytes)
			plan, err := inspectPNGTextChunk("zTXt", append([]byte("Caption\x00\x00"), wire...), &remaining)
			if err == nil || len(plan.numeric) != 0 || len(plan.allowed) != 0 || remaining > maxRasterTextBytes {
				t.Fatal("unchecked stream granted a span or increased the budget")
			}
		})
	}
}

func TestPNGCompatibilityBudgetAbsoluteWireIsStillInspected(t *testing.T) {
	// A stored DEFLATE block ends with harmless decoded "https", followed by
	// Adler32 0x3a2f2f4b. Only the original wire has the absolute scheme.
	plain := []byte("JFYIIHYTZSLKHILNHPHKSPOAVQHKJGLXSPBIXFUJRAXGLTQHYWUHLQBKIEPVSCIOBEKBAACIIXNIERFWWKOGGDPSWXZBWVQKWBENAOPGLGVULJVOGPOHYNLSUZAOHBZMHAYWOVAGXLHLHTEPDGFTMChttps")
	// Go emits an empty final block before the checksum. Use an independent
	// single final stored block so the deliberate checksum boundary is exact.
	wire := []byte{0x78, 0x01, 0x01, byte(len(plain)), byte(len(plain) >> 8), byte(^uint16(len(plain))), byte(^uint16(len(plain)) >> 8)}
	wire = append(wire, plain...)
	wire = append(wire, 0x3a, 0x2f, 0x2f, 0x4b)
	decoder, err := zlib.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal("stored stream fixture oracle failed")
	}
	decoded, decodeErr := io.ReadAll(decoder)
	closeErr := decoder.Close()
	if decodeErr != nil || closeErr != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("stored stream fixture oracle disagreed with its decoded content")
	}
	payload := append([]byte("Caption\x00\x00"), wire...)
	remaining := int64(maxRasterTextBytes)
	plan, err := inspectPNGTextChunk("zTXt", payload, &remaining)
	if err != nil {
		t.Fatal("fully valid harmless decoded stream must produce its exact representation plan")
	}
	if err := inspectPlannedBytes(bytes.NewReader(payload), plan); err == nil {
		t.Fatal("numeric representation span waived a raw absolute scheme")
	}
}

func TestPNGCompatibilityBudgetUTF8Carry(t *testing.T) {
	for name, text := range map[string][]byte{
		"valid_four_byte_rune": []byte("caption \U0001f600 ending"),
		"overlong":             {0xc0, 0xaf}, "isolated_continuation": {0x80}, "surrogate": {0xed, 0xa0, 0x80}, "incomplete": {0xf0, 0x9f, 0x98},
	} {
		for _, width := range []int{1, 2, 3} {
			t.Run(name+"/read_"+string(rune('0'+width)), func(t *testing.T) {
				reader := &pngUTF8Reader{source: &pngCompatibilityBudgetShortReader{body: bytes.NewReader(text), width: width}}
				decoded, err := io.ReadAll(reader)
				if (err == nil) != (name == "valid_four_byte_rune") {
					t.Fatal("streaming UTF8 validation lost a split or invalid code point")
				}
				if err == nil && !bytes.Equal(decoded, text) {
					t.Fatal("UTF8 validation changed decoded metadata")
				}
			})
		}
	}
}

func TestPNGCompatibilityBudgetLanguageBoundBeforeSplit(t *testing.T) {
	for name, tag := range map[string][]byte{
		"too_many_subtags":  []byte("en-x" + strings.Repeat("-a", 10000)),
		"unicode_case_fold": []byte("en-\u212a"),
	} {
		t.Run(name, func(t *testing.T) {
			if validPNGLanguageTag(tag) {
				t.Fatal("unbounded or non-ASCII language tag entered the metadata grammar")
			}
		})
	}
}

func pngCompatibilityBudgetZlib(t *testing.T, text []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := zlib.NewWriter(&encoded)
	if _, err := writer.Write(text); err != nil {
		t.Fatal("budget fixture compression failed")
	}
	if err := writer.Close(); err != nil {
		t.Fatal("budget fixture compression close failed")
	}
	return encoded.Bytes()
}

type pngCompatibilityBudgetShortReader struct {
	body  *bytes.Reader
	width int
}

func (r *pngCompatibilityBudgetShortReader) Read(body []byte) (int, error) {
	return r.body.Read(body[:min(len(body), r.width)])
}

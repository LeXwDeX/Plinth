package local

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

func TestAddressMarkerUnknownPatternFallback(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)marker`)
	if addressPatternMarker(pattern) != "" {
		t.Fatal("unknown pattern must not require address syntax")
	}
	for _, text := range []string{"/marker", `\u006darker`, "%6darker", "&#109;arker"} {
		if !containsAddress([]byte(text), pattern) || !containsEncodedAddress(text, pattern) {
			t.Fatal("unknown pattern was restricted by the address prefilter")
		}
	}
	if !containsEncodedAddress("marker", pattern) {
		t.Fatal("plain unknown pattern was restricted by the address prefilter")
	}
}

func TestAddressMarkerAdversarialOracle(t *testing.T) {
	// Numeric/binary windows commonly contain every single-byte escape marker.
	// Their presence alone must not force expensive address regex scans, but
	// literal, decoded and boundary-crossing addresses must remain detectable.
	// A split just beyond the carry size also exercises continuation mode.
	// The existing frozen stream oracle separately covers full 64 KiB chunks.
	boundary := isolationOverlap + 3
	seed := make([]byte, boundary+isolationOverlap+7)
	random := rand.New(rand.NewSource(159))
	if _, err := random.Read(seed); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"plain model data / % \\ &",
		"hTtPs://private.example.test/synth",
		"httpſ://private.example.test/synth", // Unicode folding of optional S.
		" //private.example.test/synth",
		"1//private.example.test/synth", // Not a protocol-relative boundary.
		"。//private.example.test/synth",
		"https%25253A%25252F%25252Fprivate.example.test/synth",
		`https:\u002f\u002fprivate.example.test/synth`,
		"https:&amp;#47;&amp;#47;private.example.test/synth",
		`https%3A%26%2347%3B\u002fprivate.example.test/synth`,
		"f 1//1 2//2 3//3",
		"http:%2x%2ylocalhost",
	} {
		for _, representation := range addressScanRepresentations(text) {
			for _, offset := range []int{0, isolationOverlap - 1, boundary - 3, boundary + 1} {
				data := make([]byte, 0, len(seed)+len(representation.data))
				data = append(data, seed[:offset]...)
				data = append(data, representation.data...)
				data = append(data, seed[offset:]...)
				for _, pattern := range addressScanPatterns() {
					if got, want := containsAddress(data, pattern.current), addressScanOldContains(data, pattern.oracle); got != want {
						t.Fatalf("whole-window oracle mismatch: representation=%s offset=%d pattern=%s", representation.name, offset, pattern.name)
					}
				}
				for _, absolute := range []bool{false, true} {
					addressScanCompareStream(t, data, []int{boundary - 1, boundary, boundary + 1}, absolute)
				}
			}
		}
	}
}

func BenchmarkAddressMarkerPrefilter(b *testing.B) {
	// Deterministic finite float-like wire bytes contain escape syntax and NUL,
	// but no URI marker. This forces the old scanner's repeated regex passes.
	data := bytes.Repeat([]byte{0, 0x80, '/', 0x3f, '%', '\\', '&', 0x3f}, isolationChunkBytes/8)
	for _, pattern := range addressScanPatterns() {
		b.Run(pattern.name, func(b *testing.B) {
			for _, implementation := range []struct {
				name string
				scan func([]byte) bool
			}{
				{"before", func(data []byte) bool { return addressScanOldContains(data, pattern.oracle) }},
				{"after", func(data []byte) bool { return containsAddress(data, pattern.current) }},
			} {
				b.Run(implementation.name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					for b.Loop() {
						if implementation.scan(data) {
							b.Fatal("benchmark fixture unexpectedly formed an address")
						}
					}
				})
			}
		})
	}
}

func TestAddressMarkerNestedFinalDecode(t *testing.T) {
	// The marker first appears after the third and final percent-decoding pass.
	text := "https%3A%2F%2Fprivate.example.test/synth"
	for range 2 {
		text = strings.ReplaceAll(text, "%", "%25")
	}
	for _, pattern := range addressScanPatterns() {
		if !containsEncodedAddress(text, pattern.current) {
			t.Fatalf("final decoded marker was not inspected: %s", pattern.name)
		}
	}
}

package local

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestDracoEncodedNumericAddressesAcrossDeliveryBoundaries(t *testing.T) {
	raw, err := os.ReadFile("draco/address-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string `json:"name"`
		Decoded string `json:"decoded_hex"`
		Draco   string `json:"draco_hex"`
		Reject  bool   `json:"reject"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			decoded, err := hex.DecodeString(tc.Decoded)
			if err != nil {
				t.Fatal(err)
			}
			compressed, err := hex.DecodeString(tc.Draco)
			if err != nil {
				t.Fatal(err)
			}
			if containsAddress(decoded, absoluteAddress) != tc.Reject {
				t.Fatal("fixture differs from existing address policy")
			}
			// Prove rejection comes from the decoded attributes, not a raw hit.
			if containsAddress(compressed, absoluteAddress) {
				t.Fatal("fixture address leaked into compressed bytes")
			}
			body := dracoGLB(t, compressed, func(d map[string]any) {
				d["accessors"] = append(d["accessors"].([]any), map[string]any{"componentType": 5125, "count": 24, "type": "VEC4"})
				p := d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)
				p["attributes"].(map[string]any)["_TEST"] = 2
				p["extensions"].(map[string]any)["KHR_draco_mesh_compression"].(map[string]any)["attributes"].(map[string]any)["_TEST"] = 1
			})
			assertIsolationBoundary(t, body, "glb", "model/gltf-binary", !tc.Reject)
		})
	}
}

func TestDracoNumericScannerMatchesExistingEscapesAcrossChunks(t *testing.T) {
	for _, tc := range addressScanCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, representation := range []struct {
				name string
				data []byte
			}{
				{"bytes", []byte(tc.text)}, {"utf16_le", addressScanUTF16(tc.text, true, 0)},
				{"utf16_le_odd", addressScanUTF16(tc.text, true, 1)},
				{"utf16_be", addressScanUTF16(tc.text, false, 0)}, {"utf16_be_odd", addressScanUTF16(tc.text, false, 1)},
			} {
				t.Run(representation.name, func(t *testing.T) {
					// The corpus has fixed expected outcomes; the old independent scanner
					// verifies the representation once, rather than deriving expectations
					// by calling the production scanner on every padded split.
					if got := addressScanOldContains(representation.data, addressScanOldAbsolute); got != tc.absolute {
						t.Fatalf("oracle=%t want=%t", got, tc.absolute)
					}
					checkSplit := func(split, prefix int) {
						t.Helper()
						data := append(bytes.Repeat([]byte{'a'}, prefix), representation.data...)
						data = append(data, 'z')
						boundary := prefix + split
						scanner := newDracoNumericScan(int64(len(data)) + 2*isolationOverlap)
						rejected := !scanner.consume(context.Background(), data[:boundary])
						if !rejected {
							rejected = !scanner.consume(context.Background(), data[boundary:])
						}
						if rejected != tc.absolute {
							t.Fatalf("split=%d prefix=%d rejected=%t want=%t", split, prefix, rejected, tc.absolute)
						}
					}
					// Every byte split still exercises carry, nested escapes and both
					// UTF-16 alignments. A short neutral prefix avoids scanning the same
					// 64 KiB padding thousands of times under race instrumentation.
					for split := 0; split <= len(representation.data); split++ {
						checkSplit(split, 8)
					}
					// Each representation also crosses the actual WASM 64 KiB flush at
					// its start, middle and end, independently of the compact split sweep.
					for _, split := range []int{0, len(representation.data) / 2, len(representation.data)} {
						checkSplit(split, isolationChunkBytes-split)
					}
				})
			}
		})
	}
}

func TestDracoNumericScannerBudgetAndCancellation(t *testing.T) {
	ctx := context.Background()
	first := bytes.Repeat([]byte{'a'}, isolationChunkBytes)
	scanner := newDracoNumericScan(int64(len(first)) + 1)
	if !scanner.consume(ctx, first) {
		t.Fatal("initial chunk rejected")
	}
	if scanner.consume(ctx, []byte{'b'}) {
		t.Fatal("overlap reread exceeded budget")
	}
	if scanner.consume(ctx, []byte{'a'}) {
		t.Fatal("failed scanner recovered")
	}
	scanner = newDracoNumericScan(1 << 20)
	if scanner.consume(ctx, bytes.Repeat([]byte{'a'}, isolationChunkBytes+1)) {
		t.Fatal("oversized host call accepted")
	}
	scanner = newDracoNumericScan(1 << 20)
	if !scanner.consume(ctx, []byte{'a'}) {
		t.Fatal("initial data rejected")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if scanner.consume(cancelled, []byte{'b'}) {
		t.Fatal("cancelled host call accepted")
	}
	if scanner.scanned != 1 {
		t.Fatal("cancelled call consumed more bytes")
	}
}

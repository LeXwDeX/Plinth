package local

import (
	"bytes"
	"strings"
	"testing"
)

// #189: Meshy Creative Lab relief builds (fridge magnet, keychain) export GLBs
// whose asset.generator is "https://github.com/mikedh/trimesh". glTF 2.0
// defines asset.generator and asset.copyright as descriptive strings, not
// resource references, so a plain tool address there is not an uncached
// dependency. Every other string, query/fragment/credential forms and the
// uri fields keep the full address check.
func TestGLTFAssetMetadataMayNameAToolAddress(t *testing.T) {
	accepted := map[string]string{
		"generator":         `{"asset":{"version":"2.0","generator":"https://github.com/mikedh/trimesh"}}`,
		"copyright":         `{"asset":{"version":"2.0","copyright":"CC-BY https://example.org/creator"}}`,
		"escaped generator": `{"asset":{"version":"2.0","generator":"https:\/\/github.com\/mikedh\/trimesh"}}`,
	}
	for name, document := range accepted {
		t.Run("accept "+name, func(t *testing.T) {
			body := glbWithJSON(document)
			if err := inspectGLBStream(bytes.NewReader(body), int64(len(body)), nil, ""); err != nil {
				t.Fatalf("GLB rejected: %v", err)
			}
			if err := inspectGLTFJSON(strings.NewReader(document), int64(len(document)), nil, ""); err != nil {
				t.Fatalf("glTF JSON rejected: %v", err)
			}
		})
	}
	rejected := map[string]string{
		"signed generator":     `{"asset":{"version":"2.0","generator":"https://assets.example/tool?Expires=1&Signature=secret"}}`,
		"fragment generator":   `{"asset":{"version":"2.0","generator":"https://assets.example/tool#token"}}`,
		"credential generator": `{"asset":{"version":"2.0","generator":"https://user:pass@assets.example/tool"}}`,
		"nested asset extras":  `{"asset":{"version":"2.0","extras":{"generator":"https://github.com/mikedh/trimesh"}}}`,
		"root generator":       `{"asset":{"version":"2.0"},"generator":"https://github.com/mikedh/trimesh"}`,
		"node name":            `{"asset":{"version":"2.0"},"nodes":[{"name":"https://github.com/mikedh/trimesh"}]}`,
		"image uri":            `{"asset":{"version":"2.0","generator":"trimesh"},"images":[{"uri":"https://github.com/mikedh/trimesh.png"}]}`,
		"duplicate generator":  `{"asset":{"version":"2.0","generator":"https://github.com/mikedh/trimesh","generator":"x"}}`,
		"overlong generator":   `{"asset":{"version":"2.0","generator":"https://github.com/` + strings.Repeat("a", 300) + `"}}`,
		"non-string generator": `{"asset":{"version":"2.0","generator":{"url":"https://github.com/mikedh/trimesh"}}}`,
		"generator as key":     `{"asset":{"version":"2.0","https://github.com/mikedh/trimesh":"x"}}`,
	}
	for name, document := range rejected {
		t.Run("reject "+name, func(t *testing.T) {
			body := glbWithJSON(document)
			if err := inspectGLBStream(bytes.NewReader(body), int64(len(body)), nil, ""); err == nil {
				t.Fatal("GLB accepted")
			}
			if err := inspectGLTFJSON(strings.NewReader(document), int64(len(document)), nil, ""); err == nil {
				t.Fatal("glTF JSON accepted")
			}
		})
	}
}

// #189: trimesh (Meshy Creative Lab relief GLBs) pads each bufferView to a
// 4-byte boundary and counts the padding in the image view, so an embedded
// PNG/JPEG is followed by up to three space or zero bytes inside its view.
// Only padding is tolerated: any other trailer is still refused. Since #330
// the raster check itself tolerates up to maxRasterPaddingBytes NUL/space
// bytes after the end marker in every container, so padding that does not end
// on a 4-byte boundary or is longer than three bytes is accepted too, and only
// more padding than that bound is refused.
func TestGLBEmbeddedImageMayCarryViewAlignmentPadding(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			var pixels []byte
			for size := 1; size <= 8; size++ {
				candidate := rasterNoiseFixture(t, format)
				if format == "png" {
					candidate = rasterPNGText(t, "tEXt", bytes.Repeat([]byte("a"), size))
				} else {
					candidate = rasterJPEGMetadata(candidate, 0xe1, bytes.Repeat([]byte("a"), size))
				}
				if (36+len(candidate))%4 == 1 {
					pixels = candidate
					break
				}
			}
			if pixels == nil {
				t.Fatal("no fixture needing three padding bytes")
			}
			for name, tc := range map[string]struct {
				padding  []byte
				accepted bool
			}{
				"three spaces": {[]byte("   "), true},
				"three zeros":  {[]byte{0, 0, 0}, true},
				"other bytes":  {[]byte("abc"), false},
				"unaligned":    {[]byte("  "), true},
				"four spaces":  {[]byte("    "), true},
				"over bound":   {bytes.Repeat([]byte(" "), maxRasterPaddingBytes+4), false},
			} {
				t.Run(name, func(t *testing.T) {
					body := isolatedGLBFixture(t, append(append([]byte(nil), pixels...), tc.padding...), "image/"+format)
					assertIsolationBoundary(t, body, "glb", "model/gltf-binary", tc.accepted)
				})
			}
		})
	}
}

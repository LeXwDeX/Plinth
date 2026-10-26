package local_test

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/adler32"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Independent wire fixtures and standard-library oracles cover the public
// admission and historical-delivery contract. They do not call private parsers.
// PNG fields: https://www.w3.org/TR/png-3/
// DEFLATE: https://www.rfc-editor.org/rfc/rfc1951.html
// Certificates/CMS: RFC 5280, RFC 5652 and RFC 8419.
// Certificate and message signatures are verified here; no fixture represents
// a trusted chain or a complete authenticated C2PA manifest.

func TestPNGCompatibilityInternationalText(t *testing.T) {
	t.Parallel()
	for _, compressed := range []bool{false, true} {
		for _, language := range []string{"", "en-US", "es-419", "de-1901", "zh-Hans-CN", "ar-AE-u-nu-latn", "x-private", "i-klingon"} {
			t.Run(fmt.Sprintf("valid_%s/compressed_%t", language, compressed), func(t *testing.T) {
				body := pngCompatibilityImage(t, pngCompatibilityChunk("iTXt", pngCompatibilityITXT(t, language, []byte("ordinary caption"), compressed)))
				pngCompatibilityDelivery(t, body, "png", "image/png", "image", true)
			})
		}
		for name, language := range map[string]string{
			"leading_separator": "-en", "trailing_separator": "en-", "empty_subtag": "en--US", "underscore": "en_US",
			"non_ascii": "en-世界", "numeric_language": "419", "duplicate_variant": "de-1901-1901", "duplicate_extension": "en-u-ca-latn-u-nu-latn",
		} {
			t.Run(fmt.Sprintf("invalid_%s/compressed_%t", name, compressed), func(t *testing.T) {
				body := pngCompatibilityImage(t, pngCompatibilityChunk("iTXt", pngCompatibilityITXT(t, language, []byte("ordinary caption"), compressed)))
				pngCompatibilityDelivery(t, body, "png", "image/png", "image", false)
			})
		}
		for name, caption := range map[string]string{
			"absolute": "https://provider.example/private?token=synth",
			"escaped":  `https:\/\/provider.example\/private?token=synth`,
			"encoded":  `%68%74%74%70%73%3A%2F%2Fprovider.example%2Fprivate%3Ftoken%3Dsynth`,
		} {
			t.Run(fmt.Sprintf("address_text_%s/compressed_%t", name, compressed), func(t *testing.T) {
				// Text is cached as delivered, addresses included.
				body := pngCompatibilityImage(t, pngCompatibilityChunk("iTXt", pngCompatibilityITXT(t, "es-419", []byte(caption), compressed)))
				pngCompatibilityDelivery(t, body, "png", "image/png", "image", true)
			})
		}
	}
	valid := pngCompatibilityITXT(t, "es-419", []byte("ordinary caption"), false)
	for _, item := range []struct {
		name string
		body []byte
	}{
		{"compression_flag", append([]byte("Description\x00\x02\x00"), valid[len("Description")+3:]...)},
		{"compression_method", append([]byte("Description\x00\x01\x01"), valid[len("Description")+3:]...)},
		{"missing_language_separator", []byte("Description\x00\x00\x00en-US")},
		{"bad_translated_utf8", []byte("Description\x00\x00\x00es-419\x00\xff\x00caption")},
		{"bad_text_utf8", pngCompatibilityITXT(t, "es-419", []byte{0xff}, false)},
		{"bad_compressed_utf8", pngCompatibilityITXT(t, "es-419", []byte{0xff}, true)},
	} {
		t.Run(item.name, func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("iTXt", item.body)), "png", "image/png", "image", false)
		})
	}
	t.Run("address_translated_keyword", func(t *testing.T) {
		body := []byte("Description\x00\x00\x00es-419\x00//provider.example/private?token=synth\x00caption")
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("iTXt", body)), "png", "image/png", "image", true)
	})
}

func TestPNGCompatibilitySuggestedPalette(t *testing.T) {
	t.Parallel()
	for _, depth := range []byte{8, 16} {
		t.Run(fmt.Sprintf("numeric_depth_%d", depth), func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", depth, pngCompatibilityPaletteEntries(depth)))), "png", "image/png", "image", true)
		})
		t.Run(fmt.Sprintf("multiple_entries_%d", depth), func(t *testing.T) {
			width := 6
			if depth == 16 {
				width = 10
			}
			entries := append(pngCompatibilityPaletteEntries(depth), make([]byte, width)...)
			binary.BigEndian.PutUint16(entries[width-2:width], 2)
			binary.BigEndian.PutUint16(entries[len(entries)-2:], 1)
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", depth, entries))), "png", "image/png", "image", true)
		})
		t.Run(fmt.Sprintf("address_like_entries_%d", depth), func(t *testing.T) {
			entries := []byte("https://a ")
			if depth == 8 {
				entries = append(entries, 0, 0) // First frequency 0x733a, second zero.
			}
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", depth, entries))), "png", "image/png", "image", true)
		})
	}
	t.Run("address_like_name_is_cached_as_delivered", func(t *testing.T) {
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("sPLT", pngCompatibilityPalette("//provider.example/private?token=synth", 8, make([]byte, 6)))), "png", "image/png", "image", true)
	})
	t.Run("distinct_palette_names", func(t *testing.T) {
		pngCompatibilityDelivery(t, pngCompatibilityImage(t,
			pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", 8, pngCompatibilityPaletteEntries(8))),
			pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palett\xe9", 16, pngCompatibilityPaletteEntries(16)))), "png", "image/png", "image", true)
	})
	for name, payload := range map[string][]byte{
		"empty_name":              pngCompatibilityPalette("", 8, make([]byte, 6)),
		"long_name":               pngCompatibilityPalette(strings.Repeat("P", 80), 8, make([]byte, 6)),
		"leading_space":           pngCompatibilityPalette(" Palette", 8, make([]byte, 6)),
		"trailing_space":          pngCompatibilityPalette("Palette ", 8, make([]byte, 6)),
		"repeated_space":          pngCompatibilityPalette("Two  Palettes", 8, make([]byte, 6)),
		"control_name":            pngCompatibilityPalette("Pal\x7fette", 8, make([]byte, 6)),
		"unsupported_depth":       pngCompatibilityPalette("Palette", 7, make([]byte, 6)),
		"empty_entries":           pngCompatibilityPalette("Palette", 8, nil),
		"short_eight_bit_entry":   pngCompatibilityPalette("Palette", 8, make([]byte, 5)),
		"short_sixteen_bit_entry": pngCompatibilityPalette("Palette", 16, make([]byte, 9)),
		"increasing_frequency":    pngCompatibilityPalette("Palette", 8, []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 2}),
	} {
		t.Run(name, func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("sPLT", payload)), "png", "image/png", "image", false)
		})
	}
	t.Run("duplicate_palette_identity", func(t *testing.T) {
		chunk := pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", 8, pngCompatibilityPaletteEntries(8)))
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, chunk, chunk), "png", "image/png", "image", false)
	})
	t.Run("palette_after_pixels", func(t *testing.T) {
		body := pngCompatibilityImage(t)
		chunk := pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", 8, pngCompatibilityPaletteEntries(8)))
		body = append(append(append([]byte(nil), body[:len(body)-12]...), chunk...), body[len(body)-12:]...)
		pngCompatibilityDelivery(t, body, "png", "image/png", "image", false)
	})
}

func TestPNGCompatibilityCompressedMetadata(t *testing.T) {
	t.Parallel()
	caption := []byte(strings.Repeat("ordinary synthetic caption ", 64))
	for _, mode := range []string{"default", "fixed", "stored", "huffman"} {
		wire := pngCompatibilityCompress(t, caption, mode)
		for _, kind := range []string{"zTXt", "iTXt"} {
			t.Run(kind+"_"+mode, func(t *testing.T) {
				pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk(kind, pngCompatibilityCompressedField(kind, wire))), "png", "image/png", "image", true)
			})
		}
	}
	entropy := pngCompatibilityEntropy(t)
	for _, kind := range []string{"zTXt", "iTXt"} {
		t.Run(kind+"_wire_entropy", func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk(kind, pngCompatibilityCompressedField(kind, entropy))), "png", "image/png", "image", true)
		})
	}
	t.Run("valid_icc_profile", func(t *testing.T) {
		profile := pngCompatibilityICC(t)
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("iCCP", pngCompatibilityCompressedField("iCCP", pngCompatibilityCompress(t, profile, "fixed")))), "png", "image/png", "image", true)
	})
	good := pngCompatibilityCompress(t, caption, "fixed")
	for _, item := range []struct {
		name string
		wire []byte
	}{
		{"checksum", append(bytes.Clone(good[:len(good)-1]), good[len(good)-1]^1)},
		{"truncated", bytes.Clone(good[:len(good)-1])},
		{"trailing_byte", append(bytes.Clone(good), 0)},
		{"concatenated_stream", append(bytes.Clone(good), good...)},
		{"invalid_header", append([]byte{0, 0}, good[2:]...)},
	} {
		t.Run(item.name, func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("zTXt", pngCompatibilityCompressedField("zTXt", item.wire))), "png", "image/png", "image", false)
		})
	}
	for name, plain := range map[string][]byte{
		"decoded_absolute": []byte("https://provider.example/private?token=synth"),
		"decoded_escaped":  []byte(`\u0068ttps:\u002f\u002fprovider.example/private?token=synth`),
		"decoded_percent":  []byte(`%2F%2Fprovider.example%2Fprivate%3Ftoken%3Dsynth`),
		"decoded_utf16_le": pngCompatibilityUTF16("//provider.example/private?token=synth", true),
		"decoded_utf16_be": pngCompatibilityUTF16("//provider.example/private?token=synth", false),
	} {
		t.Run(name, func(t *testing.T) {
			// Valid compressed text is cached as delivered, addresses included.
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("zTXt", pngCompatibilityCompressedField("zTXt", pngCompatibilityCompress(t, plain, "default")))), "png", "image/png", "image", true)
		})
	}
	t.Run("absolute_wire_across_checksum", func(t *testing.T) {
		// Adler32 is 0x3a2f2f4b. The final harmless text "https" and the
		// checksum form an absolute scheme only in the served zlib bytes.
		plain := []byte("JFYIIHYTZSLKHILNHPHKSPOAVQHKJGLXSPBIXFUJRAXGLTQHYWUHLQBKIEPVSCIOBEKBAACIIXNIERFWWKOGGDPSWXZBWVQKWBENAOPGLGVULJVOGPOHYNLSUZAOHBZMHAYWOVAGXLHLHTEPDGFTMChttps")
		wire := pngCompatibilityCompress(t, plain, "stored")
		if bytes.Contains(plain, []byte("https://")) || !bytes.Contains(wire, []byte("https://")) {
			t.Fatal("absolute reference must exist only across the compressed-wire checksum boundary")
		}
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("zTXt", pngCompatibilityCompressedField("zTXt", wire))), "png", "image/png", "image", true)
	})
	for name, chunks := range map[string][][]byte{
		"address_keyword":           {pngCompatibilityChunk("zTXt", append([]byte("//provider.example/private?token=synth\x00\x00"), good...))},
		"address_neighbor":          {pngCompatibilityChunk("zTXt", pngCompatibilityCompressedField("zTXt", entropy)), pngCompatibilityChunk("vpAg", []byte("//provider.example/private?token=synth"))},
		"address_like_unknown_body": {pngCompatibilityChunk("vpAg", []byte{47, 47, 97, 32})},
	} {
		t.Run(name, func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, chunks...), "png", "image/png", "image", true)
		})
	}
}

// caBX manifests are cached as delivered (Lex, 2026-10-09): production no
// longer parses certificates, CMS or COSE inside them, so even manifests that
// a C2PA validator would reject, or that carry addresses, are admitted. Only
// the PNG chunk framing (length, CRC) is enforced.
func TestPNGCompatibilityCertificateManifestsAreCachedAsDelivered(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		name                      string
		serial                    []byte
		certSplit, protectedSplit bool
	}{
		{"ordinary", []byte{1}, false, false},
		{"numeric_pattern", []byte{47, 47, 97, 32}, false, false},
		{"canonical_twenty_bytes", append([]byte{0x7f}, make([]byte, 19)...), false, false},
		{"required_sign_padding", append([]byte{0, 0x80}, make([]byte, 18)...), false, false},
		{"split_certificate", []byte{47, 47, 97, 32}, true, false},
		{"split_protected_header", []byte{47, 47, 97, 32}, false, true},
		{"both_segment_maps", []byte{47, 47, 97, 32}, true, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			cert, key := pngCompatibilityCertificate(t, item.serial, "")
			pngCompatibilityVerifyCertificate(t, cert)
			cose := pngCompatibilityCOSE(t, cert, key, item.certSplit, item.protectedSplit)
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(cose))), "png", "image/png", "image", true)
		})
	}
	for name, serial := range map[string][]byte{
		"zero": {0}, "negative": {0xff}, "empty": {}, "noncanonical": {0, 1}, "oversized": append([]byte{1}, make([]byte, 20)...),
		"absolute_serial": []byte("https://a "),
	} {
		t.Run("opaque_invalid_"+name, func(t *testing.T) {
			cert, key := pngCompatibilityCertificate(t, serial, "")
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCOSE(t, cert, key, false, false)))), "png", "image/png", "image", true)
		})
	}
	for _, option := range []string{"private_name", "private_extension", "unknown_integer_extension"} {
		t.Run(option, func(t *testing.T) {
			cert, key := pngCompatibilityCertificate(t, []byte{47, 47, 97, 32}, option)
			pngCompatibilityVerifyCertificate(t, cert)
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCOSE(t, cert, key, false, false)))), "png", "image/png", "image", true)
		})
	}
	cert, key := pngCompatibilityCertificate(t, []byte{47, 47, 97, 32}, "")
	for _, item := range []struct {
		name string
		body []byte
	}{
		{"ordinary_cms", pngCompatibilityCMS(t, cert, key, "")},
		{"private_cms_id", pngCompatibilityCMS(t, cert, key, "private_id")},
		{"bad_cms_version", pngCompatibilityCMS(t, cert, key, "bad_version")},
	} {
		t.Run(item.name, func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCBORBytes(item.body)))), "png", "image/png", "image", true)
		})
	}
	for _, option := range []string{"zero", "private_extension"} {
		t.Run("cms_"+option, func(t *testing.T) {
			serial := []byte{47, 47, 97, 32}
			certOption := option
			if option == "zero" {
				serial, certOption = []byte{0}, ""
			}
			bad, signingKey := pngCompatibilityCertificate(t, serial, certOption)
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCBORBytes(pngCompatibilityCMS(t, bad, signingKey, ""))))), "png", "image/png", "image", true)
		})
	}
	for name, value := range map[string][]byte{
		"generic_integer":   pngCompatibilityCBORBytes(pngCompatibilityDER(0x02, []byte{47, 47, 97, 32})),
		"bad_known_chain":   pngCompatibilityKnownChain(pngCompatibilityCBORBytes(pngCompatibilityDER(0x02, []byte{47, 47, 97, 32}))),
		"empty_known_chain": pngCompatibilityKnownChain([]byte{0x80}),
		"mixed_bad_chain":   pngCompatibilityKnownChain(append(append([]byte{0x82}, pngCompatibilityCBORBytes(cert)...), pngCompatibilityCBORBytes([]byte{1, 2, 3})...)),
	} {
		t.Run(name, func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(value))), "png", "image/png", "image", true)
		})
	}
	t.Run("cose_wrong_array_length", func(t *testing.T) {
		value := pngCompatibilityCOSE(t, cert, key, false, false)
		value[1] = 0x83
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(value))), "png", "image/png", "image", true)
	})
	t.Run("nested_metadata_depth", func(t *testing.T) {
		body := pngCompatibilityBox("cbor", []byte{0xf6})
		for range 40 {
			body = pngCompatibilityBox("jumb", body)
		}
		pngCompatibilityDelivery(t, pngCompatibilityImage(t, pngCompatibilityChunk("caBX", body)), "png", "image/png", "image", true)
	})
	t.Run("bad_chunk_crc", func(t *testing.T) {
		chunk := pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCOSE(t, cert, key, false, false)))
		chunk[len(chunk)-1] ^= 1
		pngCompatibilityDelivery(t, pngCompatibilityImageUnchecked(t, chunk), "png", "image/png", "image", false)
	})
}

func TestPNGCompatibilityNestedDelivery(t *testing.T) {
	t.Parallel()
	cert, key := pngCompatibilityCertificate(t, []byte{47, 47, 97, 32}, "")
	certificate := pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCOSE(t, cert, key, true, true))))
	for name, raster := range map[string][]byte{
		"language":    pngCompatibilityImage(t, pngCompatibilityChunk("iTXt", pngCompatibilityITXT(t, "es-419", []byte("ordinary caption"), true))),
		"palette":     pngCompatibilityImage(t, pngCompatibilityChunk("sPLT", pngCompatibilityPalette("Palette", 16, pngCompatibilityPaletteEntries(16)))),
		"certificate": certificate,
	} {
		t.Run(name, func(t *testing.T) { pngCompatibilityWrappers(t, raster, true, false) })
	}
	t.Run("compressed_entropy", func(t *testing.T) {
		pngCompatibilityWrappers(t, pngCompatibilityImage(t, pngCompatibilityChunk("zTXt", pngCompatibilityCompressedField("zTXt", pngCompatibilityEntropy(t)))), true, true)
	})
	t.Run("address_adjacent_metadata", func(t *testing.T) {
		private := pngCompatibilityImage(t, pngCompatibilityChunk("caBX", pngCompatibilityJUMBF(pngCompatibilityCOSE(t, cert, key, false, false))), pngCompatibilityChunk("vpAg", []byte("//provider.example/private?token=synth")))
		pngCompatibilityWrappers(t, private, true, false)
	})
}

func pngCompatibilityDelivery(t *testing.T, body []byte, variant, mime, role string, accepted bool) {
	t.Helper()
	digest := sha256.Sum256(body)
	expectedHash := hex.EncodeToString(digest[:])
	storeRoot := t.TempDir()
	store, err := local.New(storeRoot)
	if err != nil {
		t.Fatal("fixture store initialization failed")
	}
	t.Cleanup(func() { _ = store.Close() })
	check := func(t *testing.T, err error) {
		t.Helper()
		if (err == nil) != accepted {
			t.Fatalf("expected accepted=%t; error_sha256=%x", accepted, sha256.Sum256([]byte(fmt.Sprint(err))))
		}
		if err != nil && (strings.Contains(err.Error(), "provider.example") || strings.Contains(err.Error(), "token=synth")) {
			t.Fatal("private metadata reached the public error")
		}
	}
	t.Run("validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fixture")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal("fixture file write failed")
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal("fixture file open failed")
		}
		defer file.Close()
		check(t, local.ValidateDeliveredFile(file, role, variant, mime))
		if accepted {
			delivered, err := io.ReadAll(file)
			if err != nil || !bytes.Equal(delivered, body) {
				t.Fatal("validation changed bytes or reader position")
			}
		}
	})
	t.Run("put", func(t *testing.T) {
		asset, err := store.Put(context.Background(), generation.AssetWrite{AssetID: "asset-png-compat", OwnerID: "owner-png-compat", TaskID: "task-png-compat", AttemptID: "attempt-png-compat", Role: role, Variant: variant, MediaType: mime, MaxBytes: int64(len(body)) + 1, Body: bytes.NewReader(body)})
		check(t, err)
		if accepted && (asset.SHA256 != expectedHash || asset.Size != int64(len(body))) {
			t.Fatal("admission changed bytes or hash")
		}
		if !accepted {
			for _, directory := range []string{"blobs", "tmp"} {
				entries, err := os.ReadDir(filepath.Join(storeRoot, directory))
				if err != nil || len(entries) != 0 {
					t.Fatal("rejected content left temporary or admitted bytes")
				}
			}
		}
	})
	t.Run("historical_open", func(t *testing.T) {
		blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), mime, int64(len(body))+1)
		if err != nil {
			t.Fatal("old READY fixture seed failed")
		}
		reader, err := store.Open(context.Background(), generation.Asset{Role: role, Variant: variant, StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256, MediaType: blob.MediaType})
		if reader != nil {
			defer reader.Close()
		}
		check(t, err)
		if accepted {
			delivered, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(delivered, body) || blob.SHA256 != expectedHash {
				t.Fatal("historical delivery changed bytes or hash")
			}
		}
	})
}

func pngCompatibilityChunk(kind string, body []byte) []byte {
	result := make([]byte, len(body)+12)
	binary.BigEndian.PutUint32(result[:4], uint32(len(body)))
	copy(result[4:8], kind)
	copy(result[8:], body)
	binary.BigEndian.PutUint32(result[len(result)-4:], crc32.ChecksumIEEE(result[4:len(result)-4]))
	return result
}

func pngCompatibilityImageUnchecked(t *testing.T, chunks ...[]byte) []byte {
	t.Helper()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal("PNG oracle encoder failed")
	}
	base := imageBytes.Bytes()
	result := append([]byte(nil), base[:33]...)
	for _, chunk := range chunks {
		result = append(result, chunk...)
	}
	return append(result, base[33:]...)
}

func pngCompatibilityImage(t *testing.T, chunks ...[]byte) []byte {
	t.Helper()
	result := pngCompatibilityImageUnchecked(t, chunks...)
	for offset := 8; offset < len(result); {
		if offset+12 > len(result) {
			t.Fatal("oracle found truncated PNG frame")
		}
		end := offset + 8 + int(binary.BigEndian.Uint32(result[offset:]))
		if end+4 > len(result) || crc32.ChecksumIEEE(result[offset+4:end]) != binary.BigEndian.Uint32(result[end:]) {
			t.Fatal("oracle found invalid PNG CRC or length")
		}
		offset = end + 4
	}
	if decoded, err := png.Decode(bytes.NewReader(result)); err != nil || decoded.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatal("standard-library PNG oracle rejected fixture")
	}
	return result
}

func pngCompatibilityITXT(t *testing.T, language string, caption []byte, compressed bool) []byte {
	flag := byte(0)
	if compressed {
		flag, caption = 1, pngCompatibilityCompress(t, caption, "default")
	}
	result := append([]byte("Description\x00"), flag, 0)
	result = append(result, language...)
	result = append(result, 0)
	result = append(result, []byte("Caption\x00")...)
	return append(result, caption...)
}

func pngCompatibilityPalette(name string, depth byte, entries []byte) []byte {
	return append(append(append([]byte(name), 0), depth), entries...)
}

func pngCompatibilityPaletteEntries(depth byte) []byte {
	if depth == 8 {
		return []byte{47, 47, 97, 32, 0, 0}
	}
	return []byte{47, 47, 97, 32, 0, 0, 255, 255, 0, 0}
}

func pngCompatibilityCompressedField(kind string, wire []byte) []byte {
	if kind == "iTXt" {
		return append([]byte("Description\x00\x01\x00es-419\x00Caption\x00"), wire...)
	}
	return append([]byte("Description\x00\x00"), wire...)
}

func pngCompatibilityCompress(t *testing.T, plain []byte, mode string) []byte {
	t.Helper()
	var wire []byte
	switch mode {
	case "stored":
		wire = []byte{0x78, 0x01}
		remaining := plain
		for len(remaining) > 0 {
			count := min(len(remaining), 65535)
			final := byte(0)
			if count == len(remaining) {
				final = 1
			}
			wire = append(wire, final, byte(count), byte(count>>8), byte(^uint16(count)), byte(^uint16(count)>>8))
			wire = append(wire, remaining[:count]...)
			remaining = remaining[count:]
		}
		wire = binary.BigEndian.AppendUint32(wire, adler32.Checksum(plain))
	case "fixed":
		bits := pngCompatibilityBits{}
		bits.put(3, 3) // BFINAL=1, BTYPE=01; RFC 1951 fixed literal codes.
		for _, value := range plain {
			code, width := uint32(value)+0x30, 8
			if value >= 144 {
				code, width = uint32(value)-144+0x190, 9
			}
			bits.huffman(code, width)
		}
		bits.huffman(0, 7) // End-of-block symbol 256.
		wire = append([]byte{0x78, 0x01}, bits.finish()...)
		wire = binary.BigEndian.AppendUint32(wire, adler32.Checksum(plain))
	default:
		level := zlib.DefaultCompression
		if mode == "huffman" {
			level = zlib.HuffmanOnly
		}
		var output bytes.Buffer
		writer, err := zlib.NewWriterLevel(&output, level)
		if err != nil {
			t.Fatal("zlib oracle encoder failed")
		}
		if _, err := writer.Write(plain); err != nil {
			t.Fatal("zlib fixture write failed")
		}
		if err := writer.Close(); err != nil {
			t.Fatal("zlib fixture close failed")
		}
		wire = output.Bytes()
	}
	source := bytes.NewReader(wire)
	reader, err := zlib.NewReader(source)
	if err != nil {
		t.Fatal("independent DEFLATE fixture header is invalid")
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || source.Len() != 0 || !bytes.Equal(decoded, plain) {
		t.Fatal("independent DEFLATE fixture did not round-trip exactly")
	}
	return wire
}

type pngCompatibilityBits struct {
	body    []byte
	pending uint32
	count   uint
}

func (b *pngCompatibilityBits) put(value uint32, width uint) {
	b.pending |= value << b.count
	b.count += width
	for b.count >= 8 {
		b.body = append(b.body, byte(b.pending))
		b.pending >>= 8
		b.count -= 8
	}
}
func (b *pngCompatibilityBits) huffman(code uint32, width int) {
	var reversed uint32
	for range width {
		reversed = reversed<<1 | code&1
		code >>= 1
	}
	b.put(reversed, uint(width))
}
func (b *pngCompatibilityBits) finish() []byte {
	if b.count > 0 {
		b.body = append(b.body, byte(b.pending))
	}
	return b.body
}

func pngCompatibilityEntropy(t *testing.T) []byte {
	t.Helper()
	source := rand.New(rand.NewSource(2026100503))
	alphabet := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 ")
	plain := make([]byte, 1<<20)
	for index := range plain {
		plain[index] = alphabet[source.Intn(len(alphabet))]
	}
	wire := pngCompatibilityCompress(t, plain, "huffman")
	if bytes.Contains(plain, []byte("/")) || !bytes.Contains(wire, []byte("//")) {
		t.Fatal("entropy fixture must have only wire protocol-relative bytes")
	}
	return wire
}

func pngCompatibilityUTF16(text string, little bool) []byte {
	var result []byte
	for _, value := range []byte(text) {
		if little {
			result = append(result, value, 0)
		} else {
			result = append(result, 0, value)
		}
	}
	return result
}

func pngCompatibilityDER(tag byte, body []byte) []byte {
	header := []byte{tag}
	if len(body) < 128 {
		header = append(header, byte(len(body)))
	} else {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(body)))
		start := 0
		for length[start] == 0 {
			start++
		}
		header = append(header, 0x80|byte(8-start))
		header = append(header, length[start:]...)
	}
	return append(header, body...)
}

func pngCompatibilityDERChildren(t *testing.T, data []byte) []asn1.RawValue {
	t.Helper()
	var children []asn1.RawValue
	for len(data) > 0 {
		var child asn1.RawValue
		rest, err := asn1.Unmarshal(data, &child)
		if err != nil || len(rest) >= len(data) {
			t.Fatal("DER fixture oracle rejected child framing")
		}
		children, data = append(children, child), rest
	}
	return children
}

func pngCompatibilityCertificate(t *testing.T, serial []byte, option string) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte("PNG compatibility synthetic certificate key"))
	key := ed25519.NewKeyFromSeed(seed[:])
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic Certificate"}, NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, OCSPServer: []string{"https://oneocsp.microsoft.com/ocsp"}, SubjectKeyId: []byte{1, 2, 3, 4}}
	if option == "private_name" {
		template.Subject.CommonName = "https://provider.example/private?token=synth"
	}
	if option == "private_extension" || option == "unknown_integer_extension" {
		value := pngCompatibilityDER(0x16, []byte("//provider.example/private?token=synth"))
		if option == "unknown_integer_extension" {
			value = pngCompatibilityDER(0x02, []byte{47, 47, 97, 32})
		}
		template.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 2, 3, 4}, Value: value}}
	}
	der, err := x509.CreateCertificate(nil, template, template, key.Public(), key)
	if err != nil {
		t.Fatal("certificate fixture encoder failed")
	}
	outer := pngCompatibilityDERChildren(t, der)[0]
	fields := pngCompatibilityDERChildren(t, outer.Bytes)
	tbs := pngCompatibilityDERChildren(t, fields[0].Bytes)
	if len(tbs) < 2 || tbs[0].Class != asn1.ClassContextSpecific || tbs[1].Tag != asn1.TagInteger {
		t.Fatal("certificate fixture serial location oracle failed")
	}
	var newTBS []byte
	for index, value := range tbs {
		if index == 1 {
			newTBS = append(newTBS, pngCompatibilityDER(0x02, serial)...)
		} else {
			newTBS = append(newTBS, value.FullBytes...)
		}
	}
	newTBS = pngCompatibilityDER(0x30, newTBS)
	signature := ed25519.Sign(key, newTBS)
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), newTBS, signature) {
		t.Fatal("independent certificate signature verification failed")
	}
	bitString, err := asn1.Marshal(asn1.BitString{Bytes: signature, BitLength: len(signature) * 8})
	if err != nil {
		t.Fatal("certificate signature encoding failed")
	}
	result := append(append(bytes.Clone(newTBS), fields[1].FullBytes...), bitString...)
	return pngCompatibilityDER(0x30, result), key
}

func pngCompatibilityVerifyCertificate(t *testing.T, der []byte) {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil || !bytes.Equal(cert.Raw, der) {
		t.Fatal("independent X509 certificate oracle rejected positive fixture")
	}
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		t.Fatal("certificate signature oracle rejected positive fixture")
	}
}

func pngCompatibilityCBORHead(major byte, size int) []byte {
	if size < 24 {
		return []byte{major<<5 | byte(size)}
	}
	if size < 256 {
		return []byte{major<<5 | 24, byte(size)}
	}
	if size < 65536 {
		return []byte{major<<5 | 25, byte(size >> 8), byte(size)}
	}
	return []byte{major<<5 | 26, byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
}
func pngCompatibilityCBORBytes(body []byte) []byte {
	return append(pngCompatibilityCBORHead(2, len(body)), body...)
}
func pngCompatibilityCBORText(body string) []byte {
	return append(pngCompatibilityCBORHead(3, len(body)), body...)
}
func pngCompatibilitySegments(body []byte, split int) []byte {
	return append(append(append([]byte{0x5f}, pngCompatibilityCBORBytes(body[:split])...), pngCompatibilityCBORBytes(body[split:])...), 0xff)
}
func pngCompatibilityKnownChain(encodedValue []byte) []byte {
	protected := append([]byte{0xa2, 1, 0x27, 0x18, 0x21}, encodedValue...)
	result := append([]byte{0xd2, 0x84}, pngCompatibilityCBORBytes(protected)...)
	return append(append(result, 0xa0, 0xf6), pngCompatibilityCBORBytes([]byte{1})...)
}

func pngCompatibilityCOSE(t *testing.T, cert []byte, key ed25519.PrivateKey, certSplit, protectedSplit bool) []byte {
	t.Helper()
	encodedCert := pngCompatibilityCBORBytes(cert)
	if certSplit {
		index := bytes.Index(cert, []byte{0x02, 0x04, 47, 47, 97, 32})
		if index < 0 {
			t.Fatal("segmented certificate must split inside the real serial INTEGER")
		}
		encodedCert = pngCompatibilitySegments(cert, index+4)
	}
	protected := append([]byte{0xa2, 1, 0x27, 0x18, 0x21}, encodedCert...)
	toSign := append([]byte{0x84}, pngCompatibilityCBORText("Signature1")...)
	toSign = append(toSign, pngCompatibilityCBORBytes(protected)...)
	toSign = append(toSign, pngCompatibilityCBORBytes(nil)...)
	toSign = append(toSign, pngCompatibilityCBORBytes([]byte("synthetic detached content"))...)
	signature := ed25519.Sign(key, toSign)
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), toSign, signature) {
		t.Fatal("independent COSE signature verification failed")
	}
	encodedProtected := pngCompatibilityCBORBytes(protected)
	if protectedSplit {
		encodedProtected = pngCompatibilitySegments(protected, len(protected)/2)
	}
	result := append([]byte{0xd2, 0x84}, encodedProtected...)
	return append(append(result, 0xa0, 0xf6), pngCompatibilityCBORBytes(signature)...)
}

func pngCompatibilityCMS(t *testing.T, cert []byte, key ed25519.PrivateKey, option string) []byte {
	t.Helper()
	oid := func(value asn1.ObjectIdentifier) []byte {
		body, err := asn1.Marshal(value)
		if err != nil {
			t.Fatal("CMS OID encoding failed")
		}
		return body
	}
	dataOID := oid(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1})
	signedOID := oid(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2})
	digestAlg := pngCompatibilityDER(0x30, oid(asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}))
	signatureAlg := pngCompatibilityDER(0x30, oid(asn1.ObjectIdentifier{1, 3, 101, 112}))
	payload := []byte("synthetic CMS content")
	signature := ed25519.Sign(key, payload)
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), payload, signature) {
		t.Fatal("independent CMS signature verification failed")
	}
	identifier := []byte{1, 2, 3, 4}
	if option == "private_id" {
		identifier = []byte("//provider.example/private?token=synth")
	}
	// A version-3 SignerInfo uses the certificate's SubjectKeyIdentifier.
	// This avoids assigning numeric semantics to an unrelated issuer serial.
	signer := append(pngCompatibilityDER(0x02, []byte{3}), pngCompatibilityDER(0x80, identifier)...)
	signer = append(signer, digestAlg...)
	signer = append(signer, signatureAlg...)
	signer = append(signer, pngCompatibilityDER(0x04, signature)...)
	version := byte(3)
	if option == "bad_version" {
		version = 1
	}
	content := append(dataOID, pngCompatibilityDER(0xa0, pngCompatibilityDER(0x04, payload))...)
	signed := append(pngCompatibilityDER(0x02, []byte{version}), pngCompatibilityDER(0x31, digestAlg)...)
	signed = append(signed, pngCompatibilityDER(0x30, content)...)
	signed = append(signed, pngCompatibilityDER(0xa0, cert)...)
	signed = append(signed, pngCompatibilityDER(0x31, pngCompatibilityDER(0x30, signer))...)
	return pngCompatibilityDER(0x30, append(signedOID, pngCompatibilityDER(0xa0, pngCompatibilityDER(0x30, signed))...))
}

func pngCompatibilityBox(kind string, body []byte) []byte {
	result := make([]byte, 8, len(body)+8)
	binary.BigEndian.PutUint32(result, uint32(len(body)+8))
	copy(result[4:], kind)
	return append(result, body...)
}
func pngCompatibilityJUMBF(value []byte) []byte {
	description := append([]byte{0x63, 0x62, 0x6f, 0x72, 0, 0x11, 0, 0x10, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71, 3}, []byte("c2pa.signature\x00")...)
	return pngCompatibilityBox("jumb", append(pngCompatibilityBox("jumd", description), pngCompatibilityBox("cbor", value)...))
}

func pngCompatibilityICC(t *testing.T) []byte {
	t.Helper()
	// ICC v4 RGB matrix/TRC display profile with required independent tags.
	// https://www.color.org/specification/ICC.1-2022-05.pdf
	mluc := func(text string) []byte {
		value := make([]byte, 28)
		copy(value, "mluc")
		binary.BigEndian.PutUint32(value[8:], 1)
		binary.BigEndian.PutUint32(value[12:], 12)
		copy(value[16:], "enUS")
		binary.BigEndian.PutUint32(value[20:], uint32(len(text)*2))
		binary.BigEndian.PutUint32(value[24:], 28)
		return append(value, pngCompatibilityUTF16(text, false)...)
	}
	xyz := func(x, y, z uint32) []byte {
		value := make([]byte, 20)
		copy(value, "XYZ ")
		binary.BigEndian.PutUint32(value[8:], x)
		binary.BigEndian.PutUint32(value[12:], y)
		binary.BigEndian.PutUint32(value[16:], z)
		return value
	}
	curve := make([]byte, 16)
	copy(curve, "curv")
	binary.BigEndian.PutUint32(curve[8:], 1)
	binary.BigEndian.PutUint16(curve[12:], 0x0100)
	tags := []struct {
		name string
		body []byte
	}{{"desc", mluc("Synthetic RGB")}, {"cprt", mluc("Synthetic")}, {"wtpt", xyz(0xf6d6, 0x10000, 0xd32d)}, {"rXYZ", xyz(0x6fa2, 0x38f5, 0x390)}, {"gXYZ", xyz(0x6299, 0xb785, 0x18dc)}, {"bXYZ", xyz(0x24a0, 0xf84, 0xb6cf)}, {"rTRC", curve}, {"gTRC", curve}, {"bTRC", curve}}
	profile := make([]byte, 132+12*len(tags))
	copy(profile[4:], "synth")
	binary.BigEndian.PutUint32(profile[8:], 0x04300000)
	copy(profile[12:], "mntrRGB XYZ ")
	binary.BigEndian.PutUint16(profile[24:], 2026)
	binary.BigEndian.PutUint16(profile[26:], 10)
	binary.BigEndian.PutUint16(profile[28:], 5)
	copy(profile[36:], "acsp")
	binary.BigEndian.PutUint32(profile[68:], 0xf6d6)
	binary.BigEndian.PutUint32(profile[72:], 0x10000)
	binary.BigEndian.PutUint32(profile[76:], 0xd32d)
	binary.BigEndian.PutUint32(profile[128:], uint32(len(tags)))
	for index, tag := range tags {
		entry := 132 + 12*index
		copy(profile[entry:], tag.name)
		binary.BigEndian.PutUint32(profile[entry+4:], uint32(len(profile)))
		binary.BigEndian.PutUint32(profile[entry+8:], uint32(len(tag.body)))
		profile = append(profile, tag.body...)
		for len(profile)%4 != 0 {
			profile = append(profile, 0)
		}
	}
	binary.BigEndian.PutUint32(profile, uint32(len(profile)))
	return profile
}

func pngCompatibilityWrappers(t *testing.T, raster []byte, accepted, small bool) {
	t.Helper()
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		if small && method == zip.Deflate {
			continue
		}
		t.Run(fmt.Sprintf("zip_%d", method), func(t *testing.T) {
			var output bytes.Buffer
			writer := zip.NewWriter(&output)
			entry, err := writer.CreateHeader(&zip.FileHeader{Name: "image.png", Method: method})
			if err != nil {
				t.Fatal("ZIP fixture initialization failed")
			}
			if _, err = entry.Write(raster); err != nil {
				t.Fatal("ZIP fixture write failed")
			}
			if err = writer.Close(); err != nil {
				t.Fatal("ZIP fixture close failed")
			}
			pngCompatibilityDelivery(t, output.Bytes(), "zip", "application/zip", "model", accepted)
		})
	}
	if !small {
		t.Run("gltf_inline_image", func(t *testing.T) {
			document := map[string]any{"asset": map[string]any{"version": "2.0"}, "images": []any{map[string]any{"uri": "data:image/png;base64," + base64.StdEncoding.EncodeToString(raster), "mimeType": "image/png"}}}
			body, err := json.Marshal(document)
			if err != nil {
				t.Fatal("glTF fixture encoding failed")
			}
			pngCompatibilityDelivery(t, body, "gltf", "model/gltf+json", "model", accepted)
		})
	}
	t.Run("glb_image_buffer_view", func(t *testing.T) {
		bin := append(make([]byte, 4), raster...)
		document := map[string]any{"asset": map[string]any{"version": "2.0"}, "buffers": []any{map[string]any{"byteLength": len(bin)}}, "bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 4, "byteLength": len(raster)}}, "images": []any{map[string]any{"bufferView": 0, "mimeType": "image/png"}}}
		jsonBody, err := json.Marshal(document)
		if err != nil {
			t.Fatal("GLB fixture JSON failed")
		}
		for len(jsonBody)%4 != 0 {
			jsonBody = append(jsonBody, ' ')
		}
		for len(bin)%4 != 0 {
			bin = append(bin, 0)
		}
		body := make([]byte, 12)
		copy(body, "glTF")
		binary.LittleEndian.PutUint32(body[4:], 2)
		binary.LittleEndian.PutUint32(body[8:], uint32(12+8+len(jsonBody)+8+len(bin)))
		body = binary.LittleEndian.AppendUint32(body, uint32(len(jsonBody)))
		body = append(body, []byte("JSON")...)
		body = append(body, jsonBody...)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(bin)))
		body = append(body, []byte{'B', 'I', 'N', 0}...)
		body = append(body, bin...)
		pngCompatibilityDelivery(t, body, "glb", "model/gltf-binary", "model", accepted)
	})
	if !small {
		t.Run("fbx_raw_image", func(t *testing.T) {
			pngCompatibilityDelivery(t, pngCompatibilityFBX(raster), "fbx", "application/octet-stream", "model", accepted)
		})
	}
}

func pngCompatibilityFBX(raster []byte) []byte {
	property := append([]byte{'R'}, make([]byte, 4)...)
	binary.LittleEndian.PutUint32(property[1:], uint32(len(raster)))
	property = append(property, raster...)
	body := append([]byte("Kaydara FBX Binary  \x00\x1a\x00"), 0xe8, 0x1c, 0, 0)
	start := len(body)
	header := make([]byte, 13)
	binary.LittleEndian.PutUint32(header, uint32(start+13+len("Content")+len(property)))
	binary.LittleEndian.PutUint32(header[4:], 1)
	binary.LittleEndian.PutUint32(header[8:], uint32(len(property)))
	header[12] = byte(len("Content"))
	body = append(body, header...)
	body = append(body, "Content"...)
	body = append(body, property...)
	body = append(body, make([]byte, 13)...)
	body = append(body, []byte{0xfa, 0xbc, 0xab, 9, 0xd0, 0xc8, 0xd4, 0x66, 0xb1, 0x76, 0xfb, 0x83, 0x1c, 0xf7, 0x26, 0x7e}...)
	body = append(body, make([]byte, 4)...)
	body = append(body, make([]byte, 16-len(body)%16)...)
	body = append(body, 0xe8, 0x1c, 0, 0)
	body = append(body, make([]byte, 120)...)
	body = append(body, []byte{0xf8, 0x5a, 0x8c, 0x6a, 0xde, 0xf5, 0xd9, 0x7e, 0xec, 0xe9, 0x0c, 0xe3, 0x75, 0x8f, 0x29, 0x0b}...)
	return body
}

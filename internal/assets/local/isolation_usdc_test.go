package local

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func usdcFixture(t *testing.T, name string) []byte {
	t.Helper()
	d, e := os.ReadFile(filepath.Join("testdata", "usdc", name+".usdc"))
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func usdcFixtureReps(t *testing.T, name string) []uint64 {
	t.Helper()
	d, e := os.ReadFile(filepath.Join("testdata", "usdc", name+".reps.json"))
	if e != nil {
		t.Fatal(e)
	}
	var v []uint64
	if e = json.Unmarshal(d, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func usdcFixtureTokens(t *testing.T, name string) []string {
	t.Helper()
	d, e := os.ReadFile(filepath.Join("testdata", "usdc", name+".tokens.json"))
	if e != nil {
		t.Fatal(e)
	}
	var v []string
	if e = json.Unmarshal(d, &v); e != nil {
		t.Fatal(e)
	}
	return v
}

// Fixture transformations write literal LZ4 blocks. They do not use the
// production decoder to invent either wire bytes or expected outcomes.
func usdcTestLiterals(body []byte) []byte {
	result := []byte{0, byte(min(len(body), 15) << 4)}
	if len(body) >= 15 {
		n := len(body) - 15
		for n >= 255 {
			result = append(result, 255)
			n -= 255
		}
		result = append(result, byte(n))
	}
	return append(result, body...)
}
func usdcTestUint64(v uint64) []byte {
	d := make([]byte, 8)
	binary.LittleEndian.PutUint64(d, v)
	return d
}

func usdcTestUTF16(text string) []byte {
	b := make([]byte, 0, len(text)*2)
	for _, c := range []byte(text) {
		b = append(b, c, 0)
	}
	return b
}
func usdcTestSections(d []byte) []usdcSection {
	toc := int(binary.LittleEndian.Uint64(d[16:24]))
	s := make([]usdcSection, 6)
	for i := range s {
		at := toc + 8 + i*32
		name := d[at : at+16]
		name = name[:bytes.IndexByte(name, 0)]
		start := int64(binary.LittleEndian.Uint64(d[at+16:]))
		n := int64(binary.LittleEndian.Uint64(d[at+24:]))
		s[i] = usdcSection{string(name), start, start + n}
	}
	return s
}
func usdcTestRewrite(d []byte, name string, body, unclassified []byte) []byte {
	sections := usdcTestSections(d)
	result := append([]byte(nil), d[:sections[0].start]...)
	result = append(result, unclassified...)
	for i := range sections {
		s := &sections[i]
		content := d[s.start:s.end]
		if s.name == name {
			content = body
		}
		s.start = int64(len(result))
		result = append(result, content...)
		s.end = int64(len(result))
	}
	toc := len(result)
	result = append(result, usdcTestUint64(6)...)
	for _, s := range sections {
		record := make([]byte, 32)
		copy(record, s.name)
		binary.LittleEndian.PutUint64(record[16:], uint64(s.start))
		binary.LittleEndian.PutUint64(record[24:], uint64(s.end-s.start))
		result = append(result, record...)
	}
	binary.LittleEndian.PutUint64(result[16:], uint64(toc))
	return result
}
func usdcTestReps(d []byte, reps []uint64) []byte {
	var fields usdcSection
	for _, s := range usdcTestSections(d) {
		if s.name == "FIELDS" {
			fields = s
		}
	}
	count := binary.LittleEndian.Uint64(d[fields.start:])
	compressed := binary.LittleEndian.Uint64(d[fields.start+8:])
	prefix := append([]byte(nil), d[fields.start:fields.start+16+int64(compressed)]...)
	binary.LittleEndian.PutUint64(prefix, uint64(len(reps)))
	if uint64(len(reps)) != count {
		panic("test rep count must remain unchanged")
	}
	plain := []byte{}
	for _, v := range reps {
		plain = append(plain, usdcTestUint64(v)...)
	}
	wire := usdcTestLiterals(plain)
	prefix = append(prefix, usdcTestUint64(uint64(len(wire)))...)
	prefix = append(prefix, wire...)
	return usdcTestRewrite(d, "FIELDS", prefix, nil)
}
func usdcTestTokens(d []byte, tokens []string) []byte {
	plain := []byte{}
	for _, s := range tokens {
		plain = append(plain, []byte(s)...)
		plain = append(plain, 0)
	}
	wire := usdcTestLiterals(plain)
	body := append(usdcTestUint64(uint64(len(tokens))), usdcTestUint64(uint64(len(plain)))...)
	body = append(body, usdcTestUint64(uint64(len(wire)))...)
	body = append(body, wire...)
	return usdcTestRewrite(d, "TOKENS", body, nil)
}

func usdcTestZIP(t *testing.T, model []byte, texture bool, directory bool) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, item := range []struct {
		name string
		body []byte
	}{{"model.usdc", model}, {"texture.png", inlineTestPNG(t, "")}} {
		if item.name != "model.usdc" && !texture && !directory {
			continue
		}
		h := zip.FileHeader{Name: item.name, Method: zip.Store}
		if item.name != "model.usdc" && directory {
			h.SetMode(os.ModeDir | 0755)
			item.body = nil
		}
		if e := z.Flush(); e != nil {
			t.Fatal(e)
		}
		padding := (64 - (b.Len()+30+len(h.Name)+4)%64) % 64
		h.Extra = make([]byte, 4+padding)
		binary.LittleEndian.PutUint16(h.Extra, 1983)
		binary.LittleEndian.PutUint16(h.Extra[2:], uint16(padding))
		h.CRC32 = crc32.ChecksumIEEE(item.body)
		h.UncompressedSize64 = uint64(len(item.body))
		h.CompressedSize64 = uint64(len(item.body))
		w, e := z.CreateRaw(&h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(item.body); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func usdcDelivery(t *testing.T, data []byte, variant string, accepted bool) {
	t.Helper()
	ctx := context.Background()
	store, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	w := generation.AssetWrite{AssetID: "usdc-synthetic-asset", OwnerID: "usdc-synthetic-owner", TaskID: "usdc-synthetic-task", AttemptID: "usdc-synthetic-attempt", Role: "converted_model", Variant: variant, MediaType: "application/octet-stream", MaxBytes: int64(len(data)) + 1, Body: bytes.NewReader(data)}
	f, e := os.CreateTemp(t.TempDir(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(data); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Seek(0, 0); e != nil {
		t.Fatal(e)
	}
	e = ValidateDeliveredFile(f, w.Role, variant, w.MediaType)
	f.Close()
	if (e == nil) != accepted {
		t.Fatalf("Validate accepted=%v want=%v: %v", e == nil, accepted, e)
	}
	asset, e := store.Put(ctx, w)
	if (e == nil) != accepted {
		t.Fatalf("Put accepted=%v want=%v: %v", e == nil, accepted, e)
	}
	if accepted {
		digest := sha256.Sum256(data)
		if asset.SHA256 != hex.EncodeToString(digest[:]) || asset.Size != int64(len(data)) {
			t.Fatal("Put changed fixture hash/size")
		}
		r, e := store.Open(ctx, asset)
		if e != nil {
			t.Fatalf("Open stored asset: %v", e)
		}
		got, e := io.ReadAll(r)
		r.Close()
		if e != nil || !bytes.Equal(got, data) {
			t.Fatal("Open stored asset changed bytes")
		}
	}
	// Historical delivery uses a verified blob created through the older blob
	// API, so a Put rejection cannot make the Open negative test vacuous.
	blob, e := store.PutBlob(ctx, bytes.NewReader(data), w.MediaType, w.MaxBytes)
	if e != nil {
		t.Fatal(e)
	}
	legacy := generation.Asset{StorageKey: blob.StorageKey, SHA256: blob.SHA256, Size: blob.Size, Role: w.Role, Variant: variant, MediaType: w.MediaType}
	r, e := store.Open(ctx, legacy)
	if (e == nil) != accepted {
		t.Fatalf("Open accepted=%v want=%v: %v", e == nil, accepted, e)
	}
	if e == nil {
		got, e := io.ReadAll(r)
		r.Close()
		if e != nil || !bytes.Equal(got, data) {
			t.Fatal("Open changed bytes")
		}
	}
}

func TestUSDCDeliveryOfficialSyntheticNumericAndTypes(t *testing.T) {
	for _, name := range []string{"finite_vec2f", "typed", "inline_vec4f", "nonfinite"} {
		t.Run(name, func(t *testing.T) {
			d := usdcFixture(t, name)
			usdcDelivery(t, d, "usdc", true)
			usdcDelivery(t, usdcTestZIP(t, d, false, false), "usdz", true)
		})
	}
}

// Exercise both scalar encodings and the fixed-width vector/matrix array path.
// Expected bit patterns and widths are independent of the production walker.
func TestUSDCWeakNumericDeliveryPreservesIEEEValues(t *testing.T) {
	for _, number := range []struct {
		name   string
		bits32 uint32
		bits64 uint64
	}{
		{"finite", 0x3fc00000, 0x3ff8000000000000},
		{"nan", 0x7fc00001, 0x7ff8000000000001},
		{"positive_infinity", 0x7f800000, 0x7ff0000000000000},
		{"negative_infinity", 0xff800000, 0xfff0000000000000},
	} {
		for _, field := range []struct {
			name        string
			typ         uint8
			width, word int
			inline      bool
		}{
			{"inline_float", 8, 4, 4, true},
			{"inline_double", 9, 4, 4, true}, // Inline Double uses a float32 word.
			{"float", 8, 4, 4, false},
			{"double", 9, 8, 8, false},
			{"vec2f", 20, 8, 4, false},
			{"vec4f", 28, 16, 4, false},
			{"vec4d", 27, 32, 8, false},
			{"matrix4d", 15, 128, 8, false},
		} {
			for _, array := range []bool{false, true} {
				if field.inline && array {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/array_%v", number.name, field.name, array), func(t *testing.T) {
					d := usdcFixture(t, "typed")
					reps := usdcFixtureReps(t, "typed")
					rep := uint64(field.typ) << 48
					if field.inline {
						rep |= 1<<62 | uint64(number.bits32)
					} else {
						rep |= uint64(usdcTestSections(d)[0].start)
						count := 1
						var body []byte
						if array {
							rep |= 1 << 63
							count = 2
							body = usdcTestUint64(uint64(count))
						}
						for i := 0; i < count*field.width/field.word; i++ {
							word := make([]byte, field.word)
							if field.word == 4 {
								binary.LittleEndian.PutUint32(word, number.bits32)
							} else {
								binary.LittleEndian.PutUint64(word, number.bits64)
							}
							body = append(body, word...)
						}
						d = usdcTestRewrite(d, "", nil, body)
					}
					replaced := false
					for i, v := range reps {
						if uint8(v>>48) == 8 && v&(1<<62) != 0 {
							reps[i] = rep
							replaced = true
							break
						}
					}
					if !replaced {
						t.Fatal("fixture has no inline Float")
					}
					d = usdcTestReps(d, reps)
					usdcDelivery(t, d, "usdc", true)
					usdcDelivery(t, usdcTestZIP(t, d, false, false), "usdz", true)
				})
			}
		}
	}
}

func TestUSDCLocalDependenciesRequireFiles(t *testing.T) {
	d := usdcFixture(t, "local_dependency")
	for _, c := range []struct {
		name    string
		data    []byte
		variant string
		ok      bool
	}{{"standalone", d, "usdc", false}, {"missing", usdcTestZIP(t, d, false, false), "usdz", false}, {"directory", usdcTestZIP(t, d, false, true), "usdz", false}, {"file", usdcTestZIP(t, d, true, false), "usdz", true}} {
		t.Run(c.name, func(t *testing.T) { usdcDelivery(t, c.data, c.variant, c.ok) })
	}
	for _, uri := range []string{"https://private.invalid/a", "//private.invalid/a", "../texture.png", "%2f%2fprivate.invalid/a", "texture%2epng", "texture.png[other.usdc]", "texture.exr"} {
		t.Run(uri, func(t *testing.T) {
			tokens := usdcFixtureTokens(t, "local_dependency")
			for i, s := range tokens {
				if s == "texture.png" {
					tokens[i] = uri
				}
			}
			usdcDelivery(t, usdcTestZIP(t, usdcTestTokens(d, tokens), true, false), "usdz", false)
		})
	}
	t.Run("gltf_directory_image", func(t *testing.T) {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, e := z.Create("model.gltf")
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte("{\"asset\":{\"version\":\"2.0\"},\"images\":[{\"uri\":\"texture.png\"}]}"))
		h := zip.FileHeader{Name: "texture.png"}
		h.SetMode(os.ModeDir | 0755)
		if _, e = z.CreateHeader(&h); e != nil {
			t.Fatal(e)
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		usdcDelivery(t, b.Bytes(), "usdz", false)
	})
}

func TestUSDCTextAndAbsoluteWireRemainRejected(t *testing.T) {
	for _, name := range []string{"private_text", "encoded_text", "deep_dictionary"} {
		t.Run(name, func(t *testing.T) { usdcDelivery(t, usdcFixture(t, name), "usdc", false) })
	}
	d := usdcFixture(t, "finite_vec2f")
	reps := usdcFixtureReps(t, "finite_vec2f")
	for _, rep := range reps {
		if rep>>63 != 0 && uint8(rep>>48) == 20 {
			mut := append([]byte(nil), d...)
			at := int(rep&((1<<48)-1)) + 8
			copy(mut[at:at+8], "https://")
			t.Run("absolute_numeric", func(t *testing.T) { usdcDelivery(t, mut, "usdc", false) })
		}
	}
	for name, body := range map[string][]byte{"plain_neighbor": []byte(" //private.invalid/a"), "percent_neighbor": []byte("%68%74%74%70%73%3a%2f%2fprivate.invalid/a"), "utf16_neighbor": usdcTestUTF16("https://private.invalid/a")} {
		t.Run(name, func(t *testing.T) { usdcDelivery(t, usdcTestRewrite(d, "", nil, body), "usdc", false) })
	}
}

func TestUSDCSharedWorkNeverResets(t *testing.T) {
	d := usdcFixture(t, "typed")
	work := newInspectionWorkBudget()
	if e := inspectUSDC(bytes.NewReader(d), int64(len(d)), nil, "", work); e != nil {
		t.Fatal(e)
	}
	cost := maxInspectionWork - work.remaining
	work = &inspectionWorkBudget{remaining: 2*cost - 1}
	if e := inspectUSDC(bytes.NewReader(d), int64(len(d)), nil, "", work); e != nil {
		t.Fatal(e)
	}
	if e := inspectUSDC(bytes.NewReader(d), int64(len(d)), nil, "", work); e == nil {
		t.Fatal("replay reset shared budget")
	}
}

func TestUSDCMalformedStructuresFailClosed(t *testing.T) {
	base := usdcFixture(t, "finite_vec2f")
	mutations := map[string]func([]byte) []byte{
		"version":            func(d []byte) []byte { d[9] = 9; return d },
		"magic":              func(d []byte) []byte { d[0] = 0; return d },
		"reserved_bootstrap": func(d []byte) []byte { d[24] = 1; return d },
		"toc_overflow":       func(d []byte) []byte { binary.LittleEndian.PutUint64(d[16:], ^uint64(0)); return d },
		"toc_count": func(d []byte) []byte {
			toc := binary.LittleEndian.Uint64(d[16:])
			binary.LittleEndian.PutUint64(d[toc:], 7)
			return d
		},
		"unknown_section": func(d []byte) []byte { toc := binary.LittleEndian.Uint64(d[16:]); copy(d[toc+8:], "UNSEEN"); return d },
		"overlapping_sections": func(d []byte) []byte {
			toc := binary.LittleEndian.Uint64(d[16:])
			copy(d[toc+8+32+16:toc+8+32+24], d[toc+8+16:toc+8+24])
			return d
		},
		"truncation": func(d []byte) []byte { return d[:len(d)-1] },
		"trailing":   func(d []byte) []byte { return append(d, 0) },
		"token_bomb": func(d []byte) []byte {
			s := usdcTestSections(d)[0]
			binary.LittleEndian.PutUint64(d[s.start+8:], maxModelJSONBytes+1)
			return d
		},
		"field_count": func(d []byte) []byte {
			for _, s := range usdcTestSections(d) {
				if s.name == "FIELDS" {
					binary.LittleEndian.PutUint64(d[s.start:], maxArchiveFiles+1)
				}
			}
			return d
		},
		"token_compression_tail": func(d []byte) []byte {
			s := usdcTestSections(d)[0]
			b := append([]byte(nil), d[s.start:s.end]...)
			binary.LittleEndian.PutUint64(b[16:], binary.LittleEndian.Uint64(b[16:])+1)
			b = append(b, 0)
			return usdcTestRewrite(d, "TOKENS", b, nil)
		},
		"token_lz4_bomb": func(d []byte) []byte {
			b := append(usdcTestUint64(1), usdcTestUint64(1)...)
			wire := []byte{0, 0x1f, 'A', 1, 0, 255, 255, 0, 0}
			b = append(b, usdcTestUint64(uint64(len(wire)))...)
			b = append(b, wire...)
			return usdcTestRewrite(d, "TOKENS", b, nil)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) { usdcDelivery(t, mutate(append([]byte(nil), base...)), "usdc", false) })
	}
	// "unknown_type" and "unsupported_compression" no longer fail on the rep
	// itself: such values stay opaque and unclaimed, so this fixture's Vec2f
	// bytes reach the relative-address scanner and are rejected there.
	for name, mutate := range map[string]func(uint64) uint64{
		"unknown_type":            func(v uint64) uint64 { return v&^(uint64(255)<<48) | uint64(255)<<48 },
		"reserved_flag":           func(v uint64) uint64 { return v | 1<<56 },
		"array_edit_flag":         func(v uint64) uint64 { return v | 1<<60 },
		"array_inline":            func(v uint64) uint64 { return v | 1<<62 },
		"unsupported_compression": func(v uint64) uint64 { return v | 1<<61 },
		"offset_in_section":       func(v uint64) uint64 { return v&^((1<<48)-1) | uint64(usdcTestSections(base)[0].start) },
		"count_overflow":          func(v uint64) uint64 { return v&^((1<<48)-1) | 88 },
	} {
		t.Run(name, func(t *testing.T) {
			reps := usdcFixtureReps(t, "finite_vec2f")
			for i, v := range reps {
				if v>>63 != 0 && uint8(v>>48) == 20 {
					reps[i] = mutate(v)
					break
				}
			}
			d := usdcTestReps(base, reps)
			if name == "count_overflow" {
				binary.LittleEndian.PutUint64(d[88:], ^uint64(0))
			}
			usdcDelivery(t, d, "usdc", false)
		})
	}
	t.Run("overlapping_values", func(t *testing.T) {
		reps := usdcFixtureReps(t, "finite_vec2f")
		var payload uint64
		for _, v := range reps {
			if v>>63 != 0 && uint8(v>>48) == 20 {
				payload = v & ((1 << 48) - 1)
			}
		}
		for i, v := range reps {
			if uint8(v>>48) == 42 {
				reps[i] = uint64(8)<<48 | payload
				break
			}
		}
		usdcDelivery(t, usdcTestReps(base, reps), "usdc", false)
	})
	t.Run("dictionary_cycle", func(t *testing.T) {
		d := usdcFixture(t, "typed")
		for _, v := range usdcFixtureReps(t, "typed") {
			if uint8(v>>48) == 31 && v&(1<<62) == 0 {
				at := int(v & ((1 << 48) - 1))
				jump := at + 12
				target := jump + int(binary.LittleEndian.Uint64(d[jump:]))
				binary.LittleEndian.PutUint64(d[target:], v)
				break
			}
		}
		usdcDelivery(t, d, "usdc", false)
	})
	t.Run("archive_crc", func(t *testing.T) {
		d := usdcTestZIP(t, base, false, false)
		r, e := zip.NewReader(bytes.NewReader(d), int64(len(d)))
		if e != nil {
			t.Fatal(e)
		}
		at, e := r.File[0].DataOffset()
		if e != nil {
			t.Fatal(e)
		}
		d[at+24] ^= 1
		usdcDelivery(t, d, "usdz", false)
	})
}

func TestUSDCCompressedTokenOriginsAreInspected(t *testing.T) {
	d := usdcFixture(t, "typed")
	tokens := usdcFixtureTokens(t, "typed")
	plain := []byte{}
	for _, s := range tokens {
		plain = append(plain, []byte(s)...)
		plain = append(plain, 0)
	}
	first := len(plain)
	plain = append(plain, []byte("https:")...)
	plain = append(plain, 0)
	second := len(plain)
	plain = append(plain, []byte("/private.invalid/a")...)
	plain = append(plain, 0)
	// Construct the final private token through two backreferences. Neither
	// HTTP(S) nor a protocol-relative address appears in the delivered bytes.
	wire := usdcTestLiterals(plain)
	wire[1] |= 2
	distance := len(plain) - first
	wire = append(wire, byte(distance), byte(distance>>8))
	wire = append(wire, 0x1a, '/')
	distance = len(plain) + 7 - second
	wire = append(wire, byte(distance), byte(distance>>8))
	wire = append(wire, 0x50, 'i', 'd', '/', 'a', 0)
	decodedSize := len(plain) + len("https://private.invalid/a") + 1
	body := append(usdcTestUint64(uint64(len(tokens)+3)), usdcTestUint64(uint64(decodedSize))...)
	body = append(body, usdcTestUint64(uint64(len(wire)))...)
	body = append(body, wire...)
	d = usdcTestRewrite(d, "TOKENS", body, nil)
	if e := scanExternalAddresses(bytes.NewReader(d)); e != nil {
		t.Fatalf("independent wire control already rejected: %v", e)
	}
	usdcDelivery(t, d, "usdc", false)
}

func TestUSDCUnsupportedLayerPathAndEncodingFraming(t *testing.T) {
	d := usdcFixture(t, "finite_vec2f")
	for _, base := range []string{"../bad%2fmodel.usdc", "folder\\model.usdc", "other.usdz[layer.usdc]"} {
		if e := inspectUSDC(bytes.NewReader(d), int64(len(d)), nil, base, newInspectionWorkBudget()); e == nil {
			t.Fatal("unsupported layer path accepted")
		}
	}
	// A decodable but noncanonical stream must not grant an encoded span.
	for _, wire := range [][]byte{{0, 0x10, 'A', 1, 0, 0x10, 0}, {1, 2, 0, 0, 0, 0x10, 'A', 0}, {0, 0x00, 0x01, 0}} {
		if _, e := decodeUSDCFast(wire, 64, newInspectionWorkBudget()); e == nil {
			t.Fatal("invalid LZ4 framing accepted")
		}
	}
}

func TestUSDCArchiveRastersShareOneWorkBudget(t *testing.T) {
	// Each tiny, valid PNG has at least a fixed 64 KiB codec work allowance, so
	// the 512 MiB floor holds about 8000 of them. Reusing identical bytes under distinct names must not create a fresh
	// budget per member.
	makeArchive := func(count int) []byte {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, e := z.Create("model.usdc")
		if e != nil {
			t.Fatal(e)
		}
		w.Write(usdcFixture(t, "finite_vec2f"))
		image := inlineTestPNG(t, "")
		for i := 0; i < count; i++ {
			w, e = z.Create(fmt.Sprintf("texture-%03d.png", i))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = w.Write(image); e != nil {
				t.Fatal(e)
			}
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		return b.Bytes()
	}
	t.Run("small_archive", func(t *testing.T) { usdcDelivery(t, makeArchive(2), "usdz", true) })
	t.Run("cumulative_limit", func(t *testing.T) {
		d := makeArchive(8300)
		f, e := os.CreateTemp(t.TempDir(), "fixture")
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		if _, e = f.Write(d); e != nil {
			t.Fatal(e)
		}
		if _, e = f.Seek(0, 0); e != nil {
			t.Fatal(e)
		}
		e = ValidateDeliveredFile(f, "converted_model", "usdz", "application/zip")
		if e == nil || !strings.Contains(e.Error(), inspectionWorkError().Error()) {
			t.Fatalf("cumulative budget rejection missing: %v", e)
		}
		usdcDelivery(t, d, "usdz", false)
	})
}

func TestUSDCTypedBoundaryPreservesDeliveredAddressChecks(t *testing.T) {
	// The oracle performs the previous complete space mask. The boundary
	// reduction must give the same rejection at short, carry and read seams.
	texts := [][]byte{
		[]byte("./texture.png"), []byte(" //private.invalid/a"),
		[]byte("%2f%2fprivate.invalid/a"), []byte(`\u002f\u002fprivate.invalid/a`),
		usdcTestUTF16(" //private.invalid/a"),
		[]byte("https://private.invalid/a"),
	}
	for _, length := range []int{1, 2, 4095, 4096, 4097, 65535, 65536, 65537, 131073} {
		for _, prefix := range []int{1, 2, isolationChunkBytes - 3} {
			for index, text := range texts {
				t.Run(fmt.Sprintf("run_%d_prefix_%d_text_%d", length, prefix, index), func(t *testing.T) {
					d := bytes.Repeat([]byte{'x'}, prefix+length)
					d = append(d, text...)
					mask := append([]byte(nil), d...)
					for i := prefix; i < prefix+length; i++ {
						mask[i] = ' '
					}
					wantReject := containsAddress(d, absoluteAddress) || scanExternalAddresses(bytes.NewReader(mask)) != nil
					p := usdcParser{data: d, work: newInspectionWorkBudget(), plan: inspectionPlan{numeric: []inspectionSpan{{int64(prefix), int64(prefix + length)}}}}
					wireError := readUSDCWire(bytes.NewReader(d), make([]byte, len(d)))
					if got := wireError != nil || p.inspectRelativeAddresses() != nil; got != wantReject {
						t.Fatalf("boundary rejection=%v complete-mask rejection=%v", got, wantReject)
					}
				})
			}
		}
	}
	// A complete wire address can cross two numeric/text boundaries. Neither
	// the byte-level absolute check nor encoded absolute checks may be masked.
	for _, d := range [][]byte{[]byte("https://private.invalid/a"), usdcTestUTF16("https://private.invalid/a"), []byte("https:%2f%2fprivate.invalid/a")} {
		p := usdcParser{data: d, work: newInspectionWorkBudget(), plan: inspectionPlan{numeric: []inspectionSpan{{2, int64(len(d) - 2)}}}}
		if readUSDCWire(bytes.NewReader(d), make([]byte, len(d))) == nil && p.inspectRelativeAddresses() == nil {
			t.Fatal("absolute wire address across typed boundaries accepted")
		}
	}
}

func TestUSDCAbsoluteWireInspectedDuringAcquisition(t *testing.T) {
	for _, text := range [][]byte{[]byte("https://private.invalid/a"), []byte("https:%2f%2fprivate.invalid/a"), usdcTestUTF16("https://private.invalid/a")} {
		for _, prefix := range []int{1, isolationChunkBytes - 3, isolationChunkBytes - 1} {
			d := append(bytes.Repeat([]byte{'x'}, prefix), text...)
			if e := readUSDCWire(bytes.NewReader(d), make([]byte, len(d)+isolationChunkBytes)); e == nil || !strings.Contains(e.Error(), "外部地址") {
				t.Fatalf("wire address was not rejected before trailing truncation: %v", e)
			}
		}
	}
	if e := readUSDCWire(bytes.NewReader([]byte("safe")), make([]byte, isolationChunkBytes)); e == nil || !strings.Contains(e.Error(), "截断") {
		t.Fatalf("partial wire was accepted: %v", e)
	}
}

// Fixed-size crate values beyond the float vectors (integer and half vectors,
// quaternions, 64-bit integers) are claimed by their width and cached as delivered.
func TestUSDCFixedWidthValueTypesAreAccepted(t *testing.T) {
	base := usdcFixture(t, "finite_vec2f")
	// Each of these has the 8-byte element width of the fixture's Vec2f array.
	for _, typ := range []uint8{5, 6, 18, 22, 29} {
		t.Run(fmt.Sprint(typ), func(t *testing.T) {
			reps := usdcFixtureReps(t, "finite_vec2f")
			retyped := false
			for i, v := range reps {
				if v>>63 != 0 && uint8(v>>48) == 20 {
					reps[i] = v&^(uint64(255)<<48) | uint64(typ)<<48
					retyped = true
					break
				}
			}
			if !retyped {
				t.Fatal("fixture has no Vec2f array")
			}
			usdcDelivery(t, usdcTestReps(base, reps), "usdc", true)
		})
	}
}

// A value type this walker does not decode (here TimeSamples) is kept opaque:
// the file is accepted when its unclaimed bytes carry no external address.
func TestUSDCOpaqueValueTypesAreAccepted(t *testing.T) {
	base := usdcFixture(t, "typed")
	reps := usdcFixtureReps(t, "typed")
	retyped := false
	for i, v := range reps {
		if v>>63 == 0 && v&(1<<62) == 0 && uint8(v>>48) == 9 {
			reps[i] = v&^(uint64(255)<<48) | uint64(46)<<48
			retyped = true
			break
		}
	}
	if !retyped {
		t.Fatal("fixture has no out-of-line Double")
	}
	usdcDelivery(t, usdcTestReps(base, reps), "usdc", true)
}

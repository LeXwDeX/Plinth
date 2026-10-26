package local

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #330 (dev-test acceptance, note 99526): a Meshy keychain GLB embeds a
// 1024x1024 PNG whose image bufferView counts one alignment space (0x20) after
// the IEND chunk. The GLB itself passes (#189 strips that view padding), but
// Meshy Convert copies the image bytes, padding included, into its FBX and
// USDZ, and the local glTF package writes them as a standalone texture file.
// The raster check then refused all three as png_invalid_structure, after the
// paid Convert had finished. Decoders (libpng, browsers, Go image/png) stop at
// IEND/EOI and ignore such padding.
//
// The tolerated trailer is bounded: at most maxRasterPaddingBytes bytes, each
// NUL or space (alignment padding). Anything else after the end marker is
// still refused (raster_trailing_data): arbitrary trailing bytes could carry
// another container's end record (a ZIP end-of-central-directory, whose
// directory may sit inside a PNG text chunk), i.e. a polyglot. The padding is
// not admitted as image bytes: it stays outside the allowed span.

func paddedRaster330(t *testing.T, format string, padding []byte) []byte {
	t.Helper()
	body := inlineTestPNG(t, "")
	if format == "jpeg" {
		body = rasterNoiseFixture(t, "jpeg")
	}
	return append(bytes.Clone(body), padding...)
}

func TestRasterAlignmentPaddingAfterEndMarkerIsTolerated330(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		for name, padding := range map[string][]byte{
			"one_space":    {' '},
			"one_nul":      {0},
			"mixed":        {' ', 0, ' '},
			"limit_spaces": bytes.Repeat([]byte{' '}, maxRasterPaddingBytes),
		} {
			t.Run(format+"/"+name, func(t *testing.T) {
				body := paddedRaster330(t, format, padding)
				plan, err := validateRasterBytes(body, format)
				if err != nil {
					t.Fatalf("padded %s refused: %v", format, err)
				}
				imageEnd := int64(len(body) - len(padding))
				if len(plan.allowed) != 1 || plan.allowed[0].start != 0 || plan.allowed[0].end != imageEnd || len(plan.numeric) != 0 {
					t.Fatalf("padding must stay outside the admitted image: allowed=%v numeric=%d", plan.allowed, len(plan.numeric))
				}
				rasterCheckDeliveryPaths(t, body, format, false)
			})
		}
	}
}

func TestRasterTrailingDataOtherThanPaddingIsRefused330(t *testing.T) {
	eocd := make([]byte, 22) // A ZIP end-of-central-directory record.
	copy(eocd, "PK\x05\x06")
	for _, format := range []string{"png", "jpeg"} {
		for name, trailer := range map[string][]byte{
			"text":           []byte("benign trailer"),
			"zip_end_record": eocd,
			"over_limit":     bytes.Repeat([]byte{' '}, maxRasterPaddingBytes+1),
			"padding_then_x": append(bytes.Repeat([]byte{' '}, 3), 'x'),
		} {
			t.Run(format+"/"+name, func(t *testing.T) {
				body := paddedRaster330(t, format, trailer)
				plan, err := validateRasterBytes(body, format)
				var rejected *inspectionRejection
				if err == nil || !errors.As(err, &rejected) || rejected.reason != "raster_trailing_data" {
					t.Fatalf("trailing data must be refused as raster_trailing_data, got %v", err)
				}
				if len(plan.allowed) != 0 || len(plan.numeric) != 0 {
					t.Fatal("refused image received an inspection waiver")
				}
				rasterCheckDeliveryPaths(t, body, format, true)
			})
		}
	}
}

// The real failure: the padded PNG embedded in an FBX texture (Video.Content)
// and stored as a USDZ member. Both must now be delivered.
func TestPaddedPNGInsideFBXAndUSDZIsDelivered330(t *testing.T) {
	image := paddedRaster330(t, "png", []byte{' '})
	for _, version := range []uint32{7400, 7500} {
		body := fbxTestFile(t, version, fbxTestNode{name: []byte("Texture"), properties: [][]byte{fbxTestBytes('R', image)}})
		fbxTestWrappers(t, body, true)
	}
	usdz := usdzWithTexture330(t, usdcFixture(t, "typed"), image)
	usdcDelivery(t, usdz, "usdz", true)
	// A real trailer is still refused inside the containers.
	bad := paddedRaster330(t, "png", []byte("benign trailer"))
	fbxTestWrappers(t, fbxTestFile(t, 7400, fbxTestNode{name: []byte("Texture"), properties: [][]byte{fbxTestBytes('R', bad)}}), false)
	usdcDelivery(t, usdzWithTexture330(t, usdcFixture(t, "typed"), bad), "usdz", false)
}

func TestTrailingDataDiagnosticReachesTheStorageLog330(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := modelWrite(paddedRaster330(t, "png", []byte("benign trailer")), "", "image/png")
	write.Role = "image"
	_, err = store.Put(context.Background(), write)
	var diagnosed generation.DiagnosedAssetStorageError
	if err == nil || !errors.As(err, &diagnosed) {
		t.Fatalf("expected a diagnosed rejection, got %v", err)
	}
	if got := diagnosed.StorageDiagnostic(); got.Stage != "inspect" || got.Reason != "raster_trailing_data" {
		t.Fatalf("diagnostic = %+v", got)
	}
	if safe, ok := generation.AssetStorageDiagnosticOf(err); !ok || safe.Reason != "raster_trailing_data" {
		t.Fatalf("the kernel allowlist drops the reason: %+v %v", safe, ok)
	}
}

// usdzWithTexture330 stores model.usdc and texture.png uncompressed on 64-byte
// boundaries, as a USDZ requires.
func usdzWithTexture330(t *testing.T, model, texture []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, item := range []struct {
		name string
		body []byte
	}{{"model.usdc", model}, {"texture.png", texture}} {
		if err := z.Flush(); err != nil {
			t.Fatal(err)
		}
		h := zip.FileHeader{Name: item.name, Method: zip.Store}
		padding := (64 - (b.Len()+30+len(h.Name)+4)%64) % 64
		h.Extra = make([]byte, 4+padding)
		binary.LittleEndian.PutUint16(h.Extra, 1983)
		binary.LittleEndian.PutUint16(h.Extra[2:], uint16(padding))
		h.CRC32 = crc32.ChecksumIEEE(item.body)
		h.UncompressedSize64 = uint64(len(item.body))
		h.CompressedSize64 = uint64(len(item.body))
		w, err := z.CreateRaw(&h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(item.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

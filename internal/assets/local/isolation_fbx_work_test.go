package local

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestFBXContinuousNumericWorkDelivery(t *testing.T) {
	t.Parallel()
	// A standard zlib array of finite double zeros. Generate it in fixed
	// windows; no test or product allocation holds its 180MiB expansion.
	const decoded = 180 << 20
	var encoded bytes.Buffer
	writer, err := zlib.NewWriterLevel(&encoded, zlib.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	var window [1 << 16]byte
	for left := decoded; left > 0; left -= len(window) {
		if _, err := writer.Write(window[:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	property := make([]byte, 13)
	property[0] = 'd'
	binary.LittleEndian.PutUint32(property[1:5], decoded/8)
	binary.LittleEndian.PutUint32(property[5:9], 1)
	binary.LittleEndian.PutUint32(property[9:13], uint32(encoded.Len()))
	property = append(property, encoded.Bytes()...)
	body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Values"), properties: [][]byte{property}})
	fbxWorkParallelBoundary(t, body)
}

func fbxWorkParallelBoundary(t *testing.T, body []byte) {
	t.Helper()
	// The three independent gates share only the immutable compressed fixture.
	// Each gate streams the full expansion with its own reader and temporary
	// storage. Parallel scheduling keeps race instrumentation within the normal
	// test timeout without reducing the expansion or its delivery assertions.
	const role, variant, mediaType = "model", "fbx", "application/octet-stream"
	digest := inlineTestHash(body)
	checkBytes := func(t *testing.T, reader io.Reader) {
		t.Helper()
		data, err := io.ReadAll(reader)
		if err != nil || !bytes.Equal(data, body) || inlineTestHash(data) != digest {
			t.Fatal("delivery changed fixture bytes or did not reset the reader")
		}
	}
	newStore := func(t *testing.T) *Store {
		t.Helper()
		store, err := New(t.TempDir())
		if err != nil {
			t.Fatal("failed to create fixture store")
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	t.Run("validation", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "fixture")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal("failed to write fixture")
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal("failed to open fixture")
		}
		defer file.Close()
		inlineTestGate(t, ValidateDeliveredFile(file, role, variant, mediaType), true)
		checkBytes(t, file)
	})
	t.Run("put", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		asset, err := store.Put(context.Background(), generation.AssetWrite{
			AssetID: "asset-fbx-work", OwnerID: "owner-fbx-work", TaskID: "task-fbx-work", AttemptID: "attempt-fbx-work",
			Role: role, Variant: variant, MediaType: mediaType, MaxBytes: int64(len(body)) + 1, Body: bytes.NewReader(body),
		})
		inlineTestGate(t, err, true)
		if asset.Size != int64(len(body)) || asset.SHA256 != digest {
			t.Fatal("admission changed fixture metadata")
		}
		reader, err := store.OpenBlob(context.Background(), asset.StorageKey)
		if err != nil {
			t.Fatal("failed to read admitted fixture")
		}
		defer reader.Close()
		checkBytes(t, reader)
	})
	t.Run("historical_open", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		// Seed the old READY bytes without modern asset admission. Historical
		// Open must independently enforce the same current delivery boundary.
		blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), mediaType, int64(len(body))+1)
		if err != nil {
			t.Fatal("failed to seed historical fixture")
		}
		if blob.Size != int64(len(body)) || blob.SHA256 != digest {
			t.Fatal("historical seed changed fixture metadata")
		}
		reader, err := store.Open(context.Background(), generation.Asset{
			Role: role, Variant: variant, MediaType: blob.MediaType, StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256,
		})
		if reader != nil {
			defer reader.Close()
		}
		inlineTestGate(t, err, true)
		checkBytes(t, reader)
	})
}

type fbxWorkFlatImage struct{}

func (fbxWorkFlatImage) ColorModel() color.Model { return color.RGBAModel }
func (fbxWorkFlatImage) Bounds() image.Rectangle { return image.Rect(0, 0, 4096, 4096) }
func (fbxWorkFlatImage) At(x, y int) color.Color { return color.RGBA{R: 32, G: 64, B: 128, A: 255} }
func (fbxWorkFlatImage) Opaque() bool            { return true }

func TestFBXRepeatedRasterWorkDelivery(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			var encoded bytes.Buffer
			var err error
			if format == "png" {
				err = png.Encode(&encoded, fbxWorkFlatImage{})
			} else {
				err = jpeg.Encode(&encoded, image.NewGray(image.Rect(0, 0, 4096, 4096)), nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := image.Decode(bytes.NewReader(encoded.Bytes())); err != nil {
				t.Fatal("independent decoder rejected the valid texture")
			}
			property := fbxTestBytes('R', encoded.Bytes())
			body := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Texture"), properties: [][]byte{property, property}})
			fbxTestWrappers(t, body, true)
		})
	}
}

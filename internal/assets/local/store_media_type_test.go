package local

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// #264: a provider that only guesses a picture's type (from its URL) must not
// make the store serve PNG bytes as WebP; the raster format of the bytes wins.
func TestPutBlobRecordsTheRasterTypeOfTheBytes(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	picture := image.NewRGBA(image.Rect(0, 0, 4, 4))
	picture.Set(1, 1, color.RGBA{R: 200, A: 255})
	var pngBytes, jpegBytes bytes.Buffer
	if err := png.Encode(&pngBytes, picture); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBytes, picture, nil); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, declared, want string
		body                 []byte
	}{
		{"png declared as webp", "image/webp", "image/png", pngBytes.Bytes()},
		{"jpeg declared as png", "image/png", "image/jpeg", jpegBytes.Bytes()},
		{"matching declaration", "image/png", "image/png", pngBytes.Bytes()},
		{"svg keeps its declaration", "image/svg+xml", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{"non-image bytes keep the declaration", "image/png", "image/png", []byte("not a picture")},
	} {
		t.Run(test.name, func(t *testing.T) {
			blob, err := store.PutBlob(t.Context(), bytes.NewReader(test.body), test.declared, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if blob.MediaType != test.want {
				t.Fatalf("media type = %q, want %q", blob.MediaType, test.want)
			}
		})
	}
}

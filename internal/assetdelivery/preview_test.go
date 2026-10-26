package assetdelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func previewSchema() catalog.AssetPreviewRuntimeSchema {
	return catalog.AssetPreviewRuntimeSchema{
		Query: "size", RequestValue: "preview", CacheSubdirectory: "asset-previews",
		MaximumDimension: 512, MaximumInputBytes: 8 << 20, MaximumPixels: 4096 * 4096, DecodeConcurrency: 2,
		SourceMediaTypes: []string{"image/png", "image/jpeg", "image/webp"}, OutputMediaType: "image/png",
	}
}

func encodedPicture(t *testing.T, width, height int) ([]byte, string) {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buffer.Bytes())
	return buffer.Bytes(), hex.EncodeToString(sum[:])
}

func TestPreviewStoreShrinksStoresAndReusesPreview(t *testing.T) {
	root := t.TempDir()
	store, err := NewPreviewStore(root, previewSchema())
	if err != nil {
		t.Fatal(err)
	}
	original, digest := encodedPicture(t, 2048, 1024)
	if _, ok := store.Cached(digest); ok {
		t.Fatal("nothing is cached yet")
	}
	preview, read, err := store.Preview(context.Background(), digest, "image/png", bytes.NewReader(original))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !bytes.Equal(read, original) {
		t.Fatal("the original is read whole")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil || format != "png" || config.Width != 512 || config.Height != 256 {
		t.Fatalf("preview = %s %dx%d (%v), want a 512x256 png", format, config.Width, config.Height, err)
	}
	if len(preview) >= len(original) {
		t.Fatalf("preview (%d bytes) is not smaller than the original (%d)", len(preview), len(original))
	}
	cached, ok := store.Cached(digest)
	if !ok || !bytes.Equal(cached, preview) {
		t.Fatal("the preview is stored under its digest")
	}
	if _, err := os.Stat(filepath.Join(root, digest[:2], digest+"-512.png")); err != nil {
		t.Fatalf("preview file: %v", err)
	}
	if store.Tag(digest) != digest+"-preview-512" || store.MediaType() != "image/png" {
		t.Fatal("tag and media type follow the schema")
	}
}

func TestPreviewStoreDeclinesWhatItShouldNotShrink(t *testing.T) {
	store, err := NewPreviewStore(t.TempDir(), previewSchema())
	if err != nil {
		t.Fatal(err)
	}
	small, smallDigest := encodedPicture(t, 300, 200)
	if _, _, err := store.Preview(context.Background(), smallDigest, "image/png", bytes.NewReader(small)); !errors.Is(err, ErrPreviewUnsupported) {
		t.Fatalf("a picture already within the size is served as it is: %v", err)
	}
	if store.Accepts("model/gltf-binary", 10) || store.Accepts("image/png", 9<<20) || !store.Accepts("image/png; charset=binary", 10) {
		t.Fatal("Accepts follows the schema's media types and input limit")
	}
	unread := strings.NewReader("not read")
	if _, read, err := store.Preview(context.Background(), smallDigest, "application/zip", unread); !errors.Is(err, ErrPreviewUnsupported) || read != nil || unread.Len() != len("not read") {
		t.Fatal("an unsupported media type is declined before reading")
	}
	broken := []byte("\x89PNG\r\n\x1a\nbroken")
	sum := sha256.Sum256(broken)
	if _, _, err := store.Preview(context.Background(), hex.EncodeToString(sum[:]), "image/png", bytes.NewReader(broken)); !errors.Is(err, ErrPreviewUnsupported) {
		t.Fatalf("undecodable bytes: %v", err)
	}
	if _, _, err := store.Preview(context.Background(), "../../etc/passwd", "image/png", bytes.NewReader(small)); !errors.Is(err, ErrPreviewUnsupported) {
		t.Fatal("only a SHA-256 digest names a cache file")
	}
}

func TestPreviewStoreReturnsWhatItReadWhenTheOriginalIsTooLarge(t *testing.T) {
	schema := previewSchema()
	schema.MaximumInputBytes = 1024
	store, err := NewPreviewStore(t.TempDir(), schema)
	if err != nil {
		t.Fatal(err)
	}
	original, digest := encodedPicture(t, 1024, 1024)
	body := bytes.NewReader(original)
	_, read, err := store.Preview(context.Background(), digest, "image/png", body)
	if !errors.Is(err, ErrPreviewUnsupported) {
		t.Fatalf("too large: %v", err)
	}
	// What was read and what is left make the original again, for serving it unchanged.
	rest := make([]byte, body.Len())
	_, _ = body.Read(rest)
	if !bytes.Equal(append(read, rest...), original) {
		t.Fatal("read bytes plus the rest of the body are the original")
	}
}

func TestPreviewStoreRefusesTooManyPixels(t *testing.T) {
	schema := previewSchema()
	schema.MaximumPixels = 1000 * 1000
	store, err := NewPreviewStore(t.TempDir(), schema)
	if err != nil {
		t.Fatal(err)
	}
	original, digest := encodedPicture(t, 2000, 600)
	if _, _, err := store.Preview(context.Background(), digest, "image/png", bytes.NewReader(original)); !errors.Is(err, ErrPreviewUnsupported) {
		t.Fatalf("a picture over the pixel limit is not decoded: %v", err)
	}
}

func TestPreviewStoreStillAnswersWhenTheCacheCannotBeWritten(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewPreviewStore(root, previewSchema())
	if err != nil {
		t.Fatal(err)
	}
	original, digest := encodedPicture(t, 1024, 1024)
	preview, _, err := store.Preview(context.Background(), digest, "image/png", bytes.NewReader(original))
	if !errors.Is(err, ErrPreviewNotStored) || len(preview) == 0 {
		t.Fatalf("preview %d bytes, err %v", len(preview), err)
	}
}

func TestNewPreviewStoreRejectsIncompletePolicy(t *testing.T) {
	schema := previewSchema()
	schema.OutputMediaType = "image/jpeg"
	if _, err := NewPreviewStore(t.TempDir(), schema); err == nil {
		t.Fatal("only PNG output is implemented")
	}
	if _, err := NewPreviewStore(" ", previewSchema()); err == nil {
		t.Fatal("a root is required")
	}
}

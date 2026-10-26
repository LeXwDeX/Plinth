package legacyassetproxy

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
)

func TestThumbnailRejectsPixelBombBeforeDecode(t *testing.T) {
	schema := sourceSchemaFixture()
	original := thumbnailPNGFixture(t, 1, 1)
	binary.BigEndian.PutUint32(original[16:20], 100000)
	binary.BigEndian.PutUint32(original[20:24], 100000)
	binary.BigEndian.PutUint32(original[29:33], crc32.ChecksumIEEE(original[12:29]))
	decoded := false
	store, err := NewThumbnailStore(ThumbnailStoreConfig{Root: t.TempDir(), Schema: schema, DecodeImage: func(io.Reader) (image.Image, string, error) {
		decoded = true
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), "png", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(context.Background(), assetdelivery.ThumbnailRequest{URL: "https://assets.meshy.ai/bomb.png", Original: original, MaximumDimension: schema.Thumbnail.MaximumDimension, OutputMediaType: schema.Thumbnail.OutputMediaType})
	if err == nil || decoded {
		t.Fatalf("pixel bomb reached full decoder: decoded=%v error=%v", decoded, err)
	}
}

func TestThumbnailRejectsBytesAndCorruptImagesBeforeFullDecode(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
		max  int64
	}{{"bytes", []byte("too large"), 4}, {"corrupt", []byte("not a png"), 1024}} {
		t.Run(test.name, func(t *testing.T) {
			schema := sourceSchemaFixture()
			schema.Thumbnail.MaximumInputBytes = test.max
			store, err := NewThumbnailStore(ThumbnailStoreConfig{Root: t.TempDir(), Schema: schema, DecodeImage: func(io.Reader) (image.Image, string, error) {
				t.Fatal("invalid input reached full decoder")
				return nil, "", nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.Create(t.Context(), assetdelivery.ThumbnailRequest{URL: "https://assets.meshy.ai/invalid.png", Original: test.data, MaximumDimension: 256, OutputMediaType: "image/png"})
			if err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
func TestThumbnailDecodeConcurrencyAndCanceledWait(t *testing.T) {
	schema := sourceSchemaFixture()
	schema.Thumbnail.DecodeConcurrency = 1
	started := make(chan struct{})
	release := make(chan struct{})
	store, err := NewThumbnailStore(ThumbnailStoreConfig{Root: t.TempDir(), Schema: schema, DecodeImage: func(io.Reader) (image.Image, string, error) {
		close(started)
		<-release
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), "png", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := assetdelivery.ThumbnailRequest{URL: "https://assets.meshy.ai/first.png", Original: thumbnailPNGFixture(t, 1, 1), MaximumDimension: 256, OutputMediaType: "image/png"}
	finished := make(chan error, 1)
	go func() { _, err := store.Create(t.Context(), request); finished <- err }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	second := request
	second.URL = "https://assets.meshy.ai/second.png"
	if _, err := store.Create(ctx, second); err != context.Canceled {
		t.Fatalf("canceled wait=%v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

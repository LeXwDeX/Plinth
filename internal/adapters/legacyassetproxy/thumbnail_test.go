package legacyassetproxy

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
)

func TestThumbnailStoreCreatesLegacyCompatiblePNGAndThenHitsCache(
	t *testing.T,
) {
	schema := sourceSchemaFixture()
	root := t.TempDir()
	store, err := NewThumbnailStore(ThumbnailStoreConfig{
		Root:   root,
		Schema: schema,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetURL := "https://assets.meshy.ai/images/wide.png"
	original := thumbnailPNGFixture(t, 1024, 512)

	created, err := store.Create(
		context.Background(),
		assetdelivery.ThumbnailRequest{
			URL:               targetURL,
			Original:          original,
			OriginalMediaType: "image/png",
			MaximumDimension:  schema.Thumbnail.MaximumDimension,
			OutputMediaType:   schema.Thumbnail.OutputMediaType,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.MediaType != "image/png" ||
		created.CacheStatus != schema.Cache.MissValue {
		t.Fatalf("created thumbnail = %#v", created)
	}
	configuration, err := png.DecodeConfig(
		bytes.NewReader(created.Content),
	)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Width != 256 || configuration.Height != 128 {
		t.Fatalf(
			"thumbnail size = %dx%d",
			configuration.Width,
			configuration.Height,
		)
	}

	hash := sourceHash(targetURL)
	expectedPath := filepath.Join(
		root,
		hash[:schema.Cache.ShardCharacters],
		hash+schema.Thumbnail.FilenameSuffix,
	)
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("legacy thumbnail path: %v", err)
	}
	cached, err := store.Cached(context.Background(), targetURL)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cached.Content, created.Content) ||
		cached.MediaType != schema.Thumbnail.OutputMediaType ||
		cached.CacheStatus != schema.Cache.HitValue {
		t.Fatalf("cached thumbnail = %#v", cached)
	}
}

func TestThumbnailStoreDoesNotEnlargeSmallImage(t *testing.T) {
	schema := sourceSchemaFixture()
	store, err := NewThumbnailStore(ThumbnailStoreConfig{
		Root:   t.TempDir(),
		Schema: schema,
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.Create(
		context.Background(),
		assetdelivery.ThumbnailRequest{
			URL: "https://assets.meshy.ai/images/small.png",
			Original: thumbnailPNGFixture(
				t,
				100,
				50,
			),
			OriginalMediaType: "image/png",
			MaximumDimension:  schema.Thumbnail.MaximumDimension,
			OutputMediaType:   schema.Thumbnail.OutputMediaType,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := png.DecodeConfig(
		bytes.NewReader(created.Content),
	)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Width != 100 || configuration.Height != 50 {
		t.Fatalf(
			"small thumbnail size = %dx%d",
			configuration.Width,
			configuration.Height,
		)
	}
}

func thumbnailPNGFixture(t *testing.T, width int, height int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: 100,
				A: 255,
			})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

package assetdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/imaging"
)

// ErrPreviewUnsupported means no preview is made for these bytes (not a supported picture, too large, or
// already small); the caller serves the original instead.
var ErrPreviewUnsupported = errors.New("assetdelivery: no preview for this asset")

// ErrPreviewNotStored comes with a usable preview that could not be written to the cache.
var ErrPreviewNotStored = errors.New("assetdelivery: preview not stored")

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// PreviewStore makes and keeps the downscaled copies of task pictures that small views show (#290): a chat
// result card or a version-strip tile otherwise downloads and decodes a 2K–4K original (6–13 MB, 16–33 MiB
// decoded) to draw a few hundred pixels. A preview is keyed by the original's SHA-256, so it is shared by
// every reference to the same bytes and never goes stale; the caller authorizes the asset before asking for
// it. Cache files live under Root, one shard folder per two hex characters. The runtime never deletes them;
// the offline storage maintenance (local.Maintain, art-ai-storage) removes those whose digest no referenced
// blob has any more, or whose size is not the current one (#291), so the file names here are part of its contract.
type PreviewStore struct {
	root     string
	schema   catalog.AssetPreviewRuntimeSchema
	accepted map[string]struct{}
	slots    chan struct{}
	guard    sync.Mutex
	locks    map[string]*previewLock
}

type previewLock struct {
	mutex sync.Mutex
	refs  int
}

func NewPreviewStore(root string, schema catalog.AssetPreviewRuntimeSchema) (*PreviewStore, error) {
	if strings.TrimSpace(root) == "" || schema.MaximumDimension <= 0 || schema.MaximumInputBytes <= 0 ||
		schema.MaximumPixels <= 0 || schema.DecodeConcurrency <= 0 || schema.OutputMediaType != "image/png" {
		return nil, errors.New("assetdelivery: invalid preview store policy")
	}
	accepted := make(map[string]struct{}, len(schema.SourceMediaTypes))
	for _, mediaType := range schema.SourceMediaTypes {
		accepted[normalizedMediaType(mediaType)] = struct{}{}
	}
	return &PreviewStore{
		root:     filepath.Clean(root),
		schema:   schema,
		accepted: accepted,
		slots:    make(chan struct{}, schema.DecodeConcurrency),
		locks:    make(map[string]*previewLock),
	}, nil
}

// MediaType is what every preview is encoded as.
func (store *PreviewStore) MediaType() string { return store.schema.OutputMediaType }

// Tag names the preview of the bytes with this digest, for an ETag; it changes with the preview size.
func (store *PreviewStore) Tag(digest string) string {
	return fmt.Sprintf("%s-preview-%d", digest, store.schema.MaximumDimension)
}

// Accepts reports whether a preview may be made for an original of this media type and size (bytes; 0 or
// less when unknown). It reads nothing: callers check it before opening the original's body.
func (store *PreviewStore) Accepts(mediaType string, size int64) bool {
	if _, ok := store.accepted[normalizedMediaType(mediaType)]; !ok {
		return false
	}
	return size <= store.schema.MaximumInputBytes
}

// Cached returns the stored preview of the original with this digest, without reading the original.
func (store *PreviewStore) Cached(digest string) ([]byte, bool) {
	path, err := store.path(digest)
	if err != nil {
		return nil, false
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 {
		return nil, false
	}
	return content, true
}

// Preview returns the preview of `original` (whose SHA-256 is `digest`), making and storing it when it is
// not stored yet. ErrPreviewUnsupported when none is made; the bytes read so far are returned with it so
// the caller can still serve the original without reading it again.
func (store *PreviewStore) Preview(ctx context.Context, digest string, mediaType string, original io.Reader) ([]byte, []byte, error) {
	path, err := store.path(digest)
	if err != nil {
		return nil, nil, err
	}
	if !store.Accepts(mediaType, 0) {
		return nil, nil, ErrPreviewUnsupported
	}
	content, err := io.ReadAll(io.LimitReader(original, store.schema.MaximumInputBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(content)) > store.schema.MaximumInputBytes {
		return nil, content, ErrPreviewUnsupported
	}
	select {
	case store.slots <- struct{}{}:
		defer func() { <-store.slots }()
	case <-ctx.Done():
		return nil, content, ctx.Err()
	}
	release := store.lock(digest)
	defer release()
	if cached, ok := store.Cached(digest); ok {
		return cached, content, nil
	}
	encoded, err := store.render(content)
	if err != nil {
		return nil, content, err
	}
	if err := writeAtomically(path, encoded); err != nil {
		// This response still has its preview; the next request makes it again. The caller logs it.
		return encoded, content, fmt.Errorf("%w: %v", ErrPreviewNotStored, err)
	}
	return encoded, content, nil
}

func (store *PreviewStore) render(content []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return nil, ErrPreviewUnsupported
	}
	if int64(config.Width) > store.schema.MaximumPixels/int64(config.Height) {
		return nil, ErrPreviewUnsupported
	}
	width, height := imaging.FitWithin(config.Width, config.Height, store.schema.MaximumDimension)
	if width == config.Width && height == config.Height {
		// Already small: the original is the preview.
		return nil, ErrPreviewUnsupported
	}
	decoded, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, ErrPreviewUnsupported
	}
	scaled := image.NewNRGBA(image.Rect(0, 0, width, height))
	// Catmull-Rom widens its kernel when shrinking, so a 4K picture becomes a clean 512 px one instead of
	// the aliased result of sampling a few source pixels per target pixel.
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), decoded, decoded.Bounds(), xdraw.Src, nil)
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&encoded, scaled); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func (store *PreviewStore) path(digest string) (string, error) {
	if !sha256Hex.MatchString(digest) {
		return "", ErrPreviewUnsupported
	}
	return filepath.Join(store.root, digest[:2], fmt.Sprintf("%s-%d.png", digest, store.schema.MaximumDimension)), nil
}

func (store *PreviewStore) lock(key string) func() {
	store.guard.Lock()
	entry := store.locks[key]
	if entry == nil {
		entry = &previewLock{}
		store.locks[key] = entry
	}
	entry.refs++
	store.guard.Unlock()
	entry.mutex.Lock()
	return func() {
		entry.mutex.Unlock()
		store.guard.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(store.locks, key)
		}
		store.guard.Unlock()
	}
}

func writeAtomically(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		_ = os.Remove(name)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

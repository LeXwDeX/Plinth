package legacyassetproxy

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/imaging"
)

var ErrInvalidThumbnail = errors.New("legacyassetproxy: invalid thumbnail image")

type ThumbnailStoreConfig struct {
	Root        string
	Schema      catalog.AssetProxyRuntimeSchema
	DecodeImage func(io.Reader) (image.Image, string, error)
}

type ThumbnailStore struct {
	root        string
	schema      catalog.AssetProxyRuntimeSchema
	decodeImage func(io.Reader) (image.Image, string, error)
	lockGuard   sync.Mutex
	locks       map[string]*sourceLock
	decodeSlots chan struct{}
}

func NewThumbnailStore(
	config ThumbnailStoreConfig,
) (*ThumbnailStore, error) {
	if strings.TrimSpace(config.Root) == "" ||
		config.Schema.Cache.ShardCharacters <= 0 ||
		strings.TrimSpace(
			config.Schema.Thumbnail.FilenameSuffix,
		) == "" ||
		config.Schema.Thumbnail.MaximumDimension <= 0 ||
		config.Schema.Thumbnail.MaximumInputBytes <= 0 ||
		config.Schema.Thumbnail.MaximumPixels <= 0 ||
		config.Schema.Thumbnail.DecodeConcurrency <= 0 {
		return nil, errors.New(
			"legacyassetproxy: invalid thumbnail store policy",
		)
	}
	if config.DecodeImage == nil {
		config.DecodeImage = image.Decode
	}
	return &ThumbnailStore{
		root:        filepath.Clean(config.Root),
		schema:      config.Schema,
		decodeImage: config.DecodeImage,
		locks:       make(map[string]*sourceLock),
		decodeSlots: make(chan struct{}, config.Schema.Thumbnail.DecodeConcurrency),
	}, nil
}

func (store *ThumbnailStore) Cached(
	ctx context.Context,
	targetURL string,
) (assetdelivery.ThumbnailContent, error) {
	if err := ctx.Err(); err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	path := store.thumbnailPath(targetURL)
	file, err := os.Open(path)
	if err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, store.schema.Thumbnail.MaximumInputBytes+1))
	if err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	if int64(len(content)) > store.schema.Thumbnail.MaximumInputBytes {
		return assetdelivery.ThumbnailContent{}, ErrSourceTooLarge
	}
	return assetdelivery.ThumbnailContent{
		Content:     content,
		MediaType:   store.schema.Thumbnail.OutputMediaType,
		CacheStatus: store.schema.Cache.HitValue,
	}, nil
}

func (store *ThumbnailStore) Create(
	ctx context.Context,
	request assetdelivery.ThumbnailRequest,
) (assetdelivery.ThumbnailContent, error) {
	select {
	case store.decodeSlots <- struct{}{}:
		defer func() { <-store.decodeSlots }()
	case <-ctx.Done():
		return assetdelivery.ThumbnailContent{}, ctx.Err()
	}
	hash := sourceHash(request.URL)
	release := store.acquireThumbnail(hash)
	defer release()
	if cached, err := store.Cached(ctx, request.URL); err == nil {
		return cached, nil
	}
	if err := ctx.Err(); err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	if request.MaximumDimension <= 0 ||
		request.OutputMediaType !=
			store.schema.Thumbnail.OutputMediaType ||
		len(request.Original) == 0 {
		return assetdelivery.ThumbnailContent{}, errors.New(
			"legacyassetproxy: invalid thumbnail request",
		)
	}
	if int64(len(request.Original)) > store.schema.Thumbnail.MaximumInputBytes {
		return assetdelivery.ThumbnailContent{}, ErrSourceTooLarge
	}
	dimensions, _, err := image.DecodeConfig(bytes.NewReader(request.Original))
	if err != nil {
		return assetdelivery.ThumbnailContent{}, ErrInvalidThumbnail
	}
	if dimensions.Width <= 0 || dimensions.Height <= 0 || int64(dimensions.Width) > store.schema.Thumbnail.MaximumPixels/int64(dimensions.Height) {
		return assetdelivery.ThumbnailContent{}, ErrSourceTooLarge
	}
	decoded, _, err := store.decodeImage(
		bytes.NewReader(request.Original),
	)
	if err != nil {
		return assetdelivery.ThumbnailContent{}, ErrInvalidThumbnail
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != dimensions.Width || bounds.Dy() != dimensions.Height {
		return assetdelivery.ThumbnailContent{}, ErrInvalidThumbnail
	}
	width, height := imaging.FitWithin(
		bounds.Dx(),
		bounds.Dy(),
		min(request.MaximumDimension, store.schema.Thumbnail.MaximumDimension),
	)
	if width <= 0 || height <= 0 {
		return assetdelivery.ThumbnailContent{}, errors.New(
			"legacyassetproxy: invalid thumbnail dimensions",
		)
	}
	resized := imaging.ResizeBilinear(decoded, width, height)
	var encoded bytes.Buffer
	switch store.schema.Thumbnail.OutputMediaType {
	case "image/png":
		err = png.Encode(&encoded, resized)
	default:
		err = errors.New(
			"legacyassetproxy: unsupported thumbnail codec",
		)
	}
	if err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	if int64(encoded.Len()) > store.schema.HTTP.ResponseMaxBytes {
		return assetdelivery.ThumbnailContent{}, ErrSourceTooLarge
	}
	path := store.thumbnailPath(request.URL)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	temporary, err := os.CreateTemp(directory, hash+".thumbnail-*")
	if err != nil {
		return assetdelivery.ThumbnailContent{}, err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := io.Copy(
		temporary,
		bytes.NewReader(encoded.Bytes()),
	); err != nil {
		cleanup()
		return assetdelivery.ThumbnailContent{}, err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return assetdelivery.ThumbnailContent{}, err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return assetdelivery.ThumbnailContent{}, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return assetdelivery.ThumbnailContent{}, err
	}
	return assetdelivery.ThumbnailContent{
		Content:     encoded.Bytes(),
		MediaType:   request.OutputMediaType,
		CacheStatus: store.schema.Cache.MissValue,
	}, nil
}

func (store *ThumbnailStore) thumbnailPath(targetURL string) string {
	hash := sourceHash(targetURL)
	return filepath.Join(
		store.root,
		hash[:store.schema.Cache.ShardCharacters],
		hash+store.schema.Thumbnail.FilenameSuffix,
	)
}

func (store *ThumbnailStore) acquireThumbnail(key string) func() {
	store.lockGuard.Lock()
	lock := store.locks[key]
	if lock == nil {
		lock = &sourceLock{}
		store.locks[key] = lock
	}
	lock.refs++
	store.lockGuard.Unlock()
	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		store.lockGuard.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(store.locks, key)
		}
		store.lockGuard.Unlock()
	}
}

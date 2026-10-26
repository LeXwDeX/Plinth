package assetdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	ErrMissingURL     = errors.New("assetdelivery: missing source URL")
	ErrInvalidURL     = errors.New("assetdelivery: invalid source URL")
	ErrForbiddenHost  = errors.New("assetdelivery: forbidden source host")
	ErrNotFound       = errors.New("assetdelivery: source not found")
	ErrUnavailable    = errors.New("assetdelivery: source unavailable")
	ErrSourceNotFound = errors.New("assetdelivery: external source not found")
)

type Request struct {
	URL      string
	Filename string
	TaskID   string
	Size     string
}

type Result struct {
	Body        io.ReadCloser
	MediaType   string
	Size        int64
	CacheStatus string
	Thumbnail   bool
}

type SourceRequest struct {
	URL    string
	TaskID string
}

type SourceContent struct {
	Body        io.ReadCloser
	MediaType   string
	Size        int64
	CacheStatus string
}

type Source interface {
	Fetch(context.Context, SourceRequest) (SourceContent, error)
}

type ThumbnailContent struct {
	Content     []byte
	MediaType   string
	CacheStatus string
}

type ThumbnailRequest struct {
	URL               string
	Original          []byte
	OriginalMediaType string
	MaximumDimension  int
	OutputMediaType   string
}

type Thumbnailer interface {
	Cached(context.Context, string) (ThumbnailContent, error)
	Create(context.Context, ThumbnailRequest) (ThumbnailContent, error)
}

type MetadataStamper interface {
	Stamp(
		[]byte,
		string,
		catalog.AssetProxyMetadataSchema,
	) ([]byte, error)
}

type ProxyConfig struct {
	Schema       catalog.AssetProxyRuntimeSchema
	AllowedHosts []string
	Source       Source
	Thumbnailer  Thumbnailer
	Stamper      MetadataStamper
}

type Proxy struct {
	schema         catalog.AssetProxyRuntimeSchema
	allowedHosts   []string
	allowedSchemes map[string]struct{}
	metadataTypes  map[string]struct{}
	source         Source
	thumbnailer    Thumbnailer
	stamper        MetadataStamper
	thumbnailSlots chan struct{}
}

func NewProxy(config ProxyConfig) (*Proxy, error) {
	if config.Source == nil ||
		config.Thumbnailer == nil ||
		config.Stamper == nil {
		return nil, errors.New("assetdelivery: incomplete proxy dependencies")
	}
	if len(config.AllowedHosts) == 0 {
		return nil, errors.New("assetdelivery: proxy hosts are required")
	}
	allowedHosts := make([]string, 0, len(config.AllowedHosts))
	seenHosts := make(map[string]struct{}, len(config.AllowedHosts))
	for _, host := range config.AllowedHosts {
		normalized := normalizedHost(host)
		if normalized == "" {
			return nil, errors.New("assetdelivery: invalid proxy host")
		}
		if _, exists := seenHosts[normalized]; exists {
			continue
		}
		seenHosts[normalized] = struct{}{}
		allowedHosts = append(allowedHosts, normalized)
	}
	allowedSchemes := make(
		map[string]struct{},
		len(config.Schema.Security.AllowedSchemes),
	)
	for _, scheme := range config.Schema.Security.AllowedSchemes {
		allowedSchemes[strings.ToLower(strings.TrimSpace(scheme))] =
			struct{}{}
	}
	metadataTypes := make(
		map[string]struct{},
		len(config.Schema.Metadata.SupportedMediaTypes),
	)
	for _, mediaType := range config.Schema.Metadata.SupportedMediaTypes {
		metadataTypes[normalizedMediaType(mediaType)] = struct{}{}
	}
	if len(allowedSchemes) == 0 ||
		config.Schema.HTTP.ResponseMaxBytes <= 0 || config.Schema.Thumbnail.MaximumInputBytes <= 0 || config.Schema.Thumbnail.DecodeConcurrency <= 0 {
		return nil, errors.New("assetdelivery: invalid proxy schema")
	}
	return &Proxy{
		schema:         config.Schema,
		allowedHosts:   allowedHosts,
		allowedSchemes: allowedSchemes,
		metadataTypes:  metadataTypes,
		source:         config.Source,
		thumbnailer:    config.Thumbnailer,
		stamper:        config.Stamper,
		thumbnailSlots: make(chan struct{}, config.Schema.Thumbnail.DecodeConcurrency),
	}, nil
}

func (proxy *Proxy) Get(
	ctx context.Context,
	request Request,
) (Result, error) {
	sourceURL := strings.TrimSpace(request.URL)
	if sourceURL == "" {
		return Result{}, ErrMissingURL
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil ||
		parsed == nil ||
		parsed.IsAbs() == false ||
		parsed.Hostname() == "" ||
		parsed.User != nil {
		return Result{}, ErrInvalidURL
	}
	if _, exists := proxy.allowedSchemes[strings.ToLower(parsed.Scheme)]; !exists {
		return Result{}, ErrInvalidURL
	}
	if !hostAllowed(parsed.Hostname(), proxy.allowedHosts) {
		return Result{}, ErrForbiddenHost
	}
	wantsThumbnail := request.Size ==
		proxy.schema.Thumbnail.RequestValue
	if wantsThumbnail {
		select {
		case proxy.thumbnailSlots <- struct{}{}:
			defer func() { <-proxy.thumbnailSlots }()
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		thumbnail, thumbnailErr := proxy.thumbnailer.Cached(
			ctx,
			parsed.String(),
		)
		if thumbnailErr == nil && len(thumbnail.Content) > 0 {
			return byteResult(
				thumbnail.Content,
				proxy.schema.Thumbnail.OutputMediaType,
				thumbnail.CacheStatus,
				true,
			), nil
		}
	}

	content, err := proxy.source.Fetch(ctx, SourceRequest{
		URL:    parsed.String(),
		TaskID: request.TaskID,
	})
	if errors.Is(err, ErrSourceNotFound) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if content.Body == nil {
		return Result{}, ErrUnavailable
	}
	mediaType := normalizedMediaType(content.MediaType)
	if mediaType == "" {
		mediaType = proxy.schema.DefaultMediaType
	}

	_, wantsMetadata := proxy.metadataTypes[mediaType]
	if !wantsThumbnail && !wantsMetadata {
		return Result{
			Body:        content.Body,
			MediaType:   mediaType,
			Size:        content.Size,
			CacheStatus: content.CacheStatus,
		}, nil
	}

	maximumBytes := proxy.schema.HTTP.ResponseMaxBytes
	if wantsThumbnail {
		maximumBytes = min(maximumBytes, proxy.schema.Thumbnail.MaximumInputBytes)
	}
	original, readErr := readBounded(
		content.Body,
		maximumBytes,
	)
	if closeErr := content.Body.Close(); readErr == nil && closeErr != nil {
		readErr = closeErr
	}
	if readErr != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, readErr)
	}
	if wantsThumbnail &&
		content.CacheStatus == proxy.schema.Cache.HitValue {
		thumbnail, thumbnailErr := proxy.thumbnailer.Create(
			ctx,
			ThumbnailRequest{
				URL:               parsed.String(),
				Original:          original,
				OriginalMediaType: mediaType,
				MaximumDimension: proxy.schema.Thumbnail.
					MaximumDimension,
				OutputMediaType: proxy.schema.Thumbnail.
					OutputMediaType,
			},
		)
		if thumbnailErr != nil {
			return Result{}, ErrUnavailable
		}
		if thumbnailErr == nil &&
			len(thumbnail.Content) > 0 {
			return byteResult(
				thumbnail.Content,
				proxy.schema.Thumbnail.OutputMediaType,
				thumbnail.CacheStatus,
				true,
			), nil
		}
	}
	if wantsMetadata {
		stamped, stampErr := proxy.stamper.Stamp(
			original,
			mediaType,
			proxy.schema.Metadata,
		)
		if stampErr == nil && len(stamped) > 0 {
			original = stamped
		}
	}
	return byteResult(
		original,
		mediaType,
		content.CacheStatus,
		false,
	), nil
}

func byteResult(
	content []byte,
	mediaType string,
	cacheStatus string,
	thumbnail bool,
) Result {
	return Result{
		Body:        io.NopCloser(bytes.NewReader(content)),
		MediaType:   mediaType,
		Size:        int64(len(content)),
		CacheStatus: cacheStatus,
		Thumbnail:   thumbnail,
	}
}

func readBounded(body io.Reader, maximumBytes int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(body, maximumBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximumBytes {
		return nil, errors.New("asset body exceeds byte limit")
	}
	return content, nil
}

func normalizedMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err == nil {
		return strings.ToLower(strings.TrimSpace(mediaType))
	}
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizedHost(value string) string {
	return strings.TrimSuffix(
		strings.ToLower(strings.TrimSpace(value)),
		".",
	)
}

func hostAllowed(host string, allowedHosts []string) bool {
	normalized := normalizedHost(host)
	for _, allowed := range allowedHosts {
		if normalized == allowed ||
			strings.HasSuffix(normalized, "."+allowed) {
			return true
		}
	}
	return false
}

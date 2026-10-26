package legacygeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var (
	ErrLegacyAssetUnavailable = errors.New(
		"legacy generation reader: asset is unavailable",
	)
	ErrLegacyAssetRejected = errors.New(
		"legacy generation reader: asset source is rejected",
	)
	ErrLegacyAssetTooLarge = errors.New(
		"legacy generation reader: asset is too large",
	)
)

type GeneratedImageReader interface {
	Open(
		context.Context,
		string,
	) (io.ReadCloser, int64, error)
}

type ReaderConfig struct {
	Native                    administration.GenerationAssetReader
	GeneratedImages           GeneratedImageReader
	GeneratedImageRoute       string
	CacheRoot                 string
	CachePrefixCharacters     int
	CacheMetadataSuffix       string
	CacheContentTypeField     string
	AllowedHosts              []string
	DefaultMediaType          string
	UnknownExtension          string
	MaximumBytes              int64
	RemoteTimeoutMilliseconds int
	MaximumRedirects          int
	MediaTypeExtensions       map[string]string
	AssetDownloads            []catalog.LegacyAssetDownloadSchema
	HTTPClient                *http.Client
}

type Reader struct {
	native                administration.GenerationAssetReader
	generatedImages       GeneratedImageReader
	generatedImageRoute   routeTemplate
	cacheRoot             string
	cachePrefixCharacters int
	cacheMetadataSuffix   string
	cacheContentTypeField string
	allowedHosts          map[string]struct{}
	defaultMediaType      string
	unknownExtension      string
	maximumBytes          int64
	remoteTimeout         time.Duration
	maximumRedirects      int
	extensionMediaTypes   map[string]string
	assetDownloads        []localAssetDownload
	httpClient            *http.Client
}

type routeTemplate struct {
	prefix string
	suffix string
}

type localAssetDownload struct {
	route                 routeTemplate
	tokenPlaceholder      string
	cacheSubdirectory     string
	cacheFilenameTemplate string
	mediaType             string
}

func CollectAssetHosts(offerings []generation.Offering) []string {
	hosts := make(map[string]struct{})
	for _, offering := range offerings {
		for _, host := range offering.AssetHosts {
			host = normalizedHost(host)
			if host != "" {
				hosts[host] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(hosts))
	for host := range hosts {
		result = append(result, host)
	}
	sort.Strings(result)
	return result
}

func CollectAssetDownloads(
	capabilities []catalog.CapabilityPresentation,
) []catalog.LegacyAssetDownloadSchema {
	result := make([]catalog.LegacyAssetDownloadSchema, 0)
	for _, capability := range capabilities {
		if capability.LegacyAPI == nil ||
			capability.LegacyAPI.AssetDownload == nil {
			continue
		}
		result = append(result, *capability.LegacyAPI.AssetDownload)
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].Path < result[right].Path
	})
	return result
}

func NewReader(config ReaderConfig) (*Reader, error) {
	if config.Native == nil || config.GeneratedImages == nil {
		return nil, errors.New(
			"legacy generation reader: dependencies are missing",
		)
	}
	for name, value := range map[string]string{
		"generated image route":    config.GeneratedImageRoute,
		"cache root":               config.CacheRoot,
		"cache metadata suffix":    config.CacheMetadataSuffix,
		"cache content type field": config.CacheContentTypeField,
		"default media type":       config.DefaultMediaType,
		"unknown extension":        config.UnknownExtension,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf(
				"legacy generation reader: %s is empty",
				name,
			)
		}
	}
	if config.CachePrefixCharacters <= 0 ||
		config.MaximumBytes <= 0 ||
		config.RemoteTimeoutMilliseconds <= 0 ||
		config.MaximumRedirects <= 0 ||
		len(config.AllowedHosts) == 0 ||
		len(config.MediaTypeExtensions) == 0 {
		return nil, errors.New(
			"legacy generation reader: limits are invalid",
		)
	}
	generatedImageRoute, err := parseRouteTemplate(
		config.GeneratedImageRoute,
	)
	if err != nil {
		return nil, err
	}
	allowedHosts := make(
		map[string]struct{},
		len(config.AllowedHosts),
	)
	for _, host := range config.AllowedHosts {
		host = normalizedHost(host)
		if host == "" {
			return nil, errors.New(
				"legacy generation reader: allowed host is invalid",
			)
		}
		allowedHosts[host] = struct{}{}
	}
	extensionMediaTypes := make(
		map[string]string,
		len(config.MediaTypeExtensions),
	)
	for mediaType, extension := range config.MediaTypeExtensions {
		mediaType = administrationMediaType(mediaType)
		extension = normalizedExtension(extension)
		if mediaType == "" || extension == "" {
			return nil, errors.New(
				"legacy generation reader: media type extension is invalid",
			)
		}
		extensionMediaTypes[extension] = mediaType
	}
	assetDownloads := make(
		[]localAssetDownload,
		0,
		len(config.AssetDownloads),
	)
	for _, download := range config.AssetDownloads {
		tokenPlaceholder := "{" + download.TokenParameter + "}"
		if strings.TrimSpace(download.TokenParameter) == "" ||
			!strings.Contains(download.Path, tokenPlaceholder) ||
			!strings.Contains(
				download.CacheFilenameTemplate,
				tokenPlaceholder,
			) ||
			!safeRelativeCachePath(download.CacheSubdirectory) {
			return nil, errors.New(
				"legacy generation reader: asset download is invalid",
			)
		}
		route, routeErr := parseRouteTemplate(download.Path)
		if routeErr != nil {
			return nil, routeErr
		}
		mediaType := administrationMediaType(download.MediaType)
		if mediaType == "" {
			return nil, errors.New(
				"legacy generation reader: asset download media type is empty",
			)
		}
		assetDownloads = append(assetDownloads, localAssetDownload{
			route:                 route,
			tokenPlaceholder:      tokenPlaceholder,
			cacheSubdirectory:     download.CacheSubdirectory,
			cacheFilenameTemplate: download.CacheFilenameTemplate,
			mediaType:             mediaType,
		})
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	previousRedirectPolicy := client.CheckRedirect
	clientCopy.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		if len(via) > config.MaximumRedirects ||
			!allowedRemoteURL(request.URL, allowedHosts) {
			return ErrLegacyAssetRejected
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(request, via)
		}
		return nil
	}
	return &Reader{
		native:                config.Native,
		generatedImages:       config.GeneratedImages,
		generatedImageRoute:   generatedImageRoute,
		cacheRoot:             filepath.Clean(config.CacheRoot),
		cachePrefixCharacters: config.CachePrefixCharacters,
		cacheMetadataSuffix:   config.CacheMetadataSuffix,
		cacheContentTypeField: config.CacheContentTypeField,
		allowedHosts:          allowedHosts,
		defaultMediaType:      administrationMediaType(config.DefaultMediaType),
		unknownExtension:      normalizedExtension(config.UnknownExtension),
		maximumBytes:          config.MaximumBytes,
		remoteTimeout: time.Duration(
			config.RemoteTimeoutMilliseconds,
		) * time.Millisecond,
		maximumRedirects:    config.MaximumRedirects,
		extensionMediaTypes: extensionMediaTypes,
		assetDownloads:      assetDownloads,
		httpClient:          &clientCopy,
	}, nil
}

func (reader *Reader) OpenGenerationAsset(
	ctx context.Context,
	request administration.GenerationAssetRequest,
) (administration.GenerationAssetContent, error) {
	source := strings.TrimSpace(request.Asset.SourceURL)
	if source == "" {
		return reader.native.OpenGenerationAsset(ctx, request)
	}
	if filename, matches := reader.generatedImageFilename(source); matches {
		content, err := reader.openGeneratedImage(ctx, filename)
		return validateLegacyDeliveredContent(content, request.Asset, err)
	}
	if content, matches, err := reader.openLegacyDownload(
		ctx,
		source,
	); matches {
		return validateLegacyDeliveredContent(content, request.Asset, err)
	}
	if content, found := reader.openCachedAsset(ctx, source); found {
		return validateLegacyDeliveredContent(content, request.Asset, nil)
	}
	// Historical provider URLs are private recovery metadata. A browser
	// download must never turn into a live request to the upstream CDN.
	return administration.GenerationAssetContent{}, ErrLegacyAssetUnavailable
}

func validateLegacyDeliveredContent(
	content administration.GenerationAssetContent,
	asset administration.GenerationAsset,
	err error,
) (administration.GenerationAssetContent, error) {
	if err != nil {
		return administration.GenerationAssetContent{}, err
	}
	file, ok := content.Body.(*os.File)
	if !ok {
		return content, nil
	}
	if err := local.ValidateDeliveredFile(file, asset.Role, asset.Variant, content.MediaType); err != nil {
		_ = file.Close()
		return administration.GenerationAssetContent{}, ErrLegacyAssetRejected
	}
	return content, nil
}

func (reader *Reader) openLegacyDownload(
	ctx context.Context,
	source string,
) (administration.GenerationAssetContent, bool, error) {
	if err := ctx.Err(); err != nil {
		return administration.GenerationAssetContent{}, true, err
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host != "" ||
		!strings.HasPrefix(parsed.Path, "/") {
		return administration.GenerationAssetContent{}, false, nil
	}
	for _, download := range reader.assetDownloads {
		if !strings.HasPrefix(parsed.Path, download.route.prefix) ||
			!strings.HasSuffix(parsed.Path, download.route.suffix) {
			continue
		}
		token := strings.TrimSuffix(
			strings.TrimPrefix(parsed.Path, download.route.prefix),
			download.route.suffix,
		)
		if !safeAssetToken(token) {
			return administration.GenerationAssetContent{},
				true,
				ErrLegacyAssetRejected
		}
		filename := strings.ReplaceAll(
			download.cacheFilenameTemplate,
			download.tokenPlaceholder,
			token,
		)
		if filepath.Base(filename) != filename ||
			strings.ContainsAny(filename, `/\`) {
			return administration.GenerationAssetContent{},
				true,
				ErrLegacyAssetRejected
		}
		filePath := filepath.Join(
			reader.cacheRoot,
			download.cacheSubdirectory,
			filename,
		)
		info, statErr := os.Lstat(filePath)
		if statErr != nil || !info.Mode().IsRegular() {
			return administration.GenerationAssetContent{},
				true,
				ErrLegacyAssetUnavailable
		}
		if info.Size() > reader.maximumBytes {
			return administration.GenerationAssetContent{},
				true,
				ErrLegacyAssetTooLarge
		}
		file, openErr := os.Open(filePath)
		if openErr != nil {
			return administration.GenerationAssetContent{},
				true,
				openErr
		}
		return administration.GenerationAssetContent{
			Body:      file,
			MediaType: download.mediaType,
			Size:      info.Size(),
		}, true, nil
	}
	return administration.GenerationAssetContent{}, false, nil
}

func (reader *Reader) generatedImageFilename(
	source string,
) (string, bool) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", false
	}
	if !strings.HasPrefix(
		parsed.Path,
		reader.generatedImageRoute.prefix,
	) || !strings.HasSuffix(
		parsed.Path,
		reader.generatedImageRoute.suffix,
	) {
		return "", false
	}
	filename := strings.TrimSuffix(
		strings.TrimPrefix(
			parsed.Path,
			reader.generatedImageRoute.prefix,
		),
		reader.generatedImageRoute.suffix,
	)
	return filename, true
}

func (reader *Reader) openGeneratedImage(
	ctx context.Context,
	filename string,
) (administration.GenerationAssetContent, error) {
	body, size, err := reader.generatedImages.Open(ctx, filename)
	if err != nil {
		return administration.GenerationAssetContent{}, fmt.Errorf(
			"%w: %v",
			ErrLegacyAssetUnavailable,
			err,
		)
	}
	if size > reader.maximumBytes {
		_ = body.Close()
		return administration.GenerationAssetContent{},
			ErrLegacyAssetTooLarge
	}
	return administration.GenerationAssetContent{
		Body:      body,
		MediaType: reader.mediaTypeForFilename(filename),
		Size:      size,
	}, nil
}

func (reader *Reader) openCachedAsset(
	ctx context.Context,
	source string,
) (administration.GenerationAssetContent, bool) {
	if ctx.Err() != nil {
		return administration.GenerationAssetContent{}, false
	}
	for _, hash := range reader.cacheHashes(source) {
		content, err := reader.openCacheHash(source, hash)
		if err == nil {
			return content, true
		}
	}
	return administration.GenerationAssetContent{}, false
}

func (reader *Reader) openCacheHash(
	source string,
	hash string,
) (administration.GenerationAssetContent, error) {
	if len(hash) < reader.cachePrefixCharacters {
		return administration.GenerationAssetContent{},
			ErrLegacyAssetUnavailable
	}
	extension := reader.sourceExtension(source)
	directory := filepath.Join(
		reader.cacheRoot,
		hash[:reader.cachePrefixCharacters],
	)
	filePath := filepath.Join(directory, hash+extension)
	metadataPath := filepath.Join(
		directory,
		hash+reader.cacheMetadataSuffix,
	)
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return administration.GenerationAssetContent{},
			ErrLegacyAssetUnavailable
	}
	if info.Size() > reader.maximumBytes {
		return administration.GenerationAssetContent{},
			ErrLegacyAssetTooLarge
	}
	rawMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		return administration.GenerationAssetContent{}, err
	}
	metadata := map[string]any{}
	if json.Unmarshal(rawMetadata, &metadata) != nil {
		return administration.GenerationAssetContent{},
			ErrLegacyAssetUnavailable
	}
	mediaType, _ := metadata[reader.cacheContentTypeField].(string)
	mediaType = administrationMediaType(mediaType)
	if mediaType == "" {
		mediaType = reader.mediaTypeForFilename(filePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return administration.GenerationAssetContent{}, err
	}
	return administration.GenerationAssetContent{
		Body:      file,
		MediaType: mediaType,
		Size:      info.Size(),
	}, nil
}

func (reader *Reader) openRemoteAsset(
	ctx context.Context,
	source string,
) (administration.GenerationAssetContent, error) {
	parsed, err := url.Parse(source)
	if err != nil || !allowedRemoteURL(parsed, reader.allowedHosts) {
		return administration.GenerationAssetContent{},
			ErrLegacyAssetRejected
	}
	requestContext, cancel := context.WithTimeout(
		ctx,
		reader.remoteTimeout,
	)
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		parsed.String(),
		nil,
	)
	if err != nil {
		cancel()
		return administration.GenerationAssetContent{}, err
	}
	response, err := reader.httpClient.Do(request)
	if err != nil {
		cancel()
		return administration.GenerationAssetContent{}, fmt.Errorf(
			"%w: %v",
			ErrLegacyAssetUnavailable,
			err,
		)
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		cancel()
		return administration.GenerationAssetContent{},
			ErrLegacyAssetUnavailable
	}
	if response.ContentLength > reader.maximumBytes {
		_ = response.Body.Close()
		cancel()
		return administration.GenerationAssetContent{},
			ErrLegacyAssetTooLarge
	}
	mediaType := administrationMediaType(
		response.Header.Get("Content-Type"),
	)
	if mediaType == "" {
		mediaType = reader.mediaTypeForFilename(parsed.Path)
	}
	size := response.ContentLength
	if size < 0 {
		size = 0
	}
	return administration.GenerationAssetContent{
		Body: &contextReadCloser{
			Reader: io.LimitReader(
				response.Body,
				reader.maximumBytes+1,
			),
			closer: response.Body,
			cancel: cancel,
		},
		MediaType: mediaType,
		Size:      size,
	}, nil
}

func (reader *Reader) cacheHashes(source string) []string {
	full := sourceHash(source)
	parsed, err := url.Parse(source)
	if err != nil || !reader.hostAllowed(parsed.Hostname()) {
		return []string{full}
	}
	pathHash := sourceHash(parsed.Path)
	if pathHash == full {
		return []string{full}
	}
	return []string{pathHash, full}
}

func (reader *Reader) sourceExtension(source string) string {
	parsed, err := url.Parse(source)
	if err == nil {
		if extension := normalizedExtension(path.Ext(parsed.Path)); extension != "" {
			return extension
		}
	}
	return reader.unknownExtension
}

func (reader *Reader) mediaTypeForFilename(filename string) string {
	extension := normalizedExtension(path.Ext(filename))
	if mediaType := reader.extensionMediaTypes[extension]; mediaType != "" {
		return mediaType
	}
	return reader.defaultMediaType
}

func (reader *Reader) hostAllowed(host string) bool {
	host = normalizedHost(host)
	for allowed := range reader.allowedHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func parseRouteTemplate(value string) (routeTemplate, error) {
	open := strings.IndexByte(value, '{')
	close := strings.IndexByte(value, '}')
	if open < 0 || close <= open ||
		strings.Contains(value[close+1:], "{") ||
		strings.Contains(value[:open], "}") {
		return routeTemplate{}, errors.New(
			"legacy generation reader: generated image route is invalid",
		)
	}
	return routeTemplate{
		prefix: value[:open],
		suffix: value[close+1:],
	}, nil
}

func allowedRemoteURL(
	value *url.URL,
	allowedHosts map[string]struct{},
) bool {
	if value == nil ||
		(value.Scheme != "https" && value.Scheme != "http") ||
		value.User != nil {
		return false
	}
	host := normalizedHost(value.Hostname())
	for allowed := range allowedHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func normalizedHost(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func safeAssetToken(value string) bool {
	return value != "" &&
		value != "." &&
		value != ".." &&
		path.Base(value) == value &&
		!strings.ContainsAny(value, `/\`)
}

func safeRelativeCachePath(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" &&
		value != "." &&
		!filepath.IsAbs(value) &&
		filepath.Clean(value) == value &&
		!strings.HasPrefix(value, ".."+string(filepath.Separator))
}

func normalizedExtension(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "." ||
		strings.ContainsAny(value, `/\`) {
		return ""
	}
	if !strings.HasPrefix(value, ".") {
		value = "." + value
	}
	return value
}

func administrationMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if separator := strings.IndexByte(value, ';'); separator >= 0 {
		value = strings.TrimSpace(value[:separator])
	}
	return value
}

func sourceHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

type contextReadCloser struct {
	io.Reader
	closer io.Closer
	cancel context.CancelFunc
}

func (body *contextReadCloser) Close() error {
	body.cancel()
	return body.closer.Close()
}

package legacyassetproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var ErrSourceTooLarge = errors.New(
	"legacyassetproxy: source exceeds byte limit",
)

type URLRefresher interface {
	Refresh(
		context.Context,
		string,
		string,
	) (string, error)
}

type SourceConfig struct {
	Root          string
	ReadOnlyRoots []string
	Schema        catalog.AssetProxyRuntimeSchema
	AllowedHosts  []string
	Transport     http.RoundTripper
	Clock         func() time.Time
	Refresher     URLRefresher
}

type Source struct {
	root           string
	cacheRoots     []string
	schema         catalog.AssetProxyRuntimeSchema
	allowedHosts   []string
	allowedSchemes map[string]struct{}
	client         *http.Client
	clock          func() time.Time
	refresher      URLRefresher
	extensionMedia map[string]string
	lockGuard      sync.Mutex
	locks          map[string]*sourceLock
}

type sourceLock struct {
	mutex sync.Mutex
	refs  int
}

func NewSource(config SourceConfig) (*Source, error) {
	root := strings.TrimSpace(config.Root)
	if root == "" {
		return nil, errors.New("legacyassetproxy: cache root is required")
	}
	if len(config.AllowedHosts) == 0 ||
		config.Schema.HTTP.RequestTimeoutMilliseconds <= 0 ||
		config.Schema.HTTP.ResponseMaxBytes <= 0 ||
		config.Schema.HTTP.MaximumRedirects <= 0 {
		return nil, errors.New("legacyassetproxy: invalid source policy")
	}
	allowedHosts := make([]string, 0, len(config.AllowedHosts))
	for _, host := range config.AllowedHosts {
		normalized := normalizeHost(host)
		if normalized == "" {
			return nil, errors.New(
				"legacyassetproxy: invalid allowed host",
			)
		}
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
	if config.Transport == nil {
		config.Transport = http.DefaultTransport
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	cleanRoot := filepath.Clean(root)
	cacheRoots := []string{cleanRoot}
	for _, readOnlyRoot := range config.ReadOnlyRoots {
		readOnlyRoot = strings.TrimSpace(readOnlyRoot)
		if readOnlyRoot == "" {
			return nil, errors.New(
				"legacyassetproxy: read-only cache root is empty",
			)
		}
		readOnlyRoot = filepath.Clean(readOnlyRoot)
		if readOnlyRoot == cleanRoot {
			continue
		}
		cacheRoots = append(cacheRoots, readOnlyRoot)
	}
	source := &Source{
		root:           cleanRoot,
		cacheRoots:     cacheRoots,
		schema:         config.Schema,
		allowedHosts:   allowedHosts,
		allowedSchemes: allowedSchemes,
		clock:          config.Clock,
		refresher:      config.Refresher,
		extensionMedia: reverseMediaTypeExtensions(
			config.Schema.MediaTypeExtensions,
		),
		locks: make(map[string]*sourceLock),
	}
	source.client = &http.Client{
		Transport: config.Transport,
		Timeout: time.Duration(
			config.Schema.HTTP.RequestTimeoutMilliseconds,
		) * time.Millisecond,
		CheckRedirect: source.checkRedirect,
	}
	return source, nil
}

func (source *Source) Fetch(
	ctx context.Context,
	request assetdelivery.SourceRequest,
) (assetdelivery.SourceContent, error) {
	release := source.acquire(sourceHash(request.URL))
	defer release()

	if cached, found := source.openCached(request.URL); found {
		return cached, nil
	}
	content, err := source.download(
		ctx,
		request.URL,
		source.schema.Cache.MissValue,
	)
	if err == nil {
		return content, nil
	}
	if strings.TrimSpace(request.TaskID) == "" ||
		source.refresher == nil {
		return assetdelivery.SourceContent{}, err
	}
	refreshedURL, refreshErr := source.refresher.Refresh(
		ctx,
		request.TaskID,
		request.URL,
	)
	if refreshErr != nil || !source.allowedURL(refreshedURL) {
		if refreshErr != nil {
			return assetdelivery.SourceContent{}, fmt.Errorf(
				"legacyassetproxy: refresh URL: %w",
				refreshErr,
			)
		}
		return assetdelivery.SourceContent{},
			errors.New("legacyassetproxy: refreshed URL rejected")
	}
	if cached, found := source.openCached(refreshedURL); found {
		cached.CacheStatus = source.schema.Cache.RefreshedValue
		return cached, nil
	}
	return source.download(
		ctx,
		refreshedURL,
		source.schema.Cache.RefreshedValue,
	)
}

func (source *Source) download(
	ctx context.Context,
	targetURL string,
	cacheStatus string,
) (assetdelivery.SourceContent, error) {
	requestContext, cancel := context.WithTimeout(
		ctx,
		time.Duration(
			source.schema.HTTP.RequestTimeoutMilliseconds,
		)*time.Millisecond,
	)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		targetURL,
		nil,
	)
	if err != nil {
		return assetdelivery.SourceContent{}, err
	}
	response, err := source.client.Do(request)
	if err != nil {
		return assetdelivery.SourceContent{}, fmt.Errorf(
			"legacyassetproxy: fetch source: %w",
			err,
		)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return assetdelivery.SourceContent{},
			assetdelivery.ErrSourceNotFound
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return assetdelivery.SourceContent{}, fmt.Errorf(
			"legacyassetproxy: source HTTP status %d",
			response.StatusCode,
		)
	}
	maximumBytes := source.schema.HTTP.ResponseMaxBytes
	if response.ContentLength > maximumBytes {
		return assetdelivery.SourceContent{}, ErrSourceTooLarge
	}

	hash := sourceHash(targetURL)
	directory := filepath.Join(
		source.root,
		hash[:source.schema.Cache.ShardCharacters],
	)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return assetdelivery.SourceContent{}, err
	}
	temporary, err := os.CreateTemp(directory, hash+".download-*")
	if err != nil {
		return assetdelivery.SourceContent{}, err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	written, copyErr := io.Copy(
		temporary,
		io.LimitReader(response.Body, maximumBytes+1),
	)
	if copyErr != nil {
		cleanup()
		return assetdelivery.SourceContent{}, copyErr
	}
	if written > maximumBytes {
		cleanup()
		return assetdelivery.SourceContent{}, ErrSourceTooLarge
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return assetdelivery.SourceContent{}, err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return assetdelivery.SourceContent{}, err
	}

	mediaType := responseMediaType(
		response.Header.Get("Content-Type"),
		targetURL,
		source.extensionMedia,
		source.schema.DefaultMediaType,
	)
	extension := source.schema.MediaTypeExtensions[mediaType]
	if extension == "" {
		extension = source.schema.
			MediaTypeExtensions[source.schema.DefaultMediaType]
	}
	if extension == "" {
		_ = os.Remove(temporaryPath)
		return assetdelivery.SourceContent{}, errors.New(
			"legacyassetproxy: no Schema extension for media type",
		)
	}
	finalPath := filepath.Join(directory, hash+extension)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = os.Remove(temporaryPath)
		return assetdelivery.SourceContent{}, err
	}
	if err := source.writeMetadata(
		directory,
		hash,
		targetURL,
		mediaType,
		written,
	); err != nil {
		return assetdelivery.SourceContent{}, err
	}
	body, err := os.Open(finalPath)
	if err != nil {
		return assetdelivery.SourceContent{}, err
	}
	return assetdelivery.SourceContent{
		Body:        body,
		MediaType:   mediaType,
		Size:        written,
		CacheStatus: cacheStatus,
	}, nil
}

func (source *Source) openCached(
	targetURL string,
) (assetdelivery.SourceContent, bool) {
	for _, root := range source.cacheRoots {
		for _, hash := range source.cacheHashes(targetURL) {
			if content, found := source.openCachedHash(
				root,
				hash,
				targetURL,
			); found {
				return content, true
			}
		}
	}
	return assetdelivery.SourceContent{}, false
}

func (source *Source) openCachedHash(
	root string,
	hash string,
	targetURL string,
) (assetdelivery.SourceContent, bool) {
	directory := filepath.Join(
		root,
		hash[:source.schema.Cache.ShardCharacters],
	)
	metadataPath := filepath.Join(
		directory,
		hash+source.schema.Cache.MetadataSuffix,
	)
	metadataContent, err := os.ReadFile(metadataPath)
	if err != nil {
		return assetdelivery.SourceContent{}, false
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(metadataContent, &metadata); err != nil {
		return assetdelivery.SourceContent{}, false
	}
	fields := source.schema.Cache.MetadataFields
	var metadataURL, mediaType string
	if json.Unmarshal(metadata[fields.URL], &metadataURL) != nil ||
		!source.sameCachedSource(metadataURL, targetURL) ||
		json.Unmarshal(metadata[fields.ContentType], &mediaType) != nil {
		return assetdelivery.SourceContent{}, false
	}
	mediaType = normalizedMediaType(mediaType)
	extension := source.schema.MediaTypeExtensions[mediaType]
	if extension == "" {
		return assetdelivery.SourceContent{}, false
	}
	finalPath := filepath.Join(directory, hash+extension)
	info, err := os.Stat(finalPath)
	if err != nil || !info.Mode().IsRegular() {
		return assetdelivery.SourceContent{}, false
	}
	body, err := os.Open(finalPath)
	if err != nil {
		return assetdelivery.SourceContent{}, false
	}
	return assetdelivery.SourceContent{
		Body:        body,
		MediaType:   mediaType,
		Size:        info.Size(),
		CacheStatus: source.schema.Cache.HitValue,
	}, true
}

func (source *Source) cacheHashes(targetURL string) []string {
	full := sourceHash(targetURL)
	parsed, err := url.Parse(targetURL)
	if err != nil || !source.allowedURL(targetURL) {
		return []string{full}
	}
	pathHash := sourceHash(parsed.EscapedPath())
	if pathHash == full {
		return []string{full}
	}
	return []string{pathHash, full}
}

func (source *Source) sameCachedSource(
	cachedURL string,
	targetURL string,
) bool {
	if cachedURL == targetURL {
		return source.allowedURL(targetURL)
	}
	cached, cachedErr := url.Parse(cachedURL)
	target, targetErr := url.Parse(targetURL)
	if cachedErr != nil ||
		targetErr != nil ||
		!source.allowedURL(cachedURL) ||
		!source.allowedURL(targetURL) {
		return false
	}
	return normalizeHost(cached.Hostname()) ==
		normalizeHost(target.Hostname()) &&
		cached.EscapedPath() == target.EscapedPath()
}

func (source *Source) writeMetadata(
	directory string,
	hash string,
	targetURL string,
	mediaType string,
	size int64,
) error {
	fields := source.schema.Cache.MetadataFields
	content, err := json.Marshal(map[string]any{
		fields.URL:         targetURL,
		fields.ContentType: mediaType,
		fields.Size:        size,
		fields.CachedAt: source.clock().UTC().Format(
			time.RFC3339Nano,
		),
	})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, hash+".metadata-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return os.Rename(
		temporaryPath,
		filepath.Join(
			directory,
			hash+source.schema.Cache.MetadataSuffix,
		),
	)
}

func (source *Source) checkRedirect(
	request *http.Request,
	via []*http.Request,
) error {
	if len(via) > source.schema.HTTP.MaximumRedirects {
		return errors.New("legacyassetproxy: too many redirects")
	}
	if !source.allowedURL(request.URL.String()) {
		return errors.New("legacyassetproxy: redirect target rejected")
	}
	return nil
}

func (source *Source) allowedURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if _, exists := source.allowedSchemes[strings.ToLower(parsed.Scheme)]; !exists {
		return false
	}
	return sourceHostAllowed(parsed.Hostname(), source.allowedHosts)
}

func (source *Source) acquire(key string) func() {
	source.lockGuard.Lock()
	lock := source.locks[key]
	if lock == nil {
		lock = &sourceLock{}
		source.locks[key] = lock
	}
	lock.refs++
	source.lockGuard.Unlock()
	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		source.lockGuard.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(source.locks, key)
		}
		source.lockGuard.Unlock()
	}
}

func sourceHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeHost(value string) string {
	return strings.TrimSuffix(
		strings.ToLower(strings.TrimSpace(value)),
		".",
	)
}

func sourceHostAllowed(host string, allowedHosts []string) bool {
	normalized := normalizeHost(host)
	for _, allowed := range allowedHosts {
		if normalized == allowed ||
			strings.HasSuffix(normalized, "."+allowed) {
			return true
		}
	}
	return false
}

func normalizedMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err == nil {
		return strings.ToLower(strings.TrimSpace(mediaType))
	}
	return ""
}

func reverseMediaTypeExtensions(
	mediaTypeExtensions map[string]string,
) map[string]string {
	result := make(map[string]string, len(mediaTypeExtensions))
	for mediaType, extension := range mediaTypeExtensions {
		result[strings.ToLower(extension)] = mediaType
	}
	return result
}

func responseMediaType(
	header string,
	targetURL string,
	extensionMedia map[string]string,
	defaultMediaType string,
) string {
	if mediaType := normalizedMediaType(header); mediaType != "" &&
		mediaType != "application/octet-stream" {
		return mediaType
	}
	parsed, err := url.Parse(targetURL)
	if err == nil {
		extension := strings.ToLower(filepath.Ext(parsed.Path))
		if mediaType := extensionMedia[extension]; mediaType != "" {
			return mediaType
		}
	}
	if mediaType := normalizedMediaType(header); mediaType != "" {
		return mediaType
	}
	return defaultMediaType
}

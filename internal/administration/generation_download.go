package administration

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrGenerationDownloadNotFound = errors.New(
		"administration: generation download task not found",
	)
	ErrGenerationDownloadNoAssets = errors.New(
		"administration: generation download has no assets",
	)
	ErrGenerationDownloadFailed = errors.New(
		"administration: generation download failed",
	)
	ErrGenerationDownloadTooLarge = errors.New(
		"administration: generation download is too large",
	)
)

type GenerationDetailReader interface {
	GetGeneration(
		context.Context,
		string,
	) (GenerationDetail, bool, error)
}

type privateGenerationDownloadDetailReader interface {
	GetGenerationForDownload(
		context.Context,
		string,
	) (GenerationDetail, bool, error)
}

type GenerationAssetContent struct {
	Body      io.ReadCloser
	MediaType string
	Size      int64
}

type GenerationAssetRequest struct {
	OwnerID string
	TaskID  string
	Asset   GenerationAsset
}

type GenerationAssetReader interface {
	OpenGenerationAsset(
		context.Context,
		GenerationAssetRequest,
	) (GenerationAssetContent, error)
}

type GenerationAssetReaderFunc func(
	context.Context,
	GenerationAssetRequest,
) (GenerationAssetContent, error)

func (reader GenerationAssetReaderFunc) OpenGenerationAsset(
	ctx context.Context,
	request GenerationAssetRequest,
) (GenerationAssetContent, error) {
	return reader(ctx, request)
}

type GenerationDownloadPackage struct {
	Filename  string
	MediaType string
	Body      []byte
}

type GenerationDownloadServiceConfig struct {
	Details                  GenerationDetailReader
	Assets                   GenerationAssetReader
	DownloadFilenameTemplate string
	AssetFilenameTemplate    string
	VariantSeparator         string
	DuplicateSeparator       string
	OrdinalBase              int
	ArchiveMediaType         string
	DefaultMediaType         string
	UnknownExtension         string
	MaximumPackageBytes      int64
	MediaTypeExtensions      map[string]string
}

type GenerationDownloadService struct {
	details                  GenerationDetailReader
	assets                   GenerationAssetReader
	downloadFilenameTemplate string
	assetFilenameTemplate    string
	variantSeparator         string
	duplicateSeparator       string
	ordinalBase              int
	archiveMediaType         string
	defaultMediaType         string
	unknownExtension         string
	maximumPackageBytes      int64
	mediaTypeExtensions      map[string]string
}

func NewGenerationDownloadService(
	config GenerationDownloadServiceConfig,
) (*GenerationDownloadService, error) {
	if config.Details == nil || config.Assets == nil {
		return nil, errors.New(
			"administration: generation download dependencies are missing",
		)
	}
	for name, value := range map[string]string{
		"download filename template": config.DownloadFilenameTemplate,
		"asset filename template":    config.AssetFilenameTemplate,
		"variant separator":          config.VariantSeparator,
		"duplicate separator":        config.DuplicateSeparator,
		"archive media type":         config.ArchiveMediaType,
		"default media type":         config.DefaultMediaType,
		"unknown extension":          config.UnknownExtension,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf(
				"administration: generation download %s is empty",
				name,
			)
		}
	}
	if config.OrdinalBase <= 0 ||
		config.MaximumPackageBytes <= 0 ||
		len(config.MediaTypeExtensions) == 0 {
		return nil, errors.New(
			"administration: generation download limits are invalid",
		)
	}
	return &GenerationDownloadService{
		details:                  config.Details,
		assets:                   config.Assets,
		downloadFilenameTemplate: config.DownloadFilenameTemplate,
		assetFilenameTemplate:    config.AssetFilenameTemplate,
		variantSeparator:         config.VariantSeparator,
		duplicateSeparator:       config.DuplicateSeparator,
		ordinalBase:              config.OrdinalBase,
		archiveMediaType:         config.ArchiveMediaType,
		defaultMediaType:         config.DefaultMediaType,
		unknownExtension:         config.UnknownExtension,
		maximumPackageBytes:      config.MaximumPackageBytes,
		mediaTypeExtensions: cloneDownloadStringMap(
			config.MediaTypeExtensions,
		),
	}, nil
}

// GenerationDownloadStream is an opened download whose bytes have not been
// read yet. OpenDownload checks ownership, opens every readable asset and
// enforces the package bound before the HTTP handler writes a status, so a
// large task no longer has to fit in memory. WriteTo then streams the single
// asset or the ZIP directly to the response.
type GenerationDownloadStream struct {
	Filename  string
	MediaType string
	// Size is the exact body length for a single asset, or -1 when the ZIP
	// length is only known after it has been written.
	Size     int64
	single   bool
	maximum  int64
	filename string
	assets   []streamedGenerationAsset
}

type streamedGenerationAsset struct {
	filename  string
	mediaType string
	body      io.ReadCloser
	size      int64
}

func (stream *GenerationDownloadStream) Close() error {
	var first error
	for index := range stream.assets {
		if stream.assets[index].body == nil {
			continue
		}
		if err := stream.assets[index].body.Close(); err != nil && first == nil {
			first = err
		}
		stream.assets[index].body = nil
	}
	return first
}

func (service *GenerationDownloadService) Download(
	ctx context.Context,
	taskID string,
) (GenerationDownloadPackage, error) {
	stream, err := service.OpenDownload(ctx, taskID)
	if err != nil {
		return GenerationDownloadPackage{}, err
	}
	defer stream.Close()
	var buffer bytes.Buffer
	if _, err := stream.WriteTo(&buffer); err != nil {
		if errors.Is(err, ErrGenerationDownloadTooLarge) {
			return GenerationDownloadPackage{}, err
		}
		return GenerationDownloadPackage{}, fmt.Errorf(
			"%w: %v", ErrGenerationDownloadFailed, err,
		)
	}
	if stream.maximum > 0 && int64(buffer.Len()) > stream.maximum {
		return GenerationDownloadPackage{}, ErrGenerationDownloadTooLarge
	}
	return GenerationDownloadPackage{
		Filename:  stream.Filename,
		MediaType: stream.MediaType,
		Body:      buffer.Bytes(),
	}, nil
}

// OpenDownload resolves and opens everything the package needs. It returns
// the first error that prevents a meaningful response (missing task, no
// assets, nothing readable, over the bound); a later read failure can only
// truncate the stream, which the handler reports in the log.
func (service *GenerationDownloadService) OpenDownload(
	ctx context.Context,
	taskID string,
) (*GenerationDownloadStream, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrGenerationDownloadNotFound
	}
	var detail GenerationDetail
	var found bool
	var err error
	if privateReader, ok := service.details.(privateGenerationDownloadDetailReader); ok {
		detail, found, err = privateReader.GetGenerationForDownload(ctx, taskID)
	} else {
		detail, found, err = service.details.GetGeneration(ctx, taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGenerationDownloadFailed, err)
	}
	if !found {
		return nil, ErrGenerationDownloadNotFound
	}
	if len(detail.Ledger.Assets) == 0 {
		return nil, ErrGenerationDownloadNoAssets
	}

	stream := &GenerationDownloadStream{maximum: service.maximumPackageBytes, Size: -1}
	fail := func(result error) (*GenerationDownloadStream, error) {
		_ = stream.Close()
		return nil, result
	}
	used := make(map[string]struct{}, len(detail.Ledger.Assets))
	var totalBytes int64
	for _, asset := range detail.Ledger.Assets {
		content, openErr := service.assets.OpenGenerationAsset(
			ctx,
			GenerationAssetRequest{
				OwnerID: detail.Ledger.UserID,
				TaskID:  detail.Ledger.TaskID,
				Asset:   asset,
			},
		)
		if openErr != nil {
			if ctx.Err() != nil {
				return fail(ctx.Err())
			}
			continue
		}
		if content.Body == nil {
			continue
		}
		if stream.maximum > 0 && content.Size > stream.maximum-totalBytes {
			_ = content.Body.Close()
			return fail(ErrGenerationDownloadTooLarge)
		}
		totalBytes += content.Size
		mediaType := normalizedMediaType(content.MediaType)
		if mediaType == "" {
			mediaType = normalizedMediaType(asset.MediaType)
		}
		if mediaType == "" {
			mediaType = service.defaultMediaType
		}
		stream.assets = append(stream.assets, streamedGenerationAsset{
			filename:  service.uniqueFilename(service.assetFilename(asset, mediaType), used),
			mediaType: mediaType,
			body:      content.Body,
			size:      content.Size,
		})
	}
	if len(stream.assets) == 0 {
		return fail(ErrGenerationDownloadFailed)
	}
	if len(detail.Ledger.Assets) == 1 && len(stream.assets) == 1 {
		stream.single = true
		stream.Filename = stream.assets[0].filename
		stream.MediaType = stream.assets[0].mediaType
		stream.Size = stream.assets[0].size
		return stream, nil
	}
	filename := strings.ReplaceAll(
		service.downloadFilenameTemplate,
		"{task_id}",
		safeDownloadToken(taskID),
	)
	stream.Filename = path.Base(filename)
	stream.MediaType = service.archiveMediaType
	return stream, nil
}

// WriteTo streams the opened assets. Each body is read to exactly the size
// recorded for it; a shorter or longer file aborts the response rather than
// emitting a package whose contents disagree with its metadata.
func (stream *GenerationDownloadStream) WriteTo(writer io.Writer) (int64, error) {
	counter := &streamCounter{writer: writer}
	if stream.single {
		asset := &stream.assets[0]
		copied, err := io.Copy(counter, io.LimitReader(asset.body, asset.size+1))
		_ = asset.body.Close()
		asset.body = nil
		if err != nil {
			return counter.count, err
		}
		if copied != asset.size {
			return counter.count, fmt.Errorf(
				"generation download: %s size changed", asset.filename,
			)
		}
		return counter.count, nil
	}
	archive := zip.NewWriter(counter)
	for index := range stream.assets {
		asset := &stream.assets[index]
		entry, err := archive.Create(asset.filename)
		if err != nil {
			return counter.count, err
		}
		copied, err := io.Copy(entry, io.LimitReader(asset.body, asset.size+1))
		_ = asset.body.Close()
		asset.body = nil
		if err != nil {
			// Returning without Close leaves the response without a central
			// directory, so a truncated read cannot be mistaken for a
			// complete package.
			return counter.count, err
		}
		if copied != asset.size {
			return counter.count, fmt.Errorf(
				"generation download: %s size changed", asset.filename,
			)
		}
	}
	if err := archive.Close(); err != nil {
		return counter.count, err
	}
	return counter.count, nil
}

type streamCounter struct {
	writer io.Writer
	count  int64
}

func (w *streamCounter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.count += int64(n)
	return n, err
}

func (service *GenerationDownloadService) assetFilename(
	asset GenerationAsset,
	mediaType string,
) string {
	extension := service.mediaTypeExtensions[normalizedMediaType(mediaType)]
	if extension == "" {
		extension = service.unknownExtension
	}
	if preferred := safeDownloadFilename(
		asset.PreferredFilename,
	); preferred != "" {
		if path.Ext(preferred) == "" {
			preferred += extension
		}
		return preferred
	}
	role := safeDownloadToken(asset.Role)
	if role == "" {
		role = safeDownloadToken(asset.ID)
	}
	variant := safeDownloadToken(asset.Variant)
	variantSegment := ""
	if variant != "" {
		variantSegment = service.variantSeparator + variant
	}
	replacements := map[string]string{
		"{role}":            role,
		"{variant_segment}": variantSegment,
		"{ordinal}": strconv.Itoa(
			asset.Ordinal + service.ordinalBase,
		),
		"{extension}": extension,
	}
	filename := service.assetFilenameTemplate
	for token, value := range replacements {
		filename = strings.ReplaceAll(filename, token, value)
	}
	return path.Base(filename)
}

func (service *GenerationDownloadService) uniqueFilename(
	filename string,
	used map[string]struct{},
) string {
	if _, exists := used[filename]; !exists {
		used[filename] = struct{}{}
		return filename
	}
	extension := path.Ext(filename)
	base := strings.TrimSuffix(filename, extension)
	for counter := service.ordinalBase; ; counter++ {
		candidate := base +
			service.duplicateSeparator +
			strconv.Itoa(counter) +
			extension
		if _, exists := used[candidate]; exists {
			continue
		}
		used[candidate] = struct{}{}
		return candidate
	}
}

func normalizedMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if separator := strings.IndexByte(value, ';'); separator >= 0 {
		value = strings.TrimSpace(value[:separator])
	}
	return value
}

func safeDownloadToken(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, current := range value {
		switch {
		case unicode.IsLetter(current), unicode.IsDigit(current):
			result.WriteRune(current)
		case current == '-', current == '_', current == '.':
			result.WriteRune(current)
		default:
			result.WriteByte('_')
		}
	}
	return strings.Trim(result.String(), "._")
}

func safeDownloadFilename(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	value = path.Base(value)
	if value == "." || value == "/" {
		return ""
	}
	return safeDownloadToken(value)
}

func cloneDownloadStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

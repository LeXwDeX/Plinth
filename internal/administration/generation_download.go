package administration

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/assets/samebytes"
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
	// repeatText is the README wording for a file the package leaves out because
	// another entry holds its bytes (#300). Without it no file is left out.
	repeatText func(context.Context) samebytes.Text
}

// WithRepeatText gives the package the README wording that names a repeated file,
// in the UI locale of the request. It is the one-click ZIP's wording, so a file
// left out of either package is explained the same way (#300). It is set once,
// while the service is assembled.
func (service *GenerationDownloadService) WithRepeatText(text func(context.Context) samebytes.Text) {
	service.repeatText = text
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
	// readme names the files the ZIP leaves out because another entry holds their
	// bytes; empty when nothing was left out (#300).
	readmeName string
	readme     []byte
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
	used := make(map[string]struct{}, len(detail.Ledger.Assets)+1)
	// A file whose record names the same bytes as an earlier one is packed once and
	// the README says which entry holds it, as in the one-click ZIP (#300). Without
	// the wording for that README nothing is left out.
	text := service.repeatTextFor(ctx)
	readmeName := safeDownloadFilename(text.Filename)
	var repeats []samebytes.Repeat
	var totalBytes int64
	for _, group := range repeatGroups(detail.Ledger.Assets, text.Declared() && readmeName != "") {
		// The first record that opens stands for the group; a later one only
		// opens when the earlier ones could not be read.
		var (
			content GenerationAssetContent
			kept    = -1
		)
		for index, asset := range group {
			opened, openErr := service.assets.OpenGenerationAsset(
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
			if opened.Body == nil {
				continue
			}
			content, kept = opened, index
			break
		}
		if kept < 0 {
			continue
		}
		asset := group[kept]
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
		filename := service.uniqueFilename(service.assetFilename(asset, mediaType), used)
		stream.assets = append(stream.assets, streamedGenerationAsset{
			filename:  filename,
			mediaType: mediaType,
			body:      content.Body,
			size:      content.Size,
		})
		for index, other := range group {
			if index != kept {
				repeats = append(repeats, samebytes.Repeat{Label: samebytes.Label(other.Role, other.Variant), Path: filename})
			}
		}
	}
	if len(stream.assets) == 0 {
		return fail(ErrGenerationDownloadFailed)
	}
	if len(repeats) > 0 {
		stream.readmeName = service.uniqueFilename(readmeName, used)
		stream.readme = []byte(strings.Join(text.Section(repeats), "\n") + "\n")
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
	if len(stream.readme) > 0 {
		entry, err := archive.Create(stream.readmeName)
		if err != nil {
			return counter.count, err
		}
		if _, err := entry.Write(stream.readme); err != nil {
			return counter.count, err
		}
	}
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

// repeatTextFor is the README wording for the request, or none when the service
// was given no wording.
func (service *GenerationDownloadService) repeatTextFor(ctx context.Context) samebytes.Text {
	if service.repeatText == nil {
		return samebytes.Text{}
	}
	return service.repeatText(ctx)
}

// repeatGroups lines the records up in ledger order and gathers those that name
// the same bytes behind the first of them (samebytes.Same, the rule of the
// one-click ZIP). With collapse false every record is its own group, as before.
// A record that names no digest is always alone.
func repeatGroups(assets []GenerationAsset, collapse bool) [][]GenerationAsset {
	groups := make([][]GenerationAsset, 0, len(assets))
	for _, asset := range assets {
		if collapse {
			record := samebytes.Record{SHA256: asset.SHA256, Size: asset.Size}
			if index := slices.IndexFunc(groups, func(group []GenerationAsset) bool {
				return samebytes.Same(samebytes.Record{SHA256: group[0].SHA256, Size: group[0].Size}, record)
			}); index >= 0 {
				groups[index] = append(groups[index], asset)
				continue
			}
		}
		groups = append(groups, []GenerationAsset{asset})
	}
	return groups
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

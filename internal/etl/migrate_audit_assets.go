package etl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	errLegacyAuditAssetConflict   = errors.New("历史审计副本存在内容冲突")
	errLegacyAuditAssetUnreadable = errors.New(
		"历史审计副本无法安全读取",
	)
)

// legacyAuditAssetRecovery 把旧 Next 审计目录封装成一个精确匹配、只读的
// 恢复源。命名约定来自私有 Product Schema；调用方不需要知道旧目录布局。
type legacyAuditAssetRecovery struct {
	byFilename         map[string][]string
	filenameTemplate   string
	unknownExtension   string
	maximumBytes       int64
	defaultMediaType   string
	extensionMediaType map[string]string
}

func newLegacyAuditAssetRecovery(
	legacyStorageRoot string,
	recoverySchema catalog.LegacyAuditAssetRecoverySchema,
	assetSchema catalog.AdministrationGenerationAssetSchema,
) (legacyAuditAssetRecovery, error) {
	recovery := legacyAuditAssetRecovery{
		byFilename:         make(map[string][]string),
		filenameTemplate:   strings.TrimSpace(recoverySchema.FilenameTemplate),
		unknownExtension:   normalizedAuditExtension(assetSchema.UnknownExtension),
		maximumBytes:       assetSchema.MaximumPackageBytes,
		defaultMediaType:   strings.TrimSpace(assetSchema.DefaultMediaType),
		extensionMediaType: make(map[string]string),
	}
	for mediaType, extension := range assetSchema.MediaTypeExtensions {
		extension = normalizedAuditExtension(extension)
		if extension != "" {
			recovery.extensionMediaType[extension] =
				strings.TrimSpace(mediaType)
		}
	}

	root := filepath.Join(
		legacyStorageRoot,
		strings.TrimSpace(recoverySchema.RootSubdirectory),
	)
	err := filepath.WalkDir(root, func(
		filePath string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		recovery.byFilename[entry.Name()] = append(
			recovery.byFilename[entry.Name()],
			filePath,
		)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return recovery, nil
	}
	if err != nil {
		return legacyAuditAssetRecovery{},
			errLegacyAuditAssetUnreadable
	}
	for filename := range recovery.byFilename {
		sort.Strings(recovery.byFilename[filename])
	}
	return recovery, nil
}

func (recovery legacyAuditAssetRecovery) Open(
	ctx context.Context,
	taskID string,
	asset administration.GenerationAsset,
) (administration.GenerationAssetContent, error) {
	filename, ok := recovery.auditFilename(taskID, asset.SourceURL)
	if !ok {
		return administration.GenerationAssetContent{},
			legacygeneration.ErrLegacyAssetUnavailable
	}
	candidates := recovery.byFilename[filename]
	if len(candidates) == 0 {
		return administration.GenerationAssetContent{},
			legacygeneration.ErrLegacyAssetUnavailable
	}
	if len(candidates) > 1 {
		if err := recovery.verifyDuplicateCopies(ctx, candidates); err != nil {
			return administration.GenerationAssetContent{}, err
		}
	}
	body, size, err := recovery.openRegular(candidates[0])
	if err != nil {
		return administration.GenerationAssetContent{}, err
	}
	return administration.GenerationAssetContent{
		Body:      body,
		MediaType: recovery.mediaType(filename, asset.MediaType),
		Size:      size,
	}, nil
}

func (recovery legacyAuditAssetRecovery) auditFilename(
	taskID string,
	source string,
) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil || parsed.Path == "" {
		return "", false
	}
	sourceName, err := url.PathUnescape(path.Base(parsed.Path))
	if err != nil || sourceName == "" || sourceName == "." {
		return "", false
	}
	extension := path.Ext(sourceName)
	stem := strings.TrimSuffix(sourceName, extension)
	if extension == "" {
		extension = recovery.unknownExtension
	}
	filename := strings.NewReplacer(
		"{task_id}", strings.TrimSpace(taskID),
		"{source_stem}", stem,
		"{source_extension}", extension,
	).Replace(recovery.filenameTemplate)
	if !safeAuditFilename(filename) {
		return "", false
	}
	return filename, true
}

func (recovery legacyAuditAssetRecovery) verifyDuplicateCopies(
	ctx context.Context,
	candidates []string,
) error {
	var expectedDigest string
	var expectedSize int64
	for index, candidate := range candidates {
		body, size, err := recovery.openRegular(candidate)
		if err != nil {
			return err
		}
		hasher := sha256.New()
		_, copyErr := copyWithContext(ctx, hasher, body)
		closeErr := body.Close()
		if errors.Is(copyErr, context.Canceled) ||
			errors.Is(copyErr, context.DeadlineExceeded) {
			return copyErr
		}
		if copyErr != nil || closeErr != nil {
			return errLegacyAuditAssetUnreadable
		}
		digest := hex.EncodeToString(hasher.Sum(nil))
		if index == 0 {
			expectedDigest = digest
			expectedSize = size
			continue
		}
		if size != expectedSize || digest != expectedDigest {
			return errLegacyAuditAssetConflict
		}
	}
	return nil
}

func (recovery legacyAuditAssetRecovery) openRegular(
	filePath string,
) (*os.File, int64, error) {
	before, err := os.Lstat(filePath)
	if err != nil || !before.Mode().IsRegular() {
		return nil, 0, errLegacyAuditAssetUnreadable
	}
	if before.Size() > recovery.maximumBytes {
		return nil, 0, legacygeneration.ErrLegacyAssetTooLarge
	}
	body, err := os.Open(filePath)
	if err != nil {
		return nil, 0, errLegacyAuditAssetUnreadable
	}
	after, err := body.Stat()
	if err != nil ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) {
		_ = body.Close()
		return nil, 0, errLegacyAuditAssetUnreadable
	}
	return body, after.Size(), nil
}

func (recovery legacyAuditAssetRecovery) mediaType(
	filename string,
	projected string,
) string {
	if mediaType := strings.TrimSpace(projected); mediaType != "" {
		return mediaType
	}
	extension := normalizedAuditExtension(path.Ext(filename))
	if mediaType := recovery.extensionMediaType[extension]; mediaType != "" {
		return mediaType
	}
	return recovery.defaultMediaType
}

func normalizedAuditExtension(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, ".") {
		value = "." + value
	}
	return value
}

func safeAuditFilename(value string) bool {
	return value != "" &&
		value != "." &&
		value != ".." &&
		!filepath.IsAbs(value) &&
		!strings.ContainsAny(value, `/\`) &&
		filepath.Base(value) == value
}

func copyWithContext(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
) (int64, error) {
	var written int64
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			outputCount, writeErr := destination.Write(buffer[:count])
			written += int64(outputCount)
			if writeErr != nil {
				return written, writeErr
			}
			if outputCount != count {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

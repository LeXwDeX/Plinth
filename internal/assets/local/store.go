package local

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"golang.org/x/sys/unix"
)

type Store struct {
	lock          *os.File
	closeOnce     sync.Once
	closeErr      error
	root          string
	verified      *verificationCache
	verifiedBytes atomic.Uint64
	readOnly      bool
}

type Blob struct {
	StorageKey string
	MediaType  string
	Size       int64
	SHA256     string
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("资产根目录不能为空")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析资产根目录: %w", err)
	}
	for _, directory := range []string{
		absolute,
		filepath.Join(absolute, "blobs"),
		filepath.Join(absolute, "tmp"),
	} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return nil, fmt.Errorf("创建资产目录 %q: %w", directory, err)
		}
	}
	lock, err := storageLock(absolute, false)
	if err != nil {
		return nil, err
	}
	return &Store{root: absolute, lock: lock, verified: newVerificationCache(0)}, nil
}

func (s *Store) Put(
	ctx context.Context,
	write generation.AssetWrite,
) (generation.Asset, error) {
	if s.readOnly {
		return generation.Asset{}, errors.New("资产存储只读")
	}
	if write.AssetID == "" || write.OwnerID == "" || write.TaskID == "" || write.AttemptID == "" {
		return generation.Asset{}, diagnoseWrite("identity", "invalid_identity", errors.New("资产写入缺少身份"))
	}
	if write.Body == nil {
		return generation.Asset{}, diagnoseWrite("input", "empty_content", errors.New("资产写入缺少内容"))
	}
	blob, err := s.putBlob(ctx, write.Body, write.MediaType, write.MaxBytes, func(file *os.File, finalMediaType string) error {
		if err := inspectProfiledAsset(file, write.DeliveryProfile, write.Role, write.Variant, write.MediaType); err != nil {
			return err
		}
		if finalMediaType != write.MediaType {
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}
			return inspectProfiledAsset(file, write.DeliveryProfile, write.Role, write.Variant, finalMediaType)
		}
		return nil
	})
	if err != nil {
		return generation.Asset{}, err
	}
	asset := generation.Asset{
		DeliveryProfile: write.DeliveryProfile,
		ID:              write.AssetID,
		TaskID:          write.TaskID,
		AttemptID:       write.AttemptID,
		Role:            write.Role,
		Variant:         write.Variant,
		Required:        write.Required,
		Ordinal:         write.Ordinal,
		StorageKey:      blob.StorageKey,
		MediaType:       blob.MediaType,
		Size:            blob.Size,
		SHA256:          blob.SHA256,
		CreatedAt:       time.Now().UTC(),
	}
	// The bytes were just inspected at the temporary path; the atomic commit
	// kept them unchanged. Remember that verdict so the first download of a
	// freshly materialized task does not repeat the full parse.
	s.rememberInspected(asset)
	return asset, nil
}

func (s *Store) PutBlob(
	ctx context.Context,
	body io.Reader,
	declaredMediaType string,
	maxBytes int64,
) (Blob, error) {
	return s.putBlob(ctx, body, declaredMediaType, maxBytes, nil)
}

func (s *Store) putBlob(
	ctx context.Context,
	body io.Reader,
	declaredMediaType string,
	maxBytes int64,
	inspect func(*os.File, string) error,
) (blob Blob, resultErr error) {
	stage, reason := "input", "empty_content"
	defer func() { resultErr = diagnoseWrite(stage, reason, resultErr) }()
	if body == nil {
		return Blob{}, errors.New("Blob 写入缺少内容")
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 30
	}
	stage, reason = "directory_check", "io_failure"
	for _, directory := range []string{filepath.Join(s.root, "tmp"), filepath.Join(s.root, "blobs")} {
		if err := requireBlobDirectory(directory); err != nil {
			return Blob{}, err
		}
	}

	stage = "temp_create"
	temp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "asset-*")
	if err != nil {
		return Blob{}, fmt.Errorf("创建资产临时文件: %w", err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	hasher := sha256.New()
	buffered := bufio.NewReader(body)
	header, _ := buffered.Peek(512)
	detectedType := http.DetectContentType(header)
	mediaType := normalizeMediaType(declaredMediaType)
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = normalizeMediaType(detectedType)
	}
	// A provider may only guess a picture's type (from a URL extension, say). When
	// the bytes are a different raster format, the bytes win, so the asset is
	// served with the type it really has (#264: Tripo PNGs recorded as WebP).
	if detected := normalizeMediaType(detectedType); strings.HasPrefix(mediaType, "image/") &&
		strings.HasPrefix(detected, "image/") && detected != mediaType {
		mediaType = detected
	}

	reader := io.LimitReader(buffered, maxBytes+1)
	stage = "write"
	written, err := io.Copy(io.MultiWriter(temp, hasher), &contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return Blob{}, fmt.Errorf("写入资产内容: %w", err)
	}
	if written > maxBytes {
		reason = "size_limit"
		return Blob{}, fmt.Errorf("资产超过大小上限 %d 字节", maxBytes)
	}
	if written == 0 {
		reason = "empty_content"
		return Blob{}, errors.New("资产内容为空")
	}
	stage = "temp_sync"
	if err := temp.Sync(); err != nil {
		return Blob{}, fmt.Errorf("同步资产临时文件: %w", err)
	}
	stage = "temp_close"
	if err := temp.Close(); err != nil {
		return Blob{}, fmt.Errorf("关闭资产临时文件: %w", err)
	}
	if inspect != nil {
		stage = "inspect_open"
		file, err := os.Open(tempName)
		if err != nil {
			return Blob{}, fmt.Errorf("检查待缓存资产: %w", err)
		}
		stage = "inspect"
		inspectErr := inspect(file, mediaType)
		closeErr := file.Close()
		if inspectErr != nil {
			reason = "content_rejected"
			return Blob{}, fmt.Errorf("资产包含不可交付的外部依赖: %w", inspectErr)
		}
		stage = "inspect_close"
		if closeErr != nil {
			return Blob{}, fmt.Errorf("关闭待检查资产: %w", closeErr)
		}
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	extension := extensionForMediaType(mediaType)
	storageKey := filepath.Join("blobs", digest[:2], digest+extension)
	stage = "cas_path"
	destination, err := s.safePath(storageKey)
	if err != nil {
		return Blob{}, err
	}
	stage = "root_open"
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return Blob{}, fmt.Errorf("打开资产根目录: %w", err)
	}
	defer root.Close()
	stage = "cas_directory"
	if err := root.MkdirAll(filepath.Dir(storageKey), 0o750); err != nil {
		return Blob{}, fmt.Errorf("创建内容寻址目录: %w", err)
	}
	if err := requireBlobDirectory(filepath.Dir(destination)); err != nil {
		return Blob{}, err
	}
	// A CAS hit is trustworthy only when the bytes at the key still match it.
	// Rename also repairs a corrupt hit without exposing a partly written file.
	stage = "cas_verify"
	valid, err := verifyExistingBlob(root, storageKey, written, digest)
	if err != nil {
		return Blob{}, fmt.Errorf("检查内容寻址资产: %w", err)
	}
	if valid {
		stage = "temp_cleanup"
		if err := os.Remove(tempName); err != nil {
			return Blob{}, fmt.Errorf("清理重复资产临时文件: %w", err)
		}
	} else {
		stage = "cas_rename"
		if err := root.Rename(filepath.Join("tmp", filepath.Base(tempName)), storageKey); err != nil {
			return Blob{}, fmt.Errorf("原子提交资产: %w", err)
		}
		stage = "cas_sync"
		if err := syncBlobDirectory(root, filepath.Dir(storageKey)); err != nil {
			return Blob{}, fmt.Errorf("同步内容寻址目录: %w", err)
		}
	}
	committed = true

	return Blob{
		StorageKey: filepath.ToSlash(storageKey),
		MediaType:  mediaType,
		Size:       written,
		SHA256:     digest,
	}, nil
}

func verifyExistingBlob(root *os.Root, key string, expectedSize int64, expectedDigest string) (bool, error) {
	info, err := root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("内容寻址资产不是普通文件: %q", key)
	}
	// O_NOFOLLOW closes the final-component symlink race between Lstat and Open.
	file, err := root.OpenFile(key, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("内容寻址资产不是普通文件: %q", key)
	}
	if info.Size() != expectedSize {
		return false, nil
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return false, err
	}
	return hex.EncodeToString(hasher.Sum(nil)) == expectedDigest, nil
}

func requireBlobDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("检查资产目录 %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("资产目录不是普通目录: %q", path)
	}
	return nil
}

func syncBlobDirectory(root *os.Root, key string) error {
	directory, err := root.Open(key)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *Store) OpenBlob(_ context.Context, storageKey string) (io.ReadCloser, error) {
	file, err := s.openStoredFile(storageKey)
	if err != nil {
		return nil, fmt.Errorf("打开 Blob: %w", err)
	}
	if digest := digestFromStorageKey(storageKey); digest != "" {
		if err := verifyAssetFile(file, generation.Asset{SHA256: digest}); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("Blob 完整性校验失败: %w", err)
		}
	}
	return file, nil
}

func (s *Store) Open(ctx context.Context, asset generation.Asset) (io.ReadCloser, error) {
	file, err := s.openStoredFile(asset.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("打开资产: %w", err)
	}
	// READY records are only useful while their local bytes still match the
	// materialized asset. Verify before returning a reader so HTTP cannot send
	// a successful response (and a stale ETag) for a damaged CAS entry.
	if asset.SHA256 == "" {
		asset.SHA256 = digestFromStorageKey(asset.StorageKey)
	}
	before, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("读取资产大小: %w", err)
	}
	if asset.Size > 0 && before.Size() != asset.Size {
		_ = file.Close()
		return nil, fmt.Errorf("资产完整性校验失败: 资产大小不符: expected=%d actual=%d", asset.Size, before.Size())
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if asset.SHA256 != "" {
		if err := s.verifyDigest(ctx, file, asset.SHA256, before.Size()); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("资产完整性校验失败: %w", err)
		}
	}
	if err := checkFileIdentity(file, before); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("资产校验期间文件变化: %w", err)
	}
	cacheHit := false
	if asset.SHA256 != "" {
		cacheHit = s.verified.hit(before, asset)
	} else {
		s.verified.miss()
	}
	if !cacheHit {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("重置资产读取位置: %w", err)
		}
		if err := inspectProfiledAsset(file, asset.DeliveryProfile, asset.Role, asset.Variant, asset.MediaType); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("资产包含不可交付的外部依赖: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := checkFileIdentity(file, before); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("资产校验期间文件变化: %w", err)
	}
	if !cacheHit && asset.SHA256 != "" {
		s.verified.remember(before, asset)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("重置资产读取位置: %w", err)
	}
	return file, nil
}

func (s *Store) verifyDigest(ctx context.Context, reader io.Reader, expected string, size int64) error {
	hasher := sha256.New()
	read, err := io.Copy(hasher, &contextReader{ctx: ctx, reader: reader})
	s.verifiedBytes.Add(uint64(read))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if read != size || hex.EncodeToString(hasher.Sum(nil)) != expected {
		return errors.New("资产摘要或大小不符")
	}
	return nil
}

func checkFileIdentity(file *os.File, before os.FileInfo) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameFileIdentity(fileIdentityOf(before), fileIdentityOf(after)) {
		return errors.New("文件身份不一致")
	}
	return nil
}

// rememberInspected records that the committed bytes at an asset's storage
// key were inspected when they were written. It is a no-op when the file can
// no longer be opened; a later Open then verifies it normally.
func (s *Store) rememberInspected(asset generation.Asset) {
	file, err := s.openStoredFile(asset.StorageKey)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return
	}
	s.verified.remember(info, asset)
}

func digestFromStorageKey(key string) string {
	clean := filepath.ToSlash(filepath.Clean(key))
	parts := strings.Split(clean, "/")
	if len(parts) != 3 || parts[0] != "blobs" || len(parts[1]) != 2 {
		return ""
	}
	base := strings.TrimSuffix(parts[2], filepath.Ext(parts[2]))
	if len(base) != 64 || !strings.HasPrefix(base, parts[1]) {
		return ""
	}
	if _, err := hex.DecodeString(base); err != nil {
		return ""
	}
	return base
}

func verifyAssetFile(file *os.File, asset generation.Asset) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if asset.Size > 0 && info.Size() != asset.Size {
		return fmt.Errorf("资产大小不符: expected=%d actual=%d", asset.Size, info.Size())
	}
	if asset.SHA256 == "" {
		return nil // Legacy asset records may not carry a digest.
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	if hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
		return errors.New("资产摘要不符")
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

func (s *Store) openStoredFile(storageKey string) (*os.File, error) {
	path, err := s.safePath(storageKey)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	relative, err := filepath.Rel(s.root, path)
	if err != nil {
		return nil, err
	}
	// Root confines parent symlinks; O_NOFOLLOW rejects a symlink at the key.
	file, err := root.OpenFile(relative, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("资产存储键不是常规文件")
	}
	return file, nil
}

func (s *Store) Delete(_ context.Context, _ generation.Asset) error {
	// Blob 使用内容寻址，可能被多个 Asset 复用。删除由维护命令的引用扫描统一执行；
	// 任务事务失败时保留未登记 blob 比误删共享内容更安全。
	return nil
}

func (s *Store) safePath(storageKey string) (string, error) {
	if storageKey == "" || filepath.IsAbs(storageKey) {
		return "", errors.New("无效资产存储键")
	}
	clean := filepath.Clean(filepath.FromSlash(storageKey))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("资产存储键越界")
	}
	path := filepath.Join(s.root, clean)
	relative, err := filepath.Rel(s.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("资产存储键越界")
	}
	return path, nil
}

func normalizeMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.TrimSpace(strings.Split(value, ";")[0])
	}
	return mediaType
}

func extensionForMediaType(mediaType string) string {
	switch mediaType {
	case "application/vnd.art-ai.gaussian-splat":
		return ".splat"
	case "application/vnd.art-ai.print-report+json", "application/vnd.art-ai.rig-check-report+json":
		return ".json"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	case "model/gltf-binary":
		return ".glb"
	case "model/gltf+json":
		return ".gltf"
	case "model/fbx":
		return ".fbx"
	case "model/3mf":
		return ".3mf"
	case "application/zip":
		return ".zip"
	case "application/octet-stream":
		return ".bin"
	default:
		extensions, _ := mime.ExtensionsByType(mediaType)
		if len(extensions) > 0 {
			return extensions[0]
		}
		return ".bin"
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

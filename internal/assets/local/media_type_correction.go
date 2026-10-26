package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ErrMediaTypeRejected reports that a stored asset's bytes do not pass the
// delivery checks under a proposed media type (or no longer match its
// recorded size and digest). The asset must keep its current label.
var ErrMediaTypeRejected = errors.New("资产字节不符合目标媒体类型")

// VerifyAs checks a stored asset exactly as delivery would if the asset were
// labelled mediaType: the size and SHA-256 recorded for it, then the same
// format inspection used at ingest and delivery (including the OBJ-archive
// identity check). It never changes the asset. A missing or unreadable file is
// returned as an ordinary error so the caller can try again later.
func (s *Store) VerifyAs(_ context.Context, asset generation.Asset, mediaType string) error {
	file, err := s.openStoredFile(asset.StorageKey)
	if err != nil {
		return fmt.Errorf("打开资产: %w", err)
	}
	defer file.Close()
	if asset.SHA256 == "" {
		asset.SHA256 = digestFromStorageKey(asset.StorageKey)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("读取资产: %w", err)
	}
	if asset.Size > 0 && info.Size() != asset.Size {
		return fmt.Errorf("%w: 资产大小不符", ErrMediaTypeRejected)
	}
	if asset.SHA256 != "" {
		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			return fmt.Errorf("读取资产: %w", err)
		}
		if hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
			return fmt.Errorf("%w: 资产摘要不符", ErrMediaTypeRejected)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("读取资产: %w", err)
		}
	}
	if err := inspectProfiledAsset(file, asset.DeliveryProfile, asset.Role, asset.Variant, mediaType); err != nil {
		return fmt.Errorf("%w: %v", ErrMediaTypeRejected, err)
	}
	return nil
}

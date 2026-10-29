package legacyinput

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type Repository interface {
	GetLegacyInputAsset(
		context.Context,
		generation.AssetID,
	) (generation.InputAsset, error)
}

type Config struct {
	Root string
}

type Module struct {
	root       string
	repository Repository
}

func New(config Config, repository Repository) (*Module, error) {
	if repository == nil {
		return nil, errors.New("legacy input asset repository 不能为空")
	}
	root := strings.TrimSpace(config.Root)
	if root == "" {
		return nil, errors.New("legacy input asset root 不能为空")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析 legacy input asset root: %w", err)
	}
	return &Module{
		root:       filepath.Clean(absoluteRoot),
		repository: repository,
	}, nil
}

func (m *Module) OpenAuthorized(
	ctx context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
	readAll bool,
) (generation.InputAsset, io.ReadCloser, error) {
	asset, err := m.repository.GetLegacyInputAsset(ctx, id)
	if err != nil {
		var domainErr *generation.Error
		if errors.As(err, &domainErr) {
			return generation.InputAsset{}, nil, err
		}
		return generation.InputAsset{}, nil, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "get_legacy_input_asset",
			Cause:     err,
		}
	}
	if !readAll && asset.OwnerID != owner {
		return generation.InputAsset{}, nil, &generation.Error{
			// Same answer as a missing asset: no existence oracle.
			Code:      generation.ErrNotFound,
			Operation: "get_legacy_input_asset",
		}
	}
	body, size, err := m.open(asset.StorageKey)
	if err != nil {
		return generation.InputAsset{}, nil, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "open_legacy_input_asset",
			Cause:     err,
		}
	}
	asset.Size = size
	return asset, body, nil
}

func (m *Module) open(storageKey string) (io.ReadCloser, int64, error) {
	if strings.TrimSpace(storageKey) == "" ||
		strings.ContainsRune(storageKey, '\x00') ||
		filepath.IsAbs(storageKey) {
		return nil, 0, errors.New("legacy input asset storage key 非法")
	}
	cleanKey := filepath.Clean(filepath.FromSlash(storageKey))
	if cleanKey == "." ||
		cleanKey == ".." ||
		strings.HasPrefix(cleanKey, ".."+string(filepath.Separator)) {
		return nil, 0, errors.New("legacy input asset storage key 越界")
	}
	resolvedRoot, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return nil, 0, fmt.Errorf("解析 legacy input asset root: %w", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(
		filepath.Join(resolvedRoot, cleanKey),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("解析 legacy input asset: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil ||
		relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, 0, errors.New("legacy input asset storage key 越界")
	}
	body, err := os.Open(resolvedTarget)
	if err != nil {
		return nil, 0, err
	}
	info, err := body.Stat()
	if err != nil {
		body.Close()
		return nil, 0, err
	}
	if info.IsDir() {
		body.Close()
		return nil, 0, errors.New("legacy input asset 不是文件")
	}
	return body, info.Size(), nil
}

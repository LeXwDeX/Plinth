package legacygenerated

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidFilename = errors.New("legacy generated image filename is invalid")

type Store struct {
	root string
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("legacygenerated: 缺少存储根目录")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: filepath.Clean(absolute)}, nil
}

func (s *Store) Open(
	ctx context.Context,
	filename string,
) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if filename == "" ||
		filename == "." ||
		filepath.Base(filename) != filename ||
		strings.ContainsAny(filename, `/\`) {
		return nil, 0, ErrInvalidFilename
	}
	filePath := filepath.Join(s.root, filename)
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, ErrInvalidFilename
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, err
	}
	return file, info.Size(), nil
}

package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"os"
)

// PutValidatedBlob is an opt-in pre-commit boundary for new uploaded models.
// Existing Put/PutBlob and delivered-output inspection keep their contracts.
// The validator sees the same read-only 0600 staging file that putBlob commits.
func (s *Store) PutValidatedBlob(ctx context.Context, body io.Reader, mediaType string, maxBytes int64, validate func(*os.File) error) (Blob, error) {
	if body == nil || validate == nil || maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return Blob{}, errors.New("validated blob requires body, validator and bounded size")
	}
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}
	incoming := sha256.New()
	return s.putBlob(ctx, io.TeeReader(body, incoming), mediaType, maxBytes, func(file *os.File) error {
		before, err := file.Stat()
		if err != nil {
			return err
		}
		if !before.Mode().IsRegular() || before.Mode().Perm() != 0600 {
			return errors.New("invalid validated staging file")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validate(file); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		after, err := file.Stat()
		if err != nil {
			return err
		}
		pathInfo, err := os.Lstat(file.Name())
		if err != nil {
			return err
		}
		if !pathInfo.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(before, pathInfo) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return errors.New("validated staging file changed")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		actual := sha256.New()
		n, err := io.Copy(actual, &contextReader{ctx: ctx, reader: file})
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if n != before.Size() || !bytes.Equal(actual.Sum(nil), incoming.Sum(nil)) {
			return errors.New("validated staging bytes changed")
		}
		after, err = file.Stat()
		if err != nil {
			return err
		}
		pathInfo, err = os.Lstat(file.Name())
		if err != nil {
			return err
		}
		if !pathInfo.Mode().IsRegular() || !os.SameFile(before, pathInfo) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return errors.New("validated staging file changed")
		}
		return ctx.Err()
	})
}

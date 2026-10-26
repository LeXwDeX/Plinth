package modelinput

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// ValidateGLB leaves ownership with the caller and rewinds on success. It never
// opens a dependency, starts a network operation, or writes a source file.
func ValidateGLB(ctx context.Context, file *os.File, policy Policy) (Profile, error) {
	if err := policy.Validate(); err != nil {
		return Profile{}, err
	}
	if ctx.Err() != nil {
		return Profile{}, failure(Canceled, "context_canceled")
	}
	if file == nil {
		return Profile{}, failure(IO, "missing_file")
	}
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return Profile{}, failure(IO, "unreadable_file")
	}
	if before.Size() > policy.Limits.MaxBytes {
		return Profile{}, failure(Limit, "byte_limit")
	}
	if before.Size() < 28 {
		return Profile{}, failure(Invalid, "glb_framing")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Profile{}, failure(IO, "unreadable_file")
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, policy.Limits.MaxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return Profile{}, failure(Canceled, "context_canceled")
		}
		return Profile{}, failure(IO, "unreadable_file")
	}
	if len(raw) != int(before.Size()) {
		return Profile{}, failure(Invalid, "glb_framing")
	}
	profile, err := ValidateGLBBytes(ctx, raw, policy)
	if err != nil {
		return Profile{}, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return Profile{}, failure(IO, "file_changed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Profile{}, failure(IO, "unreadable_file")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, contextReader{ctx, file})
	if ctx.Err() != nil {
		return Profile{}, failure(Canceled, "context_canceled")
	}
	if err != nil || n != int64(len(raw)) {
		return Profile{}, failure(IO, "file_changed")
	}
	if hex.EncodeToString(hash.Sum(nil)) != profile.SHA256 {
		return Profile{}, failure(IO, "file_changed")
	}
	after, err = file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return Profile{}, failure(IO, "file_changed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Profile{}, failure(IO, "unreadable_file")
	}
	return profile, nil
}

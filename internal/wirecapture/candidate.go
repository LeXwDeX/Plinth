package wirecapture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteCandidate validates exchanges and writes them as one 0600 candidate file
// in an existing 0700 directory outside the worktree. It never overwrites, and it
// refuses exchanges that still carry private media.
func WriteCandidate(worktreeRoot, path string, exchanges []Exchange, p Policy) error {
	sorted := SortedExchanges(exchanges)
	if err := Validate(sorted, p); err != nil {
		return err
	}
	for _, exchange := range sorted {
		if privateMedia(exchange) {
			return fmt.Errorf("%w: private media cannot be exported", ErrMediaEvidence)
		}
	}
	if worktreeRoot == "" || path == "" {
		return fmt.Errorf("%w: root and path", ErrInsideWorktree)
	}
	rootInfo, err := os.Stat(worktreeRoot)
	if err != nil {
		return fmt.Errorf("%w: worktree root unavailable", ErrUnsafePublicValue)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("%w: worktree root is not a directory", ErrUnsafePublicValue)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: candidate path", ErrUnsafePublicValue)
	}
	dir := filepath.Dir(absolute)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("%w: candidate directory unavailable", ErrUnsafePublicValue)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || dirInfo.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: candidate directory must exist with mode 0700", ErrUnsafePublicValue)
	}
	rootReal, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return fmt.Errorf("%w: worktree root unavailable", ErrUnsafePublicValue)
	}
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil || PathInside(rootReal, dirReal) {
		return fmt.Errorf("%w: candidate directory", ErrInsideWorktree)
	}
	if same, err := SameFileAncestor(rootReal, dirReal); err != nil || same {
		return fmt.Errorf("%w: candidate directory", ErrInsideWorktree)
	}
	if _, err := os.Lstat(absolute); err == nil {
		return fmt.Errorf("%w: candidate exists", ErrExistingCandidate)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: candidate path unavailable", ErrUnsafePublicValue)
	}
	data, err := marshalJSON(sorted)
	if err != nil {
		return fmt.Errorf("%w: candidate encode", ErrUnsafePublicValue)
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: candidate exists", ErrExistingCandidate)
		}
		return fmt.Errorf("%w: candidate create failed", ErrUnsafePublicValue)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(absolute)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("%w: candidate write failed", ErrUnsafePublicValue)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: candidate close failed", ErrUnsafePublicValue)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: candidate mode", ErrUnsafePublicValue)
	}
	ok = true
	return nil
}

func privateMedia(exchange Exchange) bool {
	if media := exchange.Response.Body.Media; media != nil && media.Recipe == "" {
		return true
	}
	if multipart := exchange.Request.Body.Multipart; multipart != nil {
		for _, part := range multipart.Parts {
			if part.Media != nil && part.Media.Recipe == "" {
				return true
			}
		}
	}
	return false
}

// PathInside reports whether path is root or lies under it (or cannot be related
// to it: the caller treats an error as inside, the safe answer).
func PathInside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

// SameFileAncestor reports whether root is dir or one of its ancestors by file
// identity, which catches bind mounts and hard-link-like aliases a path
// comparison misses.
func SameFileAncestor(root, dir string) (bool, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err != nil {
			return false, err
		}
		if os.SameFile(rootInfo, info) {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
	}
}

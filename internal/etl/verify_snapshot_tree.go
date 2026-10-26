package etl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type proofTreeEntry struct {
	PathReference string `json:"path_reference"`
	Kind          string `json:"kind"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256,omitempty"`
}
type proofTree map[string]proofTreeEntry

const proofMaximumFiles = 1000000
const proofMaximumTreeBytes int64 = 1 << 40

// Device and inode identify regular files independently of their names or scope.
// If the host cannot provide this identity, independence cannot be established.
type proofFileIdentity struct{ device, inode uint64 }

func proofRegularIdentity(info os.FileInfo) (proofFileIdentity, bool) {
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return proofFileIdentity{}, false
	}
	return proofFileIdentity{device: uint64(identity.Dev), inode: uint64(identity.Ino)}, true
}

func proofRegularIdentities(objects map[string]os.FileInfo) (map[proofFileIdentity]struct{}, bool) {
	identities := map[proofFileIdentity]struct{}{}
	for _, info := range objects {
		if !info.Mode().IsRegular() {
			continue
		}
		identity, ok := proofRegularIdentity(info)
		if !ok {
			return nil, false
		}
		identities[identity] = struct{}{}
	}
	return identities, true
}

func proofTreePaths(tree proofTree) []string {
	paths := make([]string, 0, len(tree))
	for path := range tree {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
func proofTreeHash(tree proofTree) string {
	entries := make([]proofTreeEntry, 0, len(tree))
	for _, entry := range tree {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].PathReference < entries[j].PathReference })
	raw, _ := json.Marshal(entries)
	return diagnosticHash(raw)
}
func proofSameTree(a, b proofTree) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
func proofWithin(parent, child string) bool {
	return parent == child || parent == "." || (parent == "/" && strings.HasPrefix(child, "/")) || strings.HasPrefix(child, parent+"/")
}
func proofRelative(value string) bool {
	return value != "" && !filepath.IsAbs(value) && filepath.ToSlash(filepath.Clean(value)) == value && value != ".." && !strings.HasPrefix(value, "../") && !strings.Contains(value, "\\")
}
func proofAbsolute(value string) bool {
	return strings.HasPrefix(value, "/") && filepath.ToSlash(filepath.Clean(value)) == value && !strings.Contains(value, "\\") && !strings.ContainsRune(value, 0)
}
func scanProofObservedTree(path string, observed map[string]os.FileInfo) (proofTree, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, errors.New("tree_unreadable")
	}
	initial, err := os.Lstat(canonical)
	if err != nil || !initial.IsDir() {
		return nil, errors.New("tree_unreadable")
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, errors.New("tree_unreadable")
	}
	defer root.Close()
	tree := proofTree{}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("tree_unreadable")
		}
		if len(tree) >= proofMaximumFiles {
			return errors.New("tree_limit")
		}
		before, err := entry.Info()
		if err != nil {
			return errors.New("tree_unreadable")
		}
		if observed != nil {
			observed[filepath.ToSlash(name)] = before
		}
		record := proofTreeEntry{PathReference: proofRef(filepath.ToSlash(name))}
		switch {
		case entry.IsDir():
			record.Kind = "directory"
		case entry.Type()&os.ModeSymlink != 0:
			target, err := root.Readlink(name)
			if err != nil {
				return errors.New("symlink_unresolved")
			}
			resolved, err := root.Stat(name)
			if err != nil || (!resolved.IsDir() && !resolved.Mode().IsRegular()) {
				return errors.New("symlink_unresolved")
			}
			record.Kind = "symlink"
			record.SHA256 = diagnosticHash([]byte(target))
		case before.Mode().IsRegular():
			// A credential/configuration file in a claimed data scope is not silently
			// excluded from a complete tree, nor opened as asset evidence.
			base := strings.ToLower(filepath.Base(name))
			if base == ".env" || strings.HasPrefix(base, ".env.") || base == "runtime.env" {
				return errors.New("credential_in_scope")
			}
			if before.Size() < 0 || before.Size() > proofMaximumTreeBytes-total {
				return errors.New("tree_limit")
			}
			file, err := root.Open(name)
			if err != nil {
				return errors.New("tree_unreadable")
			}
			actual, err := file.Stat()
			if err != nil || !os.SameFile(before, actual) || !actual.Mode().IsRegular() {
				file.Close()
				return errors.New("tree_changed")
			}
			hash := sha256.New()
			n, readErr := io.Copy(hash, io.LimitReader(file, before.Size()+1))
			after, statErr := file.Stat()
			closeErr := file.Close()
			current, pathErr := root.Lstat(name)
			if readErr != nil || statErr != nil || closeErr != nil || pathErr != nil || n != before.Size() || !os.SameFile(before, current) || !os.SameFile(before, after) || before.Size() != after.Size() || before.Size() != current.Size() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(current.ModTime()) {
				return errors.New("tree_changed")
			}
			total += n
			record.Kind = "file"
			record.Size = n
			record.SHA256 = hex.EncodeToString(hash.Sum(nil))
		default:
			return errors.New("special_file")
		}
		tree[filepath.ToSlash(name)] = record
		return nil
	})
	final, finalErr := os.Stat(canonical)
	if err != nil {
		return nil, err
	}
	if finalErr != nil || !os.SameFile(initial, final) {
		return nil, errors.New("tree_changed")
	}
	return tree, nil
}
func proofDirectoryStats(tree proofTree, dir string) (int64, int64) {
	var files, bytes int64
	for name, entry := range tree {
		if proofWithin(dir, name) && entry.Kind == "file" {
			files++
			bytes += entry.Size
		}
	}
	return files, bytes
}

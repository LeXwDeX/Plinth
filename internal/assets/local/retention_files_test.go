package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"golang.org/x/sys/unix"
)

func retentionFixture(t *testing.T) (*RetentionFiles, generation.RetentionFile, string) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(base, "cas")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	data := []byte("retention-fixture-bytes")
	h := sha256.Sum256(data)
	digest := hex.EncodeToString(h[:])
	key := "blobs/" + digest[:2] + "/" + digest + ".bin"
	if e = os.MkdirAll(filepath.Dir(filepath.Join(root, key)), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, key), data, 0600); e != nil {
		t.Fatal(e)
	}
	files, e := OpenRetentionFiles(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { files.Close() })
	info, e := files.Inspect(context.Background(), generation.RetentionFile{Key: key, Size: int64(len(data)), SHA256: digest})
	if e != nil {
		t.Fatal(e)
	}
	return files, info, base
}
func TestRetentionIndependentBackupAndLimitedUnlink(t *testing.T) {
	files, file, base := retentionFixture(t)
	ctx := context.Background()
	backup := filepath.Join(base, "backup")
	if e := CreateRetentionDirectory(backup, files.Path); e != nil {
		t.Fatal(e)
	}
	if e := files.Backup(ctx, []generation.RetentionFile{file}, backup); e != nil {
		t.Fatal(e)
	}
	if e := files.Remove(ctx, file); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(files.Path, file.Key)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(backup, file.Key))
	if e != nil || string(b) != "retention-fixture-bytes" {
		t.Fatalf("backup not independently restorable: %s %v", b, e)
	}
	if e = files.Remove(ctx, file); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("second deletion must report absent: %v", e)
	}
	if _, e = os.Stat(filepath.Dir(filepath.Join(files.Path, file.Key))); e != nil {
		t.Fatal("cleanup removed directory", e)
	}
}
func TestRetentionRefusesPathAliasesAndModifiedFiles(t *testing.T) {
	files, file, base := retentionFixture(t)
	ctx := context.Background()
	if _, e := OpenRetentionFiles(files.Path); !errors.Is(e, ErrStorageInUse) {
		t.Fatalf("missing exclusive CAS lease: %v", e)
	}
	path := filepath.Join(files.Path, file.Key)
	hard := filepath.Join(base, "hardlink")
	if e := os.Link(path, hard); e != nil {
		t.Fatal(e)
	}
	if _, e := files.Inspect(ctx, file); e == nil {
		t.Fatal("hardlinked CAS accepted")
	}
	os.Remove(hard)
	original := path + ".original"
	if e := os.Rename(path, original); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(original, path); e != nil {
		t.Fatal(e)
	}
	if e := files.Remove(ctx, file); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Remove(path)
	os.Rename(original, path)
	alias := filepath.Join(base, "alias")
	if e := os.Symlink(files.Path, alias); e != nil {
		t.Fatal(e)
	}
	if _, e := OpenRetentionFiles(alias); e == nil {
		t.Fatal("root symlink accepted")
	}
	subalias := filepath.Join(base, "parentalias")
	if e := os.Symlink(base, subalias); e != nil {
		t.Fatal(e)
	}
	if _, _, e := RetentionPathIdentity(filepath.Join(subalias, "cas"), true); e == nil {
		t.Fatal("ancestor symlink accepted")
	}
	if e := os.WriteFile(path, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := files.Remove(ctx, file); e == nil {
		t.Fatal("modified CAS accepted")
	}
	for _, bad := range []string{"../outside", "/absolute", "blobs/../escape"} {
		v := file
		v.Key = bad
		if _, e := files.Inspect(ctx, v); e == nil {
			t.Fatalf("unsafe key %q accepted", bad)
		}
	}
	if e := CreateRetentionDirectory(filepath.Join(files.Path, "backup"), files.Path); e == nil {
		t.Fatal("backup inside source accepted")
	}
	fifo := filepath.Join(base, "fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := RetentionPathIdentity(fifo, false); e == nil {
		t.Fatal("FIFO accepted")
	}
}
func TestRetentionArtifactsAreExclusiveAndCanonicalInputBound(t *testing.T) {
	_, _, base := retentionFixture(t)
	path := filepath.Join(base, "manifest.json")
	if e := WriteRetentionArtifact(path, []byte("{}")); e != nil {
		t.Fatal(e)
	}
	if e := WriteRetentionArtifact(path, []byte("changed")); e == nil {
		t.Fatal("manifest overwritten")
	}
	b, e := ReadRetentionArtifact(path)
	if e != nil || string(b) != "{}" {
		t.Fatal(string(b), e)
	}
}

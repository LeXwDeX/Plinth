package app

import (
	"path/filepath"
	"testing"
)

// #321 A: remote Provider Adapters keep private upload copies on the storage
// volume, next to the asset store's own staging files, not in the container's
// 64 MB /tmp tmpfs.
func TestProviderScratchDirIsTheStorageVolumeTmp(t *testing.T) {
	root := t.TempDir()
	if got := providerScratchDir(Config{StorageRoot: root}); got != filepath.Join(root, "tmp") {
		t.Fatalf("scratch dir = %q", got)
	}
	if got := providerScratchDir(Config{}); got != "" {
		t.Fatalf("scratch dir without a storage root = %q", got)
	}
}

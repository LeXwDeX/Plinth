package legacygenerated_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
)

func TestStoreReadsOnlyBasenameFilesInsideConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "task-a_0.png"),
		[]byte("legacy-image"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	store, err := legacygenerated.New(root)
	if err != nil {
		t.Fatal(err)
	}
	body, size, err := store.Open(
		context.Background(),
		"task-a_0.png",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	payload, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "legacy-image" || size != int64(len(payload)) {
		t.Fatalf("payload = %q, size = %d", payload, size)
	}

	if _, _, err := store.Open(
		context.Background(),
		"../task-a_0.png",
	); !errors.Is(err, legacygenerated.ErrInvalidFilename) {
		t.Fatalf("path traversal error = %v", err)
	}
}

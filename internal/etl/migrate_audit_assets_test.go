package etl

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyAuditAssetRecoveryDoesNotCrossTaskBoundary(t *testing.T) {
	storageRoot := t.TempDir()
	writeAuditTestFile(
		t,
		storageRoot,
		"user-a/text-to-3d/other-task_model.glb",
		[]byte("other"),
	)
	recovery := newAuditTestRecovery(t, storageRoot)

	_, err := recovery.Open(
		context.Background(),
		"target-task",
		administration.GenerationAsset{
			SourceURL: "https://cdn.example.test/model.glb",
		},
	)
	if !errors.Is(err, legacygeneration.ErrLegacyAssetUnavailable) {
		t.Fatalf("跨任务副本不应命中: %v", err)
	}
}

func TestLegacyAuditAssetRecoveryRejectsConflictingExactCopies(t *testing.T) {
	storageRoot := t.TempDir()
	for relativePath, body := range map[string][]byte{
		"user-a/text-to-3d/task-a_model.glb": []byte("first"),
		"user-b/text-to-3d/task-a_model.glb": []byte("second"),
	} {
		writeAuditTestFile(t, storageRoot, relativePath, body)
	}
	recovery := newAuditTestRecovery(t, storageRoot)

	_, err := recovery.Open(
		context.Background(),
		"task-a",
		administration.GenerationAsset{
			SourceURL: "https://cdn.example.test/model.glb",
		},
	)
	if !errors.Is(err, errLegacyAuditAssetConflict) {
		t.Fatalf("冲突副本未被拒绝: %v", err)
	}
}

func TestLegacyAuditAssetRecoveryAcceptsIdenticalExactCopies(t *testing.T) {
	storageRoot := t.TempDir()
	expected := []byte("identical")
	for _, relativePath := range []string{
		"user-a/text-to-3d/task-a_model.glb",
		"user-b/text-to-3d/task-a_model.glb",
	} {
		writeAuditTestFile(t, storageRoot, relativePath, expected)
	}
	recovery := newAuditTestRecovery(t, storageRoot)

	content, err := recovery.Open(
		context.Background(),
		"task-a",
		administration.GenerationAsset{
			SourceURL: "https://cdn.example.test/model.glb",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = content.Body.Close() }()
	actual, err := io.ReadAll(content.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("恢复字节=%q want=%q", actual, expected)
	}
	if content.MediaType != "model/gltf-binary" {
		t.Fatalf("mediaType=%q", content.MediaType)
	}
}

func newAuditTestRecovery(
	t *testing.T,
	storageRoot string,
) legacyAuditAssetRecovery {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	recovery, err := newLegacyAuditAssetRecovery(
		storageRoot,
		application.Runtime.LegacyMigration.AuditAssetRecovery,
		application.Administration.Generations.Assets,
	)
	if err != nil {
		t.Fatal(err)
	}
	return recovery
}

func writeAuditTestFile(
	t *testing.T,
	storageRoot string,
	relativePath string,
	body []byte,
) {
	t.Helper()
	target := filepath.Join(storageRoot, "audit", relativePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, 0o640); err != nil {
		t.Fatal(err)
	}
}

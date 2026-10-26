package etl_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

func TestInputRecoverySkipsUnreadableCandidatesAndRetainsDiagnostics(t *testing.T) {
	for _, goodCopy := range []bool{true, false} {
		t.Run(fmt.Sprintf("valid_copy=%v", goodCopy), func(t *testing.T) {
			fixture := etltest.NewFixture(t)
			content := []byte("exact input bytes")
			digest := sha256.Sum256(content)
			sha := hex.EncodeToString(digest[:])
			fixture.Exec(t, `INSERT INTO asset_registry
				(id,assetType,originalFilename,storedFilename,mimeType,fileSize,sourceSha256,storedSha256,storagePath,uploaderUserId,createdAt)
				VALUES ('recover-me','image','input.png','input.png','image/png',?,?,?,'missing.png','owner-a','2025-02-03T04:05:06Z')`, len(content), sha, sha)
			for index := range 22 {
				name := filepath.Join(fixture.Root, "storage", "cache", fmt.Sprintf("private-name-%d.png", index))
				if err := os.WriteFile(name, content, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(name, 0o600) })
				if body, err := os.Open(name); err == nil {
					_ = body.Close()
					t.Skip("requires a user that cannot read mode-000 files")
				}
			}
			copyBytes := []byte("wrong input bytes") // Same size, wrong SHA-256.
			if goodCopy {
				copyBytes = content
			}
			if err := os.WriteFile(filepath.Join(fixture.Root, "storage", "generated", "copy.png"), copyBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			targetDB := migrateTarget(t)
			storage := filepath.Join(t.TempDir(), "storage-go")
			var firstDiagnostics string
			for run := range 2 {
				code, output := runETL(t, "migrate", "--legacy-root", fixture.Root, "--target-db", targetDB, "--target-storage", storage)
				if goodCopy && code != 0 {
					t.Fatalf("unreadable unrelated copies stopped migration: %s", output)
				}
				if !goodCopy && (code == 0 || !strings.Contains(output, "不可固化 1 行")) {
					t.Fatalf("wrong copy accepted or missing asset hidden: %s", output)
				}
				var diagnostics []string
				for _, line := range strings.Split(output, "\n") {
					if strings.HasPrefix(line, "输入资产恢复候选跳过:") {
						diagnostics = append(diagnostics, line)
					}
				}
				if len(diagnostics) != 22 {
					t.Fatalf("diagnostics=%d, want all 22: %s", len(diagnostics), output)
				}
				joined := strings.Join(diagnostics, "\n")
				if strings.Contains(joined, "private-name") || strings.Contains(joined, fixture.Root) {
					t.Fatalf("diagnostics leaked source path: %s", joined)
				}
				if !strings.Contains(joined, "cache") || !strings.Contains(joined, "recover-me") || !strings.Contains(joined, "open_failed") {
					t.Fatalf("diagnostics lack category, reason or asset clue: %s", joined)
				}
				if run == 0 {
					firstDiagnostics = joined
				} else if joined != firstDiagnostics {
					t.Fatal("diagnostics changed on identical rerun")
				}
			}
		})
	}
}

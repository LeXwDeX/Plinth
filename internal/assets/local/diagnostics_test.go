package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// Exercise the real filesystem/CAS and delivery inspection, not a stub store.
// Diagnostics must not change acceptance, cleanup or errors.Is semantics.
func TestRealStoreWriteDiagnostics(t *testing.T) {
	for _, name := range []string{"success", "content_rejected", "size_limit", "directory_missing", "identity", "png_invalid_structure", "png_metadata_checksum"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			store, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			payload := pngWithProvenance(t, "tEXt", "local fixture")
			write := modelWrite(payload, "", "image/png")
			write.Role = "image"
			wantStage, wantReason, wantErrno := "", "", "other"
			switch name {
			case "content_rejected":
				// Image bytes are cached as delivered, so the address must sit
				// in a model text field outside any image to be rejected.
				write = modelWrite([]byte("mtllib https://private.example.invalid/image?signature=PRIVATE_FIXTURE\n"), "obj", "text/plain")
				wantStage, wantReason = "inspect", "content_rejected"
			case "png_invalid_structure":
				malformed := append([]byte(nil), payload...)
				copy(malformed[8:12], []byte{255, 255, 255, 255})
				write.Body = bytes.NewReader(malformed)
				wantStage, wantReason = "inspect", "png_invalid_structure"
			case "png_metadata_checksum":
				malformed := pngWithProvenance(t, "caBX", "local fixture")
				malformed[len(malformed)-13] ^= 1
				write.Body = bytes.NewReader(malformed)
				wantStage, wantReason = "inspect", "png_metadata_checksum"
			case "size_limit":
				write.MaxBytes = 10
				wantStage, wantReason = "write", "size_limit"
			case "directory_missing":
				if err := os.Remove(filepath.Join(root, "tmp")); err != nil {
					t.Fatal(err)
				}
				wantStage, wantReason, wantErrno = "directory_check", "io_failure", "not_found"
			case "identity":
				write.OwnerID = ""
				wantStage, wantReason = "identity", "invalid_identity"
			}
			asset, err := store.Put(context.Background(), write)
			if name == "success" {
				if err != nil {
					t.Fatal(err)
				}
				reader, err := store.Open(context.Background(), asset)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(reader)
				_ = reader.Close()
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatal("CAS bytes changed")
				}
				return
			}
			var diagnosed generation.DiagnosedAssetStorageError
			if !errors.As(err, &diagnosed) {
				t.Fatalf("missing typed diagnostic: %T", err)
			}
			got := diagnosed.StorageDiagnostic()
			if got.Stage != wantStage || got.Reason != wantReason || got.Errno != wantErrno {
				t.Fatalf("diagnostic=%+v", got)
			}
			if name == "directory_missing" && !errors.Is(err, os.ErrNotExist) {
				t.Fatal("typed error lost cause identity")
			}
			entries, _ := os.ReadDir(filepath.Join(root, "tmp"))
			if len(entries) != 0 {
				t.Fatal("rejected content left a temporary file")
			}
			blobs, err := os.ReadDir(filepath.Join(root, "blobs"))
			if err != nil || len(blobs) != 0 {
				t.Fatal("rejected content committed a blob")
			}
		})
	}
}

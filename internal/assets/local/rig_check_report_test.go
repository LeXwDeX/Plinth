package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/rigcheckreport"
)

func TestRigCheckReportPutOpenStrictCAS(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, err := rigcheckreport.Marshal(rigcheckreport.Report{Format: rigcheckreport.Format, Riggable: false, RigType: "quadruped"})
	if err != nil {
		t.Fatal(err)
	}
	write := generation.AssetWrite{AssetID: "a", OwnerID: "o", TaskID: "t", AttemptID: "n", Role: "other", Variant: "report", MediaType: rigcheckreport.MediaType, MaxBytes: rigcheckreport.MaxBytes, Body: bytes.NewReader(raw)}
	asset, err := store.Put(context.Background(), write)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(asset.StorageKey, ".json") {
		t.Fatal("report extension lost")
	}
	for i := 0; i < 2; i++ {
		reader, err := store.Open(context.Background(), asset)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatal("CAS changed report bytes")
		}
	}
	// A legacy/mutated file with otherwise matching identity cannot bypass Decode.
	invalid := []byte(`{"format":"art-ai.rig-check-report.v1","riggable":false}`)
	path := filepath.Join(root, asset.StorageKey)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(invalid)
	asset.SHA256 = hex.EncodeToString(sum[:])
	asset.Size = int64(len(invalid))
	if reader, err := store.Open(context.Background(), asset); err == nil {
		reader.Close()
		t.Fatal("malformed persisted report escaped Open validation")
	}
}
func TestRigCheckReportRejectsInvalidContentAtPut(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, raw := range []string{`{"format":"art-ai.rig-check-report.v1"}`, `{"format":"art-ai.rig-check-report.v1","riggable":true}`, `{"format":"art-ai.rig-check-report.v1","riggable":false}`, `{"format":"art-ai.rig-check-report.v1","riggable":null}`, `{"format":"art-ai.rig-check-report.v1","riggable":false,"riggable":true}`, `{"format":"art-ai.rig-check-report.v1","riggable":false,"url":"https://secret"}`, `PK\x03\x04`, strings.Repeat(" ", rigcheckreport.MaxBytes+1)} {
		for _, mediaType := range []string{rigcheckreport.MediaType, rigcheckreport.MediaType + "; charset=utf-8"} {
			if _, err := store.Put(context.Background(), generation.AssetWrite{AssetID: "a", OwnerID: "o", TaskID: "t", AttemptID: "n", Role: "other", Variant: "report", MediaType: mediaType, MaxBytes: rigcheckreport.MaxBytes, Body: strings.NewReader(raw)}); err == nil {
				t.Fatal("invalid report accepted")
			}
		}
	}
}

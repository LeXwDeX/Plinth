package local

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestPrintReportRejectsMalformedEnvelopeBeforeCAS(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, data := range []string{`{"format":"unknown","kind":"analysis"}`, `{"format":"art-ai.print-report.v1","kind":"repair","repair":{"details_status":"not_reported","secret":"secret"}}`, `<html>bad report</html>`} {
		if _, err := store.Put(context.Background(), generation.AssetWrite{AssetID: "a", OwnerID: "o", TaskID: "t", AttemptID: "n", Role: "report", Variant: "printability.v1.json", MediaType: "application/vnd.art-ai.print-report+json", MaxBytes: 32768, Body: bytes.NewBufferString(data)}); err == nil {
			t.Errorf("accepted malformed report %s", data)
		}
	}
}

func TestPrintReportPutAndOpenUseSameStrictBoundary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, err := printreport.Marshal(printreport.Report{Format: printreport.Format, Kind: "repair", Repair: &printreport.Repair{DetailsStatus: "not_reported"}})
	if err != nil {
		t.Fatal(err)
	}
	asset, err := store.Put(ctx, generation.AssetWrite{AssetID: "report", OwnerID: "owner", TaskID: "task", AttemptID: "attempt", Role: "report", Variant: "print-repair.v1.json", MediaType: printreport.MediaType, MaxBytes: 32768, Body: bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(asset.StorageKey, ".json") || asset.MediaType != printreport.MediaType {
		t.Fatalf("wrong report metadata: %+v", asset)
	}
	reader, err := store.Open(ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("bytes changed: %v", err)
	}
	// Even matching forged metadata cannot make an old malformed file readable.
	invalid := []byte(`{"format":"art-ai.print-report.v2","kind":"repair","repair":{"details_status":"not_reported"}}`)
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
	if reader, err := store.Open(ctx, asset); err == nil {
		_ = reader.Close()
		t.Fatal("old malformed report bypassed read validation")
	}
}
func TestPrintReportMIMECannotDisguiseArchiveOrSkipDefaultIsolation(t *testing.T) {
	var raw bytes.Buffer
	archive := zip.NewWriter(&raw)
	entry, err := archive.Create("safe.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("safe"))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, body := range [][]byte{raw.Bytes(), []byte(`{"format":"art-ai.print-report.v1","kind":"repair","repair":{"details_status":"not_reported","url":"https://SECRET"}}`)} {
		if _, err := store.Put(context.Background(), generation.AssetWrite{AssetID: "a", OwnerID: "o", TaskID: "t", AttemptID: "n", Role: "report", Variant: "print.v1.json", MediaType: printreport.MediaType, MaxBytes: 32768, Body: bytes.NewReader(body)}); err == nil {
			t.Fatal("disguised report accepted")
		}
	}
	if _, err := store.Put(context.Background(), generation.AssetWrite{AssetID: "a", OwnerID: "o", TaskID: "t", AttemptID: "n", Role: "report", MediaType: "application/json", MaxBytes: 32768, Body: strings.NewReader(`{"url":"https://SECRET"}`)}); err == nil {
		t.Fatal("generic JSON gained an isolation exemption")
	}
}

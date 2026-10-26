package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func splatRecord() []byte {
	b := make([]byte, 32)
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint32(b[12+4*i:], math.Float32bits(.01))
	}
	b[27], b[28], b[29], b[30], b[31] = 255, 255, 128, 128, 128
	return b
}
func splatWrite(raw []byte) generation.AssetWrite {
	return generation.AssetWrite{AssetID: "splat", OwnerID: "owner", TaskID: "task", AttemptID: "attempt", Role: schema.GaussianSplatRole, Variant: schema.GaussianSplatVariant, MediaType: schema.GaussianSplatMediaType, DeliveryProfile: schema.DeliveryProfileGaussianSplat32V1, MaxBytes: schema.GaussianSplatMaxBytes, Body: bytes.NewReader(raw)}
}
func TestSplatProfilePutOpenAndIdentity(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := splatRecord()
	asset, err := s.Put(context.Background(), splatWrite(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(asset.StorageKey, ".splat") {
		t.Fatal("lost original download extension")
	}
	for _, mutate := range []func(*generation.Asset){func(a *generation.Asset) { a.Role = "model" }, func(a *generation.Asset) { a.Variant = "glb" }, func(a *generation.Asset) { a.MediaType = "model/gltf-binary" }, func(a *generation.Asset) { a.DeliveryProfile = "" }} {
		forged := asset
		mutate(&forged)
		if r, err := s.Open(context.Background(), forged); err == nil {
			r.Close()
			t.Fatal("forged Splat identity accepted")
		}
	}
	for _, mutate := range []func(*generation.AssetWrite){func(a *generation.AssetWrite) { a.Role = "other" }, func(a *generation.AssetWrite) { a.DeliveryProfile = "" }, func(a *generation.AssetWrite) { a.Variant = "bin" }, func(a *generation.AssetWrite) { a.MediaType = "application/octet-stream" }} {
		write := splatWrite(raw)
		mutate(&write)
		if _, err := s.Put(context.Background(), write); err == nil {
			t.Fatal("profile bypass accepted")
		}
	}
	r, err := s.Open(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatal("bytes changed")
	}
}

// Optional local evidence gate. The real artifact is deliberately not embedded
// in the repository and its immutable identity must match before exercising CAS.
func TestSplatQualifiedActualArtifact(t *testing.T) {
	path := os.Getenv("ART_AI_SPLAT_QUALIFIED_FIXTURE")
	if path == "" {
		t.Skip("actual artifact is an external acceptance input")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if len(raw) != 8388608 || hex.EncodeToString(sum[:]) != "2abafd9bda85d00e95d41e775c625bca7a1c56194e9c85ae4f7b1abe27bdeea5" {
		t.Fatal("wrong qualification artifact")
	}
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	asset, err := s.Put(context.Background(), splatWrite(raw))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n, err := io.Copy(h, r)
	r.Close()
	if err != nil || n != int64(len(raw)) || !bytes.Equal(h.Sum(nil), sum[:]) {
		t.Fatal("real bytes changed through Put/Open")
	}
}

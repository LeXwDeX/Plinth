package local

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func blendPayload(prefix []byte, size int, seed int64) []byte {
	body := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(body)
	copy(body, prefix)
	return body
}

// Compressed Blender files are high-entropy. The generic text address scan
// rejected most 8 MiB random payloads, failing remesh deliveries at random.
// 256 KiB fixtures with fixed seeds still reproduce that false positive.
func TestBlendContainersAreNotRejectedByTextAddressHeuristics(t *testing.T) {
	prefixes := map[string][]byte{
		"raw":  []byte("BLENDER-v405REND"),
		"zstd": {0x28, 0xb5, 0x2f, 0xfd, 0x04, 0x58},
		"gzip": {0x1f, 0x8b, 0x08, 0x00},
	}
	genericFalsePositives := 0
	defer func() {
		if genericFalsePositives == 0 {
			t.Error("fixtures never reproduced the generic scanner false positive")
		}
	}()
	for name, prefix := range prefixes {
		for seed := int64(0); seed < 4; seed++ {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			body := blendPayload(prefix, 256<<10, seed)
			if scanExternalAddresses(bytes.NewReader(body)) != nil {
				genericFalsePositives++
			}
			write := modelWrite(body, "blend", "application/octet-stream")
			_, err = store.Put(context.Background(), write)
			store.Close()
			if err != nil {
				t.Fatalf("%s seed %d: %v", name, seed, err)
			}
		}
	}
}

func TestBlendRejectsWrongMagicAndAbsoluteAddresses(t *testing.T) {
	cases := map[string][]byte{
		"html":        []byte("<html><body>Access denied, please retry later</body></html>"),
		"gzip-prefix": []byte("\x1f\x8b<html><body>not a deflate stream</body></html>"),
		"absolute":    append([]byte("BLENDER-v405REND"), []byte("....https://evil.example/x.png....")...),
	}
	for name, body := range cases {
		store, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Put(context.Background(), modelWrite(body, "blend", "application/octet-stream"))
		store.Close()
		var diagnosed generation.DiagnosedAssetStorageError
		if err == nil || !errors.As(err, &diagnosed) {
			t.Fatalf("%s: err=%v", name, err)
		}
		want := map[string]string{"html": "blend_magic_missing", "gzip-prefix": "blend_magic_missing", "absolute": "external_address"}[name]
		if got := diagnosed.StorageDiagnostic(); got.Stage != "inspect" || got.Reason != want {
			t.Fatalf("%s diagnostic=%+v", name, got)
		}
	}
}

func TestInspectionFailuresNameTheirWireFamily(t *testing.T) {
	broken := bytes.Repeat([]byte("PK\x03\x04"), 4)
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Put(context.Background(), modelWrite(broken, "usdz", "model/vnd.usdz+zip"))
	var diagnosed generation.DiagnosedAssetStorageError
	if err == nil || !errors.As(err, &diagnosed) || diagnosed.StorageDiagnostic().Reason != "archive_rejected" {
		t.Fatalf("err=%v", err)
	}
}

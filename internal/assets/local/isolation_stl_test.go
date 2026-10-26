package local

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func binarySTLFixture(triangles int) []byte {
	body := make([]byte, 84+triangles*50)
	copy(body[:80], "binary model fixture")
	binary.LittleEndian.PutUint32(body[80:84], uint32(triangles))
	for offset := 84; offset < len(body); offset += 50 {
		// A finite normal component has the same bytes as a short // authority.
		// The other components form a normal and a simple triangle.
		binary.LittleEndian.PutUint32(body[offset:offset+4], 0x20612f2f)
		for _, index := range []int{2, 6, 10} {
			binary.LittleEndian.PutUint32(body[offset+4*index:offset+4*index+4], math.Float32bits(1))
		}
	}
	return body
}

func checkSTLNewAndHistorical(t *testing.T, body []byte, reject bool) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asset, putErr := store.Put(context.Background(), modelWrite(body, "stl", "model/stl"))
	if (putErr != nil) != reject {
		t.Fatalf("new STL rejection=%t, want %t: %v", putErr != nil, reject, putErr)
	}
	if !reject {
		reader, err := store.Open(context.Background(), asset)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("STL bytes changed: %v", err)
		}
	}
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(body), "model/stl", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	historical := generation.Asset{Role: "model", Variant: "stl", StorageKey: blob.StorageKey, Size: blob.Size, SHA256: blob.SHA256, MediaType: blob.MediaType}
	reader, openErr := store.Open(context.Background(), historical)
	if reader != nil {
		reader.Close()
	}
	if (openErr != nil) != reject {
		t.Fatalf("historical STL rejection=%t, want %t: %v", openErr != nil, reject, openErr)
	}
}

func stlArchive(t *testing.T, body []byte, method uint16) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: "scene.stl", Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestBinarySTLNumericFieldsDoNotCreateNetworkReferences(t *testing.T) {
	body := binarySTLFixture(2)
	checkSTLNewAndHistorical(t, body, false)
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		store, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Put(context.Background(), modelWrite(stlArchive(t, body, method), "zip", "application/zip"))
		store.Close()
		if err != nil {
			t.Fatalf("contained STL numeric fields rejected with method %d: %v", method, err)
		}
	}
}

func TestBinarySTLStillRejectsAddressesAndInvalidGeometry(t *testing.T) {
	cases := map[string][]byte{}
	for name, reference := range map[string][]byte{
		"header IPv4":          []byte("//192.0.2.1/material"),
		"header localhost":     []byte("//localhost/material"),
		"header IPv6":          []byte("//[::1]/material"),
		"header escaped":       []byte(`\u002f\u002flocalhost/model`),
		"header percent":       []byte(`%2F%2Flocalhost%2Fmodel`),
		"header UTF16":         utf16LE("//localhost/model"),
		"header UTF16 percent": utf16LE(`%2F%2Flocalhost%2Fmodel`),
		"header UTF16 escaped": utf16LE(`\u002f\u002flocalhost/model`),
	} {
		body := binarySTLFixture(2)
		clear(body[:80])
		copy(body[:80], reference)
		cases[name] = body
	}
	for name, reference := range map[string][]byte{
		"numeric HTTP":               []byte("https://private.example/model"),
		"numeric escaped HTTP":       []byte(`https:\/\/private.example\/model`),
		"numeric percent HTTP":       []byte(`%68%74%74%70%73%3A%2F%2Fa%2Fb`),
		"numeric UTF16 HTTP":         utf16LE("https://a/b"),
		"numeric UTF16 percent HTTP": utf16LE(`%68ttps://a/b`),
		"numeric UTF16 escaped HTTP": utf16LE(`https:\/\/a/b`),
	} {
		body := binarySTLFixture(2)
		copy(body[84:132], reference)
		cases[name] = body
		crossBatch := binarySTLFixture(1100)
		copy(crossBatch[84+1024*50-3:], reference)
		cases[name+" across batches"] = crossBatch
	}
	trailer := append(binarySTLFixture(2), []byte(" //localhost/trailer")...)
	cases["protocol reference after declared records"] = trailer
	miscount := binarySTLFixture(2)
	binary.LittleEndian.PutUint32(miscount[80:84], 1)
	copy(miscount[134:], " //localhost/undeclared-record")
	cases["protocol reference outside declared count"] = miscount
	for name, bits := range map[string]uint32{"NaN coordinate": 0x7fc00000, "infinite coordinate": 0x7f800000} {
		body := binarySTLFixture(2)
		binary.LittleEndian.PutUint32(body[84:88], 0)
		binary.LittleEndian.PutUint32(body[134:138], 0)
		binary.LittleEndian.PutUint32(body[96:100], bits)
		cases[name] = body
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			checkSTLNewAndHistorical(t, body, true)
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.Put(context.Background(), modelWrite(stlArchive(t, body, zip.Deflate), "zip", "application/zip")); err == nil {
				t.Fatal("unsafe or invalid STL escaped the archive check")
			}
		})
	}
}

func TestBinarySTLNumericExceptionRequiresDeclaredFormatAndExactLayout(t *testing.T) {
	for _, body := range [][]byte{
		binarySTLFixture(2)[:100],
		append(binarySTLFixture(2), 0),
	} {
		checkSTLNewAndHistorical(t, body, true)
	}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Put(context.Background(), modelWrite(binarySTLFixture(2), "fbx", "application/octet-stream")); err == nil {
		t.Fatal("an unrelated format inherited the numeric STL exception")
	}
}

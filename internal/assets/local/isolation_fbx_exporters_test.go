package local

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Assimp aligns before its reserved zero word; Blender writes the zero word
// before aligning. Both always emit 1..16 padding bytes, including an entire
// block when already aligned. These independent writers follow the exporters:
// https://github.com/assimp/assimp/blob/master/code/AssetLib/FBX/FBXExporter.cpp
// https://github.com/blender/blender/blob/main/scripts/addons_core/io_scene_fbx/encode_bin.py
func fbxExporterFixture(t *testing.T, version uint32, alignment int, assimp bool) ([]byte, int) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("Kaydara FBX Binary  \x00\x1a\x00")
	b.Write(fbxTestNumber(4, uint64(version)))
	width := fbxTestHeaderBytes(version)
	nameLen := (alignment - (27+2*width)%16 + 16) % 16
	if nameLen == 0 {
		nameLen = 16
	}
	fbxTestWriteNode(t, &b, version, fbxTestNode{name: []byte(strings.Repeat("N", nameLen))})
	b.Write(make([]byte, width))
	footer := b.Len()
	if footer%16 != alignment {
		t.Fatal("fixture alignment mismatch")
	}
	b.Write([]byte{0xfa, 0xbc, 0xab, 0x09, 0xd0, 0xc8, 0xd4, 0x66, 0xb1, 0x76, 0xfb, 0x83, 0x1c, 0xf7, 0x26, 0x7e})
	if assimp {
		b.Write(make([]byte, 16-b.Len()%16))
		b.Write(make([]byte, 4))
	} else {
		b.Write(make([]byte, 4))
		b.Write(make([]byte, 16-b.Len()%16))
	}
	b.Write(fbxTestNumber(4, uint64(version)))
	b.Write(make([]byte, 120))
	b.Write([]byte{0xf8, 0x5a, 0x8c, 0x6a, 0xde, 0xf5, 0xd9, 0x7e, 0xec, 0xe9, 0x0c, 0xe3, 0x75, 0x8f, 0x29, 0x0b})
	return b.Bytes(), footer
}

func TestFBXExporterFootersEveryAlignment(t *testing.T) {
	for _, version := range []uint32{7400, 7700} {
		for _, assimp := range []bool{false, true} {
			for alignment := range 16 {
				t.Run(fmt.Sprintf("%d/assimp_%t/offset_%d", version, assimp, alignment), func(t *testing.T) {
					raw, footer := fbxExporterFixture(t, version, alignment, assimp)
					inspect := func(raw []byte) error {
						return inspectBinaryFBX(bytes.NewReader(raw), int64(len(raw)), newInspectionWorkBudget())
					}
					if err := inspect(raw); err != nil {
						t.Fatalf("valid exporter footer: %v", err)
					}
					versionOffset := len(raw) - 140
					for i := footer + 16; i < versionOffset; i++ {
						corrupt := bytes.Clone(raw)
						corrupt[i] = 1
						if inspect(corrupt) == nil {
							t.Fatalf("nonzero reserved/padding byte at %d accepted", i)
						}
					}
					for _, tc := range []struct {
						name string
						edit func([]byte) []byte
					}{
						{"wrong_version", func(b []byte) []byte { b[versionOffset] ^= 1; return b }},
						{"reserved_tail", func(b []byte) []byte { b[len(b)-17] = 1; return b }},
						{"closing_id", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }},
						{"truncated", func(b []byte) []byte { return b[:len(b)-1] }},
						{"extra_tail", func(b []byte) []byte { return append(b, 0) }},
						{"extra_padding", func(b []byte) []byte { return append(append(bytes.Clone(b[:versionOffset]), 0), b[versionOffset:]...) }},
						{"short_padding", func(b []byte) []byte { return append(b[:versionOffset-1], b[versionOffset:]...) }},
						{"external_identifier", func(b []byte) []byte { copy(b[footer:footer+16], []byte("https://x.test/  ")); return b }},
					} {
						t.Run(tc.name, func(t *testing.T) {
							if inspect(tc.edit(bytes.Clone(raw))) == nil {
								t.Fatal("corrupt footer accepted")
							}
						})
					}
				})
			}
		}
	}
}

func TestFBXExporterFootersAtDeliveryBoundaries(t *testing.T) {
	for _, assimp := range []bool{false, true} {
		t.Run(fmt.Sprint(assimp), func(t *testing.T) {
			raw, _ := fbxExporterFixture(t, 7700, 7, assimp)
			fbxTestWrappers(t, raw, true)
			fbxProfileBoundary(t, raw, true)
			raw[len(raw)-1] ^= 1
			fbxTestWrappers(t, raw, false)
			fbxProfileBoundary(t, raw, false)
		})
	}
}

func fbxManyNodes(t *testing.T, count int) []byte {
	t.Helper()
	nodes := make([]fbxTestNode, count)
	for i := range nodes {
		nodes[i] = fbxTestNode{name: []byte("N")}
	}
	return fbxTestFile(t, 7700, nodes...)
}

func TestFBXNodeBudgetIndependentOfArchiveEntries(t *testing.T) {
	raw := fbxManyNodes(t, 10001)
	fbxTestWrappers(t, raw, true)
	fbxProfileBoundary(t, raw, true)
	used, decode := fbxTestMeasuredWork(t, raw)
	fbxTestRequireWorkRejection(t, inspectBinaryFBX(bytes.NewReader(raw), int64(len(raw)), &inspectionWorkBudget{remaining: used - decode - 1}))
	for _, count := range []int{99999, 100000, 100001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			data := fbxManyNodes(t, count)
			err := inspectBinaryFBX(bytes.NewReader(data), int64(len(data)), newInspectionWorkBudget())
			if count <= 100000 && err != nil {
				t.Fatalf("bounded FBX node count rejected: %v", err)
			}
			if count > 100000 && err == nil {
				t.Fatal("FBX node bound ignored")
			}
		})
	}
}

func fbxProfileBoundary(t *testing.T, raw []byte, accepted bool) {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := profileWrite(schema.DeliveryProfileBinaryFBXV1, "fbx", "model/fbx", raw)
	write.Role = "clip"
	asset, err := store.Put(context.Background(), write)
	if accepted {
		if err != nil {
			t.Fatalf("strict profile Put: %v", err)
		}
		body, err := store.Open(context.Background(), asset)
		if err != nil {
			t.Fatalf("strict profile Open: %v", err)
		}
		got, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil || !bytes.Equal(got, raw) {
			t.Fatal("strict profile changed bytes")
		}
		return
	}
	if err == nil {
		t.Fatal("strict profile Put accepted invalid FBX")
	}
	blob, err := store.PutBlob(context.Background(), bytes.NewReader(raw), "model/fbx", 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	restored := generation.Asset{StorageKey: blob.StorageKey, SHA256: blob.SHA256, Size: blob.Size, Role: "clip", Variant: "fbx", MediaType: "model/fbx", DeliveryProfile: schema.DeliveryProfileBinaryFBXV1}
	if body, err := store.Open(context.Background(), restored); err == nil {
		_ = body.Close()
		t.Fatal("restored strict profile accepted invalid FBX")
	}
}

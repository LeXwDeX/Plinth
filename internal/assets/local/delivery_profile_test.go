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
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const profileModelNS = "http://schemas.microsoft.com/3dmanufacturing/core/2015/02"
const profileModelType = "application/vnd.ms-package.3dmanufacturing-3dmodel+xml"
const profileRelType = "http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"

func profile3MFEntries() map[string]string {
	return map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="` + profileModelType + `"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="start" Type="` + profileRelType + `" Target="/Models/main.model"/></Relationships>`,
		"Models/main.model":   `<model xmlns="` + profileModelNS + `"><resources/><build/></model>`,
	}
}
func profileZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, v := range entries {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func profileWrite(profile, variant, mime string, raw []byte) generation.AssetWrite {
	return generation.AssetWrite{AssetID: "asset", OwnerID: "owner", TaskID: "task", AttemptID: "attempt", Role: "arbitrary-role", Variant: variant, MediaType: mime, DeliveryProfile: profile, MaxBytes: 4 << 20, Body: bytes.NewReader(raw)}
}
func TestDeliveryProfilesPutAndOpenRejectWrongIdentity(t *testing.T) {
	validFBX := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Objects"), children: []fbxTestNode{}})
	valid3MF := profileZIP(t, profile3MFEntries())
	for _, tc := range []struct {
		name, profile, variant, mime string
		raw                          []byte
		valid                        bool
	}{
		{"binary fbx", schema.DeliveryProfileBinaryFBXV1, "fbx", "model/fbx", validFBX, true},
		{"3mf package root absolute", schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", valid3MF, true},
		{"ascii fbx strict", schema.DeliveryProfileBinaryFBXV1, "fbx", "model/fbx", []byte("; FBX 7.4.0\nObjects: {}"), false},
		{"ascii fbx legacy", "", "fbx", "application/octet-stream", []byte("; FBX 7.4.0\nObjects: {}"), true},
		{"ascii fbx legacy concrete MIME", "", "fbx", "model/fbx", []byte("; FBX 7.4.0\nObjects: {}"), true},
		{"ordinary ZIP strict", schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", profileZIP(t, map[string]string{"safe.txt": "safe"}), false},
		{"ordinary ZIP legacy", "", "3mf", "model/3mf", profileZIP(t, map[string]string{"safe.txt": "safe"}), false},
		{"3mf package legacy", "", "3mf", "model/3mf", valid3MF, true},

		{"plain fbx", schema.DeliveryProfileBinaryFBXV1, "fbx", "model/fbx", []byte("not an fbx"), false},
		{"unknown", "future-profile", "fbx", "model/fbx", validFBX, false},
		{"wrong mime", schema.DeliveryProfileBinaryFBXV1, "fbx", "application/octet-stream", validFBX, false},
		{"wrong variant", schema.DeliveryProfileBinaryFBXV1, "other", "model/fbx", validFBX, false},
		{"plain glb explicit", "", "glb", "model/gltf-binary", []byte("not a model"), false},
		{"zip glb explicit", "", "glb", "model/gltf-binary", valid3MF, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			asset, err := s.Put(context.Background(), profileWrite(tc.profile, tc.variant, tc.mime, tc.raw))
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if asset.DeliveryProfile != tc.profile {
					t.Fatal("store dropped frozen profile")
				}
				body, err := s.Open(context.Background(), asset)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(body)
				_ = body.Close()
				if err != nil || !bytes.Equal(raw, tc.raw) {
					t.Fatal("changed bytes")
				}
				return
			}
			if err == nil {
				t.Fatal("wrong identity accepted by Put")
			}
			if err := filepath.WalkDir(filepath.Join(root, "blobs"), func(_ string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					t.Error("rejected Put created a CAS file")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// A forged old row with matching size/hash must also fail the Open boundary.
			blob, err := s.PutBlob(context.Background(), bytes.NewReader(tc.raw), tc.mime, 4<<20)
			if err != nil {
				t.Fatal(err)
			}
			forged := generation.Asset{StorageKey: blob.StorageKey, SHA256: blob.SHA256, Size: blob.Size, Role: "arbitrary-role", Variant: tc.variant, MediaType: tc.mime, DeliveryProfile: tc.profile}
			if body, err := s.Open(context.Background(), forged); err == nil {
				_ = body.Close()
				t.Fatal("wrong identity accepted by Open")
			}
		})
	}
}
func TestDeliveryProfile3MFIdentityAndIsolation(t *testing.T) {
	mutations := map[string]func(map[string]string){
		"content types is only a path alias": func(e map[string]string) {
			e["Meta/../[Content_Types].xml"] = e["[Content_Types].xml"]
			delete(e, "[Content_Types].xml")
		},
		"start model is only a path alias": func(e map[string]string) {
			e["Models/./main.model"] = e["Models/main.model"]
			delete(e, "Models/main.model")
		},

		"ordinary URI text is not a namespace": func(e map[string]string) {
			e["Models/main.model"] = `<model xmlns="` + profileModelNS + `"><metadata>` + profileModelNS + `</metadata></model>`
		},
		"private namespace": func(e map[string]string) {
			e["Models/main.model"] = `<model xmlns="` + profileModelNS + `" xmlns:private="https://private.example/ns"><resources/><build/></model>`
		},
		"plain verified part": func(e map[string]string) { e["Models/main.model"] = "not an XML model" },
		"duplicate root attributes": func(e map[string]string) {
			e["Models/main.model"] = `<model xmlns="` + profileModelNS + `" unit="millimeter" unit="meter"/>`
		},

		"multiple model roots": func(e map[string]string) { e["Models/main.model"] += `<model xmlns="` + profileModelNS + `"/>` },
		"missing start relationship": func(e map[string]string) {
			e["_rels/.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`
		},
		"wrong relationships namespace": func(e map[string]string) {
			e["_rels/.rels"] = `<Relationships xmlns="urn:other"><Relationship Id="start" Type="` + profileRelType + `" Target="/Models/main.model"/></Relationships>`
		},
		"duplicate content defaults": func(e map[string]string) {
			e["[Content_Types].xml"] = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="` + profileModelType + `"/><Default Extension="MODEL" ContentType="` + profileModelType + `"/></Types>`
		},
		"encoded path separator": func(e map[string]string) {
			e["_rels/.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="s" Type="` + profileRelType + `" Target="Models%2Fmain.model"/></Relationships>`
		},

		"missing content types":     func(e map[string]string) { delete(e, "[Content_Types].xml") },
		"missing root relationship": func(e map[string]string) { delete(e, "_rels/.rels") },
		"missing model":             func(e map[string]string) { delete(e, "Models/main.model") },
		"arbitrary xml":             func(e map[string]string) { e["Models/main.model"] = `<unrelated/>` },
		"wrong core namespace":      func(e map[string]string) { e["Models/main.model"] = `<model xmlns="urn:other"/>` },
		"wrong content type": func(e map[string]string) {
			e["[Content_Types].xml"] = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="model" ContentType="text/xml"/></Types>`
		},
		"external start part": func(e map[string]string) {
			e["_rels/.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="s" Type="` + profileRelType + `" Target="https://private.example/model" TargetMode="External"/></Relationships>`
		},
		"multiple start parts": func(e map[string]string) {
			e["_rels/.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="a" Type="` + profileRelType + `" Target="/Models/main.model"/><Relationship Id="b" Type="` + profileRelType + `" Target="/Models/main.model"/></Relationships>`
		},
		"unsafe start target": func(e map[string]string) {
			e["_rels/.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="s" Type="` + profileRelType + `" Target="../Models/main.model"/></Relationships>`
		},
		"model directory":    func(e map[string]string) { delete(e, "Models/main.model"); e["Models/main.model/"] = "" },
		"metadata too large": func(e map[string]string) { e["[Content_Types].xml"] += string(bytes.Repeat([]byte{' '}, (1<<20)+1)) },
		"traversal member":   func(e map[string]string) { e["../secret.txt"] = "secret" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			entries := profile3MFEntries()
			mutate(entries)
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := s.Put(context.Background(), profileWrite(schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", profileZIP(t, entries))); err == nil {
				t.Fatal("invalid 3MF accepted")
			}
		})
	}
}
func TestDeliveryProfileOpenRevalidatesMatchingTamperedCAS(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	asset, err := s.Put(context.Background(), profileWrite(schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", profileZIP(t, profile3MFEntries())))
	if err != nil {
		t.Fatal(err)
	}
	raw := profileZIP(t, map[string]string{"innocent.txt": "not 3mf"})
	file := filepath.Join(root, asset.StorageKey)
	if err = os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	asset.Size = int64(len(raw))
	asset.SHA256 = hex.EncodeToString(sum[:])
	if body, err := s.Open(context.Background(), asset); err == nil {
		_ = body.Close()
		t.Fatal("matching hash bypassed profile validation")
	}
}

func TestDeliveryProfile3MFResolvesDeclaredRootAndContentOverride(t *testing.T) {
	for _, target := range []string{"parts/mesh.data", "/parts/mesh.data"} {
		entries := profile3MFEntries()
		delete(entries, "Models/main.model")
		entries["parts/mesh.data"] = `<model xmlns="` + profileModelNS + `"><resources/><build/></model>`
		entries["_rels/.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="s" Type="` + profileRelType + `" Target="` + target + `" TargetMode="Internal"/></Relationships>`
		entries["[Content_Types].xml"] = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/parts/mesh.data" ContentType="` + profileModelType + `"/></Types>`
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err = s.Put(context.Background(), profileWrite(schema.DeliveryProfile3MFPackageV1, "3mf", "model/3mf", profileZIP(t, entries))); err != nil {
			t.Fatalf("declared relative/root absolute part %s: %v", target, err)
		}
	}
}

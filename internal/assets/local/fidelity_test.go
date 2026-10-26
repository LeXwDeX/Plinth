package local

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const riggedJSON = `{"asset":{"version":"2.0"},"nodes":[{}],"skins":[{"joints":[0]}]}`
const animatedJSON = `{"asset":{"version":"2.0"},"nodes":[{}],"skins":[{"joints":[0]}],"accessors":[{"count":2}],"animations":[{"channels":[{"sampler":0,"target":{"node":0,"path":"translation"}}],"samplers":[{"input":0}]}]}`

func fidelityTestGLB(document string) []byte {
	data := []byte(document)
	for len(data)%4 != 0 {
		data = append(data, ' ')
	}
	body := make([]byte, 20+len(data))
	copy(body, "glTF")
	binary.LittleEndian.PutUint32(body[4:], 2)
	binary.LittleEndian.PutUint32(body[8:], uint32(len(body)))
	binary.LittleEndian.PutUint32(body[12:], uint32(len(data)))
	copy(body[16:], "JSON")
	copy(body[20:], data)
	return body
}

func checkFidelityBytes(t *testing.T, data []byte, level string) error {
	t.Helper()
	name := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return ValidateFidelityFile(file, level)
}

func TestGLTFFidelity(t *testing.T) {
	for _, tc := range []struct{ name, json, level, reason string }{
		{"rigged", riggedJSON, "skeleton", ""},
		{"animated skeleton", animatedJSON, "skeleton", ""},
		{"animated", animatedJSON, "animation", ""},
		{"static", `{"asset":{"version":"2.0"},"nodes":[{}]}`, "skeleton", "fidelity_skeleton_missing"},
		{"rigged only", riggedJSON, "animation", "fidelity_animation_missing"},
		{"invalid joint", strings.Replace(riggedJSON, `"joints":[0]`, `"joints":[1]`, 1), "skeleton", "fidelity_invalid_structure"},
		{"negative joint", strings.Replace(riggedJSON, `"joints":[0]`, `"joints":[-1]`, 1), "skeleton", "fidelity_invalid_structure"},
		{"empty joints", strings.Replace(riggedJSON, `"joints":[0]`, `"joints":[]`, 1), "skeleton", "fidelity_skeleton_missing"},
		{"invalid node", strings.Replace(animatedJSON, `"node":0`, `"node":2`, 1), "animation", "fidelity_invalid_structure"},
		{"missing node", strings.Replace(animatedJSON, `"node":0,`, ``, 1), "animation", "fidelity_invalid_structure"},
		{"invalid sampler", strings.Replace(animatedJSON, `"sampler":0`, `"sampler":1`, 1), "animation", "fidelity_invalid_structure"},
		{"invalid accessor", strings.Replace(animatedJSON, `"input":0`, `"input":1`, 1), "animation", "fidelity_invalid_structure"},
		{"empty clip", strings.Replace(animatedJSON, `"count":2`, `"count":0`, 1), "animation", "fidelity_animation_missing"},
		{"animation implies skeleton", strings.Replace(animatedJSON, `"skins":[{"joints":[0]}],`, ``, 1), "animation", "fidelity_skeleton_missing"},
		{"wrong node type", strings.Replace(riggedJSON, `"nodes":[{}]`, `"nodes":[null]`, 1), "skeleton", "fidelity_invalid_structure"},
		{"unknown", riggedJSON, "photoreal", "fidelity_unsupported"},
		{"undeclared", `{}`, "", ""},
	} {
		for _, glb := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " GLB", false: " glTF"}[glb], func(t *testing.T) {
				data := []byte(tc.json)
				if glb {
					data = fidelityTestGLB(tc.json)
				}
				err := checkFidelityBytes(t, data, tc.level)
				var rejected *inspectionRejection
				if tc.reason == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if !errors.As(err, &rejected) || rejected.reason != tc.reason {
					t.Fatalf("error=%v want=%s", err, tc.reason)
				}
			})
		}
	}
}

func TestFidelityBoundsAndMalformedGLB(t *testing.T) {
	for _, data := range [][]byte{
		fidelityTestGLB(riggedJSON)[:23],
		fidelityTestGLB(`{"skins":[],"skins":[{"joints":[0]}]}`),
		fidelityTestGLB(`{"nodes":[{}],"skins":[{"joints":[0.5]}]}`),
		fidelityTestGLB(`{"nodes":[{}],"skins":[{"joints":[0]}]} {}`),
	} {
		if err := checkFidelityBytes(t, data, "skeleton"); err == nil {
			t.Fatal("malformed input accepted")
		}
	}
	file, err := os.CreateTemp(t.TempDir(), "oversized")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(maxArchiveBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFidelityFile(file, "skeleton"); err == nil {
		t.Fatal("oversized input accepted")
	}
	if err := checkGLTFFidelity(map[string]any{}, "skeleton", &inspectionWorkBudget{remaining: 0}); err == nil {
		t.Fatal("missing structure accepted")
	}
}

func TestBinaryFBXFidelityUsesTypedObjects(t *testing.T) {
	object := func(name, subtype string) fbxTestNode {
		return fbxTestNode{name: []byte(name), properties: [][]byte{fbxTestScalar('L', fbxTestNumber(8, 1)), fbxTestBytes('S', []byte(name+"::test")), fbxTestBytes('S', []byte(subtype))}}
	}
	skin, animation := object("Deformer", "Skin"), object("AnimationStack", "")
	for _, tc := range []struct {
		name          string
		nodes         []fbxTestNode
		level, reason string
	}{
		{"skin", []fbxTestNode{skin}, "skeleton", ""},
		{"animated", []fbxTestNode{skin, animation}, "animation", ""},
		{"rigged only", []fbxTestNode{skin}, "animation", "fidelity_animation_missing"},
		{"cluster not skin", []fbxTestNode{object("Deformer", "Cluster")}, "skeleton", "fidelity_skeleton_missing"},
		{"metadata not object", []fbxTestNode{{name: []byte("Metadata"), properties: [][]byte{fbxTestBytes('S', []byte("Deformer Skin AnimationStack"))}}}, "skeleton", "fidelity_skeleton_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := fbxTestFile(t, 7400, fbxTestNode{name: []byte("Objects"), children: tc.nodes})
			err := checkFidelityBytes(t, data, tc.level)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var rejected *inspectionRejection
			if !errors.As(err, &rejected) || rejected.reason != tc.reason {
				t.Fatalf("error=%v want=%s", err, tc.reason)
			}
		})
	}
}

// A read failure of the stored file is inspection unavailability, not a
// structural rejection; a truncated file is still rejected.
func TestFidelityReadFailureIsNotStructuralRejection(t *testing.T) {
	name := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(name, fidelityTestGLB(riggedJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	err = diagnoseWrite("inspect", "fidelity_inspection_unavailable", ValidateFidelityFile(file, "skeleton"))
	var inspection *inspectionRejection
	if err == nil || errors.As(err, &inspection) {
		t.Fatalf("closed file classified as rejection: %v", err)
	}
	if diagnosed, ok := err.(interface {
		StorageDiagnostic() generation.AssetStorageDiagnostic
	}); !ok || diagnosed.StorageDiagnostic().Reason != "fidelity_inspection_unavailable" {
		t.Fatalf("diagnostic=%v", err)
	}
	truncated := checkFidelityBytes(t, fidelityTestGLB(riggedJSON)[:23], "skeleton")
	if !errors.As(truncated, &inspection) {
		t.Fatalf("truncated file not rejected: %v", truncated)
	}
}

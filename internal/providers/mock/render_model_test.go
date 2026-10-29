package mock

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
)

func validateBytes(t *testing.T, name string, data []byte, role, variant, mediaType string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := local.ValidateDeliveredFile(file, role, variant, mediaType); err != nil {
		t.Errorf("%s (%s/%s) rejected: %v", name, role, variant, err)
	}
}

func TestEveryModelFormatPassesDeliveryChecks(t *testing.T) {
	seed := digest("seed")
	spec := modelSpec{GeometrySeed: seed, TextureSeed: seed, Hue: 200, Subdivision: 8, TextureSize: 512, Label: "TEST"}
	mesh := modelfiles.BuildHumanoid(seed, spec.Subdivision)
	texture, err := renderModelTexture(spec)
	if err != nil {
		t.Fatal(err)
	}
	glb, err := modelfiles.BuildGLB(mesh, texture, "MockHumanoid")
	if err != nil {
		t.Fatal(err)
	}
	gltf, err := modelfiles.BuildGLTF(mesh, texture, "MockHumanoid")
	if err != nil {
		t.Fatal(err)
	}
	usdz, err := modelfiles.BuildUSDZ(texture)
	if err != nil {
		t.Fatal(err)
	}
	threeMF, err := modelfiles.Build3MF(mesh)
	if err != nil {
		t.Fatal(err)
	}
	thumb, err := renderModelThumbnail(spec)
	if err != nil {
		t.Fatal(err)
	}
	validateBytes(t, "a.glb", glb, "model", "glb", "model/gltf-binary")
	validateBytes(t, "a.gltf", gltf, "converted_model", "converted.gltf", "model/gltf+json")
	validateBytes(t, "a.obj", modelfiles.BuildOBJ(mesh, "TEST", modelfiles.OBJDeliveredLibrary), "model", "obj", "text/plain")
	validateBytes(t, "b.obj", modelfiles.BuildOBJ(mesh, "TEST", modelfiles.OBJGeometryOnly), "model", "obj", "text/plain")
	validateBytes(t, "c.obj", modelfiles.BuildOBJ(mesh, "TEST", modelfiles.OBJDanglingLibrary), "model", "obj", "text/plain")
	validateBytes(t, "a.mtl", modelfiles.BuildMTL(), "model", "mtl", "text/plain")
	validateBytes(t, "a.stl", modelfiles.BuildSTL(mesh), "model", "stl", "application/octet-stream")
	validateBytes(t, "a.fbx", modelfiles.BuildFBX(mesh, "TEST"), "model", "fbx", "application/octet-stream")
	validateBytes(t, "a.usdz", usdz, "model", "usdz", "application/octet-stream")
	validateBytes(t, "a.3mf", threeMF, "model", "3mf", "application/octet-stream")
	validateBytes(t, "a.blend", modelfiles.BuildBlend(), "model", "blend", "application/octet-stream")
	validateBytes(t, "a.png", texture, "texture", "texture.base_color", "image/png")
	validateBytes(t, "t.png", thumb, "thumbnail", "primary", "image/png")
	t.Logf("glb=%d bytes, tris=%d, verts=%d", len(glb), mesh.TriangleCount(), mesh.VertexCount())
}

func TestMockModelFidelityProducesDeliverableSkinAndKeyframes(t *testing.T) {
	mesh := modelfiles.BuildHumanoid(digest("fidelity"), 1)
	texture, err := renderModelTexture(modelSpec{TextureSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"skeleton", "animation"} {
		for _, format := range []string{"glb", "gltf", "fbx"} {
			t.Run(level+format, func(t *testing.T) {
				build, media := modelfiles.BuildGLB, "model/gltf-binary"
				switch format {
				case "gltf":
					build, media = modelfiles.BuildGLTF, "model/gltf+json"
				case "fbx":
					build = func(mesh modelfiles.Mesh, _ []byte, label string, fidelity ...string) ([]byte, error) {
						return modelfiles.BuildFBX(mesh, label, fidelity...), nil
					}
					media = "application/octet-stream"
				}
				data, err := build(mesh, texture, "Fidelity", level)
				if err != nil {
					t.Fatal(err)
				}
				validateBytes(t, "model."+format, data, "model", format, media)
				name := filepath.Join(t.TempDir(), "model."+format)
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
				file, err := os.Open(name)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if err := local.ValidateFidelityFile(file, level); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// The static FBX stays static: the skeleton check must still reject it, so the
// rigging fix above is not the checker accepting any FBX (#265).
func TestStaticMockFBXHasNoSkeleton(t *testing.T) {
	name := filepath.Join(t.TempDir(), "static.fbx")
	if err := os.WriteFile(name, modelfiles.BuildFBX(modelfiles.BuildHumanoid(digest("static"), 1), "Static"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := local.ValidateFidelityFile(file, "skeleton"); err == nil {
		t.Fatal("static FBX passed the skeleton check")
	}
}

// #321: the large-model marker grows the GLB past the container's 64 MB /tmp
// and Meshy's 70 MiB input limit (#329), and the file still passes delivery checks.
func TestLargeModelMarkerGrowsAValidGLB(t *testing.T) {
	seed := digest("seed")
	spec := modelSpec{GeometrySeed: seed, TextureSeed: seed, Hue: 200, Subdivision: 4, TextureSize: 64, Label: "TEST"}
	texture, err := renderModelTexture(spec)
	if err != nil {
		t.Fatal(err)
	}
	glb, err := modelfiles.BuildPaddedGLB(modelfiles.BuildHumanoid(seed, spec.Subdivision), texture, "MockHumanoid", "", LargeModelBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(glb) <= 70<<20 || len(glb) > LargeModelBytes || binary.LittleEndian.Uint32(glb[8:12]) != uint32(len(glb)) {
		t.Fatalf("large GLB is %d bytes, header says %d", len(glb), binary.LittleEndian.Uint32(glb[8:12]))
	}
	validateBytes(t, "large.glb", glb, "model", "glb", "model/gltf-binary")
}

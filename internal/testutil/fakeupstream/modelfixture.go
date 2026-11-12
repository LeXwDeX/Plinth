package fakeupstream

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// smallModelFiles builds one small, fixed humanoid in every model container the delivery checks know
// (and, for the rigging and animation flows, the "glb:skeleton", "glb:animation", "fbx:skeleton" and
// "fbx:animation" variants that carry a skin and keyframes): "glb", "gltf", "fbx", "obj" (an OBJ ZIP with
// its material library and texture), "stl", "usdz" and "3mf", plus the texture "png". The mesh and the
// format writers are Mock generation's (internal/providers/mock/modelfiles); only the texture is drawn
// here. The fixture lives in testutil, not in the Mock adapter, so no production package carries a
// test-only export.
func smallModelFiles() (map[string]File, error) {
	seed := sha256.Sum256([]byte("fixture\x00"))
	mesh := modelfiles.BuildHumanoid(seed, 1)
	texture, err := fixtureTexture(64)
	if err != nil {
		return nil, err
	}
	glb, err := modelfiles.BuildGLB(mesh, texture, "Fixture")
	if err != nil {
		return nil, err
	}
	glbSkeleton, err := modelfiles.BuildGLB(mesh, texture, "Fixture", "skeleton")
	if err != nil {
		return nil, err
	}
	glbAnimation, err := modelfiles.BuildGLB(mesh, texture, "Fixture", "animation")
	if err != nil {
		return nil, err
	}
	gltf, err := modelfiles.BuildGLTF(mesh, texture, "Fixture")
	if err != nil {
		return nil, err
	}
	usdz, err := modelfiles.BuildUSDZ(texture)
	if err != nil {
		return nil, err
	}
	threeMF, err := modelfiles.Build3MF(mesh)
	if err != nil {
		return nil, err
	}
	objZip, err := modelfiles.BuildZIP(map[string][]byte{
		"model.obj":   modelfiles.BuildOBJ(mesh, "FIXTURE", modelfiles.OBJDeliveredLibrary),
		"model.mtl":   modelfiles.BuildMTL(),
		"texture.png": texture,
	}, []string{"model.obj", "model.mtl", "texture.png"})
	if err != nil {
		return nil, err
	}
	_, jpeg, err := wirefixture.SyntheticMedia("jpeg-1x1")
	if err != nil {
		return nil, err
	}
	return map[string]File{
		"glb":           {"model/gltf-binary", glb},
		"glb:skeleton":  {"model/gltf-binary", glbSkeleton},
		"glb:animation": {"model/gltf-binary", glbAnimation},
		"fbx:skeleton":  {"model/fbx", modelfiles.BuildFBX(mesh, "FIXTURE", "skeleton")},
		"fbx:animation": {"model/fbx", modelfiles.BuildFBX(mesh, "FIXTURE", "animation")},
		// A conversion of a textured model carries the texture in the format the
		// request asked for (the official default is JPEG), embedded in the FBX.
		"fbx:texture-png":  {"model/fbx", modelfiles.BuildTexturedFBX(mesh, "FIXTURE", "texture.png", texture)},
		"fbx:texture-jpeg": {"model/fbx", modelfiles.BuildTexturedFBX(mesh, "FIXTURE", "texture.jpg", jpeg)},
		"gltf":             {"model/gltf+json", gltf},
		"fbx":              {"model/fbx", modelfiles.BuildFBX(mesh, "FIXTURE")},
		"obj":              {"application/zip", objZip},
		"stl":              {"model/stl", modelfiles.BuildSTL(mesh)},
		"usdz":             {"model/vnd.usdz+zip", usdz},
		"3mf":              {"model/3mf", threeMF},
		"png":              {"image/png", texture},
	}, nil
}

// fixtureTexture draws a size x size atlas of six flat tones in the 3x2 layout the humanoid's UVs expect.
func fixtureTexture(size int) ([]byte, error) {
	tones := []color.NRGBA{
		{236, 196, 160, 255}, {52, 120, 190, 255}, {64, 90, 150, 255},
		{70, 98, 160, 255}, {90, 70, 50, 255}, {80, 64, 46, 255},
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	cellW, cellH := size/3, size/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cell := min(y/cellH, 1)*3 + min(x/cellW, 2)
			canvas.SetNRGBA(x, y, tones[cell])
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

// The humanoid has six boxes of six faces; a face cut into s x s squares carries
// 2*s*s triangles, so the model with subdivision s has 72*s*s of them. The model
// writers use a 16-bit index buffer and the humanoid has 36*(s+1)^2 vertices,
// which caps s at 41 (121,032 triangles).
const (
	lowPolyTrianglesPerStep = 72
	lowPolyMaxSubdivision   = 41
)

var lowPolyModels sync.Map // subdivision -> File

func lowPolySubdivision(triangles int) int {
	if triangles < lowPolyTrianglesPerStep {
		return 1
	}
	return min(int(math.Sqrt(float64(triangles)/lowPolyTrianglesPerStep)), lowPolyMaxSubdivision)
}

// LowPolyTriangles is the triangle count of the model LowPolyModelFile hands out
// for a target: the largest the fixture can carry without passing the target, and
// never fewer than the 72 of the plain fixture.
func LowPolyTriangles(triangles int) int {
	steps := lowPolySubdivision(triangles)
	return lowPolyTrianglesPerStep * steps * steps
}

// LowPolyModelFile is the fixed textured humanoid GLB cut finely enough to carry
// about the given number of triangles (see LowPolyTriangles). A fake that hands
// out a "reduced" model (Tripo decimate) cannot reduce the fixed source, so it
// serves this model at the size the request asked for; the texture and UV layout
// are the fixture's, so the file is textured, has UVs and passes the delivery
// checks.
func LowPolyModelFile(triangles int) (File, error) {
	steps := lowPolySubdivision(triangles)
	if cached, ok := lowPolyModels.Load(steps); ok {
		return cached.(File), nil
	}
	seed := sha256.Sum256([]byte("fixture\x00"))
	mesh := modelfiles.BuildHumanoid(seed, steps)
	texture, err := fixtureTexture(64)
	if err != nil {
		return File{}, err
	}
	glb, err := modelfiles.BuildGLB(mesh, texture, "Fixture")
	if err != nil {
		return File{}, err
	}
	file := File{MediaType: "model/gltf-binary", Data: glb}
	lowPolyModels.Store(steps, file)
	return file, nil
}

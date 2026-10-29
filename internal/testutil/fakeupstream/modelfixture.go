package fakeupstream

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/color"
	"image/png"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
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
	return map[string]File{
		"glb":           {"model/gltf-binary", glb},
		"glb:skeleton":  {"model/gltf-binary", glbSkeleton},
		"glb:animation": {"model/gltf-binary", glbAnimation},
		"fbx:skeleton":  {"model/fbx", modelfiles.BuildFBX(mesh, "FIXTURE", "skeleton")},
		"fbx:animation": {"model/fbx", modelfiles.BuildFBX(mesh, "FIXTURE", "animation")},
		"gltf":          {"model/gltf+json", gltf},
		"fbx":           {"model/fbx", modelfiles.BuildFBX(mesh, "FIXTURE")},
		"obj":           {"application/zip", objZip},
		"stl":           {"model/stl", modelfiles.BuildSTL(mesh)},
		"usdz":          {"model/vnd.usdz+zip", usdz},
		"3mf":           {"model/3mf", threeMF},
		"png":           {"image/png", texture},
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

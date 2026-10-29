package mock

// ModelFile is one deterministic model file of the fixture set.
type ModelFile struct {
	MediaType string
	Data      []byte
}

// SmallModelFiles builds one small, fixed humanoid in every model container
// the delivery checks know (and, for the rigging and animation flows, the
// "glb:skeleton", "glb:animation", "fbx:skeleton" and "fbx:animation" variants
// that carry a skin and keyframes): "glb", "gltf", "fbx", "obj" (an OBJ ZIP with its
// material library and texture), "stl", "usdz" and "3mf", plus the texture
// "png". The bytes are the ones Mock generation delivers, so they pass
// internal/assets/local's checks; test tooling (internal/testutil/fakeupstream)
// reuses them instead of keeping a second copy of the format writers.
func SmallModelFiles() (map[string]ModelFile, error) {
	seed := digest("fixture")
	spec := modelSpec{GeometrySeed: seed, TextureSeed: seed, Hue: 200, Subdivision: 1, TextureSize: 64, Label: "FIXTURE"}
	mesh := buildHumanoid(seed, spec.Subdivision)
	texture, err := renderModelTexture(spec)
	if err != nil {
		return nil, err
	}
	glb, err := buildGLB(mesh, texture, "Fixture")
	if err != nil {
		return nil, err
	}
	glbSkeleton, err := buildGLB(mesh, texture, "Fixture", "skeleton")
	if err != nil {
		return nil, err
	}
	glbAnimation, err := buildGLB(mesh, texture, "Fixture", "animation")
	if err != nil {
		return nil, err
	}
	gltf, err := buildGLTF(mesh, texture, "Fixture")
	if err != nil {
		return nil, err
	}
	usdz, err := buildUSDZ(texture)
	if err != nil {
		return nil, err
	}
	threeMF, err := build3MF(mesh)
	if err != nil {
		return nil, err
	}
	objZip, err := buildZIP(map[string][]byte{
		"model.obj":   buildOBJ(mesh, "FIXTURE", objDeliveredLibrary),
		"model.mtl":   buildMTL(),
		"texture.png": texture,
	}, []string{"model.obj", "model.mtl", "texture.png"})
	if err != nil {
		return nil, err
	}
	return map[string]ModelFile{
		"glb":           {"model/gltf-binary", glb},
		"glb:skeleton":  {"model/gltf-binary", glbSkeleton},
		"glb:animation": {"model/gltf-binary", glbAnimation},
		"fbx:skeleton":  {"model/fbx", buildFBX(mesh, "FIXTURE", "skeleton")},
		"fbx:animation": {"model/fbx", buildFBX(mesh, "FIXTURE", "animation")},
		"gltf":          {"model/gltf+json", gltf},
		"fbx":           {"model/fbx", buildFBX(mesh, "FIXTURE")},
		"obj":           {"application/zip", objZip},
		"stl":           {"model/stl", buildSTL(mesh)},
		"usdz":          {"model/vnd.usdz+zip", usdz},
		"3mf":           {"model/3mf", threeMF},
		"png":           {"image/png", texture},
	}, nil
}

package tripov3

// Generation textures (#704). A real H-series text task (v3.1-20260211, the
// Offering tripo/tripo-v3.1-full/text-to-3d) frozen with texture=false and
// pbr=false (2026-11-28, Plinth's production defaults otherwise) ended with a
// model GLB that has no texture and no image and one material; the same request
// with texture and PBR on (Plinth's defaults) ended with one of three textures,
// three images and one material. The observed counts are
// testdata/observation-704-texture-off.json. That Offering's model GLB is the
// GLTF export of the generation (the conversion delivers the model-role GLB), so
// the observation covers the generation and its GLTF export together; the raw
// exchanges and the file bytes were not recorded, so which map each texture is
// was not seen either.
//
// What the fake does with it:
//
//   - every GLB of a generation task whose request leaves the texture off, and
//     every GLB of a conversion of that task's model, is the fixture mesh with one
//     material and no texture (fakeupstream.ModelFile("glb:untextured"));
//   - every GLB of a generation task whose request has PBR on (pbr=true, or pbr
//     omitted next to a texture that is on: the page's default pbr is true), and
//     the GLB of a plain conversion of that task's model, is the fixture mesh with
//     the PBR pack: base color, metallic-roughness and normal, three textures and
//     three images in one material (fakeupstream.ModelFile("glb:pbr")).
//
// The rest of the task is unchanged. What is constructed, because nothing real
// backs it:
//
//   - the image and multiview endpoints are taken to behave like the text one;
//   - the three maps are taken to be base color, metallic-roughness and normal;
//   - pbr=true with texture=false keeps the texture and gets the PBR pack, as
//     Plinth's native pricing counts it (billing.go);
//   - texture=true with pbr=false keeps the one-texture fixture GLB;
//   - the other files of a conversion of an untextured or PBR source (FBX, OBJ,
//     USDZ, a 3MF), a conversion that names its texture size, format or origin
//     (convert.go), and a rig or decimation of it, are still the one-texture
//     fixtures.

// generatesUntextured reports a generation whose request turns the texture off:
// texture=false and no pbr=true (an omitted texture is on).
func (t *task) generatesUntextured() bool {
	return t.isGeneration() && !flag(t.request, "texture", true) && !flag(t.request, "pbr", false)
}

// generatesPBR reports a generation whose request keeps PBR on: pbr=true, or pbr
// omitted next to a texture that is on (the page's default pbr is true).
func (t *task) generatesPBR() bool {
	return t.isGeneration() && !t.generatesUntextured() && flag(t.request, "pbr", true)
}

// deliversUntexturedGLB reports a task whose GLB carries no texture: a generation
// with texture off, or a conversion of the model of one.
func (t *task) deliversUntexturedGLB() bool {
	return t.generatesUntextured() || (t.kind.Type == "convert_model" && t.sourceUntextured)
}

// deliversPBRGLB reports a task whose GLB carries the PBR pack: a generation with
// PBR on, or a conversion of the model of one.
func (t *task) deliversPBRGLB() bool {
	return t.generatesPBR() || (t.kind.Type == "convert_model" && t.sourcePBR)
}

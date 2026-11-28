package tripov3

// Generation with texture off (#704). A real H-series text task (v3.1-20260211,
// the Offering tripo/tripo-v3.1-full/text-to-3d) frozen with texture=false and
// pbr=false (2026-11-28, Plinth's production defaults otherwise) ended with a
// model GLB that has no texture and no image and one material; the same request
// with texture and PBR on ended with one of three textures and three images. The
// observed counts are testdata/observation-704-texture-off.json. That Offering's
// model GLB is the GLTF export of the generation (the conversion delivers the
// model-role GLB), so the observation covers the generation and its GLTF export
// together; the raw exchanges and the file bytes were not recorded.
//
// What the fake does with it: every GLB of a generation task whose request leaves
// the texture off, and every GLB of a conversion of that task's model, is the
// fixture mesh with one material and no texture (fakeupstream.ModelFile
// ("glb:untextured")). A textured request keeps the fixed textured GLB it always
// got (one texture, not the three of the real model). The rest of the task is
// unchanged. What is constructed, because nothing real backs it:
//
//   - the image and multiview endpoints are taken to behave like the text one;
//   - pbr=true with texture=false keeps the texture, as Plinth's native pricing
//     counts it (billing.go);
//   - the other files of a conversion of an untextured source (FBX, OBJ, USDZ, a
//     3MF), and a rig or decimation of it, are still the textured fixtures.

// generatesUntextured reports a generation whose request turns the texture off:
// texture=false and no pbr=true (an omitted texture is on).
func (t *task) generatesUntextured() bool {
	return t.isGeneration() && !flag(t.request, "texture", true) && !flag(t.request, "pbr", false)
}

// deliversUntexturedGLB reports a task whose GLB carries no texture: a generation
// with texture off, or a conversion of the model of one.
func (t *task) deliversUntexturedGLB() bool {
	return t.generatesUntextured() || (t.kind.Type == "convert_model" && t.sourceUntextured)
}

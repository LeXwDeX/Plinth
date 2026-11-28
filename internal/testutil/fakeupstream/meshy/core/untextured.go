package core

// Generation with texture off (#704). A real Meshy 7.1 Image to 3D task (the
// Offering meshy/meshy-7.1/image-to-3d) frozen with should_texture=false and
// enable_pbr=false (2026-11-28, revision f5bc5e10) ended with a model GLB that
// has no texture and no image and one material. The observed counts are
// openapi-v1/testdata/observation-704-texture-off.json; the raw exchanges and
// the file bytes were not recorded.
//
// What the fake does with it: every GLB of a generation task whose request sends
// should_texture=false is the fixture mesh with one material and no texture
// (fakeupstream.ModelFile("glb:untextured")). should_texture true or omitted
// keeps the textured GLB it always got. What is constructed, because nothing real
// backs it:
//
//   - Multi Image to 3D is taken to behave like Image to 3D (also with
//     enable_pbr=true, which that page does not refuse);
//   - the other files of an untextured task (FBX, USDZ, a 3MF), and a remesh,
//     convert, rig or retexture of it, are still the textured fixtures.

// generatesUntextured reports a generation task (a kind with a Textured rule)
// whose request turns texturing off.
func (t *task) generatesUntextured() bool {
	return t.kind.Textured != nil && t.request["should_texture"] == false
}

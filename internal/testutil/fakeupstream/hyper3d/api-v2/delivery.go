package hyper3dapiv2

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// What a Gen-2.5 generation delivers (#303). The PBR pack the fake serves is no
// longer one fixed file: its triangle count and its texture sizes follow the
// request, through fakeupstream.SizedModelFile, so the checks that read a
// delivered model (the triangle and texture budgets of a purpose, the PBR slots)
// can be regressed on the fake.
//
// The rules are constructed from the pages (docsRoot+"rodin-gen2-5" and
// docsRoot+"rodin-gen1-1-5", read in full on formDocs) and anchored by what real
// files showed: the game-character run (realRun: 10,000 Quad faces delivered
// about 21,800 triangles and three 2048x2048 PNGs for texture_mode=high) and the
// #188 acceptance (the display request's base color is 6144 beside two 2048 maps;
// the Bang sample of the 12K pack had two 4096 maps). Everything else here is the
// page's table applied, not observed.

// quadTrianglesPerHundredFaces is how many triangles a Quad face becomes in the
// GLB: the run turned 10,000 quads into about 21,800 triangles (2.18). It is one
// sample at one scale.
const quadTrianglesPerHundredFaces = 218

// gen25FacePresets are the page's quality presets of a Gen-2.5 tier, by topology.
var gen25FacePresets = map[string]map[string]int{
	"Quad": {"high": 50000, "medium": 18000, "low": 8000, "extra-low": 4000},
	"Raw":  {"high": 1000000, "medium": 500000, "low": 60000, "extra-low": 20000},
}

// gen1FacePresets are the presets of a form without a Gen-2.5 tier (a Gen-1/1.5
// asset), the same in both topologies (docsRoot+"rodin-gen1-1-5").
var gen1FacePresets = map[string]int{"high": 50000, "medium": 18000, "low": 8000, "extra-low": 4000}

// isGen25 reports whether the form names a Gen-2.5 tier; an omitted tier is
// Regular, a Gen-1/1.5 asset.
func isGen25(fields map[string]string) bool {
	return strings.HasPrefix(fields["tier"], "Gen-2.5-")
}

// meshModeOf is the topology the form asks for: Gen-2.5 tiers default to Raw and
// every other tier to Quad.
func meshModeOf(fields map[string]string) string {
	if mode, present := fields["mesh_mode"]; present {
		return mode
	}
	if isGen25(fields) {
		return "Raw"
	}
	return "Quad"
}

// targetFaces is the face count the form targets: quality_override when present
// (it takes precedence over quality), else the preset of quality (medium when
// omitted). The form has passed validateGenerationForm.
func targetFaces(fields map[string]string) int {
	if value, present := fields["quality_override"]; present {
		number, _ := wholeNumber(value)
		return int(number)
	}
	quality := orDefault(fields["quality"], "medium")
	if isGen25(fields) {
		return gen25FacePresets[meshModeOf(fields)][quality]
	}
	return gen1FacePresets[quality]
}

// deliveredTriangles is the triangle count of the delivered GLB before the
// fixture's ceiling: a triangle mesh has one triangle per face, a Quad mesh 2.18.
func deliveredTriangles(fields map[string]string) int {
	faces := targetFaces(fields)
	if meshModeOf(fields) == "Quad" {
		return (faces*quadTrianglesPerHundredFaces + 50) / 100
	}
	return faces
}

// The texture resolution table of the page (texture_mode with addons and
// uhd_texture): 2K for every mode below extreme-high, 4K with uhd_texture or the
// HighPack add-on, 8K with both; 6K for extreme-high, 12K with HighPack (uhd_texture
// changes nothing there). K is 1024 pixels, as in the measured 6144 and 12288
// maps. The page's own table has no row for legacy; the Generate Texture page
// lists it with the 2K modes, so it is 2K here too.
const (
	textureEdge2K  = 2048
	textureEdge4K  = 4096
	textureEdge6K  = 6144
	textureEdge8K  = 8192
	textureEdge12K = 12288
)

func baseTextureEdge(mode string, ultraHD, highPack bool) int {
	switch {
	case mode == "extreme-high" && highPack:
		return textureEdge12K
	case mode == "extreme-high":
		return textureEdge6K
	case highPack && ultraHD:
		return textureEdge8K
	case highPack || ultraHD:
		return textureEdge4K
	}
	return textureEdge2K
}

// packMapEdge is the edge of the metallic-roughness and normal maps beside a base
// color of the given edge. The 6144 base color of the display request came with
// two 2048 maps (the #188 acceptance); the 12288 one of the Bang sample with two
// 4096 maps, and 8192 is taken to follow it. Below 6144 the pack is one edge.
func packMapEdge(base int) int {
	switch {
	case base == textureEdge6K:
		return textureEdge2K
	case base > textureEdge6K:
		return textureEdge4K
	}
	return base
}

// hasHighPack reports whether the form lists the HighPack add-on. The wire
// encoding of an array field is not known (nothing sends it), so a field named
// addons, addons[] or addons[n] that names HighPack counts.
func hasHighPack(fields map[string]string) bool {
	for name, value := range fields {
		if (name == "addons" || strings.HasPrefix(name, "addons[")) && strings.Contains(value, "HighPack") {
			return true
		}
	}
	return false
}

// textureEdges are the edges of the PBR pack's base color, metallic-roughness and
// normal maps for a valid form. An omitted texture_mode is the default of the
// tier (a form without a Gen-2.5 tier has none, which is a 2K pack).
func textureEdges(fields map[string]string) []int {
	mode, present := fields["texture_mode"]
	if !present {
		mode = defaultTextureModes[fields["tier"]]
	}
	base := baseTextureEdge(mode, fields["uhd_texture"] == "true", hasHighPack(fields))
	other := packMapEdge(base)
	return []int{base, other, other}
}

// DeliveredModel is the PBR GLB the fake serves as the pack of a valid Gen-2.5
// generation submitted with these form fields: the file its download list names
// and its file route serves. A test of another package can take the model of a
// request from it without a round trip. The triangle count is the request's
// (deliveredTriangles) held at fakeupstream.MaxSizedModelTriangles, so a
// 2,000,000-face request delivers the ceiling and not two million triangles.
func DeliveredModel(fields map[string]string) (fakeupstream.File, error) {
	return fakeupstream.SizedModelFile(deliveredTriangles(fields), textureEdges(fields)...)
}

// textureRoute is the Generate Texture route.
const textureRoute = "/rodin_texture_only"

// TextureModel is the GLB a valid Generate Texture task submitted with these form
// fields delivers: the plain fixture's geometry (the real service keeps the
// uploaded mesh; the fake does not read it) under a PBR pack whose edges follow
// the request. The 1.9 real round (2026-11-24, #568) sent the Catalog's form
// (texture_mode=extreme-high, resolution=High, hd_texture=true) and got a
// base_high_pbr.glb whose texture edge was 12288: the generation table's
// extreme-high with HighPack. So resolution=High is taken for the HighPack
// add-on and hd_texture for uhd_texture, and every other cell of the table
// (6144 for extreme-high at Basic, 2K to 8K for the other modes) is that table
// applied, not observed. The two other maps of the 12288 pack follow
// packMapEdge (4096, as in the Bang sample of the 12K pack).
func TextureModel(fields map[string]string) (fakeupstream.File, error) {
	base := baseTextureEdge(fields["texture_mode"], fields["hd_texture"] == "true", fields["resolution"] == "High")
	other := packMapEdge(base)
	return fakeupstream.SizedModelFile(fakeupstream.SizedModelTriangles(0), base, other, other)
}

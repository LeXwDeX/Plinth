package fakeupstream

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

func inspectGLB(t *testing.T, data []byte) modelinspect.Report {
	t.Helper()
	model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
	if err != nil {
		t.Fatalf("the GLB cannot be read: %v", err)
	}
	return modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
}

// slotEdges is the longest edge of every texture slot the report finds present.
func slotEdges(report modelinspect.Report) map[string]int {
	edges := map[string]int{}
	for _, material := range report.Materials {
		for _, slot := range material.Slots {
			if slot.Present {
				edges[slot.Slot] = max(slot.Width, slot.Height)
			}
		}
	}
	return edges
}

// The count the model carries is the request rounded down to an even number (a
// closed triangle mesh has an even count) and kept between the one-step humanoid
// and the largest the 16-bit index fixture can hold.
func TestSizedModelTrianglesRoundsDownToEvenAndClamps(t *testing.T) {
	for requested, want := range map[int]int{
		-5: 72, 0: 72, 71: 72, 72: 72, 73: 72, 74: 74, 500: 500, 501: 500,
		8720: 8720, 17440: 17440, 21800: 21800, 109000: 109000,
		MaxSizedModelTriangles: MaxSizedModelTriangles, MaxSizedModelTriangles + 1: MaxSizedModelTriangles, 5_000_000: MaxSizedModelTriangles,
	} {
		if got := SizedModelTriangles(requested); got != want {
			t.Errorf("SizedModelTriangles(%d) = %d, want %d", requested, got, want)
		}
	}
	if MaxSizedModelTriangles != LowPolyTriangles(5_000_000) {
		t.Errorf("the ceiling %d differs from the low-poly fixture's %d", MaxSizedModelTriangles, LowPolyTriangles(5_000_000))
	}
}

// The model carries exactly the triangles it reports, however they fall between
// the humanoid's subdivision steps, stays one textured, unwrapped, closed mesh
// like the plain fixture, and keeps the vertices inside its 16-bit indices.
func TestSizedModelFileCarriesTheExactTriangleCount(t *testing.T) {
	plain, err := ModelFile("glb")
	if err != nil {
		t.Fatal(err)
	}
	plainReport := inspectGLB(t, plain.Data)
	// 286 is the one-step model with every triangle split once and 35 of the
	// results split again (107 splits); 121030 is the largest gap to fill (2,915
	// splits).
	for _, requested := range []int{72, 74, 75, 144, 286, 288, 290, 500, 8720, 17440, 21800, 60000, 121030, MaxSizedModelTriangles} {
		file, err := SizedModelFile(requested, 64, 64, 64)
		if err != nil {
			t.Fatalf("%d: %v", requested, err)
		}
		if file.MediaType != "model/gltf-binary" {
			t.Fatalf("%d: media type %q", requested, file.MediaType)
		}
		report := inspectGLB(t, file.Data)
		if want := int64(SizedModelTriangles(requested)); report.Triangles != want {
			t.Errorf("%d: the model carries %d triangles, want %d", requested, report.Triangles, want)
		}
		if report.Meshes != 1 || report.Primitives != 1 || len(report.Skins) != 0 || !report.UV.AllHaveUV || report.Vertices > 65535 {
			t.Errorf("%d: %d meshes, %d primitives, %d skins, all UV %v, %d vertices", requested, report.Meshes, report.Primitives, len(report.Skins), report.UV.AllHaveUV, report.Vertices)
		}
		// The splits that fill the gap between two steps keep the surface closed
		// the way the plain fixture is, so a purpose that asks for a watertight
		// model judges the fake as it judges the fixture.
		if report.Topology.Watertight != plainReport.Topology.Watertight || report.Topology.BoundaryEdges != plainReport.Topology.BoundaryEdges {
			t.Errorf("%d: watertight %v with %d boundary edges, the plain fixture's %v with %d", requested,
				report.Topology.Watertight, report.Topology.BoundaryEdges, plainReport.Topology.Watertight, plainReport.Topology.BoundaryEdges)
		}
		if report.Topology.DegenerateTriangles != 0 {
			t.Errorf("%d: %d degenerate triangles", requested, report.Topology.DegenerateTriangles)
		}
	}
	// The one-step model is the plain fixture's geometry.
	file, err := SizedModelFile(72, 64, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	report := inspectGLB(t, file.Data)
	if report.Triangles != plainReport.Triangles || report.Vertices != plainReport.Vertices || report.BBox.Size != plainReport.BBox.Size {
		t.Errorf("the one-step model (%d triangles, %d vertices, %v) is not the fixture's (%d, %d, %v)",
			report.Triangles, report.Vertices, report.BBox.Size, plainReport.Triangles, plainReport.Vertices, plainReport.BBox.Size)
	}
}

// Every edge asked for is one embedded PNG of that size, wired to the PBR slots
// in the order base color, metallic-roughness, normal, and the delivery checks
// accept the files. The 6144 base color of the display pack goes through the
// delivery check once, in the Hyper3D fake's tests; here a base color larger than
// its two maps stands for it.
func TestSizedModelFileEmbedsOnePNGPerRequestedEdge(t *testing.T) {
	for name, tc := range map[string]struct {
		edges []int
		want  map[string]int
	}{
		"the 2K pack of texture_mode high":  {[]int{2048, 2048, 2048}, map[string]int{"base_color": 2048, "metallic_roughness": 2048, "normal": 2048}},
		"a base color larger than its maps": {[]int{1536, 512, 512}, map[string]int{"base_color": 1536, "metallic_roughness": 512, "normal": 512}},
		"a base color alone":                {[]int{96}, map[string]int{"base_color": 96}},
		"a base color and a roughness map":  {[]int{96, 48}, map[string]int{"base_color": 96, "metallic_roughness": 48}},
		"no texture":                        {nil, map[string]int{}},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := SizedModelFile(21800, tc.edges...)
			if err != nil {
				t.Fatal(err)
			}
			report := inspectGLB(t, file.Data)
			if report.TextureCount != len(tc.edges) {
				t.Errorf("%d textures, want %d", report.TextureCount, len(tc.edges))
			}
			got := slotEdges(report)
			if len(got) != len(tc.want) {
				t.Fatalf("slots %v, want %v", got, tc.want)
			}
			for slot, edge := range tc.want {
				if got[slot] != edge {
					t.Errorf("slot %s is %d, want %d (all %v)", slot, got[slot], edge, got)
				}
			}
			for _, material := range report.Materials {
				for _, slot := range material.Slots {
					if slot.Present && slot.MIME != "image/png" {
						t.Errorf("slot %s is %q, want image/png", slot.Slot, slot.MIME)
					}
				}
			}
			validateDelivered(t, "sized", file.Data, "model", "glb", "model/gltf-binary")
		})
	}
}

func TestSizedModelFileIsDeterministic(t *testing.T) {
	first, err := SizedModelFile(21800, 256, 256, 256)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SizedModelFile(21801, 256, 256, 256)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Data, second.Data) {
		t.Error("two requests that round to the same count differ")
	}
	other, err := SizedModelFile(21800, 256, 256, 512)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Data, other.Data) {
		t.Error("a different edge produced the same bytes")
	}
}

func TestSizedModelFileRefusesTexturesItCannotBuild(t *testing.T) {
	for name, edges := range map[string][]int{
		"four textures": {64, 64, 64, 64}, "an edge of zero": {0}, "a negative edge": {-64}, "an edge too small for the atlas": {5},
		"an edge above the ceiling": {MaxSizedModelTextureEdge + 1},
	} {
		if _, err := SizedModelFile(72, edges...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The base color is the plain fixture's six-tone atlas in the 3x2 layout the
// humanoid's UVs expect, whatever its size, and the encoder's output reads back
// through the standard decoder.
func TestSizedTexturesDecodeToTheFixtureAtlas(t *testing.T) {
	for _, size := range []int{6, 64, 96, 301} {
		encoded, err := fixtureTexture(size)
		if err != nil {
			t.Fatal(err)
		}
		want, err := png.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		got, err := png.Decode(bytes.NewReader(sizedTexturePNG(textureBaseColor, size)))
		if err != nil {
			t.Fatalf("%d: %v", size, err)
		}
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%d: bounds %v, want %v", size, got.Bounds(), want.Bounds())
		}
		// The standard encoder drops the alpha channel of an opaque picture, so the
		// two decode to different color types; the pixels are what must agree.
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				gr, gg, gb, ga := got.At(x, y).RGBA()
				wr, wg, wb, wa := want.At(x, y).RGBA()
				if gr != wr || gg != wg || gb != wb || ga != wa {
					t.Fatalf("%d: pixel (%d,%d) is %v, want %v", size, x, y, got.At(x, y), want.At(x, y))
				}
			}
		}
	}
	// The two flat maps are one tone each and carry the size they were asked for.
	for kind, tone := range map[int][3]uint8{textureMetallicRoughness: {255, 128, 0}, textureNormal: {128, 128, 255}} {
		picture, err := png.Decode(bytes.NewReader(sizedTexturePNG(kind, 40)))
		if err != nil {
			t.Fatal(err)
		}
		if picture.Bounds() != image.Rect(0, 0, 40, 40) {
			t.Fatalf("kind %d: bounds %v", kind, picture.Bounds())
		}
		r, g, b, a := picture.At(17, 29).RGBA()
		if uint8(r>>8) != tone[0] || uint8(g>>8) != tone[1] || uint8(b>>8) != tone[2] || a != 0xffff {
			t.Errorf("kind %d: tone %d %d %d %d, want %v", kind, r>>8, g>>8, b>>8, a>>8, tone)
		}
	}
}

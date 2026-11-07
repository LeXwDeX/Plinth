package modelinspect

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"slices"
	"testing"
)

// bigSphereDocument builds a UV sphere with approximately two million
// triangles, normals, UVs and a small base-color texture. It is the shared
// fixture for the parse, inspect and render benchmarks.
func bigSphereDocument(tb testing.TB) []byte {
	tb.Helper()
	const (
		longitude = 1000
		latitude  = 1000
	)
	ringVertex := func(ring, segment int) uint32 {
		return uint32(1 + (ring-1)*longitude + segment)
	}
	vertexCount := 1 + (latitude-1)*longitude + 1
	positions := make([]float32, 0, vertexCount*3)
	uvs := make([]float32, 0, vertexCount*2)
	positions = append(positions, 0, 1, 0)
	uvs = append(uvs, 0.5, 0)
	for ring := 1; ring < latitude; ring++ {
		theta := math.Pi * float64(ring) / float64(latitude)
		sinTheta, cosTheta := math.Sin(theta), math.Cos(theta)
		for segment := 0; segment < longitude; segment++ {
			phi := 2 * math.Pi * float64(segment) / float64(longitude)
			positions = append(positions,
				float32(sinTheta*math.Cos(phi)),
				float32(cosTheta),
				float32(sinTheta*math.Sin(phi)),
			)
			uvs = append(uvs,
				float32(segment)/float32(longitude),
				float32(ring)/float32(latitude),
			)
		}
	}
	south := uint32(vertexCount - 1)
	positions = append(positions, 0, -1, 0)
	uvs = append(uvs, 0.5, 1)

	triangleCount := 2 * longitude * (latitude - 1)
	indices := make([]uint32, 0, triangleCount*3)
	for segment := 0; segment < longitude; segment++ {
		next := (segment + 1) % longitude
		indices = append(indices, 0, ringVertex(1, next), ringVertex(1, segment))
	}
	for ring := 1; ring < latitude-1; ring++ {
		for segment := 0; segment < longitude; segment++ {
			next := (segment + 1) % longitude
			a := ringVertex(ring, segment)
			b := ringVertex(ring, next)
			c := ringVertex(ring+1, next)
			d := ringVertex(ring+1, segment)
			indices = append(indices, a, b, c, a, c, d)
		}
	}
	for segment := 0; segment < longitude; segment++ {
		next := (segment + 1) % longitude
		indices = append(indices,
			ringVertex(latitude-1, segment),
			ringVertex(latitude-1, next),
			south,
		)
	}

	builder := newTestGLB()
	positionView := builder.addBufferView(floatBytes(positions), 0)
	position := builder.addAccessor(positionView, 0, 5126, vertexCount, "VEC3", false,
		[]float64{-1, -1, -1}, []float64{1, 1, 1})
	// The sphere is centered at the origin and unit-radius, so normals are the
	// same values as positions and can share the bufferView.
	normal := builder.addAccessor(positionView, 0, 5126, vertexCount, "VEC3", false, nil, nil)
	uvView := builder.addBufferView(floatBytes(uvs), 0)
	uv := builder.addAccessor(uvView, 0, 5126, vertexCount, "VEC2", false, nil, nil)
	indexView := builder.addBufferView(uint32Bytes(indices), 0)
	indexAccessor := builder.addAccessor(indexView, 0, 5125, len(indices), "SCALAR", false, nil, nil)

	textureImage := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			textureImage.SetNRGBA(x, y, color.NRGBA{R: 80, G: 160, B: 255, A: 255})
		}
	}
	imageView := builder.addBufferView(encodePNG(tb, textureImage), 0)
	imageIndex := builder.addImage(map[string]any{
		"bufferView": imageView, "mimeType": "image/png",
	})
	texture := builder.addTexture(map[string]any{"source": imageIndex})
	material := builder.addMaterial(map[string]any{
		"pbrMetallicRoughness": map[string]any{
			"baseColorTexture": map[string]any{"index": texture},
			"roughnessFactor":  0.8,
		},
	})
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{
			"POSITION": position, "NORMAL": normal, "TEXCOORD_0": uv,
		},
		"indices":  indexAccessor,
		"material": material,
	}})
	node := builder.addNode(map[string]any{"name": "sphere", "mesh": mesh})
	builder.addScene([]int{node})
	return builder.finish()
}

func BenchmarkModelPipeline(b *testing.B) {
	data := bigSphereDocument(b)
	limits := DefaultLimits()
	b.Run("Parse", func(b *testing.B) {
		b.ReportAllocs()
		// Parse validates the container and JSON eagerly but decodes accessor
		// payloads lazily, so this measures only that structural work. The
		// Inspect and Render benchmarks below include the accessor decode.
		for i := 0; i < b.N; i++ {
			model, err := Parse(bytes.NewReader(data), int64(len(data)), limits)
			if err != nil {
				b.Fatalf("Parse: %v", err)
			}
			if model == nil {
				b.Fatal("Parse returned nil")
			}
		}
	})

	model, err := Parse(bytes.NewReader(data), int64(len(data)), limits)
	if err != nil {
		b.Fatalf("Parse: %v", err)
	}
	b.Run("Inspect", func(b *testing.B) {
		b.ReportAllocs()
		// The default 1e-4 weld tolerance merges the first ring of a sphere
		// this fine near the poles, so use a tolerance below the ring spacing.
		options := InspectOptions{WeldEpsilonRatio: 1e-6, DebrisRatio: 0.01}
		for i := 0; i < b.N; i++ {
			report := Inspect(model, options)
			if report.Triangles != 1_998_000 {
				b.Fatalf("Triangles = %d, want 1998000", report.Triangles)
			}
			if !report.Topology.Watertight {
				b.Fatalf("benchmark sphere is not watertight: %+v", report.Topology)
			}
		}
	})
	b.Run("Render/Textured768", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			img, err := Render(model, RenderRequest{
				Width: 768, Height: 768, View: ViewThreeQuarter,
				Mode: ModeTextured, Workers: 1,
			})
			if err != nil {
				b.Fatalf("Render: %v", err)
			}
			if img == nil {
				b.Fatal("Render returned a nil image")
			}
		}
	})
}

// BenchmarkRenderTexturedPBR compares the legacy base-color path with the
// approximate PBR path at the render size used by the review pipeline. The PBR
// fixture uses base color, metallic-roughness and normal textures; both render
// the same 2x2-unit quad so geometry and coverage are identical. Warm-up runs
// keep decode and accessor costs out of the measured loop.
func BenchmarkRenderTexturedPBR(b *testing.B) {
	build := func(pbr bool) *Model {
		builder := newTestGLB()
		base := addSolidTexture(b, builder, 256, 256, color.NRGBA{R: 210, G: 180, B: 140, A: 255})
		var fields map[string]any
		if pbr {
			mr := addSolidTexture(b, builder, 256, 256, color.NRGBA{R: 255, G: 90, B: 255, A: 255})
			normal := addSolidTexture(b, builder, 256, 256, color.NRGBA{R: 128, G: 160, B: 255, A: 255})
			fields = pbrFactorFields(0.85, 0.35)
			fields["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"] =
				map[string]any{"index": base}
			fields["pbrMetallicRoughness"].(map[string]any)["metallicRoughnessTexture"] =
				map[string]any{"index": mr}
			fields["normalTexture"] = map[string]any{"index": normal}
		} else {
			fields = texturedQuadFields([4]float64{1, 1, 1, 1})
			fields["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"] =
				map[string]any{"index": base}
		}
		material := builder.addMaterial(fields)
		addTexturedQuad(builder, material, true, nil)
		return mustParseModel(b, builder.finish(), DefaultLimits())
	}
	request := RenderRequest{
		Width: 768, Height: 768, View: ViewFront, Mode: ModeTextured, Workers: 1,
	}
	for _, test := range []struct {
		name  string
		model *Model
	}{
		{name: "Legacy", model: build(false)},
		{name: "PBRThreeTextures", model: build(true)},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			if _, err := Render(test.model, request); err != nil {
				b.Fatalf("warm-up Render: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				img, err := Render(test.model, request)
				if err != nil {
					b.Fatalf("Render: %v", err)
				}
				benchmarkRenderSink = img.Bounds().Dx()
			}
		})
	}
}

var benchmarkRenderSink int

// BenchmarkTopologyEdgeCounting compares the two candidate edge-counting
// implementations. The package uses the sorted variant because it allocates
// less while remaining close in speed for multi-million-edge meshes.
func BenchmarkTopologyEdgeCounting(b *testing.B) {
	const edgeCount = 6_000_000
	const vertexCount = 999_000
	edges := make([]uint64, edgeCount)
	for i := range edges {
		edges[i] = edgeKey(uint32(i%vertexCount), uint32((i*7+1)%vertexCount))
	}
	b.Run("sort", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			boundary, nonManifold := countEdgesSorted(append([]uint64(nil), edges...))
			benchmarkEdgeSink = boundary + nonManifold
		}
	})
	b.Run("map", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			boundary, nonManifold := countEdgesMap(edges)
			benchmarkEdgeSink = boundary + nonManifold
		}
	})
}

var benchmarkEdgeSink int64

func countEdgesSorted(edges []uint64) (boundary, nonManifold int64) {
	slices.Sort(edges)
	for start := 0; start < len(edges); {
		end := start + 1
		for end < len(edges) && edges[end] == edges[start] {
			end++
		}
		switch used := end - start; {
		case used == 1:
			boundary++
		case used > 2:
			nonManifold++
		}
		start = end
	}
	return boundary, nonManifold
}

func countEdgesMap(edges []uint64) (boundary, nonManifold int64) {
	counts := make(map[uint64]uint32, len(edges))
	for _, edge := range edges {
		counts[edge]++
	}
	for _, count := range counts {
		switch {
		case count == 1:
			boundary++
		case count > 2:
			nonManifold++
		}
	}
	return boundary, nonManifold
}

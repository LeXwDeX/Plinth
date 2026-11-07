package modelinspect

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
)

// addSolidTexture embeds one solid-colour PNG and returns its texture index.
func addSolidTexture(tb testing.TB, b *testGLB, width, height int, pixel color.NRGBA) int {
	tb.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, pixel)
		}
	}
	view := b.addBufferView(encodePNG(tb, picture), 0)
	imageIndex := b.addImage(map[string]any{
		"bufferView": view, "mimeType": "image/png",
	})
	return b.addTexture(map[string]any{"source": imageIndex})
}

// addTexturedQuad adds a front-facing quad with TEXCOORD_0 and, optionally,
// outward vertex normals. nodeFields are merged into the node.
func addTexturedQuad(b *testGLB, material int, withNormals bool, nodeFields map[string]any) {
	position := b.addVec3Accessor([]float32{
		-1, -1, 0,
		1, -1, 0,
		1, 1, 0,
		-1, 1, 0,
	}, true)
	uv := b.addVec2Accessor([]float32{
		0, 1,
		1, 1,
		1, 0,
		0, 0,
	})
	attributes := map[string]int{"POSITION": position, "TEXCOORD_0": uv}
	if withNormals {
		attributes["NORMAL"] = b.addVec3Accessor([]float32{
			0, 0, 1,
			0, 0, 1,
			0, 0, 1,
			0, 0, 1,
		}, false)
	}
	indices := b.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	mesh := b.addMesh([]any{map[string]any{
		"attributes": attributes,
		"indices":    indices,
		"material":   material,
	}})
	node := map[string]any{"name": "quad", "mesh": mesh}
	for key, value := range nodeFields {
		node[key] = value
	}
	b.addScene([]int{b.addNode(node)})
}

func texturedQuadFields(baseColor [4]float64) map[string]any {
	return map[string]any{
		"pbrMetallicRoughness": map[string]any{
			"baseColorFactor": []float64{baseColor[0], baseColor[1], baseColor[2], baseColor[3]},
		},
	}
}

func pbrFactorFields(metallic, roughness float64) map[string]any {
	fields := texturedQuadFields([4]float64{1, 1, 1, 1})
	fields["pbrMetallicRoughness"].(map[string]any)["metallicFactor"] = metallic
	fields["pbrMetallicRoughness"].(map[string]any)["roughnessFactor"] = roughness
	return fields
}

func quadCenter(img *image.NRGBA) color.NRGBA {
	return img.NRGBAAt(img.Bounds().Dx()/2, img.Bounds().Dy()/2)
}

// TestMaterialPBRDeclaration locks the compatibility gate: only an explicit
// metallic factor, roughness factor or metallic-roughness texture opts into
// the PBR path. Base color and normal-only materials keep legacy lighting.
func TestMaterialPBRDeclaration(t *testing.T) {
	build := func(fields func(int, int) map[string]any) Material {
		t.Helper()
		b := newTestGLB()
		base := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
		normal := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 128, G: 128, B: 255, A: 255})
		material := b.addMaterial(fields(base, normal))
		addTexturedQuad(b, material, true, nil)
		model := mustParseModel(t, b.finish(), DefaultLimits())
		return model.Materials[0]
	}
	tests := []struct {
		name   string
		fields func(int, int) map[string]any
		want   bool
	}{
		{
			name: "base color factor only",
			fields: func(_, _ int) map[string]any {
				return texturedQuadFields([4]float64{1, 1, 1, 1})
			},
		},
		{
			name: "base color texture only",
			fields: func(base, _ int) map[string]any {
				fields := texturedQuadFields([4]float64{1, 1, 1, 1})
				fields["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"] =
					map[string]any{"index": base}
				return fields
			},
		},
		{
			name: "normal texture only",
			fields: func(_, normal int) map[string]any {
				fields := texturedQuadFields([4]float64{1, 1, 1, 1})
				fields["normalTexture"] = map[string]any{"index": normal}
				return fields
			},
		},
		{
			name: "metallic factor",
			fields: func(_, _ int) map[string]any {
				return pbrFactorFields(1, 1)
			},
			want: true,
		},
		{
			name: "roughness factor",
			fields: func(_, _ int) map[string]any {
				fields := texturedQuadFields([4]float64{1, 1, 1, 1})
				fields["pbrMetallicRoughness"].(map[string]any)["roughnessFactor"] = 0.4
				return fields
			},
			want: true,
		},
		{
			name: "metallic roughness texture",
			fields: func(base, _ int) map[string]any {
				fields := texturedQuadFields([4]float64{1, 1, 1, 1})
				fields["pbrMetallicRoughness"].(map[string]any)["metallicRoughnessTexture"] =
					map[string]any{"index": base}
				return fields
			},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if material := build(test.fields); material.pbrDeclared != test.want {
				t.Fatalf("pbrDeclared = %v, want %v", material.pbrDeclared, test.want)
			}
		})
	}
}

// TestTextureSampleSRGBAndLinear fixes the colour-space split: a mid-grey
// metallic-roughness or normal texel is read as linear 0.502, not sRGB 0.216.
func TestTextureSampleSRGBAndLinear(t *testing.T) {
	texture := &decodedTexture{
		width: 1, height: 1,
		pixels: []uint8{128, 128, 128, 255},
		wrapS:  10497, wrapT: 10497,
	}
	linear := texture.sampleLinear(0.5, 0.5)
	srgb := texture.sampleSRGB(0.5, 0.5)
	const wantLinear = 128.0 / 255.0
	if math.Abs(linear[0]-wantLinear) > 1e-9 {
		t.Fatalf("linear 128 sample = %v, want %v", linear[0], wantLinear)
	}
	if math.Abs(srgb[0]-srgbToLinear[128]) > 1e-9 {
		t.Fatalf("sRGB 128 sample = %v, want %v", srgb[0], srgbToLinear[128])
	}
	if math.Abs(linear[0]-0.502) > 0.001 {
		t.Fatalf("linear 128 sample = %v, want approximately 0.502", linear[0])
	}
	if math.Abs(srgb[0]-0.216) > 0.001 {
		t.Fatalf("sRGB 128 sample = %v, want approximately 0.216", srgb[0])
	}
	if linear == srgb {
		t.Fatal("linear and sRGB sampling returned the same value")
	}
	if linear[3] != 1 || srgb[3] != 1 {
		t.Fatalf("alpha samples = %v, %v; want 1", linear[3], srgb[3])
	}
}

// TestRenderPBRMetallicRoughnessFactors checks that metallic and roughness
// factors change the rendered response: a smooth metal is brighter than a
// rough metal, and a rough dielectric is brighter still.
func TestRenderPBRMetallicRoughnessFactors(t *testing.T) {
	render := func(metallic, roughness float64) color.NRGBA {
		b := newTestGLB()
		material := b.addMaterial(pbrFactorFields(metallic, roughness))
		addTexturedQuad(b, material, true, nil)
		model := mustParseModel(t, b.finish(), DefaultLimits())
		img := renderOrFail(t, model, RenderRequest{
			Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
		})
		return quadCenter(img)
	}
	smoothMetal := render(1, 0.08)
	roughMetal := render(1, 0.9)
	roughDielectric := render(0, 0.9)
	if smoothMetal.R <= roughMetal.R+40 {
		t.Fatalf("smooth metal %v is not brighter than rough metal %v", smoothMetal, roughMetal)
	}
	if roughDielectric.R <= roughMetal.R+40 {
		t.Fatalf("rough dielectric %v is not brighter than rough metal %v", roughDielectric, roughMetal)
	}
	if roughMetal == testBackground {
		t.Fatalf("rough metal centre is the background: %v", roughMetal)
	}
}

// TestRenderPBRMetallicRoughnessTexture drives the same response from the MR
// texture channels: G is roughness and B is metallic.
func TestRenderPBRMetallicRoughnessTexture(t *testing.T) {
	render := func(pixel color.NRGBA) color.NRGBA {
		b := newTestGLB()
		texture := addSolidTexture(t, b, 2, 2, pixel)
		fields := pbrFactorFields(1, 1)
		fields["pbrMetallicRoughness"].(map[string]any)["metallicRoughnessTexture"] =
			map[string]any{"index": texture}
		material := b.addMaterial(fields)
		addTexturedQuad(b, material, true, nil)
		model := mustParseModel(t, b.finish(), DefaultLimits())
		img := renderOrFail(t, model, RenderRequest{
			Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
		})
		return quadCenter(img)
	}
	roughMetal := render(color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	smoothMetal := render(color.NRGBA{R: 255, G: 0, B: 255, A: 255})
	roughDielectric := render(color.NRGBA{R: 255, G: 255, B: 0, A: 255})
	if smoothMetal.R <= roughMetal.R+40 {
		t.Fatalf("MR texture roughness channel did not sharpen the highlight: smooth %v rough %v",
			smoothMetal, roughMetal)
	}
	if roughDielectric.R <= roughMetal.R+40 {
		t.Fatalf("MR texture metallic channel did not change the response: dielectric %v metal %v",
			roughDielectric, roughMetal)
	}
}

// TestRenderNormalScaleZeroMatchesNoNormalMap checks that a zero normal scale
// is exactly equivalent to omitting the normal texture, which also avoids
// decoding a useless image.
func TestRenderNormalScaleZeroMatchesNoNormalMap(t *testing.T) {
	render := func(withNormal bool) *image.NRGBA {
		b := newTestGLB()
		normal := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 255, G: 0, B: 128, A: 255})
		fields := texturedQuadFields([4]float64{1, 1, 1, 1})
		if withNormal {
			fields["normalTexture"] = map[string]any{"index": normal, "scale": 0.0}
		}
		material := b.addMaterial(fields)
		addTexturedQuad(b, material, true, nil)
		model := mustParseModel(t, b.finish(), DefaultLimits())
		return renderOrFail(t, model, RenderRequest{
			Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
		})
	}
	withZeroScale := render(true)
	withoutNormal := render(false)
	if !bytes.Equal(withZeroScale.Pix, withoutNormal.Pix) {
		t.Fatal("normalScale=0 render differs from no normal map")
	}
}

// TestRenderNormalMapTiltsLighting checks that the derived TBN frame applies
// a tangent-space normal: tilting the map away from the main light darkens the
// surface while ambient light keeps it visible.
func TestRenderNormalMapTiltsLighting(t *testing.T) {
	render := func(normalPixel *color.NRGBA) color.NRGBA {
		b := newTestGLB()
		fields := texturedQuadFields([4]float64{1, 1, 1, 1})
		if normalPixel != nil {
			texture := addSolidTexture(t, b, 2, 2, *normalPixel)
			fields["normalTexture"] = map[string]any{"index": texture}
		}
		material := b.addMaterial(fields)
		addTexturedQuad(b, material, true, nil)
		model := mustParseModel(t, b.finish(), DefaultLimits())
		img := renderOrFail(t, model, RenderRequest{
			Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
		})
		return quadCenter(img)
	}
	// Green 255 gives +Y in tangent space. With this quad's UVs the bitangent
	// points down in world space, so the mapped normal tilts away from the
	// main light above.
	tilted := color.NRGBA{R: 128, G: 255, B: 128, A: 255}
	withTilt := render(&tilted)
	flat := render(nil)
	if flat.R <= withTilt.R+60 {
		t.Fatalf("normal map did not tilt the shading: flat %v tilted %v", flat, withTilt)
	}
	if withTilt.R < 30 {
		t.Fatalf("normal-mapped surface is effectively black: %v", withTilt)
	}
}

// TestRenderPBRMirroredAndBackface checks that the TBN frame and the
// approximate environment keep mirrored and back-facing PBR geometry lit.
func TestRenderPBRMirroredAndBackface(t *testing.T) {
	build := func(nodeFields map[string]any) *Model {
		b := newTestGLB()
		normal := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 200, G: 100, B: 200, A: 255})
		fields := pbrFactorFields(0.9, 0.35)
		fields["normalTexture"] = map[string]any{"index": normal}
		material := b.addMaterial(fields)
		addTexturedQuad(b, material, true, nodeFields)
		return mustParseModel(t, b.finish(), DefaultLimits())
	}
	mirrored := map[string]any{
		// Column-major scale (-1, 1, 1): determinant -1 reverses winding.
		"matrix": []float64{-1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
	}
	cases := []struct {
		name  string
		model *Model
		view  View
	}{
		{name: "mirrored front", model: build(mirrored), view: ViewFront},
		{name: "back face", model: build(nil), view: ViewBack},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			img := renderOrFail(t, test.model, RenderRequest{
				Width: 64, Height: 64, View: test.view, Mode: ModeTextured, Workers: 1,
			})
			center := quadCenter(img)
			if center == testBackground {
				t.Fatalf("centre pixel is the background: %v", center)
			}
			if int(center.R)+int(center.G)+int(center.B) < 60 {
				t.Fatalf("centre pixel is effectively black: %v", center)
			}
		})
	}
}

// TestRenderPBROverBudgetSlotsDegrade checks that optional MR and normal
// textures that exhaust the cumulative budget are skipped, leaving a render
// that matches the same material without those textures.
func TestRenderPBROverBudgetSlotsDegrade(t *testing.T) {
	limits := Limits{MaxTexturePixels: 16, MaxTotalTexturePixels: 4, MaxTextureBytes: 16}
	build := func(withOptional bool) *Model {
		b := newTestGLB()
		base := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		fields := pbrFactorFields(1, 0.8)
		fields["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"] =
			map[string]any{"index": base}
		if withOptional {
			mr := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 255, G: 0, B: 255, A: 255})
			normal := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 128, G: 255, B: 128, A: 255})
			fields["pbrMetallicRoughness"].(map[string]any)["metallicRoughnessTexture"] =
				map[string]any{"index": mr}
			fields["normalTexture"] = map[string]any{"index": normal}
		}
		material := b.addMaterial(fields)
		addTexturedQuad(b, material, true, nil)
		return mustParseModel(t, b.finish(), limits)
	}
	withOptional := build(true)
	withoutOptional := build(false)
	request := RenderRequest{Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1}
	degraded := renderOrFail(t, withOptional, request)
	factorsOnly := renderOrFail(t, withoutOptional, request)
	if !bytes.Equal(degraded.Pix, factorsOnly.Pix) {
		t.Fatal("over-budget MR/normal textures did not degrade to material factors")
	}
}

// TestPBRTexturesCountTowardRetainedBytes checks that decoding MR and normal
// images charges four RGBA bytes per pixel each.
func TestPBRTexturesCountTowardRetainedBytes(t *testing.T) {
	b := newTestGLB()
	addSolidTexture(t, b, 2, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	mr := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 255, G: 128, B: 128, A: 255})
	normal := addSolidTexture(t, b, 2, 2, color.NRGBA{R: 128, G: 128, B: 255, A: 255})
	fields := pbrFactorFields(1, 1)
	fields["pbrMetallicRoughness"].(map[string]any)["metallicRoughnessTexture"] =
		map[string]any{"index": mr}
	fields["normalTexture"] = map[string]any{"index": normal}
	material := b.addMaterial(fields)
	addTexturedQuad(b, material, true, nil)
	model := mustParseModel(t, b.finish(), DefaultLimits())

	before := model.RetainedBytes()
	if _, err := model.decodeTexture(mr); err != nil {
		t.Fatalf("decode MR texture: %v", err)
	}
	if _, err := model.decodeTexture(normal); err != nil {
		t.Fatalf("decode normal texture: %v", err)
	}
	const wantGrown = 2 * 2 * 4 * 2 // two 2x2 RGBA images
	if grown := model.RetainedBytes() - before; grown != wantGrown {
		t.Fatalf("MR and normal textures grew RetainedBytes by %d, want %d", grown, wantGrown)
	}
}

// addQuadTwoUVSets adds a front-facing quad with two constant UV sets, so a
// material slot's own texCoord or KHR_texture_transform alone decides which
// texel it samples.
func addQuadTwoUVSets(b *testGLB, material int, uv0, uv1 float64) {
	position := b.addVec3Accessor([]float32{
		-1, -1, 0,
		1, -1, 0,
		1, 1, 0,
		-1, 1, 0,
	}, true)
	u0, u1 := float32(uv0), float32(uv1)
	uvSet0 := b.addVec2Accessor([]float32{u0, u0, u0, u0, u0, u0, u0, u0})
	uvSet1 := b.addVec2Accessor([]float32{u1, u1, u1, u1, u1, u1, u1, u1})
	indices := b.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	mesh := b.addMesh([]any{map[string]any{
		"attributes": map[string]int{
			"POSITION": position, "TEXCOORD_0": uvSet0, "TEXCOORD_1": uvSet1,
		},
		"indices":  indices,
		"material": material,
	}})
	b.addScene([]int{b.addNode(map[string]any{"name": "quad", "mesh": mesh})})
}

// TestRenderPBRUsesSlotTexCoordAndTransform checks that the metallic-roughness
// slot uses its own texCoord and KHR_texture_transform instead of the base
// color UV set.
func TestRenderPBRUsesSlotTexCoordAndTransform(t *testing.T) {
	build := func(texCoord int, transform bool) color.NRGBA {
		b := newTestGLB()
		// Left column is rough metal, right column is rough dielectric.
		picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
		for y := 0; y < 2; y++ {
			picture.SetNRGBA(0, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			picture.SetNRGBA(1, y, color.NRGBA{R: 255, G: 255, B: 0, A: 255})
		}
		view := b.addBufferView(encodePNG(t, picture), 0)
		imageIndex := b.addImage(map[string]any{"bufferView": view, "mimeType": "image/png"})
		texture := b.addTexture(map[string]any{"source": imageIndex})
		fields := pbrFactorFields(1, 1)
		info := map[string]any{"index": texture, "texCoord": texCoord}
		if transform {
			info["extensions"] = map[string]any{
				"KHR_texture_transform": map[string]any{"offset": []float64{0.5, 0}},
			}
		}
		fields["pbrMetallicRoughness"].(map[string]any)["metallicRoughnessTexture"] = info
		material := b.addMaterial(fields)
		addQuadTwoUVSets(b, material, 0.25, 0.75)
		model := mustParseModel(t, b.finish(), DefaultLimits())
		img := renderOrFail(t, model, RenderRequest{
			Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
		})
		return quadCenter(img)
	}
	slotOne := build(1, false)
	slotZero := build(0, false)
	transformed := build(0, true)
	if slotOne.R <= slotZero.R+40 {
		t.Fatalf("texCoord 1 sampled the metal column: slot1 %v slot0 %v", slotOne, slotZero)
	}
	if transformed.R <= slotZero.R+40 {
		t.Fatalf("MR texture transform was not applied: transformed %v slot0 %v",
			transformed, slotZero)
	}
	if absByte(slotOne.R, transformed.R) > 2 {
		t.Fatalf("texCoord 1 and transformed texCoord 0 differ: %v vs %v", slotOne, transformed)
	}
}

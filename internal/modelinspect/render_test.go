package modelinspect

import (
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

var testBackground = color.NRGBA{R: 0xEC, G: 0xEC, B: 0xEC, A: 0xFF}

func renderOrFail(t *testing.T, model *Model, request RenderRequest) *image.NRGBA {
	t.Helper()
	image, err := Render(model, request)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return image
}

func countNonBackground(img *image.NRGBA) int {
	count := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.NRGBAAt(x, y) != testBackground {
				count++
			}
		}
	}
	return count
}

func TestRenderTexturedQuadCenter(t *testing.T) {
	model := mustParseModel(t, texturedQuadGLB(t), DefaultLimits())
	img := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 2,
	})
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("image bounds = %v, want 64x64", img.Bounds())
	}
	if img.NRGBAAt(0, 0) != testBackground {
		t.Fatalf("corner pixel = %v, want background %v", img.NRGBAAt(0, 0), testBackground)
	}
	center := img.NRGBAAt(32, 32)
	want := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	if absByte(center.R, want.R) > 3 || absByte(center.G, want.G) > 3 ||
		absByte(center.B, want.B) > 3 || center.A != 255 {
		t.Fatalf("centre pixel = %v, want approximately %v", center, want)
	}
}

func TestRenderTextureVNotFlipped(t *testing.T) {
	red := color.NRGBA{R: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	model := mustParseModel(t, texturedQuadDocument(t,
		[4]color.NRGBA{red, red, blue, blue}, [4]float64{1, 1, 1, 1}).finish(),
		DefaultLimits())
	img := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
	})
	top := img.NRGBAAt(32, 16)
	bottom := img.NRGBAAt(32, 48)
	if top.R < 150 || top.B > 100 {
		t.Fatalf("top pixel = %v, want the red texture row", top)
	}
	if bottom.B < 150 || bottom.R > 100 {
		t.Fatalf("bottom pixel = %v, want the blue texture row", bottom)
	}
}

func TestRenderFrontBackDistinct(t *testing.T) {
	model := mustParseModel(t, twoToneBoxDocument().finish(), DefaultLimits())
	front := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
	})
	back := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewBack, Mode: ModeTextured, Workers: 1,
	})
	if countNonBackground(front) < 100 || countNonBackground(back) < 100 {
		t.Fatalf("front/back rendered too few pixels: front=%d back=%d",
			countNonBackground(front), countNonBackground(back))
	}
	frontCenter := front.NRGBAAt(32, 32)
	backCenter := back.NRGBAAt(32, 32)
	if frontCenter.R < 150 || frontCenter.G > 90 || frontCenter.B > 90 {
		t.Fatalf("front centre = %v, want red", frontCenter)
	}
	if backCenter.G < 150 || backCenter.R > 90 || backCenter.B > 90 {
		t.Fatalf("back centre = %v, want green", backCenter)
	}
	if frontCenter == backCenter {
		t.Fatalf("front and back centre pixels are identical: %v", frontCenter)
	}
}

func TestRenderViewsModesAndProjections(t *testing.T) {
	model := mustParseModel(t, cubeGLB(), DefaultLimits())
	views := []View{ViewFront, ViewBack, ViewLeft, ViewRight, ViewTop, ViewThreeQuarter, ViewCustom}
	for _, view := range views {
		request := RenderRequest{
			Width: 48, Height: 48, View: view, Azimuth: 30, Elevation: 15,
			Mode: ModeClay, Workers: 1,
		}
		img := renderOrFail(t, model, request)
		if countNonBackground(img) == 0 {
			t.Fatalf("view %s rendered no geometry", view)
		}
	}
	for _, mode := range []RenderMode{ModeTextured, ModeClay, ModeNormals, ModeWireframe} {
		request := RenderRequest{
			Width: 48, Height: 48, View: ViewThreeQuarter, Mode: mode,
			Workers: 2, WireframeMaxTriangles: 100,
		}
		img := renderOrFail(t, model, request)
		if countNonBackground(img) == 0 {
			t.Fatalf("mode %s rendered no geometry", mode)
		}
	}
	perspective := renderOrFail(t, model, RenderRequest{
		Width: 48, Height: 48, View: ViewThreeQuarter, Projection: ProjectionPerspective,
		Mode: ModeClay, Workers: 1,
	})
	if countNonBackground(perspective) == 0 {
		t.Fatal("perspective render produced no geometry")
	}
}

func TestRenderWireframeTooDense(t *testing.T) {
	model := mustParseModel(t, cubeGLB(), DefaultLimits())
	_, err := Render(model, RenderRequest{
		Width: 32, Height: 32, Mode: ModeWireframe,
	})
	if !errors.Is(err, ErrTooDense) {
		t.Fatalf("zero wireframe budget error = %v, want ErrTooDense", err)
	}
	_, err = Render(model, RenderRequest{
		Width: 32, Height: 32, Mode: ModeWireframe, WireframeMaxTriangles: 1,
	})
	if !errors.Is(err, ErrTooDense) {
		t.Fatalf("error = %v, want ErrTooDense", err)
	}
	img := renderOrFail(t, model, RenderRequest{
		Width: 48, Height: 48, Mode: ModeWireframe, WireframeMaxTriangles: 12,
	})
	if countNonBackground(img) == 0 {
		t.Fatal("wireframe render produced no geometry")
	}
	clay := renderOrFail(t, model, RenderRequest{
		Width: 48, Height: 48, View: ViewThreeQuarter, Mode: ModeClay,
	})
	wireframe := renderOrFail(t, model, RenderRequest{
		Width: 48, Height: 48, View: ViewThreeQuarter, Mode: ModeWireframe,
		WireframeMaxTriangles: 12,
	})
	differences := 0
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			if clay.NRGBAAt(x, y) != wireframe.NRGBAAt(x, y) {
				differences++
			}
		}
	}
	if differences == 0 {
		t.Fatal("wireframe render is identical to clay; triangle edges were not drawn")
	}
}

func TestRenderIsolate(t *testing.T) {
	model := mustParseModel(t, twoCubesAndSliverDocument().finish(), DefaultLimits())
	img := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeClay,
		Highlight: []string{"n1.p0"}, Isolate: true, Workers: 1,
	})
	count := countNonBackground(img)
	if count < 1000 {
		t.Fatalf("isolated cube rendered only %d pixels; framing probably included hidden parts", count)
	}
	highlighted := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeClay,
		Highlight: []string{"n0.p0"}, Workers: 1,
	})
	if countNonBackground(highlighted) == 0 {
		t.Fatal("highlight render produced no geometry")
	}
}

func TestRenderOverLimitTextureFallsBackToFactor(t *testing.T) {
	orange := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	model := mustParseModel(t, texturedQuadDocument(t,
		[4]color.NRGBA{orange, orange, orange, orange},
		[4]float64{0, 0.4, 0, 1}).finish(),
		Limits{MaxTexturePixels: 1})
	img := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
	})
	center := img.NRGBAAt(32, 32)
	if center.G < 120 || center.R > 60 || center.B > 60 {
		t.Fatalf("centre pixel = %v, want the green baseColorFactor", center)
	}
}

func TestRenderVertexColor(t *testing.T) {
	builder := newTestGLB()
	position := builder.addVec3Accessor([]float32{
		-1, -1, 0,
		1, -1, 0,
		1, 1, 0,
		-1, 1, 0,
	}, true)
	uv := builder.addVec2Accessor([]float32{
		0, 1,
		1, 1,
		1, 0,
		0, 0,
	})
	colorView := builder.addBufferView(floatBytes([]float32{
		0.5, 0.5, 0.5, 1,
		0.5, 0.5, 0.5, 1,
		0.5, 0.5, 0.5, 1,
		0.5, 0.5, 0.5, 1,
	}), 0)
	vertexColor := builder.addAccessor(colorView, 0, 5126, 4, "VEC4", false, nil, nil)
	white := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			white.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	imageView := builder.addBufferView(encodePNG(t, white), 0)
	imageIndex := builder.addImage(map[string]any{
		"bufferView": imageView, "mimeType": "image/png",
	})
	texture := builder.addTexture(map[string]any{"source": imageIndex})
	material := builder.addMaterial(map[string]any{
		"pbrMetallicRoughness": map[string]any{
			"baseColorTexture": map[string]any{"index": texture},
		},
	})
	indices := builder.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{
			"POSITION": position, "TEXCOORD_0": uv, "COLOR_0": vertexColor,
		},
		"indices":  indices,
		"material": material,
	}})
	node := builder.addNode(map[string]any{"mesh": mesh})
	builder.addScene([]int{node})
	model := mustParseModel(t, builder.finish(), DefaultLimits())
	img := renderOrFail(t, model, RenderRequest{
		Width: 64, Height: 64, View: ViewFront, Mode: ModeTextured, Workers: 1,
	})
	center := img.NRGBAAt(32, 32)
	want := linearToSRGBByte(0.5)
	if absByte(center.R, want) > 3 || absByte(center.G, want) > 3 || absByte(center.B, want) > 3 {
		t.Fatalf("centre pixel = %v, want COLOR_0 grey %d", center, want)
	}
}

func TestTextureSampleOrientationAndWrap(t *testing.T) {
	texture := &decodedTexture{
		width: 2, height: 2,
		pixels: []uint8{
			255, 0, 0, 255, 255, 0, 0, 255, // top row: red
			0, 0, 255, 255, 0, 0, 255, 255, // bottom row: blue
		},
		wrapS: 10497, wrapT: 10497,
	}
	top := texture.sample(0.5, 0.25)
	bottom := texture.sample(0.5, 0.75)
	if top[0] < 0.9 || top[2] > 0.1 {
		t.Fatalf("sample at v=0.25 = %v, want red (v=0 is the top row)", top)
	}
	if bottom[2] < 0.9 || bottom[0] > 0.1 {
		t.Fatalf("sample at v=0.75 = %v, want blue", bottom)
	}
	if repeated := texture.sample(1.5, 0.25); repeated != top {
		t.Fatalf("repeat sample = %v, want %v", repeated, top)
	}
	horizontal := &decodedTexture{
		width: 2, height: 2,
		pixels: []uint8{
			255, 0, 0, 255, 0, 0, 255, 255, // top row: red, blue
			255, 0, 0, 255, 0, 0, 255, 255,
		},
		wrapS: 33071, wrapT: 33071,
	}
	clamped := horizontal.sample(1.5, 0.25)
	if clamped[2] < 0.9 || clamped[0] > 0.1 {
		t.Fatalf("clamped sample = %v, want the rightmost blue texel", clamped)
	}
	if nan := texture.sample(math.NaN(), 0.5); nan != [4]float64{} {
		t.Fatalf("NaN sample = %v, want zero", nan)
	}
}

func TestRenderErrors(t *testing.T) {
	model := mustParseModel(t, cubeGLB(), DefaultLimits())
	tests := []struct {
		name    string
		request RenderRequest
		want    error
	}{
		{"unknown mode", RenderRequest{Width: 8, Height: 8, Mode: "hologram"}, ErrInvalid},
		{"unknown projection", RenderRequest{Width: 8, Height: 8, Projection: "fisheye"}, ErrInvalid},
		{"unknown view", RenderRequest{Width: 8, Height: 8, View: "underside"}, ErrInvalid},
		{"zero width", RenderRequest{Width: 0, Height: 8}, ErrInvalid},
		{"too wide", RenderRequest{Width: MaxRenderDimension + 1, Height: 8}, ErrTooLarge},
		{"negative workers", RenderRequest{Width: 8, Height: 8, Workers: -1}, ErrInvalid},
		{"negative wireframe limit", RenderRequest{Width: 8, Height: 8, WireframeMaxTriangles: -1}, ErrInvalid},
		{"unknown highlight", RenderRequest{Width: 8, Height: 8, Highlight: []string{"n99.p0"}}, ErrInvalid},
		{"isolate without highlight", RenderRequest{Width: 8, Height: 8, Isolate: true}, ErrInvalid},
		{"custom elevation", RenderRequest{Width: 8, Height: 8, View: ViewCustom, Elevation: 100}, ErrInvalid},
		{"custom NaN", RenderRequest{Width: 8, Height: 8, View: ViewCustom, Azimuth: math.NaN()}, ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Render(model, test.request); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := Render(nil, RenderRequest{Width: 8, Height: 8}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil model error = %v, want ErrInvalid", err)
	}
}

func TestRenderZeroDimensionsSmall(t *testing.T) {
	model := mustParseModel(t, cubeGLB(), DefaultLimits())
	img := renderOrFail(t, model, RenderRequest{Width: 1, Height: 1, Mode: ModeClay})
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("image bounds = %v, want 1x1", img.Bounds())
	}
}

func absByte(value, want uint8) int {
	difference := int(value) - int(want)
	if difference < 0 {
		return -difference
	}
	return difference
}

// alphaMaskQuadGLB builds one front-facing quad whose base-color alpha is
// masked at the given cutoff. A nil cutoff exercises the glTF default.
func alphaMaskQuadGLB(t testing.TB, alpha float64, cutoff *float64) []byte {
	t.Helper()
	builder := newTestGLB()
	position := builder.addVec3Accessor([]float32{
		-1, -1, 0,
		1, -1, 0,
		1, 1, 0,
		-1, 1, 0,
	}, true)
	indices := builder.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	fields := map[string]any{
		"pbrMetallicRoughness": map[string]any{
			"baseColorFactor": []float64{1, 1, 1, alpha},
		},
		"alphaMode": "MASK",
	}
	if cutoff != nil {
		fields["alphaCutoff"] = *cutoff
	}
	material := builder.addMaterial(fields)
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
		"material":   material,
	}})
	node := builder.addNode(map[string]any{"mesh": mesh})
	builder.addScene([]int{node})
	return builder.finish()
}

func TestRenderMaskUsesMaterialAlphaCutoff(t *testing.T) {
	low, high := 0.2, 0.8
	cases := []struct {
		name    string
		cutoff  *float64
		visible bool
	}{
		{name: "default cutoff keeps alpha at the threshold", visible: true},
		{name: "low cutoff keeps a half-transparent texel", cutoff: &low, visible: true},
		{name: "high cutoff discards a half-transparent texel", cutoff: &high, visible: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := mustParseModel(t, alphaMaskQuadGLB(t, 0.5, test.cutoff), DefaultLimits())
			image := renderOrFail(t, model, RenderRequest{
				Width: 32, Height: 32, View: ViewFront, Mode: ModeTextured, Workers: 1,
			})
			center := image.NRGBAAt(16, 16)
			if test.visible && center == testBackground {
				t.Fatalf("centre pixel = background, want the masked material to survive")
			}
			if !test.visible && center != testBackground {
				t.Fatalf("centre pixel = %v, want background for the discarded mask", center)
			}
		})
	}
}

// quadWithMatrixDocument builds a front-facing quad, optionally under a
// mirrored world transform (the matrix determinant is negative).
func quadWithMatrixDocument(t testing.TB, mirrored bool) *testGLB {
	t.Helper()
	builder := newTestGLB()
	position := builder.addVec3Accessor([]float32{
		-1, -1, 0,
		1, -1, 0,
		1, 1, 0,
		-1, 1, 0,
	}, true)
	indices := builder.addIndices16([]uint16{0, 1, 2, 0, 2, 3})
	mesh := builder.addMesh([]any{map[string]any{
		"attributes": map[string]int{"POSITION": position},
		"indices":    indices,
	}})
	node := map[string]any{"mesh": mesh}
	if mirrored {
		// Column-major scale (-1, 1, 1); determinant = -1 reverses winding.
		node["matrix"] = []float64{-1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	}
	builder.addNode(node)
	builder.addScene([]int{0})
	return builder
}

func TestRenderCullingAccountsForMirroredTransform(t *testing.T) {
	for _, mirrored := range []bool{false, true} {
		name := "upright"
		if mirrored {
			name = "mirrored"
		}
		t.Run(name, func(t *testing.T) {
			model := mustParseModel(t, quadWithMatrixDocument(t, mirrored).finish(), DefaultLimits())
			image := renderOrFail(t, model, RenderRequest{
				Width: 32, Height: 32, View: ViewFront, Mode: ModeClay,
				CullBackfaces: true, Workers: 1,
			})
			if countNonBackground(image) == 0 {
				t.Fatal("front-facing quad was culled")
			}
		})
	}
}

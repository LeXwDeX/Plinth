package fakeupstream

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
)

func approximately(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-4 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func mustSizedHumanoid(t *testing.T, triangles int) modelfiles.Mesh {
	t.Helper()
	mesh, err := sizedHumanoid(SizedModelTriangles(triangles))
	if err != nil {
		t.Fatal(err)
	}
	return mesh
}

// A converted model carries the texture in the format and at the size the
// conversion was asked for, the triangles of its source, and the origin the
// conversion left it at (#305).
func TestConvertedModelFileWritesTheRequestedTextureAndOrigin(t *testing.T) {
	_, maximum := ModelBounds()
	for name, tc := range map[string]struct {
		spec   ConvertedModelSpec
		mime   string
		edge   int
		bottom [3]float64 // the bottom centre of the model as the inspector reads it
	}{
		"PNG at 2048 with the pivot at the bottom": {
			ConvertedModelSpec{Triangles: 72, TextureEdge: 2048, TextureFormat: TextureFormatPNG, Offset: CenterOrigin(true)},
			"image/png", 2048, [3]float64{0, 0, 0},
		},
		"PNG at 1024 left at the middle of the box": {
			ConvertedModelSpec{Triangles: 72, TextureEdge: 1024, TextureFormat: TextureFormatPNG, Offset: CenterOrigin(false)},
			"image/png", 1024, [3]float64{0, -maximum[1] / 2, 0},
		},
		"JPEG at 512": {
			ConvertedModelSpec{Triangles: 72, TextureEdge: 512, TextureFormat: TextureFormatJPEG, Offset: CenterOrigin(true)},
			"image/jpeg", 512, [3]float64{0, 0, 0},
		},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := ConvertedModelFile(tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			if file.MediaType != "model/gltf-binary" {
				t.Fatalf("media type %q", file.MediaType)
			}
			report := inspectGLB(t, file.Data)
			if report.Triangles != 72 || len(report.Skins) != 0 || len(report.Animations) != 0 {
				t.Fatalf("triangles %d skins %d animations %d, want the plain humanoid", report.Triangles, len(report.Skins), len(report.Animations))
			}
			var base *int
			for _, material := range report.Materials {
				for _, slot := range material.Slots {
					if slot.Slot == "base_color" && slot.Present {
						if slot.MIME != tc.mime || slot.Width != tc.edge || slot.Height != tc.edge {
							t.Fatalf("base color %s %dx%d, want %s %d", slot.MIME, slot.Width, slot.Height, tc.mime, tc.edge)
						}
						base = new(int)
					}
				}
			}
			if base == nil {
				t.Fatal("the model has no base color texture")
			}
			for axis, want := range tc.bottom {
				approximately(t, "bottom centre "+string(rune('x'+axis)), report.BBox.BottomCenterOffset[axis], want)
			}
			// The bytes really are that picture, not a header that says so.
			start := bytes.Index(file.Data, map[string][]byte{"image/png": []byte("\x89PNG"), "image/jpeg": []byte("\xff\xd8\xff")}[tc.mime])
			if start < 0 {
				t.Fatalf("no %s picture in the GLB", tc.mime)
			}
			var size image.Config
			var decodeErr error
			if tc.mime == "image/png" {
				size, decodeErr = png.DecodeConfig(bytes.NewReader(file.Data[start:]))
			} else {
				size, decodeErr = jpeg.DecodeConfig(bytes.NewReader(file.Data[start:]))
			}
			if decodeErr != nil || size.Width != tc.edge || size.Height != tc.edge {
				t.Fatalf("the embedded picture is %dx%d (%v), want %d", size.Width, size.Height, decodeErr, tc.edge)
			}
		})
	}
}

// The skin of a rigged source and the clip of an animated one survive the
// conversion, with the same joints; a clip needs the skin it moves.
func TestConvertedModelFileKeepsTheSkinAndTheClip(t *testing.T) {
	skinned, err := ConvertedModelFile(ConvertedModelSpec{Triangles: 72, TextureEdge: 256, TextureFormat: TextureFormatPNG, Skeleton: true, Offset: CenterOrigin(false)})
	if err != nil {
		t.Fatal(err)
	}
	report := inspectGLB(t, skinned.Data)
	if len(report.Skins) != 1 || report.Skins[0].Joints != 1 || len(report.Animations) != 0 {
		t.Fatalf("skins %+v animations %+v", report.Skins, report.Animations)
	}
	// The skinned model is posed by its joint, which sits at the origin: the move
	// of the origin is in the vertices, so the inspector reads it back.
	_, maximum := ModelBounds()
	approximately(t, "skinned bottom centre y", report.BBox.BottomCenterOffset[1], -maximum[1]/2)
	if bytes.Count(skinned.Data, []byte("JOINTS_0")) != 1 || bytes.Count(skinned.Data, []byte("WEIGHTS_0")) != 1 ||
		bytes.Count(skinned.Data, []byte("inverseBindMatrices")) != 1 {
		t.Fatal("the skin lacks its joints, weights or inverse bind matrices")
	}

	animated, err := ConvertedModelFile(ConvertedModelSpec{Triangles: 72, TextureEdge: 256, TextureFormat: TextureFormatPNG, Skeleton: true, Animation: true})
	if err != nil {
		t.Fatal(err)
	}
	report = inspectGLB(t, animated.Data)
	if len(report.Skins) != 1 || len(report.Animations) != 1 || report.Animations[0].Channels != 1 {
		t.Fatalf("skins %+v animations %+v", report.Skins, report.Animations)
	}
	if _, err := ConvertedModelFile(ConvertedModelSpec{Triangles: 72, TextureEdge: 256, TextureFormat: TextureFormatPNG, Animation: true}); err == nil {
		t.Fatal("a clip without its skin was accepted")
	}
}

// The conversion keeps the triangles of its source however many they are, and
// refuses what the fixture cannot draw instead of falling back silently.
func TestConvertedModelFileKeepsTheTrianglesAndRefusesWhatItCannotDraw(t *testing.T) {
	for _, triangles := range []int{72, LowPolyTriangles(9370), LowPolyTriangles(20000)} {
		file, err := ConvertedModelFile(ConvertedModelSpec{Triangles: triangles, TextureEdge: 64, TextureFormat: TextureFormatPNG})
		if err != nil {
			t.Fatal(err)
		}
		if got := inspectGLB(t, file.Data).Triangles; got != int64(triangles) {
			t.Errorf("%d triangles in, %d out", triangles, got)
		}
	}
	for name, spec := range map[string]ConvertedModelSpec{
		"an edge below the atlas":     {Triangles: 72, TextureEdge: 2, TextureFormat: TextureFormatPNG},
		"an edge above the fixture":   {Triangles: 72, TextureEdge: MaxSizedModelTextureEdge + 1, TextureFormat: TextureFormatPNG},
		"a JPEG above its cap":        {Triangles: 72, TextureEdge: maxConvertedJPEGEdge + 1, TextureFormat: TextureFormatJPEG},
		"a format it cannot draw":     {Triangles: 72, TextureEdge: 64, TextureFormat: "WEBP"},
		"no texture format":           {Triangles: 72, TextureEdge: 64},
		"a clip without a skin":       {Triangles: 72, TextureEdge: 64, TextureFormat: TextureFormatPNG, Animation: true},
		"a negative edge":             {Triangles: 72, TextureEdge: -64, TextureFormat: TextureFormatPNG},
		"a zero edge with a skeleton": {Triangles: 72, Skeleton: true, TextureFormat: TextureFormatPNG},
	} {
		if _, err := ConvertedModelFile(spec); err == nil {
			t.Errorf("%s: the model was built", name)
		}
	}
}

// The plain sized model is byte for byte what it was before the extras existed.
func TestSizedModelFileIsUnchangedByTheConvertedModelExtras(t *testing.T) {
	plain, err := SizedModelFile(500, 64)
	if err != nil {
		t.Fatal(err)
	}
	again, err := buildGLB(mustSizedHumanoid(t, 500), [][]byte{sizedTexturePNG(textureBaseColor, 64)}, glbExtras{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.Data, again) {
		t.Fatal("a sized model changed")
	}
	if bytes.Contains(plain.Data, []byte("JOINTS_0")) || bytes.Contains(plain.Data, []byte("skins")) {
		t.Fatal("a sized model carries a skin")
	}
}

// The origin offsets: the middle of the box puts the centre on the origin, the
// bottom pivot puts the feet there; the fixture's feet rest on y = 0.
func TestCenterOriginMovesTheBoundingBox(t *testing.T) {
	minimum, maximum := ModelBounds()
	if minimum[1] != 0 || maximum[1] <= 0 {
		t.Fatalf("the fixture stands on %v..%v, want feet on y = 0", minimum[1], maximum[1])
	}
	middle, bottom := CenterOrigin(false), CenterOrigin(true)
	approximately(t, "middle y", middle[1], -maximum[1]/2)
	approximately(t, "bottom y", bottom[1], 0)
	for axis := 0; axis < 3; axis += 2 {
		approximately(t, "x/z", middle[axis], -(minimum[axis]+maximum[axis])/2)
		approximately(t, "x/z", bottom[axis], middle[axis])
	}
}

// The FBX a conversion delivers embeds the picture in the requested format and
// at the requested size (observed: a PNG of 2048 pixels for a PNG/2048 request),
// keeps the skin of a rigged source and moves the origin like the GLB does.
func TestConvertedFBXFileEmbedsTheRequestedTexture(t *testing.T) {
	for name, tc := range map[string]struct {
		spec  ConvertedModelSpec
		magic []byte
	}{
		"PNG at 2048": {ConvertedModelSpec{TextureEdge: 2048, TextureFormat: TextureFormatPNG, Offset: CenterOrigin(true)}, []byte("\x89PNG")},
		"JPEG at 256": {ConvertedModelSpec{TextureEdge: 256, TextureFormat: TextureFormatJPEG}, []byte("\xff\xd8\xff")},
		"a rig":       {ConvertedModelSpec{TextureEdge: 128, TextureFormat: TextureFormatPNG, Skeleton: true}, []byte("\x89PNG")},
	} {
		t.Run(name, func(t *testing.T) {
			file, err := ConvertedFBXFile(tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			if file.MediaType != "model/fbx" || !bytes.HasPrefix(file.Data, []byte("Kaydara FBX Binary")) {
				t.Fatalf("not a binary FBX: %q", file.MediaType)
			}
			start := bytes.Index(file.Data, tc.magic)
			if start < 0 {
				t.Fatal("the picture is not embedded")
			}
			var size image.Config
			var decodeErr error
			if tc.magic[0] == 0x89 {
				size, decodeErr = png.DecodeConfig(bytes.NewReader(file.Data[start:]))
			} else {
				size, decodeErr = jpeg.DecodeConfig(bytes.NewReader(file.Data[start:]))
			}
			if decodeErr != nil || size.Width != tc.spec.TextureEdge {
				t.Fatalf("embedded picture %dx%d (%v), want %d", size.Width, size.Height, decodeErr, tc.spec.TextureEdge)
			}
			if rigged := bytes.Contains(file.Data, []byte("MockRoot")); rigged != tc.spec.Skeleton {
				t.Fatalf("skin present = %v, want %v", rigged, tc.spec.Skeleton)
			}
		})
	}
	if _, err := ConvertedFBXFile(ConvertedModelSpec{TextureEdge: 64, TextureFormat: "WEBP"}); err == nil {
		t.Fatal("a texture the fixture cannot draw was embedded")
	}
	if _, err := ConvertedFBXFile(ConvertedModelSpec{TextureEdge: 64, TextureFormat: TextureFormatPNG, Animation: true}); err == nil {
		t.Fatal("a clip without its skin was accepted")
	}
}

func TestConvertedTextureDrawsOnlyWhatTheFixtureCan(t *testing.T) {
	if data := ConvertedTexture(TextureFormatPNG, 128); !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Fatal("no PNG")
	}
	if data := ConvertedTexture(TextureFormatJPEG, 128); !bytes.HasPrefix(data, []byte("\xff\xd8\xff")) {
		t.Fatal("no JPEG")
	}
	for name, data := range map[string][]byte{
		"WEBP": ConvertedTexture("WEBP", 128), "tiny": ConvertedTexture(TextureFormatPNG, 2),
		"huge": ConvertedTexture(TextureFormatPNG, MaxSizedModelTextureEdge+1), "huge JPEG": ConvertedTexture(TextureFormatJPEG, maxConvertedJPEGEdge+1),
	} {
		if data != nil {
			t.Errorf("%s: a picture was drawn", name)
		}
	}
}

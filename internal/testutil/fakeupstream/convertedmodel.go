package fakeupstream

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/providers/mock/modelfiles"
)

// A model conversion writes a new file: the textures leave in the format and at
// the size the request names, the model keeps what its source had (the skin of a
// rig, the clip of a retargeted rig) and the origin moves. ConvertedModelFile
// builds such a GLB from the same humanoid every fixture uses, so a fake that
// converts a model (Tripo models/convert) delivers bytes whose triangles,
// textures, skin and bounding box are what Plinth reads back; nothing is declared
// that the bytes do not hold.
//
// Observed 2026-11-16 (#305), converting the GLB a Tripo rig returned: the
// skin (78 joints, their order and names, inverse bind matrices, JOINTS_0 and
// WEIGHTS_0) survives, the textures are written in the requested format at the
// requested size, and the model is moved so the middle of its bounding box sits
// at the origin (the output reports normalize_scale and normalize_translation).
// What the fixture adds is constructed: one joint, one base color texture, the
// triangles of the source model.

// Texture formats ConvertedModelFile can draw.
const (
	TextureFormatPNG  = "png"
	TextureFormatJPEG = "jpeg"
)

// maxConvertedJPEGEdge bounds the JPEG textures the fake encodes: the picture is
// drawn per pixel, so the edge is capped well below the PNG one.
const maxConvertedJPEGEdge = 8192

// ConvertedModelSpec describes the GLB a conversion delivers.
type ConvertedModelSpec struct {
	// Triangles is the triangle count of the source model; SizedModelTriangles
	// rounds it as it does for every sized model.
	Triangles int
	// TextureEdge is the square edge, in pixels, of the one base color texture.
	TextureEdge int
	// TextureFormat is TextureFormatPNG or TextureFormatJPEG.
	TextureFormat string
	// Skeleton keeps the one-joint skin of a rigged source, Animation (which
	// needs Skeleton) the two-keyframe clip of an animated one.
	Skeleton  bool
	Animation bool
	// Offset is added to every vertex: the move the conversion applied to the
	// origin (see ModelBounds).
	Offset [3]float64
}

// ModelBounds is the bounding box of the fixture humanoid every GLB of the fake
// is made from: the feet rest on y = 0 and the model is centered on x and z, the
// shape of a Tripo result before a conversion moves it.
func ModelBounds() (minimum, maximum [3]float64) {
	boundsOnce.Do(func() {
		seed := sha256.Sum256([]byte("fixture\x00"))
		mesh := modelfiles.BuildHumanoid(seed, 1)
		fixtureMinimum, fixtureMaximum = [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for index := 0; index < len(mesh.Position); index += 3 {
			for axis := 0; axis < 3; axis++ {
				value := float64(mesh.Position[index+axis])
				fixtureMinimum[axis], fixtureMaximum[axis] = math.Min(fixtureMinimum[axis], value), math.Max(fixtureMaximum[axis], value)
			}
		}
	})
	return fixtureMinimum, fixtureMaximum
}

var (
	boundsOnce                     sync.Once
	fixtureMinimum, fixtureMaximum [3]float64
)

// CenterOrigin is the offset that moves the fixture's bounding box onto the
// origin: the middle of the box (bottomCenter false, what a Tripo conversion did
// without pivot_to_center_bottom) or the middle of its bottom face (true).
func CenterOrigin(bottomCenter bool) [3]float64 {
	minimum, maximum := ModelBounds()
	offset := [3]float64{-(minimum[0] + maximum[0]) / 2, -(minimum[1] + maximum[1]) / 2, -(minimum[2] + maximum[2]) / 2}
	if bottomCenter {
		offset[1] = -minimum[1]
	}
	return offset
}

var convertedModels sync.Map // ConvertedModelSpec -> File

// ConvertedModelFile is the GLB a conversion delivers for spec, built once per
// process and spec.
func ConvertedModelFile(spec ConvertedModelSpec) (File, error) {
	if spec.TextureEdge < minSizedTextureEdge || spec.TextureEdge > MaxSizedModelTextureEdge {
		return File{}, fmt.Errorf("fakeupstream: a converted model texture edge is between %d and %d pixels, got %d",
			minSizedTextureEdge, MaxSizedModelTextureEdge, spec.TextureEdge)
	}
	if spec.Animation && !spec.Skeleton {
		return File{}, fmt.Errorf("fakeupstream: a converted model keeps a clip only with its skin")
	}
	if cached, ok := convertedModels.Load(spec); ok {
		return cached.(File), nil
	}
	var texture []byte
	mime := "image/png"
	switch spec.TextureFormat {
	case TextureFormatPNG:
		texture = sizedTexturePNG(textureBaseColor, spec.TextureEdge)
	case TextureFormatJPEG:
		if spec.TextureEdge > maxConvertedJPEGEdge {
			return File{}, fmt.Errorf("fakeupstream: a JPEG texture is at most %d pixels, got %d", maxConvertedJPEGEdge, spec.TextureEdge)
		}
		texture, mime = sizedTextureJPEG(spec.TextureEdge), "image/jpeg"
	default:
		return File{}, fmt.Errorf("fakeupstream: a converted model texture is %q or %q, got %q", TextureFormatPNG, TextureFormatJPEG, spec.TextureFormat)
	}
	mesh, err := sizedHumanoid(SizedModelTriangles(spec.Triangles))
	if err != nil {
		return File{}, err
	}
	for index := 0; index < len(mesh.Position); index += 3 {
		for axis := 0; axis < 3; axis++ {
			mesh.Position[index+axis] += float32(spec.Offset[axis])
		}
	}
	glb, err := buildGLB(mesh, [][]byte{texture}, glbExtras{imageMime: mime, skeleton: spec.Skeleton, animation: spec.Animation})
	if err != nil {
		return File{}, err
	}
	built, _ := convertedModels.LoadOrStore(spec, File{MediaType: "model/gltf-binary", Data: glb})
	return built.(File), nil
}

var convertedFBXs sync.Map // ConvertedModelSpec -> File

// ConvertedFBXFile is the FBX a conversion delivers for spec: the fixture
// humanoid (spec.Triangles does not apply, the FBX writer cuts no finer than the
// one-step humanoid), moved by spec.Offset, with the base color texture embedded
// in the requested format and size, and the skin (and clip) of a rigged source.
func ConvertedFBXFile(spec ConvertedModelSpec) (File, error) {
	if spec.Animation && !spec.Skeleton {
		return File{}, fmt.Errorf("fakeupstream: a converted model keeps a clip only with its skin")
	}
	spec.Triangles = 0
	if cached, ok := convertedFBXs.Load(spec); ok {
		return cached.(File), nil
	}
	texture := ConvertedTexture(spec.TextureFormat, spec.TextureEdge)
	if texture == nil {
		return File{}, fmt.Errorf("fakeupstream: no %q texture of %d pixels for a converted FBX", spec.TextureFormat, spec.TextureEdge)
	}
	filename := "texture.png"
	if spec.TextureFormat == TextureFormatJPEG {
		filename = "texture.jpg"
	}
	seed := sha256.Sum256([]byte("fixture\x00"))
	mesh := modelfiles.BuildHumanoid(seed, 1)
	for index := 0; index < len(mesh.Position); index += 3 {
		for axis := 0; axis < 3; axis++ {
			mesh.Position[index+axis] += float32(spec.Offset[axis])
		}
	}
	var fidelity []string
	switch {
	case spec.Animation:
		fidelity = []string{"animation"}
	case spec.Skeleton:
		fidelity = []string{"skeleton"}
	}
	built, _ := convertedFBXs.LoadOrStore(spec, File{MediaType: "model/fbx", Data: modelfiles.BuildTexturedFBX(mesh, "FIXTURE", filename, texture, fidelity...)})
	return built.(File), nil
}

// ConvertedTexture is the picture a conversion embeds in an FBX or a GLB: the
// atlas of the fixture at edge pixels in the named format, or nil when the
// fixture cannot draw the format or the edge.
func ConvertedTexture(format string, edge int) []byte {
	if edge < minSizedTextureEdge || edge > MaxSizedModelTextureEdge {
		return nil
	}
	switch format {
	case TextureFormatPNG:
		return sizedTexturePNG(textureBaseColor, edge)
	case TextureFormatJPEG:
		if edge <= maxConvertedJPEGEdge {
			return sizedTextureJPEG(edge)
		}
	}
	return nil
}

var sizedJPEGCache sync.Map // edge -> []byte

// sizedTextureJPEG is the base color atlas as a JPEG of edge x edge pixels, built
// once per process.
func sizedTextureJPEG(edge int) []byte {
	if cached, ok := sizedJPEGCache.Load(edge); ok {
		return cached.([]byte)
	}
	var out bytes.Buffer
	_ = jpeg.Encode(&out, atlasPicture{edge: edge}, &jpeg.Options{Quality: 80})
	built, _ := sizedJPEGCache.LoadOrStore(edge, out.Bytes())
	return built.([]byte)
}

// atlasPicture is the six-tone base color atlas as an image.Image that holds no
// pixels, so a large JPEG is encoded without allocating its raster.
type atlasPicture struct{ edge int }

func (p atlasPicture) ColorModel() color.Model { return color.NRGBAModel }
func (p atlasPicture) Bounds() image.Rectangle { return image.Rect(0, 0, p.edge, p.edge) }
func (p atlasPicture) At(x, y int) color.Color {
	tone := atlasTones[min(y/(p.edge/2), 1)*3+min(x/(p.edge/3), 2)]
	return color.NRGBA{R: tone[0], G: tone[1], B: tone[2], A: tone[3]}
}

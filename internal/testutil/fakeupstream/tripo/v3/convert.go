package tripov3

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The POST /models/convert request of the official API, read from
// https://developers.tripo3d.ai/en/docs/models-convert on 2026-11-12. Before
// that reading the fake accepted any body with input and format, so a request
// that sent a wrong texture format, a non-integer size or an unknown FBX preset
// looked fine against it. Every documented parameter is now checked for its type
// and, where the page lists values, for the value; a refusal is the real
// 400/code 1004 answer.
//
// What the page does not say is not invented: a parameter it never mentions is
// ignored, and the interplay it only hints at (force_symmetry needs quad,
// export_vertex_colors is for OBJ and GLTF) is accepted rather than refused,
// because the page does not say that the API answers an error. The one
// documented refusal is texture_packaging on a format other than FBX ("other
// formats return 400").
//
// Constructed, not captured: the real answer to an invalid value. The committed
// convert sample covers a valid STL export with bake, fbx_preset, texture_format,
// texture_size and with_animation, and shows that the task object echoes those
// parameters under input; the evidence of 2026-11-16 adds a GLTF and an FBX
// export with the same options (see the end of this file for what a conversion
// does to the model).
var (
	convertFormats         = []string{"GLTF", "FBX", "USDZ", "OBJ", "STL", "3MF"}
	convertTextureFormats  = []string{"JPEG", "PNG", "WEBP", "BMP", "DPX", "HDR", "OPEN_EXR", "TARGA", "TIFF"}
	convertOrientations    = []string{"+x", "-x", "-y", "+y"}
	convertFBXPresets      = []string{"blender", "3dsmax", "mixamo", "bake_scale"}
	convertTexturePackings = []string{"embedded", "zip"}
)

// convertParameter is one documented optional parameter: its JSON kind and, for
// a string with a closed value list, the values.
type convertParameter struct {
	kind   string
	values []string
}

var convertParameters = map[string]convertParameter{
	"quad":                     {kind: "boolean"},
	"force_symmetry":           {kind: "boolean"},
	"face_limit":               {kind: "integer"},
	"flatten_bottom":           {kind: "boolean"},
	"flatten_bottom_threshold": {kind: "number"},
	"texture_size":             {kind: "integer"},
	"texture_format":           {kind: "string", values: convertTextureFormats},
	"bake":                     {kind: "boolean"},
	"pack_uv":                  {kind: "boolean"},
	"export_vertex_colors":     {kind: "boolean"},
	"pivot_to_center_bottom":   {kind: "boolean"},
	"scale_factor":             {kind: "number"},
	"with_animation":           {kind: "boolean"},
	"animate_in_place":         {kind: "boolean"},
	"part_names":               {kind: "strings"},
	"export_orientation":       {kind: "string", values: convertOrientations},
	"fbx_preset":               {kind: "string", values: convertFBXPresets},
	"texture_packaging":        {kind: "string", values: convertTexturePackings},
}

// convertRequestError returns what is wrong with a convert request body, or "".
// The body was decoded with UseNumber.
func convertRequestError(body map[string]any) string {
	format, _ := body["format"].(string)
	if !slices.Contains(convertFormats, format) {
		return fmt.Sprintf("format must be one of %v", convertFormats)
	}
	for name, spec := range convertParameters {
		value, present := body[name]
		if !present {
			continue
		}
		if !convertValueValid(spec, value) {
			if len(spec.values) > 0 {
				return fmt.Sprintf("%s must be one of %v", name, spec.values)
			}
			return fmt.Sprintf("%s must be a %s", name, spec.kind)
		}
	}
	if packaging, present := body["texture_packaging"]; present && format != "FBX" {
		return fmt.Sprintf("texture_packaging %v is only valid for the FBX format", packaging)
	}
	return ""
}

func convertValueValid(spec convertParameter, value any) bool {
	switch spec.kind {
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Float64()
		return err == nil
	case "string":
		text, ok := value.(string)
		return ok && (len(spec.values) == 0 || slices.Contains(spec.values, text))
	case "strings":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if _, isText := item.(string); !isText {
				return false
			}
		}
		return true
	}
	return false
}

// convertEcho is what the task object shows under input for a convert task: the
// documented parameters of the request, format included, with the source named
// original_model_id by the caller (committed sample, 2026-10-29).
func convertEcho(body map[string]any) map[string]any {
	echo := map[string]any{"format": body["format"]}
	for name := range convertParameters {
		if value, present := body[name]; present {
			echo[name] = value
		}
	}
	return echo
}

// convertTexture names the texture the exported FBX embeds: the requested
// texture_format, JPEG when the request names none (the official default), and
// nothing for a format the fixtures cannot draw.
func (t *task) convertTexture() string {
	if t.kind.Type != "convert_model" {
		return ""
	}
	switch format, _ := t.convert["texture_format"].(string); format {
	case "", "JPEG":
		return "jpeg"
	case "PNG":
		return "png"
	}
	return ""
}

// convertFormat is the format the export is delivered in: quad forces FBX ("Enabling
// quad will force the output format to FBX").
func (t *task) convertFormat() string {
	if quad, _ := t.convert["quad"].(bool); quad {
		return "FBX"
	}
	format, _ := t.convert["format"].(string)
	return format
}

// What a conversion does to the model (#305). Observed 2026-11-16 on the GLB of a
// v3.0 rig, converted to GLTF and to FBX with PNG textures of 2048 pixels and
// with_animation, without pivot_to_center_bottom:
//
//   - the skin survives (the one skin, its joints in their order, the inverse bind
//     matrices, JOINTS_0 and WEIGHTS_0), and the textures are written in the
//     requested format and at the requested size;
//   - the model is moved so the middle of its bounding box sits on the origin,
//     and the finished task reports the move as output.normalize_scale and
//     output.normalize_translation (the same values for both formats). A
//     conversion to STL, observed on an earlier model, reports neither.
//
// What the page promises but the run did not exercise is constructed: with
// pivot_to_center_bottom the origin is the middle of the bottom face (so the feet
// rest on y = 0 and normalize_translation moves the bottom face, not the middle),
// and a texture larger than the source's is drawn at the requested size.
//
// A conversion that asks for nothing about its output (no texture_size,
// texture_format or pivot_to_center_bottom) is delivered as the fixed fixture it
// always was, so the requests of the generation Offerings keep their bytes.

// convertInt reads an integer parameter of the convert request.
func (t *task) convertInt(name string) (int, bool) {
	number, ok := t.convert[name].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	return int(value), err == nil
}

// convertedModelSpec is the model a conversion of this task writes, and false
// when the request asks for nothing about the output (or for what the fixture
// cannot draw), which leaves the plain fixture.
func (t *task) convertedModelSpec() (fakeupstream.ConvertedModelSpec, bool) {
	if t.kind.Type != "convert_model" {
		return fakeupstream.ConvertedModelSpec{}, false
	}
	size, hasSize := t.convertInt("texture_size")
	format, hasFormat := t.convert["texture_format"].(string)
	pivot, hasPivot := t.convert["pivot_to_center_bottom"].(bool)
	if !hasSize && !hasFormat && !hasPivot {
		return fakeupstream.ConvertedModelSpec{}, false
	}
	if !hasSize {
		size = convertDefaultTextureSize
	}
	if !hasFormat {
		format = convertDefaultTextureFormat
	}
	spec := fakeupstream.ConvertedModelSpec{TextureEdge: size, Offset: fakeupstream.CenterOrigin(pivot), Triangles: t.sourceTriangles}
	switch format {
	case "PNG":
		spec.TextureFormat = fakeupstream.TextureFormatPNG
	case "JPEG":
		spec.TextureFormat = fakeupstream.TextureFormatJPEG
	default:
		return fakeupstream.ConvertedModelSpec{}, false
	}
	if fakeupstream.ConvertedTexture(spec.TextureFormat, size) == nil {
		return fakeupstream.ConvertedModelSpec{}, false
	}
	keepsAnimation := true // the page's default is not read; the committed request names it
	if value, named := t.convert["with_animation"].(bool); named {
		keepsAnimation = value
	}
	spec.Skeleton = t.sourceFidelity != ""
	spec.Animation = t.sourceFidelity == "animation" && keepsAnimation
	return spec, true
}

// normalize adds what a GLTF or FBX conversion reports about the move it made.
func (t *task) normalize(output map[string]any) {
	if t.kind.Type != "convert_model" {
		return
	}
	if format := t.convertFormat(); format != "GLTF" && format != "FBX" {
		return
	}
	pivot, _ := t.convert["pivot_to_center_bottom"].(bool)
	offset := fakeupstream.CenterOrigin(pivot)
	output["normalize_scale"] = json.Number("1")
	output["normalize_translation"] = []float64{offset[0], offset[1], offset[2]}
}

// modelTriangles is the triangle count of the model this task delivers when it is
// not the plain fixture: the low-poly model a decimation builds.
func (t *task) modelTriangles() int {
	if t.isDecimate() && !t.decimatesToQuad() {
		return fakeupstream.LowPolyTriangles(t.decimateTriangles())
	}
	return 0
}

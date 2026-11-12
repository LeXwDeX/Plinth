package tripov3

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The POST /mesh/decimate (Retopology) request of the official API, read from
// https://developers.tripo3d.ai/en/docs/mesh-decimate on 2026-11-12 (#276).
// Before that reading the fake took any body with an input and a model, and any
// task as the source, so a request the real API refuses looked fine against it.
//
// What the page says, and what the fake does with it:
//
//   - input is exactly one of a task id (of "a previous 3D generation task
//     (text_to_model, image_to_model, or multiview_to_model)"), a file token or a
//     public URL. The fake accepts those three task types and refuses any other
//     task (a rig, a conversion, another decimation, a segmentation) with the
//     create envelope of the samples (400/code 1004). The page names no model
//     version of the source task, so a generation task of any model version
//     (a P-series task included) is accepted: the fake cannot tell P2 from H3 by
//     task type and the page does not exclude it either.
//   - model is v3.0, v2.0 (the default when omitted) or v1.0.
//   - face_limit is an integer. v3.0 and v2.0: optional, 500-20,000 for a triangle
//     mesh and 500-10,000 for a quad mesh, adaptive when omitted. v1.0: required,
//     up to 2,000,000 triangles or 150,000 quads.
//   - smart_uv is valid with v3.0 only: "passing it on v1.0 or v2.0 returns 400
//     with error code 1004 rather than being silently ignored". The fake reads
//     "passing it" literally, so a smart_uv of false on v2.0 is refused too.
//   - bake (default true), quad (default false) and part_names are accepted with
//     the documented types; the page says v1.0 does not support bake and
//     part_names but not what happens when they are sent, so the fake does not
//     refuse them there.
//   - quad: "On v3.0 a quad mesh is billed at 40 credits instead of 35" (billing
//     is not modelled). The page's only format line is under v2.0 ("outputs GLB;
//     FBX when quad=true"); it says nothing for v3.0, and the one real v3.0
//     quad=true task delivered a binary FBX (#185), so the fake delivers the FBX
//     for quad=true.
//
// The task object repeats bake, quad and smart_uv under input with their
// documented defaults (bake true, quad false, smart_uv false) when the request
// leaves them out; the committed real sample shows those three next to
// original_model_id and no model or face_limit. That the real API echoes the
// defaults of an omitted field is an assumption.
//
// Constructed, not captured: every refusal below. The committed decimate sample
// is one valid v3.0 request (bake, quad false, smart_uv, no face_limit).
//
// The result is one fixed model, so a "reduction" is not computed: the fake
// builds the low-poly model the request asked for, a textured humanoid with the
// largest triangle count of its fixture that does not pass face_limit
// (fakeupstream.LowPolyModelFile), and a request without face_limit gets the
// adaptive size the real task delivered once (decimateAdaptiveTriangles). It has
// UVs and a baked texture, as a bake=true result has; what bake=false delivers
// is not documented and is not modelled.
var (
	decimateModels = []string{"v3.0", "v2.0", "v1.0"}
	// decimateSourceTypes are the task types the page lists for a task id input.
	decimateSourceTypes = []string{"text_to_model", "image_to_model", "multiview_to_model"}
)

// decimateAdaptiveTriangles is the size an omitted face_limit stands for: the
// triangle count the one real v3.0 decimate task delivered on its own (#185,
// 9,370 triangles for the Tripo H3 text-to-3D source of that run).
const decimateAdaptiveTriangles = 9370

// decimateRequestError returns what is wrong with a mesh/decimate request body,
// or "". The body was decoded with UseNumber.
func decimateRequestError(body map[string]any) string {
	model := "v2.0"
	if value, present := body["model"]; present {
		text, ok := value.(string)
		if !ok || !slices.Contains(decimateModels, text) {
			return fmt.Sprintf("model must be one of %v", decimateModels)
		}
		model = text
	}
	for _, name := range []string{"quad", "bake", "smart_uv"} {
		if value, present := body[name]; present {
			if _, ok := value.(bool); !ok {
				return name + " must be a boolean"
			}
		}
	}
	if _, present := body["smart_uv"]; present && model != "v3.0" {
		return "smart_uv is only valid with model v3.0"
	}
	if value, present := body["part_names"]; present {
		items, ok := value.([]any)
		if !ok {
			return "part_names must be an array of strings"
		}
		for _, item := range items {
			if _, isText := item.(string); !isText {
				return "part_names must be an array of strings"
			}
		}
	}
	quad, _ := body["quad"].(bool)
	value, present := body["face_limit"]
	if !present {
		if model == "v1.0" {
			return "face_limit is required with model v1.0"
		}
		return ""
	}
	number, ok := value.(json.Number)
	if !ok {
		return "face_limit must be an integer"
	}
	faces, err := number.Int64()
	if err != nil {
		return "face_limit must be an integer"
	}
	low, high := decimateFaceRange(model, quad)
	if faces < low || faces > high {
		mesh := "triangle mesh"
		if quad {
			mesh = "quad mesh"
		}
		return fmt.Sprintf("face_limit must be between %d and %d (%s), got %d", low, high, mesh, faces)
	}
	return ""
}

// decimateFaceRange is the face_limit range the page lists for a model and
// topology.
func decimateFaceRange(model string, quad bool) (low, high int64) {
	if model == "v1.0" {
		if quad {
			return 1, 150000
		}
		return 1, 2000000
	}
	if quad {
		return 500, 10000
	}
	return 500, 20000
}

// acceptsDecimateSource reports whether a task can be the input of a decimation.
func acceptsDecimateSource(source *task) bool {
	return slices.Contains(decimateSourceTypes, source.kind.Type)
}

func (t *task) isDecimate() bool { return t.kind.Path == "/mesh/decimate" }

// decimatesToQuad reports a decimation that asked for a quad mesh, which the
// official API delivers as an FBX.
func (t *task) decimatesToQuad() bool {
	quad, _ := t.request["quad"].(bool)
	return t.isDecimate() && quad
}

// decimateTriangles is the triangle count the fake's low-poly model aims at.
func (t *task) decimateTriangles() int {
	if number, ok := t.request["face_limit"].(json.Number); ok {
		if faces, err := number.Int64(); err == nil && faces > 0 {
			return int(faces)
		}
	}
	return decimateAdaptiveTriangles
}

// artifact returns the bytes of one named output file of the task.
func (t *task) artifact(name string) (fakeupstream.File, error) {
	if t.isDecimate() && name == "model.glb" {
		return fakeupstream.LowPolyModelFile(t.decimateTriangles())
	}
	return artifactFile(name, t.fidelity(), t.convertTexture())
}

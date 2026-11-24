package tripov3

import "slices"

// What a task poll repeats of its create request under input, settled by the real
// round of 2026-11-24 (#577, evidence-577-real-round.candidate.json). That round sent
// every parameter Plinth's Offerings declare, and the polls showed each of them as
// sent: a text and an image generation repeated auto_size, generate_parts, quad,
// smart_low_poly, texture_quality, texture_version, orientation and
// texture_alignment next to the keys the 2026-10-29 samples already held, with the
// request's face_limit, geometry_quality, texture and pbr; a texture task its
// texture_quality, pbr, texture_alignment and texture_prompt; a segmentation its
// segmentation_granularity; a completion its completion_mode; a decimation its
// face_limit; a Smart Segmentation of a model its granularity and no hint (the
// request sent none). The 2026-10-29 samples, whose requests left most of those
// out, show what an omitted parameter looks like: the keys of the committed
// template (export_uv, geometry_quality, texture_alignment of a text task, for
// example) with their defaults, and nothing for the others.
//
// So the fake starts from the template's input (the defaults), writes every request
// field over it as sent, and drops the template fields the 2026-11-24 polls showed
// to depend on the request (onlyWhenSent). Fields the poll never shows (the input
// token, which becomes file or original_model_id, and for some kinds the model) are
// skipped. That a field neither round sent is repeated as sent is an assumption.
type requestEcho struct {
	// modelAs is the input key the request's model is shown under; "" when the
	// poll does not show it (a completion and a decimation do not).
	modelAs string
	// skip names request fields the poll never repeats.
	skip []string
	// onlyWhenSent names template fields the poll shows only when the request
	// sends them.
	onlyWhenSent []string
}

var requestEchoes = map[string]requestEcho{
	"text_to_model":       {modelAs: "model_version"},
	"image_to_model":      {modelAs: "model_version", skip: []string{"input"}},
	"texture_model":       {modelAs: "model_version", skip: []string{"input"}},
	"mesh_segmentation":   {modelAs: "model_version", skip: []string{"input"}},
	"mesh_completion":     {skip: []string{"input"}},
	"highpoly_to_lowpoly": {skip: []string{"input"}},
	// The poll shows model_ext (the uploaded file's type) and no seg_type or
	// transform; the image pipeline was not run on 2026-11-24 and keeps its sample.
	"smartsegment_model": {skip: []string{"input", "seg_type", "transform"}, onlyWhenSent: []string{"hint"}},
	// The 2026-10-29 request sent model_seed and its poll showed it; the
	// 2026-11-24 request did not and its poll did not either.
	"image_to_splat": {skip: []string{"input"}, onlyWhenSent: []string{"model_seed"}},
}

// echoRequest rewrites input, which holds the template's, into what the real poll
// of the task's type shows for its request. A type without an entry keeps its
// template input.
func (t *task) echoRequest(input map[string]any) {
	echo, ok := requestEchoes[t.typ]
	if !ok {
		return
	}
	for _, name := range echo.onlyWhenSent {
		if _, sent := t.request[name]; !sent {
			delete(input, name)
		}
	}
	for name, value := range t.request {
		switch {
		case name == "model":
			if echo.modelAs != "" {
				input[echo.modelAs] = value
			}
		case slices.Contains(echo.skip, name):
		default:
			input[name] = value
		}
	}
}

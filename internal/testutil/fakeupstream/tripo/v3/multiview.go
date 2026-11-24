package tripov3

import "slices"

// What a multiview-to-model poll shows under input, observed on the real task of
// 2026-11-19 (#386; evidence-386-multiview-model.candidate.json). The task was
// requested with Plinth's production defaults and three views, and its poll named:
//
//   - every request parameter as sent (texture, pbr, texture_quality,
//     texture_version, texture_alignment, geometry_quality, orientation,
//     face_limit, quad, smart_low_poly, auto_size, export_uv, generate_parts),
//     but not inputs, and the request's model under model_version;
//   - files, an array of four entries in the order front, left, back, right: for a
//     view that was sent {"object": {"bucket": …, "key": …}} (the stored upload),
//     null for one that was not (the fourth entry of that run).
//
// Constructed from the one observation: a parameter the request leaves out is not
// shown (the run sent every parameter, so what the API shows for an omitted one,
// a default perhaps, is not known), and a gap between views is a null in its place
// (the run left out only the last).

// multiviewViews are the view keys of the documented inputs array, in the order
// of the real files array.
var multiviewViews = []string{"front", "left", "back", "right"}

// echoMultiviewInput rewrites input, which holds the sample's, into what the real
// multiview poll shows.
func (t *task) echoMultiviewInput(input map[string]any) {
	for name := range input {
		delete(input, name)
	}
	for name, value := range t.request {
		if name != "model" && name != "inputs" {
			input[name] = value
		}
	}
	if model, present := t.request["model"]; present {
		input["model_version"] = model
	}
	input["files"] = t.multiviewFiles()
}

// multiviewFiles is the files array of the poll: one entry per view of
// multiviewViews, the stored upload for a view the request carries and null for
// the others.
func (t *task) multiviewFiles() []any {
	tokens := map[string]string{}
	inputs, _ := t.request["inputs"].([]any)
	for index, entry := range inputs {
		if token, positional := entry.(string); positional && index < len(multiviewViews) && token != "" {
			tokens[multiviewViews[index]] = token // the legacy positional form (#541)
			continue
		}
		view, _ := entry.(map[string]any)
		for name, value := range view {
			if token, ok := value.(string); ok && slices.Contains(multiviewViews, name) {
				tokens[name] = token
			}
		}
	}
	files := make([]any, 0, len(multiviewViews))
	for _, view := range multiviewViews {
		token, sent := tokens[view]
		if !sent {
			files = append(files, nil)
			continue
		}
		// The real key is the account's storage path (tcli_<account>/<date>/…); the
		// fake's names the upload token and nothing else.
		files = append(files, map[string]any{"object": map[string]any{"bucket": "tripo-data", "key": "fake/" + token}})
	}
	return files
}

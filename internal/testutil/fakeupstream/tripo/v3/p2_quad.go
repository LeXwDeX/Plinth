package tripov3

import "strings"

// P-series quad output (#294). A real P2 task with quad=true
// (2026-11-12) delivered an FBX and no GLB: the one model file it delivered was
// an FBX, which Plinth kept as a model of the role "model" with no variant and
// the media type application/octet-stream, and which neither the stage preview
// nor inspect_model can read. The game character purpose therefore no longer
// turns quad output on.
//
// What the fake does with it: a generation task of a P-series model whose request
// says quad=true names model.fbx for every model URL of its output, so no model
// of the task is a GLB. The task object, the other output URLs and the polls are
// the usual ones. Which output key the real poll uses for the FBX is not part of
// the record; the fake keeps model_url. What is not modelled, because nothing
// real backs it:
//
//   - the topology of the FBX (the fixture is the fixed model of every task);
//   - quad=true on a model of another family (the H series, v3.x): the fake keeps
//     delivering a GLB for it, as it did before the observation;
//   - the multiview endpoint is taken to behave like the text and image ones
//     because they share the model and the page layout.

// isGeneration reports a task made by one of the three generation endpoints.
func (t *task) isGeneration() bool {
	switch t.kind.Path {
	case "/generation/text-to-model", "/generation/image-to-model", "/generation/multiview-to-model":
		return true
	}
	return false
}

// isPSeries reports a request for a P-series model (P2-20260801): a name that is
// the letter P followed by a version digit, which no other family's name is.
func (t *task) isPSeries() bool {
	model, _ := t.request["model"].(string)
	return len(model) >= 2 && strings.HasPrefix(model, "P") && model[1] >= '0' && model[1] <= '9'
}

// generatesQuadFBX reports a P-series generation that asked for quad output,
// which the official API delivers as an FBX and no GLB.
func (t *task) generatesQuadFBX() bool {
	quad, _ := t.request["quad"].(bool)
	return quad && t.isGeneration() && t.isPSeries()
}

package tripov3

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The running polls of a text to model task (#608). One geometry-only v3.0 text
// task of 2026-11-25 (evidence-608-progress.candidate.json), polled every 2
// seconds, answered: a queued create, one queued poll (progress 0, no output
// key), seven running polls with progress 15, 23, 30, 38, 46, 58 and 65, then
// success at 100. The first running poll has output {}; from the second on the
// output already holds generated_image_url (the picture the text step drew),
// and model_url with rendered_image_url arrive only with success, as does
// completed_at. The fake answers its running polls of a text task with that
// progress sequence (the last value repeats when more running polls are
// configured) and that output.
//
// Not observed for this task, so unchanged: the progress of the other task
// types' running polls (one value each in the #577 record, from 3 to 55;
// the fake keeps 50), and whether a running conversion carries model_url (the
// #577 record saw both).
var textRunningProgress = []int{15, 23, 30, 38, 46, 58, 65}

// The same round's queued poll is the running poll without the output key (the
// fake had no queued text sample and answered the queued rig poll, whose input
// is another task's). Its polls also showed delight false and texture_version
// v3.5-20260815, which the request left out; the 2026-10-29 text template
// (v3.1-20260211, the same request otherwise) showed neither. Whether the model
// or the date decides is not known, so the fake adds them for the observed model
// only.
var textOmittedShown = map[string]map[string]any{
	"v3.0-20250812": {"delight": false, "texture_version": "v3.5-20260815"},
}

// queuedTextTemplate is the queued poll of a text task: its running template
// without output.
func (h *handler) queuedTextTemplate() map[string]any {
	running := h.polls["text_to_model"][statusRunning]
	if running == nil {
		return nil
	}
	body := fakeupstream.Clone(running)
	if data, ok := fakeupstream.DigMap(body, "data"); ok {
		delete(data, "output")
	}
	return body
}

// echoOmittedText adds what the observed text poll shows for parameters its
// request left out.
func (t *task) echoOmittedText(input map[string]any) {
	if t.typ != "text_to_model" {
		return
	}
	model, _ := t.request["model"].(string)
	for name, value := range textOmittedShown[model] {
		if _, sent := t.request[name]; !sent {
			input[name] = value
		}
	}
}

// shapeRunning applies the observed running poll of a text task; running is the
// 1-based number of the running poll.
func (h *handler) shapeRunning(r *http.Request, t *task, body map[string]any, running int) {
	if t.typ != "text_to_model" || running < 1 {
		return
	}
	data, ok := fakeupstream.DigMap(body, "data")
	if !ok {
		return
	}
	if _, has := data["progress"]; has {
		data["progress"] = json.Number(strconv.Itoa(textRunningProgress[min(running, len(textRunningProgress))-1]))
	}
	output := map[string]any{}
	if running >= 2 {
		// Not an issue of the task's artifact URLs: the artifact-expired
		// scenario counts the issues of the finished task's URLs only.
		output["generated_image_url"] = h.env.ArtifactOriginFor(r) + Prefix + "/artifacts/" + t.id + "/" + t.artifactName("generated_image")
	}
	data["output"] = output
}

package tripov3

import (
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #541: the three forms of the multiview-to-model inputs (multiview page, read
// 2026-11-24): view-key objects, the legacy positional array of four strings and a
// single task id.
func TestMultiviewInputsTakeTheThreeDocumentedForms(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	source := f.imageMultiview()
	edited := f.create("/generation/edit-multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "front", "prompt": "green"}}}, key)
	f.finish(edited, key)
	pending := f.create("/generation/image-to-multiview", map[string]any{"input": "file_x"}, key) // never polled
	text := f.generated()

	with := func(inputs any) map[string]any { return map[string]any{"model": modelH31, "inputs": inputs} }
	obj := func(view, token string) map[string]any { return map[string]any{view: token} }
	for name, tc := range map[string]struct {
		inputs any
		status int
		code   int
	}{
		"view keys":                      {[]any{obj("front", "f"), obj("back", "b")}, http.StatusOK, 0},
		"view keys, four":                {[]any{obj("front", "f"), obj("left", "l"), obj("back", "b"), obj("right", "r")}, http.StatusOK, 0},
		"positional, all four":           {[]any{"f", "l", "b", "r"}, http.StatusOK, 0},
		"positional with gaps":           {[]any{"f", "", "b", ""}, http.StatusOK, 0},
		"positional without the front":   {[]any{"", "l", "b", "r"}, http.StatusBadRequest, 1004},
		"positional of three":            {[]any{"f", "l", "b"}, http.StatusBadRequest, 1004},
		"positional with one view":       {[]any{"f", "", "", ""}, http.StatusBadRequest, 1004},
		"positional mixed with objects":  {[]any{"f", obj("left", "l"), "b", "r"}, http.StatusBadRequest, 1004},
		"objects mixed with positionals": {[]any{obj("front", "f"), "l"}, http.StatusBadRequest, 1004},
		"task id of an image multiview":  {[]any{map[string]any{"task_id": source}}, http.StatusOK, 0},
		"task id of an edited multiview": {[]any{map[string]any{"task_id": edited}}, http.StatusOK, 0},
		"task id of a text task":         {[]any{map[string]any{"task_id": text}}, http.StatusBadRequest, 1004},
		"task id not yet succeeded":      {[]any{map[string]any{"task_id": pending}}, http.StatusBadRequest, 1004},
		"task id that does not exist":    {[]any{map[string]any{"task_id": "00000000-0000-4000-8000-0000000000aa"}}, http.StatusNotFound, 2001},
		"a single view object":           {[]any{obj("front", "f")}, http.StatusBadRequest, 1004},
	} {
		t.Run(name, func(t *testing.T) {
			f.expect("/generation/multiview-to-model", with(tc.inputs), tc.status, tc.code)
		})
	}
}

// The poll of a positional request shows the same files array as the view-key one.
func TestMultiviewPollShowsThePositionalViews(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	id := f.create("/generation/multiview-to-model", map[string]any{"model": modelH31, "inputs": []any{"f", "", "b", ""}}, key)
	_, done := f.finish(id, key)
	files, _ := field(t, done, "data", "input", "files").([]any)
	if len(files) != 4 || files[0] == nil || files[1] != nil || files[2] == nil || files[3] != nil {
		t.Fatalf("files = %v", files)
	}
}

// #579: a view-key value may be a URL, a file token, or nested {url}, {file_token}
// or {object:{bucket,key}} (multiview page, re-read 2026-11-25). A nested {url} is an
// http(s) URL, the other nested forms carry non-empty strings, and a nested object
// holds exactly one of those keys.
func TestMultiviewViewValuesTakeTheNestedForms(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	with := func(front any) map[string]any {
		return map[string]any{"model": modelH31, "inputs": []any{map[string]any{"front": front}, map[string]any{"back": "file_back"}}}
	}
	stored := map[string]any{"bucket": "b", "key": "k/front.png"}
	for name, tc := range map[string]struct {
		front  any
		status int
	}{
		"a URL":                      {"https://example.test/front.png", http.StatusOK},
		"a file token":               {"file_front", http.StatusOK},
		"nested url":                 {map[string]any{"url": "https://example.test/front.png"}, http.StatusOK},
		"nested file_token":          {map[string]any{"file_token": "file_front"}, http.StatusOK},
		"nested object":              {map[string]any{"object": stored}, http.StatusOK},
		"nested url not a URL":       {map[string]any{"url": "file_front"}, http.StatusBadRequest},
		"nested empty file_token":    {map[string]any{"file_token": " "}, http.StatusBadRequest},
		"nested object without key":  {map[string]any{"object": map[string]any{"bucket": "b"}}, http.StatusBadRequest},
		"nested object, extra field": {map[string]any{"object": map[string]any{"bucket": "b", "key": "k", "x": "y"}}, http.StatusBadRequest},
		"two nested keys":            {map[string]any{"url": "https://example.test/f.png", "file_token": "file_front"}, http.StatusBadRequest},
		"an unknown nested key":      {map[string]any{"token": "file_front"}, http.StatusBadRequest},
		"a number":                   {3, http.StatusBadRequest},
		"an empty string":            {"", http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			f.expect("/generation/multiview-to-model", with(tc.front), tc.status, 1004)
		})
	}

	// The poll's files array names a stored object for every nested form; one sent
	// as {object} is shown as sent.
	id := f.create("/generation/multiview-to-model", map[string]any{"model": modelH31, "inputs": []any{
		map[string]any{"front": map[string]any{"object": stored}},
		map[string]any{"back": map[string]any{"url": "https://example.test/back.png"}},
		map[string]any{"right": map[string]any{"file_token": "file_right"}},
	}}, key)
	_, done := f.finish(id, key)
	files, _ := field(t, done, "data", "input", "files").([]any)
	if len(files) != 4 || files[1] != nil || files[2] == nil || files[3] == nil {
		t.Fatalf("files = %v", files)
	}
	front, _ := files[0].(map[string]any)
	if object, _ := front["object"].(map[string]any); object["bucket"] != "b" || object["key"] != "k/front.png" {
		t.Fatalf("front file = %v, want the object as sent", files[0])
	}
}

package tripov3

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #625: the one real failed edit_multiview task (2026-11-25) was accepted and
// ended "failed" with error_code 1001 "Failed to generate image", unbilled. The
// task-failed scenario ends an edit the same way; other task types keep the
// fake's own words.
func TestTaskFailedEditMultiviewEndsWithTheObservedCodeAndText(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	source := f.imageMultiview()
	failed := string(fakeupstream.ScenarioTaskFailed)
	edit := f.create("/generation/edit-multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "front", "prompt": "make the body plain red"}}},
		key, fakeupstream.ScenarioHeader, failed)
	seen, body := f.finish(edit, key)
	if seen[len(seen)-1] != statusFailed {
		t.Fatalf("statuses = %v", seen)
	}
	data := field(t, body, "data").(map[string]any)
	if data["type"] != "edit_multiview" || data["error_code"] != float64(1001) || data["error_message"] != "Failed to generate image" {
		t.Fatalf("failed edit poll = %v", data)
	}
	if charged, shown := data["credits_consumed"]; shown && charged != float64(0) {
		t.Fatalf("a failed edit is billed %v", charged)
	}

	// The same request without the scenario succeeds (the real retry did).
	retried := f.create("/generation/edit-multiview", map[string]any{"input": source, "prompts": []map[string]any{{"view": "front", "prompt": "make the body plain red"}}}, key)
	if seen, _ := f.finish(retried, key); seen[len(seen)-1] != statusSuccess {
		t.Fatalf("retry statuses = %v", seen)
	}

	text := f.create("/generation/text-to-model", generationBody("/generation/text-to-model", modelH31, nil), key, fakeupstream.ScenarioHeader, failed)
	_, body = f.finish(text, key)
	if data := field(t, body, "data").(map[string]any); data["error_code"] != float64(1001) || data["error_message"] != "Fake injected task failure" {
		t.Fatalf("failed text poll = %v", data)
	}
}

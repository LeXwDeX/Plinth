package wirecapture

import "testing"

// A task stream (Meshy GET /<kind>/{id}/stream, #608) has no [DONE] and no
// terminal event name: the server closes it after the message block of a final
// task state. That block frames the stream; a running state does not.
func TestSSEFramedEndsATaskStreamAtAFinalState(t *testing.T) {
	block := func(status string) string {
		return "event:message\ndata:{\"id\":\"id-1\",\"type\":\"image-to-3d\",\"status\":\"" + status + "\",\"progress\":0}\n\n"
	}
	for _, check := range []struct {
		raw  string
		want bool
	}{
		{block("SUCCEEDED"), true},
		{block("FAILED"), true},
		{block("CANCELED"), true},
		{block("PENDING") + block("IN_PROGRESS") + block("SUCCEEDED"), true},
		{block("PENDING"), false},
		{block("IN_PROGRESS"), false},
		// A partial final block is not a boundary.
		{block("SUCCEEDED")[:40], false},
		{"event:error\ndata:{\"message\":\"Task not found\",\"status_code\":404}\n\n", true},
	} {
		if got := SSEFramed([]byte(check.raw)); got != check.want {
			t.Errorf("SSEFramed(%q) = %v, want %v", check.raw, got, check.want)
		}
	}
	// A final block before another block is a partial stream, not two streams.
	if _, err := parseSSE([]byte(block("SUCCEEDED") + block("SUCCEEDED"))); err == nil {
		t.Error("a block after the final state must not parse")
	}
}

package meshy

import (
	"strings"
	"testing"
)

func TestTaskErrorEvidenceRedactsAndBounds(t *testing.T) {
	var response taskResponse
	response.TaskError = &struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}{Type: "invalid_input", Code: "https://x.test/?token=s", Message: "bad \x00model msy-abcdefghijkl at https://assets.meshy.ai/a.glb?sig=1 " + strings.Repeat("长", 400)}
	code, excerpt := taskErrorEvidence(response)
	if code != "" {
		t.Fatalf("non-token code kept: %q", code)
	}
	for _, leaked := range []string{"https", "msy-abc", "sig=1", "\x00"} {
		if strings.Contains(excerpt, leaked) {
			t.Fatalf("excerpt leaked %q: %q", leaked, excerpt)
		}
	}
	if !strings.HasPrefix(excerpt, "type=invalid_input message=bad") || len([]rune(excerpt)) > 400 {
		t.Fatalf("excerpt shape = %q", excerpt)
	}
	if code, excerpt := taskErrorEvidence(taskResponse{}); code != "" || excerpt != "" {
		t.Fatal("missing task_error must yield no evidence")
	}
}

package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #509: the shipped Schema declares the first-byte timeout, the round bound
// and the retry of model calls, consistent with the request timeout, and the
// text a person reads when the call still failed, in both languages.
func TestDefaultCatalogDeclaresModelCallResilience(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	agent := taskCatalog.Application().Chat.Agent
	if agent.FirstByteTimeoutSeconds != 120 || agent.RoundTimeoutSeconds != 900 || agent.RequestTimeoutSeconds != 600 {
		t.Fatalf("first byte %d, round %d, request %d", agent.FirstByteTimeoutSeconds, agent.RoundTimeoutSeconds, agent.RequestTimeoutSeconds)
	}
	retry := agent.Retry
	if retry == nil || retry.MaxAttempts != 3 || retry.InitialBackoffMillis != 2000 ||
		retry.MaxBackoffMillis != 20000 || retry.MaxRetryAfterSeconds != 60 {
		t.Fatalf("retry = %+v", retry)
	}
	message := taskCatalog.Application().Chat.Presentation.Messages.AgentUpstreamInterrupted
	if !strings.Contains(message, "重新发送") {
		t.Fatalf("message = %q", message)
	}
	texts, err := catalog.NewTextLocalizer(taskCatalog.Application())
	if err != nil {
		t.Fatal(err)
	}
	if english := texts.Text("en", message); english == message || !strings.Contains(english, "send the message again") {
		t.Fatalf("english = %q", english)
	}
	// The values are server-private: the public projection has none of them.
	public, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first_byte_timeout_seconds", "round_timeout_seconds", "max_retry_after_seconds"} {
		if bytes.Contains(public, []byte(key)) {
			t.Errorf("the public projection carries %s", key)
		}
	}
}

func TestModelCallResilienceIsValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	const (
		firstByte  = `"first_byte_timeout_seconds": 120`
		round      = `"round_timeout_seconds": 900`
		attempts   = `"max_attempts": 3`
		initial    = `"initial_backoff_millis": 2000`
		maxBackoff = `"max_backoff_millis": 20000`
		retryAfter = `"max_retry_after_seconds": 60`
	)
	for name, test := range map[string]struct{ old, new, detail string }{
		"first byte negative":           {firstByte, `"first_byte_timeout_seconds": -1`, "first_byte_timeout_seconds"},
		"first byte over the request":   {firstByte, `"first_byte_timeout_seconds": 601`, "first_byte_timeout_seconds"},
		"round negative":                {round, `"round_timeout_seconds": -1`, "round_timeout_seconds"},
		"round under the request":       {round, `"round_timeout_seconds": 599`, "round_timeout_seconds"},
		"no attempt":                    {attempts, `"max_attempts": 0`, "max_attempts"},
		"too many attempts":             {attempts, `"max_attempts": 11`, "max_attempts"},
		"no backoff":                    {initial, `"initial_backoff_millis": 0`, "initial_backoff_millis"},
		"max backoff under the initial": {maxBackoff, `"max_backoff_millis": 1999`, "max_backoff_millis"},
		"no retry-after bound":          {retryAfter, `"max_retry_after_seconds": 0`, "max_retry_after_seconds"},
		"retry-after over the round":    {retryAfter, `"max_retry_after_seconds": 900`, "round_timeout_seconds"},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
	// Each is optional: a Schema without them sends every call once, bounded
	// only by the request timeout.
	changed := bytes.Replace(source, []byte(firstByte+",\n        "+round+",\n"), nil, 1)
	start := bytes.Index(changed, []byte(`        "retry": {`))
	end := bytes.Index(changed[start:], []byte("},\n"))
	if start < 0 || end < 0 || bytes.Equal(changed, source) {
		t.Fatal("fixture not found")
	}
	changed = append(changed[:start:start], changed[start+end+len("},\n"):]...)
	if _, err := catalog.Load(bytes.NewReader(changed)); err != nil {
		t.Fatalf("without resilience: %v", err)
	}
}

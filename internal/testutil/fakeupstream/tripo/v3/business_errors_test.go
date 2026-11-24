package tripov3

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #541: the rate-limit headers and the business errors of the error-handling page
// the fake can inject (business errors: fake.go businessErrors).

// X-RateLimit-Reset is "Unix timestamp, in seconds, when the rate limit window
// resets" (rate-limits page, read 2026-11-24): the fake wrote the relative seconds
// there before #541, which a reader of the documented timestamp never saw as a wait.
func TestRateLimitResetIsAUnixTimestamp(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	f := newFixture(t, fakeupstream.Config{Now: func() time.Time { return now }, RetryAfter: 7 * time.Second})
	status, header, body := f.do("POST", "/generation/text-to-model", "fake-rate-limit", textBody())
	if status != http.StatusTooManyRequests || int(field(t, body, "code").(float64)) != 1007 {
		t.Fatalf("%d %v", status, body)
	}
	reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		t.Fatalf("X-RateLimit-Reset = %q: %v", header.Get("X-RateLimit-Reset"), err)
	}
	if want := now.Add(7 * time.Second).Unix(); reset != want {
		t.Fatalf("X-RateLimit-Reset = %d, want the Unix time %d at which the window ends", reset, want)
	}
	if header.Get("Retry-After") != "7" || header.Get("X-RateLimit-Limit") == "" || header.Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("headers = %v", header)
	}
}

// Only the reset header and no Retry-After: the case the Adapter's reset-header
// fallback is for (#487). Both the header form and the key form select it.
func TestRateLimitWithOnlyTheResetHeader(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	f := newFixture(t, fakeupstream.Config{Now: func() time.Time { return now }, RetryAfter: 5 * time.Second})
	for name, tc := range map[string]struct {
		key     string
		headers []string
	}{
		"header form": {key, []string{"X-Fake-Scenario", "rate-limit-reset-only"}},
		"key form":    {"fake-rate-limit-reset-only", nil},
	} {
		t.Run(name, func(t *testing.T) {
			status, header, body := f.do("POST", "/generation/text-to-model", tc.key, textBody(), tc.headers...)
			if status != http.StatusTooManyRequests || int(field(t, body, "code").(float64)) != 1007 {
				t.Fatalf("%d %v", status, body)
			}
			if header.Get("Retry-After") != "" {
				t.Fatalf("Retry-After = %q, want none", header.Get("Retry-After"))
			}
			if got, want := header.Get("X-RateLimit-Reset"), strconv.FormatInt(now.Add(5*time.Second).Unix(), 10); got != want {
				t.Fatalf("X-RateLimit-Reset = %q, want %q", got, want)
			}
			if header.Get("X-RateLimit-Limit") == "" || header.Get("X-RateLimit-Remaining") == "" {
				t.Fatalf("headers = %v", header)
			}
		})
	}
}

// The business errors of the error-handling page: a task creation refused with the
// page's code, HTTP status and message. Nothing is created, so an accepted request
// right after still works.
func TestBusinessErrorScenariosRefuseTaskCreation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: 1})
	for _, tc := range []struct {
		word    string
		status  int
		code    int
		message string
	}{
		{"content-policy", http.StatusBadRequest, 2008, "The input is rejected due to violation of content policy"},
		{"version-deprecated", http.StatusBadRequest, 2015, "The version has been deprecated, please try higher version"},
		{"model-too-complex", http.StatusBadRequest, 2018, "The model is too complex to remesh"},
		{"task-type-unsupported", http.StatusBadRequest, 2002, "This task type is not supported"},
		{"endpoint-not-found", http.StatusNotFound, 4001, "No endpoint found"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			for name, send := range map[string]func() (int, map[string]any){
				"header": func() (int, map[string]any) {
					status, _, body := f.do("POST", "/generation/text-to-model", key, textBody(), "X-Fake-Scenario", tc.word)
					return status, body
				},
				"key": func() (int, map[string]any) {
					status, _, body := f.do("POST", "/mesh/decimate", "fake-"+tc.word, map[string]any{"input": "file_x", "model": "v3.0"})
					return status, body
				},
			} {
				status, body := send()
				if status != tc.status || int(field(t, body, "code").(float64)) != tc.code || field(t, body, "message") != tc.message {
					t.Fatalf("%s: %d %v", name, status, body)
				}
				if field(t, body, "status") != "error" {
					t.Fatalf("%s: not the error envelope: %v", name, body)
				}
			}
		})
	}
	// The pool is one task wide and none of the refusals took the slot.
	if status, _, body := f.do("POST", "/generation/text-to-model", key, textBody()); status != http.StatusOK {
		t.Fatalf("a request after the refusals = %d %v", status, body)
	}
}

// A task-level failure: the task is created and a poll ends "failed" with the business
// code as its error_code.
func TestTaskFailedScenariosEndWithABusinessErrorCode(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	for word, code := range map[string]int{
		"task-failed-content-policy":        2008,
		"task-failed-model-too-complex":     2018,
		"task-failed-version-deprecated":    2015,
		"task-failed-task-type-unsupported": 2002,
	} {
		t.Run(word, func(t *testing.T) {
			for name, tc := range map[string]struct {
				key     string
				headers []string
			}{"header": {key, []string{"X-Fake-Scenario", word}}, "key": {"fake-" + word, nil}} {
				id := f.create("/generation/text-to-model", textBody(), tc.key, tc.headers...)
				statuses, done := f.finish(id, key)
				if statuses[len(statuses)-1] != statusFailed || int(field(t, done, "data", "error_code").(float64)) != code {
					t.Fatalf("%s: %v %v", name, statuses, done)
				}
				if message, _ := field(t, done, "data", "error_message").(string); message == "" {
					t.Fatalf("%s: no error_message: %v", name, done)
				}
				// The failed template is the retarget's (the real text_to_model failure was
				// never captured) and may not show credits_consumed; when it does, 0.
				if charged, shown := fakeupstream.Dig(done, "data", "credits_consumed"); shown && charged.(float64) != 0 {
					t.Fatalf("%s: a failed task was charged: %v", name, done)
				}
			}
		})
	}
	// The plain shared word keeps its generic server-side code.
	id := f.create("/generation/text-to-model", textBody(), "fake-task-failed")
	_, done := f.finish(id, key)
	if int(field(t, done, "data", "error_code").(float64)) != 1001 {
		t.Fatalf("task-failed = %v", done)
	}
}

// The credit refusal and the upload refusals carry the error page's own messages.
func TestErrorMessagesAreTheErrorPagesOwn(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, _, body := f.do("POST", "/generation/text-to-model", key, textBody(), "X-Fake-Scenario", "insufficient-balance")
	if field(t, body, "message") != "You don't have enough credit to create this task" {
		t.Fatalf("2010 message = %v", body)
	}
	status, _, unsupported := f.upload("model.exe", "application/octet-stream", []byte("MZ"), key)
	if status != http.StatusBadRequest || int(field(t, unsupported, "code").(float64)) != 2004 || field(t, unsupported, "message") != "This image file type is not supported" {
		t.Fatalf("2004 = %d %v", status, unsupported)
	}
}

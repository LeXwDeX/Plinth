package diagbundle

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// What the scan flags, the scrubber must have replaced: each input below trips
// a rule of the independent scan, and after Text it trips none and no longer
// holds the sensitive part. The scan stays the judge; these cases are the
// reason it rarely has to drop a line.
func TestTextReplacesEverythingTheScanWouldFlag(t *testing.T) {
	long := strings.Repeat("abcdefghij", 25)
	mixed := "Aa1" + strings.Repeat("xyz", 30)
	// Replacements the scrubber makes on its own account: the scan has no rule
	// for a public address or for the shape of a user ID.
	ownPolicy := map[string]bool{"public ipv4": true, "user id shape": true}
	for _, tc := range []struct {
		name, input string
		gone        []string
		placeholder string
	}{
		{"bearer header", "request failed: Authorization: Bearer abcdefghijklmnop1234 rejected", []string{"abcdefghijklmnop1234"}, "[redacted]"},
		{"basic header", "Authorization: Basic dXNlcjpwYXNzd29yZA== rejected", []string{"dXNlcjpwYXNzd29yZA"}, "[redacted]"},
		{"provider key", "key sk-abcdefghijklmnopqrstuvwx was refused", []string{"sk-abcdefghijklmnopqrstuvwx"}, "[redacted]"},
		{"aws key", "using AKIAABCDEFGHIJKLMNOP now", []string{"AKIAABCDEFGHIJKLMNOP"}, "[redacted]"},
		{"jwt", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdef", []string{"eyJhbGciOiJIUzI1NiJ9"}, "[redacted]"},
		{"github token", "pushed with ghp_abcdefghijklmnop0123456789", []string{"ghp_abcdefghijklmnop0123456789"}, "[redacted]"},
		{"slack token", "hook xoxb-1234567890-abcdefghij", []string{"xoxb-1234567890-abcdefghij"}, "[redacted]"},
		// Written in two pieces: the public-tree scan refuses the header as one literal.
		{"private key", "-----BEGIN RSA " + "PRIVATE KEY----- MIIE", []string{"BEGIN RSA " + "PRIVATE KEY"}, "[redacted]"},
		{"signed url", "GET https://cdn.vendor.example/a/b.glb?X-Amz-Signature=deadbeefcafe&X-Amz-Credential=abc failed",
			[]string{"deadbeefcafe", "cdn.vendor.example", "X-Amz-Credential"}, "[url]"},
		{"url with user info", "dial https://user:hunter2@host.vendor.example/x", []string{"hunter2", "host.vendor.example"}, "[url]"},
		{"bare signature parameter", "retry with signature=abcdef0123 and expires=1700000000 and token=xyz987", []string{"abcdef0123", "xyz987"}, "[redacted]"},
		{"already redacted assignment", "api_key=[redacted] token=[redacted]", []string{"api_key=", "token="}, "[redacted]"},
		{"email", "account alice.o'neil+test@corp-mail.example is over quota", []string{"alice", "corp-mail.example"}, "[email]"},
		{"private ipv4", "dial tcp 10.1.2.3:5432: connection refused", []string{"10.1.2.3"}, "[ip]"},
		{"loopback", "listen 127.0.0.1:13333 and 0.0.0.0:13333", []string{"127.0.0.1", "0.0.0.0"}, "[ip]"},
		{"public ipv4", "upstream resolved to 203.0.113.9", []string{"203.0.113.9"}, "[ip]"},
		{"bracketed ipv6", "dial tcp [fd00::1]:443: timeout", []string{"fd00::1"}, "[ip]"},
		{"bare ipv6", "peer fe80::1 gone", []string{"fe80::1"}, "[ip]"},
		{"hex integer address", "target 0x7f000001 refused", []string{"0x7f000001"}, "[ip]"},
		{"octal dotted address", "target 010.0.0.1 refused", []string{"010.0.0.1"}, "[ip]"},
		{"decimal that reads as an address", "estimated 10.01 credits", []string{"10.01"}, "[ip]"},
		{"private host name", "lookup postgres.internal: no such host; also store.corp and svc.local", []string{"postgres.internal", "store.corp", "svc.local"}, "[host]"},
		{"localhost", "http localhost:3000 refused", []string{"localhost"}, "[host]"},
		{"uuid", "job 123e4567-e89b-12d3-a456-426614174000 not found", []string{"123e4567-e89b-12d3-a456-426614174000"}, "id-"},
		{"user id shape", "quota for 4f3c2a1b4f3c2a1b4f3c2a1b4f3c2a1b exceeded", []string{"4f3c2a1b4f3c2a1b4f3c2a1b4f3c2a1b"}, "id-"},
		{"long blob", "body " + long + " end", []string{long}, "[blob]"},
		{"mixed case blob", "body " + mixed + " end", []string{mixed}, "[blob]"},
		{"data uri", "image data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB rejected", []string{"iVBORw0KGgo"}, "[blob]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if findings := privacyscan.ScanText("input", "$", "", tc.input); len(findings) == 0 && !ownPolicy[tc.name] {
				t.Fatalf("the input trips no rule, so the case proves nothing: %q", tc.input)
			}
			got := NewScrubber().Text(tc.input)
			if findings := privacyscan.ScanText("output", "$", "", got); len(findings) != 0 {
				t.Fatalf("scrubbed text still trips the scan: %v in %q", findings, got)
			}
			for _, gone := range tc.gone {
				if strings.Contains(got, gone) {
					t.Errorf("%q survived in %q", gone, got)
				}
			}
			if !strings.Contains(got, tc.placeholder) {
				t.Errorf("no %q in %q", tc.placeholder, got)
			}
		})
	}
}

// Diagnosis needs the words: the scrubber must leave ordinary log text, time
// stamps, identifiers of tasks and requests, hashes and numbers as they are.
func TestTextKeepsWhatDiagnosisNeeds(t *testing.T) {
	for _, keep := range []string{
		"decode response: invalid character 'x' looking for beginning of value",
		"context deadline exceeded (Client.Timeout exceeded while awaiting headers)",
		"HTTP request", "任务提交失败：上游返回 402", "meshy/meshy-7/text-to-3d", "hyper3d/rodin-gen-2.5/text-to-3d",
		"req_ab12cd34ef567890", "task_AbCdEfGhIjKlMnOp", "retry after 30s, attempt 3 of 5",
		"next at 2026-11-16T10:00:00.123456789Z", "started 2026-11-16T10:00:00.123456789+08:00", "elapsed 1.5s cost 0.05 ratio 12.5%",
		"go1.27.4 v1.2.3 1.0.0", "sha256 " + strings.Repeat("ab12", 16), "/api/v1/tasks/{taskID}/assets", "status=402 code=insufficient_balance",
		"max_tokens=4096 token_count=12", "format glb, quad, 30000 polygons",
	} {
		if got := NewScrubber().Text(keep); got != keep {
			t.Errorf("changed %q into %q", keep, got)
		}
	}
}

func TestScrubIsIdempotentAndBoundsEveryValue(t *testing.T) {
	scrubber := NewScrubber()
	for _, input := range []string{
		"Bearer abcdefghijklmnop1234 at https://x.vendor.example/a?b=c from 10.0.0.1 by bob@corp-mail.example",
		"plain text with nothing to hide",
	} {
		once := scrubber.Text(input)
		if twice := scrubber.Text(once); twice != once {
			t.Errorf("not idempotent: %q then %q", once, twice)
		}
	}
	long := scrubber.Text(strings.Repeat("语", 2000))
	if len(long) > maxValueBytes+len("…") || !utf8.ValidString(long) || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long value is cut on a character boundary: %d bytes, valid=%v", len(long), utf8.ValidString(long))
	}
	if got := scrubber.Text("bad \xff\xfe bytes"); !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8 survived: %q", got)
	}
}

// Which key names are credentials or identities is the scan's decision, asked
// through a probe: the scrubber carries no list of its own for them.
func TestKeysAreClassifiedByTheScanItself(t *testing.T) {
	scrubber := NewScrubber()
	for _, tc := range []struct{ key, value, want string }{
		{"authorization", "Bearer abcdefghijklmnop", "[redacted]"},
		{"api_key", "real-looking-value-123", "[redacted]"},
		{"password", "hunter2hunter2", "[redacted]"},
		{"session_id", "sess_AbCdEfGhIjKlMnOp", "[redacted]"},
		{"X-Signature", "abc", "[redacted]"},
		{"prompt", "a tiny dragon wearing a red hat", "[redacted]"},
		{"negative_prompt", "blurry", "[redacted]"},
		{"owner_id", "owner-1", "id-1"},
		{"owner_id", "owner-1", "id-1"},
		{"username", "alice", "id-2"},
		{"email", "alice@corp-mail.example", "id-3"},
		{"actor_id", "approver-77", "id-4"},
		{"task_id", "task_AbCdEfGhIjKlMnOp", "task_AbCdEfGhIjKlMnOp"},
		{"request_id", "req_ab12cd34ef567890", "req_ab12cd34ef567890"},
		{"offering_id", "meshy/meshy-7/text-to-3d", "meshy/meshy-7/text-to-3d"},
		{"owner_id", "[redacted]", "[redacted]"},
		{"owner_id", "id-9", "id-9"},
		{"empty", "", ""},
	} {
		if got := scrubber.Leaf(tc.key, tc.value); got != tc.want {
			t.Errorf("%s=%q became %q, want %q", tc.key, tc.value, got, tc.want)
		}
	}
}

func TestValueScrubsNestedStructuresAndNumbersUnderIdentityKeys(t *testing.T) {
	scrubber := NewScrubber()
	var input map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{
		"owner_id": 12345,
		"req": {"authorization": "Bearer abcdefghijklmnop", "path": "/api/v1/tasks", "members": ["bob@corp-mail.example", "ok"]},
		"count": 7, "ok": true, "none": null,
		"detail": "see https://api.vendor.example/x?token=abc"
	}`))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(scrubber.Value("", input))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, gone := range []string{"12345", "abcdefghijklmnop", "bob@", "api.vendor.example", "token=abc"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q survived: %s", gone, text)
		}
	}
	for _, kept := range []string{`"owner_id":"id-1"`, `"path":"/api/v1/tasks"`, `"count":7`, `"ok":true`, `"none":null`, "[email]", "[url]"} {
		if !strings.Contains(text, kept) {
			t.Errorf("%q lost: %s", kept, text)
		}
	}
	if findings := privacyscan.ScanJSON("value", encoded); len(findings) != 0 {
		t.Fatalf("scrubbed value trips the scan: %v", findings)
	}
}

func TestRegisteredIdentitiesAndPromptsAreReplacedInFreeText(t *testing.T) {
	scrubber := NewScrubber()
	scrubber.AddIdentity("user-accounting-77")
	scrubber.AddIdentity("alice.example@corp-mail.example")
	scrubber.AddIdentity("bob") // shorter than minIdentityBytes: only its key is replaced
	scrubber.AddPrivate("a tiny dragon wearing a red hat")
	scrubber.AddPrivate("hat")         // too short to be a prompt echo
	scrubber.AddIdentity("[redacted]") // placeholders are not identities
	scrubber.AddIdentity("id-4")       // nor are pseudonyms
	text := scrubber.Text("quota of user-accounting-77 and alice.example@corp-mail.example; bob typed: a tiny dragon wearing a red hat; hat")
	for _, gone := range []string{"user-accounting-77", "alice.example", "a tiny dragon wearing a red hat"} {
		if strings.Contains(text, gone) {
			t.Errorf("%q survived: %s", gone, text)
		}
	}
	if !strings.Contains(text, "bob") || !strings.Contains(text, "[input]") || !strings.Contains(text, "; hat") {
		t.Errorf("only long identities and real prompts are replaced: %s", text)
	}
	// The same identity gets the same placeholder in every place it is found.
	again := scrubber.Text("user-accounting-77")
	if again != scrubber.Leaf("owner_id", "user-accounting-77") || !strings.HasPrefix(again, "id-") {
		t.Errorf("placeholder differs between text and key: %q", again)
	}
	// Leaks is the check behind the replacement.
	if rule := scrubber.Leaks([]byte(`{"x":"user-accounting-77"}`)); rule != "known-identity" {
		t.Errorf("a known identity went unnoticed: %q", rule)
	}
	if rule := scrubber.Leaks([]byte(`{"x":"typed: a tiny dragon wearing a red hat"}`)); rule != "known-private-text" {
		t.Errorf("a known prompt went unnoticed: %q", rule)
	}
	// A prompt with a quote or newline is checked in its JSON form too.
	scrubber.AddPrivate("line one\nline \"two\" of a prompt")
	if rule := scrubber.Leaks([]byte(`{"x":"line one\nline \"two\" of a prompt"}`)); rule != "known-private-text" {
		t.Errorf("an escaped prompt went unnoticed: %q", rule)
	}
	if rule := scrubber.Leaks([]byte(`{"x":"nothing of the kind"}`)); rule != "" {
		t.Errorf("false alarm: %q", rule)
	}
	// What the scrubber produced holds nothing it knows.
	if rule := scrubber.Leaks([]byte(scrubber.Text("bob typed: a tiny dragon wearing a red hat; user-accounting-77"))); rule != "" {
		t.Errorf("scrubbed text leaks %q", rule)
	}
}

// Identities seen under their keys anywhere are collected first, so the text
// that mentions them earlier in the same bundle is already covered.
func TestCollectRegistersIdentitiesFromEveryNestedKey(t *testing.T) {
	scrubber := NewScrubber()
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"msg":"x","detail":{"owner_id":"owner-long-identity-1","list":[{"email":"carol@corp-mail.example"}],"n":{"user_id":987654321}}}`))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	scrubber.Collect("", object)
	text := scrubber.Text("seen owner-long-identity-1 and 987654321 and carol@corp-mail.example")
	if strings.Contains(text, "owner-long-identity-1") || strings.Contains(text, "carol") || strings.Contains(text, "987654321") {
		t.Fatalf("a collected identity survived: %s", text)
	}
}

func TestLegacyAddressDecisionMatchesTheScan(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"010.0.0.1", true}, {"10.01", true}, {"10.00", true}, {"127.01", true}, {"0x7f.1", true}, {"0177.1", true},
		{"00.123", false}, {"0.05", false}, {"1.2.3", false}, {"1.5", false}, {"3.05", false}, {"2026.11", false},
	} {
		if got := legacyAddress(tc.text); got != tc.want {
			t.Errorf("legacyAddress(%q) = %v, want %v", tc.text, got, tc.want)
		}
		// The decision is the scan's own: compare with what it reports.
		flagged := privacyscan.HasRule(privacyscan.ScanText("t", "$", "", " "+tc.text+" "), "private-ip")
		if flagged != tc.want {
			t.Errorf("the scan flags %q = %v but the scrubber decides %v", tc.text, flagged, tc.want)
		}
	}
}

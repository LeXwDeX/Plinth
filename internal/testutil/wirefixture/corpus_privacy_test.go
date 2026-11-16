package wirefixture_test

// Privacy scan of the committed wire corpus (#70). The sanitizer is the first
// line of defence; the independent second one is internal/wirecapture/privacyscan
// (its rules moved there in #233 so the application's recorder output, the
// recorder tests and the diagnostics export run the same scan). This file
// re-reads every committed candidate/meta/policy/evidence file with it, so a
// sanitizer regression, a hand-edited sample or a policy that registers a leak
// is caught at review time. Findings carry file, JSON path and rule name only,
// never the matched value. Negative controls prove each rule fires and that
// Validate/WriteCandidate refuse the same injected content.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

const corpusMinCandidates = 3 // agent-flow, meshy, hyper3d; guards against a vacuous pass

// finding deliberately has no value field.
type finding = privacyscan.Finding

func scanCandidate(file string, exchanges []wirefixture.Exchange, policy wirefixture.Policy) []finding {
	return privacyscan.ScanExchanges(file, exchanges, policy)
}

// scanPolicy checks what the policy itself would let through.
func scanPolicy(file string, policy wirefixture.Policy) []finding {
	return privacyscan.ScanPolicy(file, policy)
}

func scanGenericJSON(file string, raw []byte) []finding { return privacyscan.ScanJSON(file, raw) }

func scanEvidence(file string, raw []byte) []finding { return privacyscan.ScanEvidence(file, raw) }

func hasRule(findings []finding, rule string) bool { return privacyscan.HasRule(findings, rule) }

// compactJSONBodies undoes pretty-printing; Validate needs canonical bodies.
func compactJSONBodies(exchanges []wirefixture.Exchange) []wirefixture.Exchange {
	out := append([]wirefixture.Exchange(nil), exchanges...)
	for i := range out {
		for _, body := range []*wirefixture.Payload{&out[i].Request.Body, &out[i].Response.Body} {
			var compact bytes.Buffer
			if len(body.JSON) > 0 && json.Compact(&compact, body.JSON) == nil {
				body.JSON = append(json.RawMessage(nil), compact.Bytes()...)
			}
		}
	}
	return out
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// corpusFiles lists wire sample files: tracked ones via git, otherwise (no git
// metadata, e.g. a source archive) every matching file in the tree.
func corpusFiles(t *testing.T, root string) []string {
	t.Helper()
	match := func(path string) bool {
		slash := filepath.ToSlash(path)
		if !strings.Contains(slash, "/testdata/") || !strings.HasSuffix(slash, ".json") {
			return false
		}
		base := filepath.Base(slash)
		return strings.HasSuffix(base, ".candidate.json") || strings.HasSuffix(base, ".meta.json") ||
			strings.HasSuffix(base, ".policy.json") || strings.HasSuffix(base, ".evidence.json") ||
			base == "wire-coverage.generated.json"
	}
	var files []string
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	if out, err := cmd.Output(); err == nil {
		for _, name := range strings.Split(string(out), "\x00") {
			if name != "" && match(name) {
				files = append(files, name)
			}
		}
	} else {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil && !d.IsDir() && match(rel) {
				files = append(files, rel)
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

func TestCommittedWireCorpusHasNoPrivateData(t *testing.T) {
	root := repoRoot(t)
	files := corpusFiles(t, root)
	candidates := 0
	var findings []finding
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: read failed", rel)
			continue
		}
		switch {
		case strings.HasSuffix(rel, ".candidate.json"):
			candidates++
			policyRel := strings.TrimSuffix(rel, ".candidate.json") + ".policy.json"
			policyRaw, err := os.ReadFile(filepath.Join(root, policyRel))
			if err != nil {
				t.Errorf("%s: sibling policy is missing", rel)
				continue
			}
			var policy wirefixture.Policy
			var exchanges []wirefixture.Exchange
			if decodeStrict(policyRaw, &policy) != nil || decodeStrict(raw, &exchanges) != nil {
				t.Errorf("%s: not in wirefixture format (unknown field or bad JSON)", rel)
				continue
			}
			findings = append(findings, scanCandidate(rel, exchanges, policy)...)
			if err := wirefixture.Validate(compactJSONBodies(exchanges), policy); err != nil {
				t.Errorf("%s: wirefixture.Validate rejects the committed sample (%v)", rel, errors.Unwrap(err))
			}
		case strings.HasSuffix(rel, ".policy.json"):
			var policy wirefixture.Policy
			if decodeStrict(raw, &policy) != nil {
				t.Errorf("%s: not a wirefixture policy", rel)
				continue
			}
			findings = append(findings, scanPolicy(rel, policy)...)
		case strings.HasSuffix(rel, ".evidence.json"):
			findings = append(findings, scanEvidence(rel, raw)...)
		default: // meta, coverage matrix
			findings = append(findings, scanGenericJSON(rel, raw)...)
		}
	}
	if candidates < corpusMinCandidates {
		t.Fatalf("found %d committed candidate sets, want at least %d", candidates, corpusMinCandidates)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if !seen[f.String()] {
			seen[f.String()] = true
			t.Errorf("privacy finding: %s", f)
		}
	}
}

// ---- negative controls ----------------------------------------------------

type privacyCase struct {
	name string
	key  string // JSON field holding value; empty uses "message"
	text string
	rule string
	// unregisteredOnly cases are only unsafe while the policy does not approve
	// the value (prompts, long text); everything else must fail even when the
	// policy registers the exact value.
	unregisteredOnly bool
}

func privacyCases() []privacyCase {
	return []privacyCase{
		{name: "bearer token", text: "Bearer abcdefghijklmnop1234", rule: "credential-value"},
		{name: "sk key", text: "sk-abcdefghijklmnopqrstuv", rule: "credential-value"},
		{name: "aws access key", text: "AKIAABCDEFGHIJKLMNOP", rule: "credential-value"},
		{name: "jwt", text: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghij", rule: "credential-value"},
		{name: "api_key field", key: "api_key", text: "real-looking-value-123", rule: "credential-field"},
		{name: "authorization field", key: "authorization", text: "real-looking-value-123", rule: "credential-field"},
		{name: "x-api-key field", key: "x-api-key", text: "real-looking-value-123", rule: "credential-field"},
		{name: "token field", key: "token", text: "abc123", rule: "credential-field"},
		{name: "secret field", key: "client_secret", text: "abc123", rule: "credential-field"},
		{name: "password field", key: "password", text: "hunter2", rule: "credential-field"},
		{name: "10/8 address", text: "upstream at 10.1.2.3 failed", rule: "private-ip"},
		{name: "172.16/12 address", text: "upstream at 172.16.5.4 failed", rule: "private-ip"},
		{name: "192.168/16 address", text: "upstream at 192." + "168.0.9 failed", rule: "private-ip"},
		{name: "link-local address", text: "http metadata 169.254.169.254", rule: "private-ip"},
		{name: "loopback address", text: "dial 127.0.0.1", rule: "private-ip"},
		{name: "unique-local IPv6", text: "dial fd00::1", rule: "private-ip"},
		{name: ".internal host", text: "db.internal refused", rule: "private-hostname"},
		{name: ".local host", text: "nas.local refused", rule: "private-hostname"},
		{name: ".lan host", text: "box.lan refused", rule: "private-hostname"},
		{name: "localhost", text: "localhost refused", rule: "private-hostname"},
		{name: "URL with query", text: "https://example.test/file?id=1", rule: "url-query"},
		{name: "signed URL", text: "https://example.test/file?X-Amz-Signature=abc", rule: "url-query"},
		{name: "signature text", text: "Signature=abcdef", rule: "signed-url"},
		{name: "expires text", text: "Expires=1700000000", rule: "signed-url"},
		{name: "URL with userinfo", text: "https://u:p@example.test/file", rule: "url-userinfo"},
		{name: "URL with fragment", text: "https://example.test/file#frag", rule: "url-fragment"},
		{name: "URL on a real host", text: "https://cdn.vendor.com/file", rule: "url-host"},
		{name: "email", text: "contact alice@corp-mail.com", rule: "email"},
		{name: "user_id", key: "user_id", text: "u_8841", rule: "identity-field"},
		{name: "owner", key: "owner", text: "alice", rule: "identity-field"},
		{name: "uid", key: "uid", text: "8841", rule: "identity-field"},
		{name: "session_id", key: "session_id", text: "sess-123", rule: "credential-field"},
		{name: "raw UUID", text: "task 123e4567-e89b-12d3-a456-426614174000", rule: "raw-identifier"},
		{name: "unregistered prompt", key: "prompt", text: "my private business plan", rule: "unregistered-prompt", unregisteredOnly: true},
		{name: "unregistered long text", text: strings.Repeat("lorem ipsum ", 20), rule: "unregistered-long-text", unregisteredOnly: true},
		{name: "base64 blob", text: strings.Repeat("QUJDREVGR0g", 40), rule: "embedded-binary"},
		{name: "data URI", text: "data:image/png;base64,AAAA", rule: "embedded-binary"},
		// Encoded forms (#90). Private literals are built from parts.
		{name: "percent-encoded private IP", text: "host " + pctAll(privateIP()), rule: "private-ip"},
		{name: "double percent-encoded private IP", text: "host " + pctAll(pctAll(privateIP())), rule: "private-ip"},
		{name: "percent-encoded bearer token", text: pctAll("Bearer abcdefghijklmnop1234"), rule: "credential-value"},
		{name: "percent-encoded email", text: pctAll("alice@corp-mail.com"), rule: "email"},
		{name: "percent-encoded signed query", text: "a" + pctAll("?X-Amz-Signature=abc"), rule: "signed-url"},
		{name: "hex integer IP", text: fmt.Sprintf("host 0x%08X", privateIPNumber()), rule: "private-ip"},
		{name: "dotted hex IP", text: "host 0xC0.0xA8.0.9", rule: "private-ip"},
		{name: "dotted octal IP", text: "host 0300.0250.0.9", rule: "private-ip"},
		{name: "percent-encoded hex IP", text: pctAll(fmt.Sprintf("0x%08x", privateIPNumber())), rule: "private-ip"},
		{name: "mixed-case token under 200 characters", text: strings.Repeat("aB3dE6gH9", 12), rule: "embedded-binary"},
	}
}

// privateIP assembles a private IPv4 literal from parts (no private literal in source).
func privateIP() string { return strings.Join([]string{"192", "168", "0", "9"}, ".") }

func privateIPNumber() uint32 { return 192<<24 | 168<<16 | 9 }

// pctAll percent-encodes every byte of s.
func pctAll(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, "%%%02X", s[i])
	}
	return b.String()
}

// Values that merely look like the encoded forms above must stay accepted, so
// the new rules are attributable to the injected content and do not flag
// ordinary identifiers, percentages, digests or public hex numbers.
func TestPrivacyEncodedFormsDoNotFlagBenignValues(t *testing.T) {
	for _, text := range []string{
		"100% done", "50%% of 3 items", "discount 20%25 applied", "%20 leading space is plain text",
		"0xdeadbeef", "build 0x1A2B", "version 1.2.3.4", "released 2026.10.28", "release 1.0.0",
		strings.Repeat("a", 90),                 // single-class run below the 200 hard limit
		strings.Repeat("abcdef0123456789", 4),   // 64 hex characters
		"a-slug-" + strings.Repeat("word-", 20), // long lower-case identifier
		strings.Repeat("Ab1", 20),               // 60 mixed characters, below the 80 floor
		"https://example.test/v1/tasks/id-1",    // canonical public URL
	} {
		c := privacyCase{text: text, key: "message"}
		policy := privacyPolicy(c, true)
		exchanges := []wirefixture.Exchange{privacyExchange(c)}
		if err := wirefixture.Validate(exchanges, policy); err != nil {
			t.Errorf("Validate rejected benign %q: %v", text, err)
		}
		if f := scanCandidate("benign.candidate.json", exchanges, policy); len(f) != 0 {
			t.Errorf("scan flagged benign %q: %v", text, f)
		}
	}
}

func TestPublicHostRejectsLegacyNumericForms(t *testing.T) {
	for _, host := range []string{"3232235529", "0xC0A80009", "0300.0250.0.9", "0xC0.0xA8.0.9"} {
		policy := wirefixture.Policy{MaxBodyBytes: 1 << 20, Keys: map[string]bool{}, Allowed: map[string]bool{}, PublicHosts: map[string]bool{host: true}}
		if err := wirefixture.ValidatePolicy(policy); err == nil {
			t.Errorf("ValidatePolicy accepted numeric host %q", host)
		}
	}
	ok := wirefixture.Policy{MaxBodyBytes: 1 << 20, Keys: map[string]bool{}, Allowed: map[string]bool{}, PublicHosts: map[string]bool{"api.vendor.com": true}}
	if err := wirefixture.ValidatePolicy(ok); err != nil {
		t.Errorf("ValidatePolicy rejected a public host: %v", err)
	}
}

func privacyPolicy(c privacyCase, register bool) wirefixture.Policy {
	key := c.key
	if key == "" {
		key = "message"
	}
	policy := wirefixture.Policy{
		MaxBodyBytes: 1 << 20,
		Keys:         map[string]bool{key: true},
		Allowed:      map[string]bool{"v1": true, "tasks": true},
		Aliases:      map[string]string{},
	}
	if register {
		policy.Allowed[c.text] = true
	}
	return policy
}

func privacyExchange(c privacyCase) wirefixture.Exchange {
	key := c.key
	if key == "" {
		key = "message"
	}
	exchange := safetyExchange()
	body, _ := json.Marshal(map[string]string{key: c.text})
	exchange.Response.Body.JSON = body
	return exchange
}

func TestPrivacyScanDetectsInjectedContent(t *testing.T) {
	for _, c := range privacyCases() {
		t.Run(c.name, func(t *testing.T) {
			policy := privacyPolicy(c, !c.unregisteredOnly)
			findings := scanCandidate("injected.candidate.json", []wirefixture.Exchange{privacyExchange(c)}, policy)
			if !hasRule(findings, c.rule) {
				t.Fatalf("scan rules = %v, want %q", findings, c.rule)
			}
			for _, f := range findings {
				if strings.Contains(f.String(), c.text) && len(c.text) > 6 {
					t.Fatalf("finding leaks the matched value: %s", f)
				}
			}
		})
	}
}

// The controls below only mean something if a clean exchange passes the same
// policy, so rejection is attributable to the injected content.
func TestPrivacyControlBaselinePasses(t *testing.T) {
	c := privacyCase{text: "hello", key: "message"}
	policy := privacyPolicy(c, true)
	exchanges := []wirefixture.Exchange{privacyExchange(c)}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatalf("clean baseline rejected: %v", err)
	}
	if f := scanCandidate("clean.candidate.json", exchanges, policy); len(f) != 0 {
		t.Fatalf("clean baseline flagged: %v", f)
	}
	root, dir := safetyCandidateDir(t)
	if err := wirefixture.WriteCandidate(root, filepath.Join(dir, "candidate.json"), exchanges, policy); err != nil {
		t.Fatalf("clean baseline not exportable: %v", err)
	}
}

func TestWirefixtureRefusesInjectedPrivateContent(t *testing.T) {
	for _, c := range privacyCases() {
		for _, register := range []bool{false, true} {
			if register && c.unregisteredOnly {
				continue
			}
			t.Run(fmt.Sprintf("%s/registered=%t", c.name, register), func(t *testing.T) {
				policy := privacyPolicy(c, register)
				exchanges := []wirefixture.Exchange{privacyExchange(c)}
				if err := wirefixture.Validate(exchanges, policy); err == nil {
					t.Fatal("Validate accepted injected content")
				}
				root, dir := safetyCandidateDir(t)
				path := filepath.Join(dir, "candidate.json")
				if err := wirefixture.WriteCandidate(root, path, exchanges, policy); err == nil {
					t.Fatal("WriteCandidate accepted injected content")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("rejected candidate created a file: %v", err)
				}
			})
		}
	}
}

func TestPrivacyControlsForHostsHeadersPathsAndMedia(t *testing.T) {
	policy := wirefixture.Policy{
		MaxBodyBytes: 1 << 20, Keys: map[string]bool{}, Allowed: map[string]bool{"v1": true, "tasks": true}, Aliases: map[string]string{},
		PublicHosts: map[string]bool{"api.vendor.com": true},
	}
	cases := []struct {
		name   string
		mutate func(*wirefixture.Exchange)
		rule   string
		policy func(wirefixture.Policy) wirefixture.Policy
	}{
		{"unregistered request host", func(e *wirefixture.Exchange) { e.Request.Host = "api.other.com" }, "host-not-public-alias", nil},
		{"internal request host", func(e *wirefixture.Exchange) { e.Request.Host = "db.internal" }, "private-hostname", nil},
		{"private IP request host", func(e *wirefixture.Exchange) { e.Request.Host = "10.0.0.5" }, "private-ip", nil},
		{"private host registered as public", func(e *wirefixture.Exchange) { e.Request.Host = "10.0.0.5" }, "private-ip", func(p wirefixture.Policy) wirefixture.Policy {
			p.PublicHosts = map[string]bool{"10.0.0.5": true}
			return p
		}},
		{"query in path", func(e *wirefixture.Exchange) {
			e.Request.Path = "/v1/tasks?token=1"
			e.Metadata.Endpoint = "GET /v1/tasks?token=1"
		}, "url-query", nil},
		{"authorization request header", func(e *wirefixture.Exchange) {
			e.Request.Headers = map[string]string{"authorization": "Bearer abcdefghijklmnop1234"}
		}, "header-not-allowed", nil},
		{"cookie response header", func(e *wirefixture.Exchange) {
			e.Response.Headers = map[string]string{"set-cookie": "session=abc"}
		}, "header-not-allowed", nil},
		{"raw media bytes descriptor", func(e *wirefixture.Exchange) {
			e.Metadata.Protocol = "media"
			e.Response.ContentType = "model/gltf-binary"
			e.Response.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMedia, Media: &wirefixture.Media{MediaType: "model/gltf-binary", Length: 100}}
		}, "media-not-descriptor", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := policy
			if c.policy != nil {
				p = c.policy(policy)
			}
			exchange := safetyExchange()
			c.mutate(&exchange)
			exchanges := []wirefixture.Exchange{exchange}
			if findings := scanCandidate("injected.candidate.json", exchanges, p); !hasRule(findings, c.rule) {
				t.Fatalf("scan rules = %v, want %q", findings, c.rule)
			}
			if err := wirefixture.Validate(exchanges, p); err == nil {
				t.Fatal("Validate accepted injected content")
			}
			root, dir := safetyCandidateDir(t)
			if err := wirefixture.WriteCandidate(root, filepath.Join(dir, "candidate.json"), exchanges, p); err == nil {
				t.Fatal("WriteCandidate accepted injected content")
			}
		})
	}
}

func TestPrivacyScanRejectsPolicyThatRegistersLeaks(t *testing.T) {
	policy := wirefixture.Policy{
		MaxBodyBytes: 1 << 20, Keys: map[string]bool{"message": true},
		Allowed:     map[string]bool{"Bearer abcdefghijklmnop1234": true, "see 10.0.0.1": true},
		Aliases:     map[string]string{"x": "https://example.test/a?token=1"},
		PublicHosts: map[string]bool{"db.internal": true},
	}
	findings := scanPolicy("leaky.policy.json", policy)
	for _, rule := range []string{"credential-value", "private-ip", "url-query", "private-hostname", "policy-invalid"} {
		if !hasRule(findings, rule) {
			t.Errorf("policy scan missed %q: %v", rule, findings)
		}
	}
}

func TestPrivacyScanRejectsBadEvidenceAndMeta(t *testing.T) {
	if f := scanEvidence("e.evidence.json", []byte(`[{"sequence":1,"part":-1,"media_type":"model/stl","length":3,"sha256":"00","bytes":"AAAA"}]`)); !hasRule(f, "evidence-shape") {
		t.Errorf("evidence with raw bytes accepted: %v", f)
	}
	if f := scanEvidence("e.evidence.json", []byte(`[{"sequence":1,"part":-1,"media_type":"model/stl","length":3,"sha256":"zz"}]`)); !hasRule(f, "evidence-shape") {
		t.Errorf("evidence with bad digest accepted: %v", f)
	}
	if f := scanGenericJSON("m.meta.json", []byte(`{"owner":"alice","note":"mail bob@corp-mail.com from 10.0.0.9"}`)); !hasRule(f, "identity-field") || !hasRule(f, "email") || !hasRule(f, "private-ip") {
		t.Errorf("meta scan missed injected content: %v", f)
	}
}

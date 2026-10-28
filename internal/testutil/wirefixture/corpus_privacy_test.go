package wirefixture_test

// Privacy scan of the committed wire corpus (#70). The sanitizer is the first
// line of defence; this file is an independent second one. It re-reads every
// committed candidate/meta/policy/evidence file with its own rules, so a
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
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

const (
	corpusLongTextBytes = 128 // strings longer than this must be registered in the policy
	corpusMinCandidates = 3   // agent-flow, meshy, hyper3d; guards against a vacuous pass
)

var (
	cpCredentialRE = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}|\bsk-[A-Za-z0-9_-]{8,}|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [A-Z ]*PRIVATE KEY-----|\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|\bgh[pousr]_[A-Za-z0-9]{16,}|\bxox[abprs]-[A-Za-z0-9-]{8,}`)
	cpSignedRE     = regexp.MustCompile(`(?i)x-amz-[a-z-]+=|x-goog-[a-z-]+=|\bsignature=|\bexpires=|\btoken=|\bsig=|\bsas=|\bgoogleaccessid=|\baccess_token=|\bapikey=|\bapi_key=`)
	cpHostnameRE   = regexp.MustCompile(`(?i)\blocalhost\b|\.(?:internal|local|lan|corp|home|intranet|private|localdomain|svc)\b|\.home\.arpa\b|\.cluster\.local\b`)
	cpIPv4RE       = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	cpIPv6RE       = regexp.MustCompile(`(?i)\[[0-9a-f:.]+\]|\b(?:[0-9a-f]{0,4}:){2,}[0-9a-f:.]*`)
	cpURLRE        = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>\\]+`)
	cpEmailRE      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	cpUUIDRE       = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	cpBlobRE       = regexp.MustCompile(`[A-Za-z0-9+/_-]{200,}={0,2}`)
	cpDataURIRE    = regexp.MustCompile(`(?i)\bdata:[a-z0-9.+/-]*[;,]`)
	cpPlaceholder  = regexp.MustCompile(`^(?:id-\d+|\[redacted\]|ok)?$`)
	cpHex64RE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// cpSensitiveKeys are field names (compacted: lower case, no separators)
// whose string value may only be a redaction.
var cpSensitiveKeys = []string{
	"authorization", "cookie", "setcookie", "apikey", "xapikey", "accesskey", "token", "secret", "password",
	"passwd", "signature", "credential", "credentials", "privatekey", "sessionid", "signedurl", "auth",
}

// cpIdentityKeys are field names whose string value must be a placeholder.
var cpIdentityKeys = map[string]bool{
	"userid": true, "user": true, "username": true, "email": true, "owner": true, "ownerid": true,
	"uid": true, "accountid": true, "tenantid": true, "deviceid": true, "clientip": true, "phone": true,
	"customerid": true, "organizationid": true, "orgid": true,
}

// cpPromptKeys are field names that carry user or model text.
var cpPromptKeys = map[string]bool{
	"prompt": true, "textureprompt": true, "negativeprompt": true, "system": true, "systemprompt": true,
	"instructions": true, "input": true, "query": true, "content": true, "text": true, "description": true,
}

var cpAllowedRequestHeaders = map[string]bool{"content-type": true, "accept": true, "x-api-version": true}
var cpAllowedResponseHeaders = map[string]bool{"content-type": true, "retry-after": true}

func cpCompact(key string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || r == ' ' {
			return -1
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}, key)
}

func cpSensitive(compact string) bool {
	for _, name := range cpSensitiveKeys {
		if compact == name || strings.HasSuffix(compact, name) {
			return true
		}
	}
	return false
}

// finding deliberately has no value field.
type finding struct{ file, path, rule string }

func (f finding) String() string { return fmt.Sprintf("%s %s [%s]", f.file, f.path, f.rule) }

// scanCtx carries what a file is checked against.
type scanCtx struct {
	file        string
	registered  map[string]bool // exact strings the policy approves (Allowed values, alias targets)
	publicHosts map[string]bool
	strict      bool // wire payload: enforce registration of prompts and long text
	out         *[]finding
}

func (c scanCtx) add(path, rule string) { *c.out = append(*c.out, finding{c.file, path, rule}) }

func newScanCtx(file string, policy wirefixture.Policy, strict bool, out *[]finding) scanCtx {
	registered := map[string]bool{}
	for value := range policy.Allowed {
		registered[value] = true
	}
	for _, target := range policy.Aliases {
		registered[target] = true
	}
	hosts := map[string]bool{"example.test": true}
	for host := range policy.PublicHosts {
		hosts[host] = true
	}
	return scanCtx{file: file, registered: registered, publicHosts: hosts, strict: strict, out: out}
}

func cpNonPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	return !addr.IsValid() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() ||
		(addr.Is6() && !netip.MustParsePrefix("2000::/3").Contains(addr))
}

// scanText applies the content rules to one string. key is the JSON field
// name that holds it ("" when not a field value).
func (c scanCtx) scanText(path, key, text string) {
	compact := cpCompact(key)
	if text != "" && text != "[redacted]" && cpSensitive(compact) {
		c.add(path, "credential-field")
	}
	if cpIdentityKeys[compact] && !cpPlaceholder.MatchString(text) {
		c.add(path, "identity-field")
	}
	if cpCredentialRE.MatchString(text) {
		c.add(path, "credential-value")
	}
	if cpHostnameRE.MatchString(text) {
		c.add(path, "private-hostname")
	}
	for _, raw := range cpIPv4RE.FindAllString(text, -1) {
		if addr, err := netip.ParseAddr(raw); err == nil && cpNonPublic(addr) {
			c.add(path, "private-ip")
			break
		}
	}
	for _, raw := range cpIPv6RE.FindAllString(text, -1) {
		if addr, err := netip.ParseAddr(strings.Trim(raw, "[]")); err == nil && cpNonPublic(addr) {
			c.add(path, "private-ip")
			break
		}
	}
	for _, raw := range cpURLRE.FindAllString(text, -1) {
		parsed, err := url.Parse(raw)
		switch {
		case err != nil || parsed.Host == "":
			c.add(path, "url-malformed")
		case !c.publicHosts[parsed.Hostname()]:
			c.add(path, "url-host")
		case parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "?"):
			c.add(path, "url-query")
		case parsed.Fragment != "" || strings.Contains(raw, "#"):
			c.add(path, "url-fragment")
		case parsed.User != nil:
			c.add(path, "url-userinfo")
		}
	}
	if cpSignedRE.MatchString(text) {
		c.add(path, "signed-url")
	}
	if cpEmailRE.MatchString(text) {
		c.add(path, "email")
	}
	if cpUUIDRE.MatchString(text) {
		c.add(path, "raw-identifier")
	}
	if cpBlobRE.MatchString(text) || cpDataURIRE.MatchString(text) {
		c.add(path, "embedded-binary")
	}
	if !c.strict || text == "" || cpPlaceholder.MatchString(text) || c.registered[text] {
		return
	}
	if cpPromptKeys[compact] {
		c.add(path, "unregistered-prompt")
	} else if len(text) > corpusLongTextBytes {
		c.add(path, "unregistered-long-text")
	}
}

// scanValue walks decoded JSON.
func (c scanCtx) scanValue(path, key string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := path + "." + k
			c.scanText(child+"#key", "", k)
			c.scanValue(child, k, typed[k])
		}
	case []any:
		for i, child := range typed {
			c.scanValue(fmt.Sprintf("%s[%d]", path, i), key, child)
		}
	case string:
		if trimmed := strings.TrimSpace(typed); len(trimmed) > 1 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(trimmed)) {
			c.scanText(path, key, typed)
			var nested any
			if json.Unmarshal([]byte(trimmed), &nested) == nil {
				c.scanValue(path+"(nested)", key, nested)
			}
			return
		}
		c.scanText(path, key, typed)
	}
}

func cpDecode(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func (c scanCtx) scanRawJSON(path string, raw []byte) {
	value, err := cpDecode(raw)
	if err != nil {
		c.add(path, "json-invalid")
		return
	}
	c.scanValue(path, "", value)
}

func (c scanCtx) scanSSE(path, text string) {
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "data: [DONE]" {
			continue
		}
		linePath := fmt.Sprintf("%s:line%d", path, i)
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			c.scanText(linePath, "", line)
			continue
		}
		c.scanRawJSON(linePath, []byte(strings.TrimSpace(payload)))
	}
}

func (c scanCtx) scanMedia(path string, media *wirefixture.Media) {
	if media == nil {
		return
	}
	// Only a descriptor (type, length, synthetic recipe) may be public.
	if media.Recipe == "" || !media.Substituted || media.Length <= 0 {
		c.add(path, "media-not-descriptor")
	}
	c.scanText(path+".media_type", "", media.MediaType)
}

func (c scanCtx) scanPayload(path string, payload wirefixture.Payload) {
	if len(payload.JSON) > 0 {
		c.scanRawJSON(path+".json", payload.JSON)
	}
	if payload.SSE != "" {
		c.scanSSE(path+".sse", payload.SSE)
	}
	c.scanMedia(path+".media", payload.Media)
	if payload.Multipart != nil {
		for i, part := range payload.Multipart.Parts {
			partPath := fmt.Sprintf("%s.multipart[%d]", path, i)
			c.scanText(partPath+".name", "", part.Name)
			c.scanText(partPath+".value", part.Name, part.Value)
			c.scanMedia(partPath+".media", part.Media)
		}
	}
}

func (c scanCtx) scanHeaders(path string, headers map[string]string, allowed map[string]bool) {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !allowed[strings.ToLower(name)] {
			c.add(path+"."+name, "header-not-allowed")
		}
		c.scanText(path+"."+name, name, headers[name])
	}
}

func (c scanCtx) scanExchange(index int, e wirefixture.Exchange) {
	base := fmt.Sprintf("$[%d]", index)
	m := e.Metadata
	for field, value := range map[string]string{
		"scenario": m.Scenario, "connection": m.Connection, "endpoint": m.Endpoint, "api_version": m.APIVersion,
		"requested_model": m.RequestedModel, "reported_model": m.ReportedModel, "server_build": m.ServerBuild,
		"app_revision": m.AppRevision, "catalog_revision": m.CatalogRevision, "environment": m.Environment,
	} {
		c.scanText(base+".metadata."+field, "", value)
	}
	if !c.publicHosts[e.Request.Host] {
		c.add(base+".request.host", "host-not-public-alias")
	}
	if strings.ContainsAny(e.Request.Path, "?#") || strings.Contains(e.Request.Path, "://") {
		c.add(base+".request.path", "url-query")
	}
	c.scanText(base+".request.host", "", e.Request.Host)
	c.scanText(base+".request.path", "", e.Request.Path)
	c.scanHeaders(base+".request.headers", e.Request.Headers, cpAllowedRequestHeaders)
	c.scanHeaders(base+".response.headers", e.Response.Headers, cpAllowedResponseHeaders)
	c.scanPayload(base+".request.body", e.Request.Body)
	c.scanPayload(base+".response.body", e.Response.Body)
}

func scanCandidate(file string, exchanges []wirefixture.Exchange, policy wirefixture.Policy) []finding {
	var out []finding
	ctx := newScanCtx(file, policy, true, &out)
	for i, e := range exchanges {
		ctx.scanExchange(i, e)
	}
	return out
}

// scanPolicy checks what the policy itself would let through.
func scanPolicy(file string, policy wirefixture.Policy) []finding {
	var out []finding
	ctx := newScanCtx(file, policy, false, &out)
	// The capture exporter emits an empty-string Allowed entry and lists the
	// reserved example.test among PublicHosts; ValidatePolicy rejects both
	// harmless entries, so judge the rest.
	checked := policy
	checked.Allowed = map[string]bool{}
	for value, enabled := range policy.Allowed {
		if value != "" {
			checked.Allowed[value] = enabled
		}
	}
	checked.PublicHosts = map[string]bool{}
	for host, enabled := range policy.PublicHosts {
		if host != "example.test" {
			checked.PublicHosts[host] = enabled
		}
	}
	if err := wirefixture.ValidatePolicy(checked); err != nil {
		ctx.add("$", "policy-invalid")
	}
	for key := range policy.Keys {
		ctx.scanText("$.Keys."+key, "", key)
	}
	for value := range policy.Allowed {
		ctx.scanText("$.Allowed", "", value)
	}
	for raw, target := range policy.Aliases {
		ctx.scanText("$.Aliases[key]", "", raw)
		ctx.scanText("$.Aliases[value]", "", target)
	}
	for host := range policy.PublicHosts {
		ctx.scanText("$.PublicHosts", "", host)
		if net := strings.TrimSpace(host); net == "" || strings.ContainsAny(host, "/?#@") {
			ctx.add("$.PublicHosts", "private-hostname")
		}
	}
	return out
}

func scanGenericJSON(file string, raw []byte) []finding {
	var out []finding
	ctx := scanCtx{file: file, registered: map[string]bool{}, publicHosts: map[string]bool{"example.test": true}, out: &out}
	ctx.scanRawJSON("$", raw)
	return out
}

func scanEvidence(file string, raw []byte) []finding {
	var out []finding
	ctx := scanCtx{file: file, out: &out}
	var records []wirefixture.MediaEvidenceRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		ctx.add("$", "evidence-shape")
		return out
	}
	for i, record := range records {
		path := fmt.Sprintf("$[%d]", i)
		if !cpHex64RE.MatchString(record.SHA256) || record.Length < 0 {
			ctx.add(path, "evidence-shape")
		}
		ctx.scanText(path+".media_type", "", record.MediaType)
	}
	return out
}

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
			base == "wire-coverage.json"
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
		{name: "192.168/16 address", text: "upstream at 192.168.0.9 failed", rule: "private-ip"},
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

func hasRule(findings []finding, rule string) bool {
	for _, f := range findings {
		if f.rule == rule {
			return true
		}
	}
	return false
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

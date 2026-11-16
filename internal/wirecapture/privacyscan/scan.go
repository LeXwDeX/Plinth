// Package privacyscan is the independent second line of defence behind the
// wire sanitizer (#70, extended for #233). The sanitizer decides what is
// written; this package re-reads sanitized exchanges, policies, metadata and
// record files with its own, separately written rules and reports anything a
// regression, a hand edit or a lenient policy let through: credentials,
// private hosts and addresses, signed URLs, identifiers, personal data,
// unregistered prompts and embedded binary.
//
// A Finding carries the file, the JSON path and the rule name, never the
// matched value, so findings are safe to log and to put in a bundle. The
// package imports only the standard library and wirecapture: committed-corpus
// tests, the recorder's own tests and the diagnostics export (#234) all run
// the same rules.
package privacyscan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

// longTextBytes: strings longer than this must be registered in the policy.
const corpusLongTextBytes = 128

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
	cpMixedBlobRE  = regexp.MustCompile(`[A-Za-z0-9+/_-]{80,}={0,2}`)
	cpPercentRE    = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
	cpHexIntIPRE   = regexp.MustCompile(`(?i)\b0x[0-9a-f]{8}\b`)
	cpDottedIPRE   = regexp.MustCompile(`(?i)\b(?:0x[0-9a-f]{1,8}|\d{1,10})(?:\.(?:0x[0-9a-f]{1,8}|\d{1,10})){1,3}\b`)
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

// Finding deliberately has no value field: where and which rule, never what.
type Finding struct{ File, Path, Rule string }

func (f Finding) String() string { return fmt.Sprintf("%s %s [%s]", f.File, f.Path, f.Rule) }

// scanCtx carries what a file is checked against.
type scanCtx struct {
	file        string
	registered  map[string]bool // exact strings the policy approves (Allowed values, alias targets)
	publicHosts map[string]bool
	strict      bool // wire payload: enforce registration of prompts and long text
	out         *[]Finding
}

func (c scanCtx) add(path, rule string) {
	*c.out = append(*c.out, Finding{File: c.file, Path: path, Rule: rule})
}

func newScanCtx(file string, policy wirecapture.Policy, strict bool, out *[]Finding) scanCtx {
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
	c.scanContent(path, text)
	for _, decoded := range cpPercentVariants(text) {
		c.scanContent(path, decoded)
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

// cpPercentVariants decodes valid %XX escapes up to twice; invalid escapes stay.
func cpPercentVariants(text string) []string {
	var out []string
	current := text
	for round := 0; round < 2 && cpPercentRE.MatchString(current); round++ {
		current = cpPercentRE.ReplaceAllStringFunc(current, func(e string) string {
			var b byte
			fmt.Sscanf(e[1:], "%02x", &b)
			return string([]byte{b})
		})
		out = append(out, current)
	}
	return out
}

func cpUint32Addr(v uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// cpLegacyIP finds a non-public IPv4 written as a 32-bit hex number or with
// hex/octal components, which netip refuses.
func cpLegacyIP(text string) bool {
	for _, raw := range cpHexIntIPRE.FindAllString(text, -1) {
		var v uint64
		if _, err := fmt.Sscanf(raw[2:], "%x", &v); err == nil && cpNonPublic(cpUint32Addr(v)) {
			return true
		}
	}
	for _, raw := range cpDottedIPRE.FindAllString(text, -1) {
		parts := strings.Split(raw, ".")
		legacy := false
		for _, p := range parts {
			legacy = legacy || (len(p) > 1 && p[0] == '0')
		}
		if !legacy {
			continue
		}
		for _, base := range []int{0, 10} {
			var number uint64
			ok := true
			for i, p := range parts {
				v, err := strconv.ParseUint(p, base, 32)
				if err != nil {
					ok = false
					break
				}
				if i < len(parts)-1 {
					ok = ok && v <= 255
					number |= v << (8 * uint(3-i))
				} else {
					ok = ok && v < 1<<(8*uint(4-i))
					number |= v
				}
			}
			if ok && cpNonPublic(cpUint32Addr(number)) {
				return true
			}
		}
	}
	return false
}

func cpMixedBlob(text string) bool {
	for _, run := range cpMixedBlobRE.FindAllString(text, -1) {
		if strings.IndexFunc(run, unicode.IsUpper) >= 0 && strings.IndexFunc(run, unicode.IsLower) >= 0 && strings.IndexFunc(run, unicode.IsDigit) >= 0 {
			return true
		}
	}
	return false
}

// scanContent applies the content rules to text (or to a decoded form of it).
func (c scanCtx) scanContent(path, text string) {
	if cpCredentialRE.MatchString(text) {
		c.add(path, "credential-value")
	}
	if cpLegacyIP(text) {
		c.add(path, "private-ip")
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
	if cpBlobRE.MatchString(text) || cpMixedBlob(text) || cpDataURIRE.MatchString(text) {
		c.add(path, "embedded-binary")
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

func (c scanCtx) scanMedia(path string, media *wirecapture.Media) {
	if media == nil {
		return
	}
	// Only a descriptor (type, length, synthetic recipe) may be public.
	if media.Recipe == "" || !media.Substituted || media.Length <= 0 {
		c.add(path, "media-not-descriptor")
	}
	c.scanText(path+".media_type", "", media.MediaType)
}

func (c scanCtx) scanPayload(path string, payload wirecapture.Payload) {
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

func (c scanCtx) scanExchange(index int, e wirecapture.Exchange) {
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

// ScanExchanges checks sanitized exchanges against the policy that approved
// them: every rule, plus unregistered prompts and long text.
func ScanExchanges(file string, exchanges []wirecapture.Exchange, policy wirecapture.Policy) []Finding {
	var out []Finding
	ctx := newScanCtx(file, policy, true, &out)
	for i, e := range exchanges {
		ctx.scanExchange(i, e)
	}
	return out
}

// ScanPolicy checks what the policy itself would let through.
func ScanPolicy(file string, policy wirecapture.Policy) []Finding {
	var out []Finding
	ctx := newScanCtx(file, policy, false, &out)
	if err := wirecapture.ValidatePolicy(policy); err != nil {
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

// ScanJSON checks any JSON document (metadata, a coverage matrix, a record file)
// with the content rules.
func ScanJSON(file string, raw []byte) []Finding {
	var out []Finding
	ctx := scanCtx{file: file, registered: map[string]bool{}, publicHosts: map[string]bool{"example.test": true}, out: &out}
	ctx.scanRawJSON("$", raw)
	return out
}

// ScanEvidence checks a media evidence manifest: locations, types, lengths and
// digests only.
func ScanEvidence(file string, raw []byte) []Finding {
	var out []Finding
	ctx := scanCtx{file: file, out: &out}
	var records []evidenceRecord
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

// ScanText scans one string. key is the JSON field that holds it ("" when it
// is not a field value).
func ScanText(file, path, key, text string) []Finding {
	var out []Finding
	scanCtx{file: file, registered: map[string]bool{}, publicHosts: map[string]bool{"example.test": true}, out: &out}.
		scanText(path, key, text)
	return out
}

// HasRule reports whether any finding fired the named rule.
func HasRule(findings []Finding, rule string) bool {
	for _, finding := range findings {
		if finding.Rule == rule {
			return true
		}
	}
	return false
}

// ScanRecordDir scans every record file the upstream recorder wrote to dir
// (#233). Each recorded exchange is checked against the replay policy derived
// from it alone, so one odd record cannot hide another; skip markers and query
// key names are checked as text. It returns the findings and how many exchange
// records it covered, so a caller can refuse a vacuous pass. A file the reader
// refuses (wrong mode, unknown field, bad version) is an error, not a finding.
func ScanRecordDir(dir string) ([]Finding, int, error) {
	names, err := wirecapture.ListUpstreamRecords(dir)
	if err != nil {
		return nil, 0, err
	}
	var findings []Finding
	covered := 0
	for _, name := range names {
		record, err := wirecapture.ReadUpstreamRecord(filepath.Join(dir, name))
		if err != nil {
			return nil, 0, err
		}
		findings = append(findings, ScanText(name, "$.skipped", "", record.Skipped)...)
		findings = append(findings, ScanText(name, "$.connection", "", record.Connection)...)
		findings = append(findings, ScanText(name, "$.method", "", record.Method)...)
		if record.Status != 0 && (record.Status < 100 || record.Status > 599) {
			findings = append(findings, Finding{File: name, Path: "$.status", Rule: "record-shape"})
		}
		for index, key := range record.QueryKeys {
			findings = append(findings, ScanText(name, fmt.Sprintf("$.query_keys[%d]", index), "", key)...)
		}
		if record.Exchange == nil {
			continue
		}
		covered++
		exchange := []wirecapture.Exchange{*record.Exchange}
		policy, err := wirecapture.DraftReplayPolicy(exchange)
		if err != nil {
			// An exchange the sanitizer's own validator rejects is a finding in
			// itself: nothing unvalidated may sit in a record.
			findings = append(findings, Finding{File: name, Path: "$.exchange", Rule: "exchange-invalid"})
			continue
		}
		findings = append(findings, ScanExchanges(name, exchange, policy)...)
		findings = append(findings, ScanPolicy(name+".policy", policy)...)
	}
	return findings, covered, nil
}

// evidenceRecord is the shape of a media evidence manifest entry: a location,
// a type, a length and a digest; never bytes.
type evidenceRecord struct {
	Sequence  int    `json:"sequence"`
	Part      int    `json:"part"`
	MediaType string `json:"media_type"`
	Length    int64  `json:"length"`
	SHA256    string `json:"sha256"`
}

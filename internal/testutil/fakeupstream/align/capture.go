package align

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// CaptureConfig describes the sanitized sample an official run exports.
type CaptureConfig struct {
	// Connection is the Provider connection id the exchanges are filed under
	// ("tripo", "LLM"); the wirefixture coverage matrix maps it to an adapter.
	Connection  string
	Scenario    string
	APIVersion  string
	Environment string
	// AppRevision and CatalogRevision are required for "captured" samples.
	AppRevision     string
	CatalogRevision string
	// Projection maps JSON field names to wirefixture projection kinds
	// ("id", "url", "timestamp", "number", "text", "opaque").
	Projection map[string]string
	// PublicHosts are upstream hosts that may appear as-is.
	PublicHosts []string
	// HostAlias maps a non-public host to the reserved example.test host.
	HostAlias map[string]string
	// AliasBaseHost aliases the official base URL\'s own host to example.test
	// (for gateways whose host is not public).
	AliasBaseHost bool
	CapturedAt    time.Time
	// OmitKeys are JSON keys removed from every response before it is recorded,
	// for keys the sanitizer cannot hold (a leading underscore is not an
	// identifier it accepts). The fake adds them back itself, and the version's
	// drift test knows the list.
	OmitKeys []string
	// ExtraKeys are JSON keys registered even though the exporter would not
	// reach them: it does not descend into an object whose own key looks like a
	// credential ("input_tokens_details" contains "token"), so the keys below
	// it must be named here. Their values are still sanitized as usual.
	ExtraKeys []string
}

// Capture records the official run through a deferred wirefixture recorder.
type Capture struct {
	cfg      CaptureConfig
	Recorder *wirefixture.Recorder
	store    *wirefixture.PrivateStore
}

// Close releases the private evidence store.
func (c *Capture) Close() error { return c.store.Close() }

func mediaRecipes() map[string]string {
	return map[string]string{
		"image/png": "png-1x1", "image/jpeg": "jpeg-1x1", "image/webp": "webp-1x1",
		"model/gltf-binary": "glb-triangle", "model/stl": "stl-triangle", "model/fbx": "fbx-triangle",
		"model/vnd.usdz+zip": "usdz-empty", "model/3mf": "3mf-empty", "application/zip": "zip-empty",
		"application/octet-stream": "octet-empty", "text/plain": "text-plain",
	}
}

func mediaExtensions() map[string]string {
	return map[string]string{
		".glb": "glb-triangle", ".obj": "obj-triangle", ".stl": "stl-triangle", ".fbx": "fbx-triangle",
		".usdz": "usdz-empty", ".3mf": "3mf-empty", ".zip": "zip-empty", ".png": "png-1x1",
		".jpg": "jpeg-1x1", ".jpeg": "jpeg-1x1", ".webp": "webp-1x1",
	}
}

// NewCapture prepares a recorder. Wrap the official API client's transport
// with Capture.Transport before running the script.
func NewCapture(cfg CaptureConfig, worktree, evidenceDir string) (*Capture, error) {
	if cfg.CapturedAt.IsZero() {
		cfg.CapturedAt = time.Now().UTC()
	}
	store, err := wirefixture.NewPrivateStore(worktree, evidenceDir)
	if err != nil {
		return nil, err
	}
	recorder, err := wirefixture.NewDeferredRecorderWithEvidence(wirefixture.Metadata{
		Scenario: cfg.Scenario, Connection: cfg.Connection, APIVersion: cfg.APIVersion,
		CapturedAt: cfg.CapturedAt.UTC(), AppRevision: cfg.AppRevision, CatalogRevision: cfg.CatalogRevision,
		Environment: cfg.Environment, Provenance: wirefixture.ProvenanceCaptured,
	}, wirefixture.DeferredLimits{MaxBytes: 64 << 20, MaxExchanges: 512}, store)
	if err != nil {
		return nil, err
	}
	return &Capture{cfg: cfg, Recorder: recorder, store: store}, nil
}

// Transport wraps base.
func (c *Capture) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if len(c.cfg.OmitKeys) > 0 {
		base = omitTransport{base: base, keys: c.cfg.OmitKeys}
	}
	return c.Recorder.Wrap(c.cfg.Connection, base)
}

// omitTransport removes configured keys from JSON responses.
type omitTransport struct {
	base http.RoundTripper
	keys []string
}

func (t omitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || !strings.Contains(response.Header.Get("Content-Type"), "json") {
		return response, err
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		var strip func(any)
		strip = func(node any) {
			switch typed := node.(type) {
			case map[string]any:
				for _, key := range t.keys {
					delete(typed, key)
				}
				for _, child := range typed {
					strip(child)
				}
			case []any:
				for _, child := range typed {
					strip(child)
				}
			}
		}
		strip(value)
		if cleaned, marshalErr := json.Marshal(value); marshalErr == nil {
			raw = cleaned
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	return response, nil
}

// Export finalizes the recording into out (an existing 0700 directory outside
// the worktree) as <name>.candidate.json, then writes the replay policy and
// meta next to it. The policy registers every key and scalar the official
// bundle contained except the projected fields: review the exported files
// before committing them.
func (c *Capture) Export(worktree, out, name, offering string, bundle *Bundle) (candidate string, err error) {
	defer func() {
		if err != nil {
			_ = c.Recorder.Release()
		}
	}()
	final := c.finalPolicy(bundle)
	if err := c.Recorder.DiscardIncomplete(); err != nil {
		return "", fmt.Errorf("align: discard incomplete: %w", err)
	}
	if err := c.Recorder.Finalize(final); err != nil {
		return "", fmt.Errorf("align: finalize: %w", err)
	}
	if err := c.Recorder.AssertComplete(); err != nil {
		return "", fmt.Errorf("align: incomplete capture: %w", err)
	}
	exchanges := c.Recorder.Exchanges()
	if exchanges, err = wirefixture.SubstituteMediaWithExtensions(exchanges, mediaRecipes(), mediaExtensions()); err != nil {
		return "", fmt.Errorf("align: substitute media: %w", err)
	}
	replay, err := replayPolicy(exchanges, final)
	if err != nil {
		return "", fmt.Errorf("align: replay policy: %w", err)
	}
	candidate = filepath.Join(out, name+".candidate.json")
	if err := wirefixture.WriteCandidate(worktree, candidate, exchanges, replay); err != nil {
		return "", fmt.Errorf("align: write candidate: %w", err)
	}
	policyJSON, err := json.MarshalIndent(replay, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(out, name+".policy.json"), append(policyJSON, '\n'), 0o600); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(candidate)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	meta := map[string]any{
		"version":          1,
		"scenario":         c.cfg.Scenario,
		"captured_at_utc":  c.cfg.CapturedAt.UTC().Format("2006-01-02"),
		"source_revision":  "unknown",
		"api_version":      c.cfg.APIVersion,
		"provenance":       "captured",
		"transformation":   "IDs, signed URLs, timestamps and amounts are deterministic placeholders; media bytes are not stored.",
		"exchanges":        len(exchanges),
		"candidate_sha256": hex.EncodeToString(sum[:]),
	}
	if offering != "" {
		meta["offering"] = offering
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(out, name+".meta.json"), append(metaJSON, '\n'), 0o600); err != nil {
		return "", err
	}
	return candidate, nil
}

// finalPolicy registers the keys of every body in the bundle and every plain
// scalar outside the projected fields.
func (c *Capture) finalPolicy(bundle *Bundle) wirefixture.Policy {
	policy := wirefixture.Policy{
		MaxBodyBytes: 4 << 20,
		Keys:         map[string]bool{"file": true},
		Allowed:      map[string]bool{"application/json": true},
		Aliases:      map[string]string{},
		PublicHosts:  map[string]bool{},
		Projection:   map[string]string{},
		QueryKeys:    map[string]bool{},
	}
	for key, kind := range c.cfg.Projection {
		policy.Projection[key] = kind
	}
	for _, key := range c.cfg.ExtraKeys {
		policy.Keys[key] = true
	}
	for _, host := range c.cfg.PublicHosts {
		policy.PublicHosts[host] = true
	}
	for host, alias := range c.cfg.HostAlias {
		policy.Aliases[host] = alias
	}
	aliases := 0
	placeholder := func(raw string) {
		if _, done := policy.Aliases[raw]; done {
			return
		}
		aliases++
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			parsed, err := url.Parse(raw)
			ext := ""
			if err == nil {
				ext = filepath.Ext(parsed.Path)
			}
			policy.Aliases[raw] = fmt.Sprintf("https://example.test/fixture-%d%s", aliases, ext)
			return
		}
		target := fmt.Sprintf("id-%d", aliases)
		policy.Aliases[raw] = target
		policy.Allowed[target] = true
	}
	var collect func(key string, value any)
	collect = func(key string, value any) {
		if key != "" {
			policy.Keys[key] = true
		}
		// A credential-like field (a subscription key, a token) is redacted by the
		// sanitizer; its value must not be registered as an allowed scalar either,
		// or the committed policy would carry it.
		if credentialKey(key) {
			return
		}
		if _, projected := policy.Projection[key]; projected {
			collectURLQuery(policy.QueryKeys, value)
			return
		}
		switch typed := value.(type) {
		case string:
			switch {
			case dynamicString(typed):
				placeholder(typed)
				collectURLQuery(policy.QueryKeys, typed)
			case typed != "" && len(typed) <= 512:
				policy.Allowed[typed] = true
			}
		case json.Number:
			policy.Allowed[typed.String()] = true
		case []any:
			for _, child := range typed {
				collect("", child)
			}
		case map[string]any:
			for childKey, child := range typed {
				collect(childKey, child)
			}
		}
	}
	scan := func(raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) == nil {
			collect("", value)
		}
	}
	if base, err := url.Parse(bundle.BaseURL); err == nil {
		for _, part := range strings.Split(base.Path, "/") {
			if part != "" {
				policy.Allowed[part] = true
			}
		}
	}
	for _, obs := range bundle.Obs {
		scan(obs.Request)
		scan(obs.Body)
		for _, body := range obs.PollBodies {
			scan(body)
		}
		for _, chunk := range obs.Chunks {
			scan(chunk)
		}
		if obs.RetryAfter != "" {
			policy.Allowed[obs.RetryAfter] = true
		}
		for _, part := range strings.Split(obs.Path+"/"+obs.Resolved, "/") {
			switch {
			case part == "" || strings.HasPrefix(part, "{"):
			case dynamicString(part):
				placeholder(part)
			default:
				policy.Allowed[part] = true
			}
		}
	}
	for key := range policy.Projection {
		if !policy.Keys[key] {
			delete(policy.Projection, key) // the field never appeared in this run
		}
	}
	return policy
}

var dynamicPattern = regexp.MustCompile(`^(https?://.*|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{16,}|[A-Za-z]+_[0-9a-fA-F-]{8,})$`)

// dynamicString reports values that must become placeholders: URLs, UUIDs
// and long hexadecimal identifiers.
func dynamicString(value string) bool { return dynamicPattern.MatchString(value) }

// collectURLQuery registers the query parameter names of projected URL
// values; their values are never retained.
func collectURLQuery(keys map[string]bool, value any) {
	switch typed := value.(type) {
	case string:
		if parsed, err := url.Parse(typed); err == nil {
			for name := range parsed.Query() {
				keys[name] = true
			}
		}
	case map[string]any:
		for _, child := range typed {
			collectURLQuery(keys, child)
		}
	case []any:
		for _, child := range typed {
			collectURLQuery(keys, child)
		}
	}
}

// replayPolicy derives the committed policy: the public registrations plus
// every terminal value of the validated, sanitized exchanges. It mirrors the
// exporter of the full-stack capture server.
func replayPolicy(exchanges []wirefixture.Exchange, public wirefixture.Policy) (wirefixture.Policy, error) {
	policy := wirefixture.Policy{
		MaxBodyBytes: public.MaxBodyBytes, Keys: cloneBool(public.Keys), Allowed: cloneBool(public.Allowed),
		PublicHosts: cloneBool(public.PublicHosts), QueryKeys: cloneBool(public.QueryKeys),
	}
	allow := func(value string) {
		if value != "" {
			policy.Allowed[value] = true
		}
	}
	decode := func(text string) any {
		var value any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil
		}
		return value
	}
	var collect func(any, int)
	collect = func(value any, depth int) {
		if depth > 12 {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for _, child := range typed {
				collect(child, depth+1)
			}
		case []any:
			for _, child := range typed {
				collect(child, depth+1)
			}
		case string:
			allow(typed)
			if nested := decode(typed); nested != nil {
				switch nested.(type) {
				case map[string]any, []any:
					collect(nested, depth+1)
				}
			}
		case json.Number:
			allow(typed.String())
		}
	}
	for _, exchange := range exchanges {
		for _, part := range strings.Split(exchange.Request.Path, "/") {
			if part != "" {
				policy.Allowed[part] = true
			}
		}
		for _, headers := range []map[string]string{exchange.Request.Headers, exchange.Response.Headers} {
			for _, value := range headers {
				allow(value)
			}
		}
		for _, payload := range []wirefixture.Payload{exchange.Request.Body, exchange.Response.Body} {
			collect(decode(string(payload.JSON)), 0)
			for _, line := range strings.Split(payload.SSE, "\n") {
				if text, ok := strings.CutPrefix(line, "data: "); ok && text != "[DONE]" {
					collect(decode(text), 0)
				}
			}
			if payload.Multipart != nil {
				for _, part := range payload.Multipart.Parts {
					collect(part.Value, 0)
				}
			}
		}
	}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		return wirefixture.Policy{}, err
	}
	return policy, nil
}

func cloneBool(source map[string]bool) map[string]bool {
	out := make(map[string]bool, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

// SaveBundle writes a bundle as private JSON (mode 0600).
func SaveBundle(path string, bundle *Bundle) error {
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// LoadBundle reads a bundle written by SaveBundle.
func LoadBundle(path string) (*Bundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bundle Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}
	if bundle.Provider == "" || len(bundle.Obs) == 0 {
		return nil, errors.New("align: bundle is empty")
	}
	return &bundle, nil
}

// SortedKeys is a small helper for deterministic output.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// DynamicString reports whether a value must never be committed as it is.
func DynamicString(value string) bool { return dynamicString(value) }

// credentialKey reports field names that carry a secret or a signed token.
func credentialKey(key string) bool {
	compact := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	for _, marker := range []string{"key", "token", "secret", "password", "signature", "authorization", "credential"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// Set only when building the opt-in test binary, never by runtime configuration.
var wireCaptureSourceSHA string

type wireCaptureManifest struct {
	Address         string                      `json:"address"`
	SourceSHA       string                      `json:"source_sha"`
	CatalogSHA256   string                      `json:"catalog_sha256"`
	WebAssetsRoot   string                      `json:"web_assets_root"`
	Scenario        string                      `json:"scenario"`
	PolicyName      string                      `json:"policy_name"`
	DurationSeconds int                         `json:"duration_seconds"`
	Connections     []string                    `json:"connections"`
	Policy          wirefixture.Policy          `json:"policy"`
	DeferredLimits  *wirefixture.DeferredLimits `json:"deferred_limits,omitempty"`
}

type wireCapture struct {
	config      Config
	recorder    *wirefixture.Recorder
	selected    map[string]bool
	worktree    string
	run         string
	duration    time.Duration
	deferred    bool
	policy      wirefixture.Policy
	policyName  string
	connections map[string]string
}

type wireCaptureTransport func(*http.Request) (*http.Response, error)

func (f wireCaptureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func wireCapturePrivateFile(path string, limit int64) ([]byte, error) {
	dir, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("wire capture: private directory unavailable")
	}
	defer dir.Close()
	name := filepath.Base(path)
	info, err := dir.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > limit {
		return nil, errors.New("wire capture: expected bounded private regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("wire capture: file must be caller-owned")
	}
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("wire capture: cannot open private file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("wire capture: private file identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("wire capture: cannot read private file")
	}
	return data, nil
}

func prepareWireCapture(worktree, root, ack, sourceSHA string, lookup func(string) string) (*wireCapture, error) {
	if ack != "isolated-wire-capture-only" {
		return nil, errors.New("wire capture: explicit acknowledgement required")
	}
	sha, err := hex.DecodeString(sourceSHA)
	if err != nil || len(sha) != 20 || sourceSHA != strings.ToLower(sourceSHA) {
		return nil, errors.New("wire capture: source-bound test binary required")
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("wire capture: absolute private root required")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("wire capture: real 0700 root required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("wire capture: root must be caller-owned")
	}
	store, err := wirefixture.NewPrivateStore(worktree, root)
	if err != nil {
		return nil, errors.New("wire capture: private root isolation failed")
	}
	defer store.Close()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("wire capture: root identity unavailable")
	}
	manifestBytes, err := wireCapturePrivateFile(filepath.Join(root, "capture.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	var m wireCaptureManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(new(any)) != io.EOF || m.SourceSHA != sourceSHA {
		return nil, errors.New("wire capture: invalid or mismatched manifest")
	}
	address, err := netip.ParseAddrPort(m.Address)
	if err != nil || !address.Addr().IsLoopback() || address.Addr().Zone() != "" || address.Port() == 0 {
		return nil, errors.New("wire capture: explicit loopback IP and nonzero port required")
	}
	if m.PolicyName == "" || m.Policy.MaxBodyBytes <= 0 || m.Policy.MaxBodyBytes > 8<<20 || len(m.Policy.Keys) == 0 || len(m.Policy.Allowed) == 0 || len(m.Connections) == 0 {
		return nil, errors.New("wire capture: registered bounded policy and connections required")
	}
	if m.DurationSeconds < 1 || m.DurationSeconds > 3600 {
		return nil, errors.New("wire capture: duration must be between 1 and 3600 seconds")
	}
	if limits := m.DeferredLimits; limits != nil && (limits.MaxBytes <= 0 || limits.MaxBytes > 64<<20 || limits.MaxExchanges <= 0 || limits.MaxExchanges > 1024) {
		return nil, errors.New("wire capture: deferred limits require 1–67108864 bytes and 1–1024 exchanges")
	}
	catalogBytes, err := wireCapturePrivateFile(filepath.Join(root, "catalog.json"), 16<<20)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(catalogBytes)
	if hex.EncodeToString(digest[:]) != m.CatalogSHA256 {
		return nil, errors.New("wire capture: catalog digest mismatch")
	}
	product, err := catalog.Load(bytes.NewReader(catalogBytes))
	if err != nil {
		return nil, errors.New("wire capture: invalid catalog")
	}
	schema := product.Application()
	config, err := loadConfigFromSchema(schema, lookup)
	if err != nil || config.ProviderMode != "live" || config.AgentMode != "live" {
		return nil, errors.New("wire capture: valid live Provider and Agent configuration required")
	}
	if !filepath.IsAbs(m.WebAssetsRoot) {
		return nil, errors.New("wire capture: explicit built frontend required")
	}
	if info, err := os.Stat(filepath.Join(m.WebAssetsRoot, "index.html")); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("wire capture: built frontend unavailable")
	}
	known := map[string]string{}
	for _, c := range schema.Runtime.ProviderConnections {
		if c.Protocol == "local-model" || c.Protocol == "local-recovery" || c.Protocol == "http-json" || c.Protocol == "openai-compatible" {
			continue
		}
		if configuredRuntimeSetting(config, schema, c.APIKeySetting) != "" {
			known[c.ID] = configuredRuntimeSetting(config, schema, c.BaseURLSetting)
			if c.Protocol == "tripo" {
				known[c.ID+".native-source"] = known[c.ID]
			}
		}
	}
	for _, c := range chatConnections(config, schema) {
		if c.APIKey != "" && c.Model != "" {
			known[c.ID] = c.BaseURL
		}
	}
	meta := wirefixture.Metadata{Scenario: m.Scenario, AppRevision: sourceSHA, CatalogRevision: m.CatalogSHA256,
		Environment: "isolated-wire-capture", Provenance: wirefixture.ProvenanceCaptured, CapturedAt: time.Now().UTC()}
	selected := map[string]bool{}
	connections := map[string]string{}
	for _, id := range m.Connections {
		baseURL, exists := known[id]
		u, parseErr := url.Parse(baseURL)
		if !exists || selected[id] || parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return nil, errors.New("wire capture: unknown, duplicate, unavailable or unsafe connection")
		}
		selected[id], connections[id] = true, baseURL
	}
	if err := wirefixture.ValidatePolicy(m.Policy); err != nil {
		return nil, errors.New("wire capture: invalid policy registration")
	}
	if m.DeferredLimits == nil && len(m.Policy.Projection) > 0 {
		return nil, errors.New("wire capture: projection requires deferred capture")
	}
	if err := wireCapturePolicyPreflight(m.Policy, m.PolicyName, meta, connections); err != nil {
		return nil, err
	}
	run := filepath.Join(root, "run")
	if err := os.Mkdir(run, 0700); err != nil {
		return nil, errors.New("wire capture: fresh run directory required; previous evidence is never overwritten")
	}
	for _, name := range []string{"assets", "generated", "legacy-input", "legacy-cache", "proxy-cache", "thumbnails", "evidence"} {
		if os.Mkdir(filepath.Join(run, name), 0700) != nil {
			return nil, errors.New("wire capture: isolated storage creation failed")
		}
	}
	if os.WriteFile(filepath.Join(run, "catalog.json"), catalogBytes, 0600) != nil || os.WriteFile(filepath.Join(run, "capture.json"), manifestBytes, 0600) != nil {
		return nil, errors.New("wire capture: freeze configuration failed")
	}
	config.CatalogFile, config.SkillCatalogFile = filepath.Join(run, "catalog.json"), ""
	config.DatabasePath, config.StorageRoot = filepath.Join(run, "capture.db"), filepath.Join(run, "assets")
	config.LegacyGeneratedImagesRoot = filepath.Join(run, "generated")
	config.WebAssetsRoot, config.Address = m.WebAssetsRoot, m.Address
	config.Environment, config.AuthMode = "development", "development"
	config.DevelopmentUser, config.DevelopmentRoles = "wire-capture-owner", nil
	config.MockAuth, config.MockUserJSON = true, `{"id":"wire-capture-owner","name":"Wire capture","email":"capture@example.invalid","roles":[]}`
	config.AuthTokens, config.TrustedProxies, config.AdminAllowlist = nil, nil, nil
	config.AdminUsername, config.AdminPassword, config.AdminName, config.AdminEmail = "", "", "", ""
	config.AdminPasswordReset = false
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, errors.New("wire capture: isolated session secret failed")
	}
	config.SessionSecret = hex.EncodeToString(secret)
	config.FrontendPublicAppURL, config.PublicBaseURL, config.AssetSigningKey = "http://"+m.Address, "", ""
	config.RuntimeSettings["public_base_url"], config.RuntimeSettings["asset_signing_key"] = "", ""
	config.RuntimeSettings["session_cookie_name"] = "plinth_wire_capture"
	for name, directory := range map[string]string{
		schema.Runtime.InputAssets.LegacyStorageRootSetting:             "legacy-input",
		schema.Administration.Generations.Assets.LegacyCacheRootSetting: "legacy-cache",
		schema.Runtime.AssetDelivery.Proxy.Cache.RootSetting:            "proxy-cache",
		schema.Runtime.AssetDelivery.Proxy.Thumbnail.RootSetting:        "thumbnails",
	} {
		if name != "" {
			config.RuntimeSettings[name] = filepath.Join(run, directory)
		}
	}
	config.Logging.Directory = run
	if config.Validate() != nil {
		return nil, errors.New("wire capture: isolated configuration invalid")
	}
	c := &wireCapture{config: config, selected: selected, connections: connections, policy: m.Policy, policyName: m.PolicyName, worktree: worktree, run: run, duration: time.Duration(m.DurationSeconds) * time.Second, deferred: m.DeferredLimits != nil}
	if c.deferred {
		var evidence *wirefixture.PrivateStore
		evidence, err = wirefixture.NewPrivateStore(worktree, filepath.Join(run, "evidence"))
		if err == nil {
			c.recorder, err = wirefixture.NewDeferredRecorderWithEvidence(meta, *m.DeferredLimits, evidence)
		}
	} else {
		var evidence *wirefixture.PrivateStore
		evidence, err = wirefixture.NewPrivateStore(worktree, filepath.Join(run, "evidence"))
		c.recorder = &wirefixture.Recorder{Policy: m.Policy, Default: meta, Evidence: evidence}
	}
	if err != nil {
		return nil, errors.New("wire capture: recorder unavailable")
	}
	return c, nil
}

func wireCapturePolicyPreflight(policy wirefixture.Policy, name string, meta wirefixture.Metadata, connections map[string]string) error {
	// Exercise the sanitizer with a separate synthetic recorder; these probes
	// never enter the captured exchanges and never use a network transport.
	// Projection only applies during deferred Finalize, so the probe uses the
	// registered structure and public values without invented raw mappings.
	probePolicy := policy
	probePolicy.Projection = nil
	keys := map[string]any{}
	values := []string{name}
	for key, enabled := range policy.Keys {
		if !enabled {
			return errors.New("wire capture: disabled policy registration")
		}
		keys[key] = nil
	}
	for value, enabled := range policy.Allowed {
		if !enabled {
			return errors.New("wire capture: disabled policy registration")
		}
		values = append(values, value)
	}
	publicCount := len(values)
	for raw := range policy.Aliases {
		values = append(values, raw)
	}
	probeBytes, _ := json.Marshal([]any{keys, values})
	for id, baseURL := range connections {
		probeMeta := meta
		probeMeta.Provenance = wirefixture.ProvenanceSynthetic
		probe := &wirefixture.Recorder{Policy: probePolicy, Default: probeMeta}
		request, _ := http.NewRequest(http.MethodGet, baseURL, nil)
		response, err := probe.Wrap(id, wireCaptureTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(probeBytes)), ContentLength: int64(len(probeBytes))}, nil
		})).RoundTrip(request)
		if err != nil {
			return errors.New("wire capture: policy preflight failed")
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if probe.AssertComplete() != nil {
			return errors.New("wire capture: policy or metadata preflight failed")
		}
		var sanitized []json.RawMessage
		var publicValues []string
		if json.Unmarshal(probe.Exchanges()[0].Response.Body.JSON, &sanitized) != nil || len(sanitized) != 2 || json.Unmarshal(sanitized[1], &publicValues) != nil || len(publicValues) != len(values) {
			return errors.New("wire capture: policy preflight representation failed")
		}
		for i := 0; i < publicCount; i++ {
			if publicValues[i] != values[i] {
				return errors.New("wire capture: public registration requires sanitization")
			}
		}
	}
	return nil
}

func (c *wireCapture) close() {
	// A completed export keeps its private evidence for human review. Failed
	// rounds are released by export before this runs.
	if c.recorder.Evidence != nil {
		_ = c.recorder.Evidence.Close()
	}
}

func (c *wireCapture) assemble(logger *slog.Logger, offline http.RoundTripper) (*Application, error) {
	seen := map[string]bool{}
	application, err := newWithDependencies(c.config, logger, assemblyOverrides{httpTransport: func(id string, base http.RoundTripper) http.RoundTripper {
		if offline != nil {
			base = offline
		}
		if !c.selected[id] {
			return base
		}
		seen[id] = true
		return c.recorder.Wrap(id, base)
	}})
	if err != nil {
		if c.deferred {
			_ = c.recorder.Release()
		}
		c.close()
		return nil, errors.New("wire capture: application assembly failed")
	}
	for id := range c.selected {
		if !seen[id] {
			_ = application.Close()
			if c.deferred {
				_ = c.recorder.Release()
			}
			c.close()
			return nil, errors.New("wire capture: selected connection was not assembled")
		}
	}
	return application, nil
}

func wireCaptureMediaRecipes() map[string]string {
	return map[string]string{
		"image/png": "png-1x1", "image/jpeg": "jpeg-1x1",
		"model/gltf-binary": "glb-triangle", "model/gltf+json": "gltf-json",
		"model/obj": "obj-triangle", "model/stl": "stl-triangle", "model/fbx": "fbx-triangle",
		"model/vnd.usdz+zip": "usdz-empty", "model/3mf": "3mf-empty",
		"application/zip": "zip-empty", "application/octet-stream": "octet-empty",
		"application/x-tgif": "obj-triangle", "text/plain": "text-plain",
		"application/mtl": "text-plain", "application/sla": "stl-triangle",
		"application/vnd.autodesk.fbx": "fbx-triangle", "application/x-fbx": "fbx-triangle",
		"application/vnd.ms-package.3dmanufacturing-3dmodel+xml": "3mf-empty",
		"application/x-3mf": "3mf-empty", "application/x-usdz": "usdz-empty",
		"application/x-obj": "obj-triangle", "model/mtl": "text-plain",
		"image/webp": "webp-1x1",
	}
}

func wireCaptureMediaExtensions() map[string]string {
	return map[string]string{
		".glb": "glb-triangle", ".gltf": "gltf-json", ".obj": "obj-triangle",
		".stl": "stl-triangle", ".fbx": "fbx-triangle", ".usdz": "usdz-empty",
		".3mf": "3mf-empty", ".zip": "zip-empty", ".png": "png-1x1",
		".jpg": "jpeg-1x1", ".jpeg": "jpeg-1x1", ".mtl": "text-plain", ".txt": "text-plain",
		".webp": "webp-1x1",
	}
}

func (c *wireCapture) export(runErr error) (err error) {
	defer func() {
		if err != nil && c.deferred {
			_ = c.recorder.Release()
		}
	}()
	if runErr != nil {
		return errors.New("wire capture: unclean shutdown; no candidate exported")
	}
	policy := c.recorder.Policy
	if c.deferred {
		policy, err = c.finalPolicy()
		if err != nil {
			return errors.New("wire capture: final policy rejected; no candidate exported")
		}
		if err := c.recorder.Finalize(policy); err != nil {
			return fmt.Errorf("wire capture: finalize rejected: %w", err)
		}
	}
	if c.recorder.AssertComplete() != nil {
		return errors.New("wire capture: incomplete or unsafe capture; no candidate exported")
	}
	exchanges := c.recorder.Exchanges()
	evidence := c.recorder.MediaEvidence()
	if len(evidence) > 0 {
		if err := wirefixture.WriteEvidenceManifest(filepath.Join(c.run, "evidence-manifest.json"), evidence); err != nil {
			return errors.New("wire capture: private evidence manifest failed")
		}
		exchanges, err = wirefixture.SubstituteMediaWithExtensions(exchanges, wireCaptureMediaRecipes(), wireCaptureMediaExtensions())
		if err != nil {
			return errors.New("wire capture: unsupported captured media; no candidate exported")
		}
	}
	seen := map[string]bool{}
	for _, exchange := range exchanges {
		seen[exchange.Metadata.Connection] = true
	}
	for id := range c.selected {
		if !seen[id] {
			return errors.New("wire capture: selected connection has no exchanges; no candidate exported")
		}
	}
	if c.deferred {
		policy, err = wireCaptureReplayPolicy(exchanges, wirefixture.Policy{
			MaxBodyBytes: policy.MaxBodyBytes, Keys: c.policy.Keys, Allowed: c.policy.Allowed,
			PublicHosts: c.policy.PublicHosts, QueryKeys: c.policy.QueryKeys,
		})
		if err != nil {
			return errors.New("wire capture: public replay policy rejected")
		}
		cleanup, writeErr := c.writeReplayPolicy(policy)
		if writeErr != nil {
			return errors.New("wire capture: public replay policy export failed")
		}
		defer func() {
			cleanup(err != nil)
		}()
	}
	if wirefixture.WriteCandidate(c.worktree, filepath.Join(c.run, "candidate.json"), exchanges, policy) != nil {
		return errors.New("wire capture: safe candidate export failed")
	}
	return nil
}

func (c *wireCapture) finalPolicy() (wirefixture.Policy, error) {
	root := filepath.Dir(c.run)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return wirefixture.Policy{}, errors.New("wire capture: private root unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return wirefixture.Policy{}, errors.New("wire capture: private root owner changed")
	}
	store, err := wirefixture.NewPrivateStore(c.worktree, root)
	if err != nil {
		return wirefixture.Policy{}, errors.New("wire capture: final policy isolation failed")
	}
	defer store.Close()
	data, err := wireCapturePrivateFile(filepath.Join(root, "final-policy.json"), 1<<20)
	if err != nil {
		return wirefixture.Policy{}, err
	}
	var policy wirefixture.Policy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF ||
		!reflect.DeepEqual(policy.Keys, c.policy.Keys) ||
		!reflect.DeepEqual(policy.PublicHosts, c.policy.PublicHosts) ||
		!reflect.DeepEqual(policy.Projection, c.policy.Projection) ||
		!reflect.DeepEqual(policy.QueryKeys, c.policy.QueryKeys) ||
		policy.MaxBodyBytes <= 0 || policy.MaxBodyBytes > c.policy.MaxBodyBytes {
		return wirefixture.Policy{}, errors.New("wire capture: invalid final policy or expanded structure/body limit")
	}
	for value, enabled := range c.policy.Allowed {
		if policy.Allowed[value] != enabled {
			return wirefixture.Policy{}, errors.New("wire capture: original public registration changed")
		}
	}
	for raw, target := range c.policy.Aliases {
		if policy.Aliases[raw] != target {
			return wirefixture.Policy{}, errors.New("wire capture: original alias changed")
		}
	}
	if err := wireCapturePolicyPreflight(policy, c.policyName, c.recorder.Default, c.connections); err != nil {
		return wirefixture.Policy{}, err
	}
	return policy, nil
}

// Only already-validated public payloads supply new terminal values, never raw input.
func wireCaptureReplayPolicy(exchanges []wirefixture.Exchange, public wirefixture.Policy) (wirefixture.Policy, error) {
	public.Keys, public.Allowed, public.Aliases = maps.Clone(public.Keys), maps.Clone(public.Allowed), nil
	public.Projection = nil
	var collect func(any, int)
	decode := func(text string) any {
		var value any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil
		}
		return value
	}
	collect = func(value any, depth int) {
		if depth > 12 {
			return // Validate below rejects any missing terminal registration.
		}
		switch value := value.(type) {
		case map[string]any:
			for _, child := range value {
				collect(child, depth+1)
			}
		case []any:
			for _, child := range value {
				collect(child, depth+1)
			}
		case string:
			public.Allowed[value] = true
			if nested := decode(value); nested != nil {
				switch nested.(type) {
				case map[string]any, []any:
					collect(nested, depth+1)
				}
			}
		case json.Number:
			public.Allowed[value.String()] = true
		}
	}
	for _, exchange := range exchanges {
		for _, part := range strings.Split(exchange.Request.Path, "/") {
			if part != "" {
				public.Allowed[part] = true
			}
		}
		for _, headers := range []map[string]string{exchange.Request.Headers, exchange.Response.Headers} {
			for _, value := range headers {
				public.Allowed[value] = true
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
	return public, wirefixture.Validate(exchanges, public)
}

func (c *wireCapture) writeReplayPolicy(policy wirefixture.Policy) (func(bool), error) {
	store, err := wirefixture.NewPrivateStore(c.worktree, c.run)
	if err != nil {
		return nil, errors.New("wire capture: replay policy isolation failed")
	}
	defer store.Close()
	dir, err := os.OpenRoot(c.run)
	if err != nil {
		return nil, errors.New("wire capture: replay policy directory unavailable")
	}
	const name = "replay-policy.json"
	created := false
	cleanup := func(remove bool) {
		if remove && created {
			_ = dir.Remove(name)
		}
		_ = dir.Close()
	}
	data, err := json.Marshal(policy)
	if err != nil || len(data) > 1<<20 {
		cleanup(false)
		return nil, errors.New("wire capture: replay policy encoding failed")
	}
	file, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		cleanup(false)
		return nil, errors.New("wire capture: replay policy create failed")
	}
	created = true
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	info, statErr := dir.Lstat(name)
	if writeErr != nil || closeErr != nil || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		cleanup(true)
		return nil, errors.New("wire capture: replay policy write failed")
	}
	return cleanup, nil
}

func TestWireCaptureServer(t *testing.T) {
	ack := os.Getenv("ART_AI_WIRE_CAPTURE_ACK")
	if ack == "" {
		t.Skip("interactive capture requires explicit opt-in")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(source) {
		t.Fatal("wire capture: untrimmed source location required")
	}
	capture, err := prepareWireCapture(filepath.Dir(filepath.Dir(filepath.Dir(source))), os.Getenv("ART_AI_WIRE_CAPTURE_ROOT"), ack, wireCaptureSourceSHA, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.close()
	logFile, err := os.OpenFile(filepath.Join(capture.run, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("wire capture: isolated log creation failed")
	}
	defer logFile.Close()
	// App errors can include upstream content: keep only event time and level.
	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
			return a
		}
		return slog.Attr{}
	}}))
	application, err := capture.assemble(logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	application.server.ErrorLog = log.New(io.Discard, "", 0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(capture.duration, cancel)
	defer timer.Stop()
	// Run returns only after HTTP and background workers have stopped. A failed
	// business task can make Run non-nil; it does not leave requests running, so
	// keep the evidence, record the reason, and let Finalize decide completeness.
	if runErr := application.Run(ctx); runErr != nil {
		_ = os.WriteFile(filepath.Join(capture.run, "shutdown-warning.txt"), []byte(runErr.Error()), 0600)
	}
	if err := capture.export(nil); err != nil {
		// Fixed reasons and schema field names only; the private root stays outside Git.
		_ = os.WriteFile(filepath.Join(capture.run, "export-error.txt"), []byte(err.Error()), 0600)
		t.Fatal(err)
	}
	t.Log("wire capture: complete candidate exported; privacy review still required")
}

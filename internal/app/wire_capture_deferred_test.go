package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

const wireCaptureSyntheticJSON = `{"id":"dynamic-job-98765","choices":[{"message":{"role":"assistant","content":"private generated text"}}]}`
const wireCaptureSyntheticSSE = "data: {\"id\":\"dynamic-job-98765\",\"created\":1730000000,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"private generated text\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func wireCaptureDeferredFixture(t *testing.T) (string, string, wireCaptureManifest, map[string]string) {
	t.Helper()
	worktree, root, m, env := wireCaptureFixture(t)
	m.DeferredLimits = &wirefixture.DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 8}
	env["LLM_MODEL"] = "offline-model"
	for _, key := range strings.Fields("model messages role content type text image_url url detail stream temperature id created choices message delta index finish_reason") {
		m.Policy.Keys[key] = true
	}
	for _, value := range []string{"offline-model", "user", "assistant", "text", "image_url", "low", "0", "stop"} {
		m.Policy.Allowed[value] = true
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	probe := product.Application().LLMSettings.Probe
	// These are known synthetic inputs, not strings discovered in captured raw data.
	m.Policy.Aliases[probe.Prompt] = "[redacted]"
	m.Policy.Aliases[probePicture(probe.ImageSize)] = "[redacted]"
	return worktree, root, m, env
}

func wireCaptureExactSyntheticPolicy(m wireCaptureManifest) wirefixture.Policy {
	p := m.Policy
	p.Keys, p.Allowed, p.Aliases = maps.Clone(p.Keys), maps.Clone(p.Allowed), maps.Clone(p.Aliases)
	p.MaxBodyBytes /= 2 // The final policy may tighten, but never raise, this limit.
	p.Allowed["call-1"], p.Allowed["public result"] = true, true
	p.Aliases["dynamic-job-98765"] = "call-1"
	p.Aliases["private generated text"] = "public result"
	p.Aliases["1730000000"] = "0"
	return p
}

func wireCaptureCreatePrivate(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("private fixture write: %v %v", writeErr, closeErr)
	}
}

func wireCaptureWriteFinal(t *testing.T, root string, policy wirefixture.Policy) {
	t.Helper()
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	wireCaptureCreatePrivate(t, filepath.Join(root, "final-policy.json"), data)
}

func wireCaptureCollectDeferred(t *testing.T, c *wireCapture, firstJSON, secondSSE string) *int {
	t.Helper()
	c.recorder.Default.Provenance = wirefixture.ProvenanceSynthetic
	// Only offline tests grant admin to use the real connection-probe HTTP route.
	c.config.DevelopmentRoles, c.config.DevelopmentUser, c.config.MockAuth = []string{"admin"}, "owner-a", false
	c.config.Address = "127.0.0.1:0"
	calls := 0
	application, err := c.assemble(discardLogger(), wireCaptureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 2 || r.Method != http.MethodPost || r.URL.String() != "https://gateway.example.test/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer offline-test-key" {
			t.Fatal("unexpected request through actual app transport seam")
		}
		requestBody, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Contains(requestBody, []byte(`"model":"offline-model"`)) {
			t.Fatal("business request changed")
		}
		if err := r.Body.Close(); err != nil {
			t.Fatal(err)
		}
		body, contentType, status := firstJSON, "application/json", http.StatusOK
		if calls == 2 {
			body, contentType, status = secondSSE, "text/event-stream", http.StatusInternalServerError
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	for i := 0; i < 2; i++ {
		response := appChatRequest(application.server.Handler, http.MethodPost, application.schema.LLMSettings.Routes.Test, `{"connection_id":"LLM"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("probe route failed: %d", response.Code)
		}
		// A synthetic 500 makes the real adapter consume the entire SSE error
		// body. A 200 SSE sent to its JSON decoder would stop at the first chunk.
		if i == 0 && firstJSON == wireCaptureSyntheticJSON && !strings.Contains(response.Body.String(), `"reachable":true`) {
			t.Fatal("synthetic JSON business result was changed")
		}
		if secondSSE == wireCaptureSyntheticSSE && len(c.recorder.Failures()) != 0 {
			t.Fatalf("synthetic interaction %d failed: %v", i, c.recorder.Failures())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(c.recorder.Exchanges()) != 0 || c.recorder.AssertComplete() == nil || c.recorder.Evidence == nil {
		t.Fatal("deferred capture published early, retried, or lacked its private evidence store")
	}
	if secondSSE == wireCaptureSyntheticSSE && len(c.recorder.Failures()) != 0 {
		t.Fatalf("synthetic app capture failed before policy validation: %v", c.recorder.Failures())
	}
	return &calls
}

func TestWireCaptureDeferredLatePolicyAndPublicReplay(t *testing.T) {
	worktree, root, m, env := wireCaptureDeferredFixture(t)
	c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "final-policy.json")); !os.IsNotExist(err) {
		t.Fatal("startup required or created a final policy")
	}
	calls := wireCaptureCollectDeferred(t, c, wireCaptureSyntheticJSON, wireCaptureSyntheticSSE)
	// Dynamic mappings are supplied only after the real app has consumed both
	// synthetic interactions and closed. They are not inferred from raw evidence.
	wireCaptureWriteFinal(t, root, wireCaptureExactSyntheticPolicy(m))
	if err := c.export(nil); err != nil {
		t.Fatal(err)
	}
	var exported []wirefixture.Exchange
	var public wirefixture.Policy
	for name, target := range map[string]any{"candidate.json": &exported, "replay-policy.json": &public} {
		data, err := wireCapturePrivateFile(filepath.Join(c.run, name), 1<<20)
		if err != nil || json.Unmarshal(data, target) != nil {
			t.Fatalf("invalid protected export: %v", err)
		}
		for _, raw := range []string{"gateway.example.test", "dynamic-job-98765", "private generated text", "1730000000", "offline-test-key", "data:image/png"} {
			if bytes.Contains(data, []byte(raw)) {
				t.Fatal("private value leaked into public export")
			}
		}
	}
	if len(public.Aliases) != 0 || !maps.Equal(public.Keys, m.Policy.Keys) || public.MaxBodyBytes != m.Policy.MaxBodyBytes/2 || public.Allowed["1730000000"] || !public.Allowed["public result"] || !public.Allowed["call-1"] {
		t.Fatal("replay policy did not retain only safe registrations")
	}
	if wirefixture.Validate(exported, public) != nil || len(exported) != 2 || exported[0].Sequence != 1 || exported[1].Sequence != 2 || exported[1].Response.Body.Transform != wirefixture.TransformNormalizedSSE || exported[0].Metadata.Provenance != wirefixture.ProvenanceSynthetic {
		t.Fatal("invalid synthetic candidate")
	}
	const publicRequest = `{"messages":[{"content":[{"text":"[redacted]","type":"text"},{"image_url":{"detail":"low","url":"[redacted]"},"type":"image_url"}],"role":"user"}],"model":"offline-model","stream":false,"temperature":0}`
	const publicJSON = `{"choices":[{"message":{"content":"public result","role":"assistant"}}],"id":"call-1"}`
	const publicSSE = "data: {\"choices\":[{\"delta\":{\"content\":\"public result\",\"role\":\"assistant\"},\"finish_reason\":\"stop\",\"index\":0}],\"created\":0,\"id\":\"call-1\"}\n\ndata: [DONE]\n\n"
	if string(exported[0].Request.Body.JSON) != publicRequest || string(exported[1].Request.Body.JSON) != publicRequest || string(exported[0].Response.Body.JSON) != publicJSON || exported[1].Response.Body.SSE != publicSSE {
		t.Fatal("candidate differs from independently specified synthetic public bytes")
	}
	replay, err := wirefixture.NewReplay(exported, public)
	if err != nil {
		t.Fatal(err)
	}
	for index, expectedBody := range []string{publicJSON, publicSSE} {
		request, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat/completions", strings.NewReader(publicRequest))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := replay.WithConnection("LLM").RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil || string(body) != expectedBody || response.StatusCode != []int{200, 500}[index] {
			t.Fatal("replay changed the synthetic response")
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if replay.AssertConsumed() != nil || *calls != 2 {
		t.Fatal("offline replay or export caused an extra transport call")
	}
}

func TestWireCaptureDeferredPrepareGuards(t *testing.T) {
	for _, limits := range []wirefixture.DeferredLimits{{}, {MaxBytes: 0, MaxExchanges: 1}, {MaxBytes: 1, MaxExchanges: 0}, {MaxBytes: -1, MaxExchanges: 1}, {MaxBytes: 1, MaxExchanges: -1}, {MaxBytes: 64<<20 + 1, MaxExchanges: 1}, {MaxBytes: 1, MaxExchanges: 1025}} {
		t.Run("limits", func(t *testing.T) {
			worktree, root, m, env := wireCaptureDeferredFixture(t)
			m.DeferredLimits = &limits
			if _, err := wireCapturePrepareFixture(t, worktree, root, m, env); err == nil {
				t.Fatal("invalid deferred limits accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "run")); !os.IsNotExist(err) {
				t.Fatal("invalid limits created runtime storage")
			}
		})
	}
	for _, mode := range []string{"unsafe-policy", "unregistered-host", "disabled-key"} {
		t.Run(mode, func(t *testing.T) {
			worktree, root, m, env := wireCaptureDeferredFixture(t)
			switch mode {
			case "unsafe-policy":
				m.Policy.Allowed["Bearer abcdefghijklmnop"] = true
			case "unregistered-host":
				env["LLM_BASE_URL"] = "https://unregistered.test/v1"
			case "disabled-key":
				m.Policy.Keys["ok"] = false
			}
			if _, err := wireCapturePrepareFixture(t, worktree, root, m, env); err == nil {
				t.Fatal("deferred mode bypassed original startup preflight")
			}
		})
	}
}

func TestWireCaptureDeferredAssemblyFailureReleases(t *testing.T) {
	worktree, root, m, env := wireCaptureDeferredFixture(t)
	c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
	if err != nil {
		t.Fatal(err)
	}
	c.selected["unassembled"] = true
	calls := 0
	application, err := c.assemble(discardLogger(), wireCaptureTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("offline test forbids upstream traffic")
	}))
	if err == nil || application != nil || calls != 0 || len(c.recorder.Failures()) != 1 || !errors.Is(c.recorder.Failures()[0], wirefixture.ErrCaptureReleased) {
		t.Fatal("failed assembly did not release deferred capture without traffic")
	}
}

func TestWireCaptureDeferredFinalPolicyGuardsAndRelease(t *testing.T) {
	for _, mode := range []string{"missing", "permissions", "oversized", "unknown-field", "trailing-json", "symlink", "keys-added", "keys-removed", "body-increased", "body-zero", "allowed-removed", "alias-changed", "unsafe-allowed", "unsafe-alias", "alias-chain", "disabled-allowed", "nested-unknown-sensitive", "unknown-value", "unknown-number", "unknown-key", "unknown-sensitive-key", "partial-sse", "pending", "late", "shutdown", "existing-policy", "policy-symlink", "existing-candidate", "public-run"} {
		t.Run(mode, func(t *testing.T) {
			worktree, root, m, env := wireCaptureDeferredFixture(t)
			c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
			if err != nil {
				t.Fatal(err)
			}
			firstJSON, secondSSE := wireCaptureSyntheticJSON, wireCaptureSyntheticSSE
			switch mode {
			case "unknown-key":
				firstJSON = `{"unregistered":true}`
			case "unknown-sensitive-key":
				firstJSON = `{"provider_auth_token":"private generated text"}`
			case "partial-sse":
				secondSSE = strings.TrimSuffix(secondSSE, "data: [DONE]\n\n")
			}
			calls := wireCaptureCollectDeferred(t, c, firstJSON, secondSSE)
			policy := wireCaptureExactSyntheticPolicy(m)
			switch mode {
			case "keys-added":
				policy.Keys["provider_auth_token"] = true
			case "keys-removed":
				delete(policy.Keys, "ok")
			case "body-increased":
				policy.MaxBodyBytes = m.Policy.MaxBodyBytes + 1
			case "body-zero":
				policy.MaxBodyBytes = 0
			case "allowed-removed":
				delete(policy.Allowed, "guard-v1")
			case "alias-changed":
				policy.Aliases["gateway.example.test"] = "[redacted]"
			case "unsafe-allowed":
				policy.Allowed["secret=abcdefg"] = true
			case "unsafe-alias":
				policy.Aliases["unused-private"] = "https://10.0.0.1/private"
			case "alias-chain":
				policy.Aliases["unused-private"] = "dynamic-job-98765"
			case "disabled-allowed":
				policy.Allowed["unused-value"] = false
			case "nested-unknown-sensitive":
				policy.Aliases["unused-private"] = `{"provider_auth_token":"private generated text"}`
			case "unknown-value":
				delete(policy.Aliases, "private generated text")
			case "unknown-number":
				delete(policy.Aliases, "1730000000")
			}
			data, err := json.Marshal(policy)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "oversized":
				data = bytes.Repeat([]byte(" "), 1<<20+1)
			case "unknown-field":
				data = append(data[:len(data)-1], []byte(`,"unregistered":true}`)...)
			case "trailing-json":
				data = append(data, []byte(" {}")...)
			}
			path := filepath.Join(root, "final-policy.json")
			if mode != "missing" {
				if mode == "symlink" {
					target := filepath.Join(t.TempDir(), "external-policy.json")
					wireCaptureCreatePrivate(t, target, data)
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				} else {
					wireCaptureCreatePrivate(t, path, data)
				}
			}
			if mode == "permissions" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "public-run" {
				if err := os.Chmod(c.run, 0755); err != nil {
					t.Fatal(err)
				}
			}
			var pending *http.Response
			if mode == "pending" || mode == "late" {
				if mode == "late" && c.recorder.Finalize(policy) != nil {
					t.Fatal("late-request fixture did not finalize")
				}
				request, _ := http.NewRequest(http.MethodGet, "https://gateway.example.test/v1", nil)
				pending, err = c.recorder.Wrap("LLM", wireCaptureTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), ContentLength: 11}, nil
				})).RoundTrip(request)
				if err != nil {
					t.Fatal(err)
				}
				defer pending.Body.Close()
			}
			preserved := ""
			if mode == "existing-policy" || mode == "existing-candidate" {
				name := "replay-policy.json"
				if mode == "existing-candidate" {
					name = "candidate.json"
				}
				preserved = filepath.Join(c.run, name)
				wireCaptureCreatePrivate(t, preserved, []byte("preserve"))
			}
			if mode == "policy-symlink" {
				preserved = filepath.Join(t.TempDir(), "target")
				wireCaptureCreatePrivate(t, preserved, []byte("preserve"))
				if err := os.Symlink(preserved, filepath.Join(c.run, "replay-policy.json")); err != nil {
					t.Fatal(err)
				}
			}
			var runErr error
			if mode == "shutdown" {
				runErr = errors.New("secret=abcdefg")
			}
			if err := c.export(runErr); err == nil || strings.Contains(err.Error(), "abcdefg") || strings.Contains(err.Error(), root) {
				t.Fatalf("unsafe export outcome: %v", err)
			}
			if len(c.recorder.Exchanges()) != 0 || len(c.recorder.Failures()) != 1 || c.recorder.AssertComplete() == nil || c.recorder.Finalize(wireCaptureExactSyntheticPolicy(m)) == nil {
				t.Fatal("failed export did not permanently release/invalidate capture")
			}
			if mode == "missing" || mode == "pending" || mode == "shutdown" {
				if !errors.Is(c.recorder.Failures()[0], wirefixture.ErrCaptureReleased) {
					t.Fatal("abandoned raw capture was not explicitly released")
				}
			}
			if pending != nil {
				body, err := io.ReadAll(pending.Body)
				if err != nil || string(body) != `{"ok":true}` || pending.Body.Close() != nil || len(c.recorder.Exchanges()) != 0 {
					t.Fatal("release consumed/closed a business body or restarted collection")
				}
			}
			if mode != "existing-candidate" {
				if _, err := os.Lstat(filepath.Join(c.run, "candidate.json")); !os.IsNotExist(err) {
					t.Fatal("failed export left a candidate")
				}
			}
			if mode != "existing-policy" && mode != "policy-symlink" {
				if _, err := os.Lstat(filepath.Join(c.run, "replay-policy.json")); !os.IsNotExist(err) {
					t.Fatal("failed export left a new replay policy")
				}
			}
			if preserved != "" {
				data, err := os.ReadFile(preserved)
				if err != nil || string(data) != "preserve" {
					t.Fatal("export overwrote existing content")
				}
			}
			if *calls != 2 {
				t.Fatal("export after app close retried the offline transport")
			}
		})
	}
}

func TestWireCaptureDeferredProjectionExport(t *testing.T) {
	worktree, root, m, env := wireCaptureFixture(t)
	m.Policy.Keys["id"] = true
	m.Policy.PublicHosts = map[string]bool{"gateway.example.test": true}
	m.Policy.Projection = map[string]string{"id": "id"}
	m.Policy.QueryKeys = map[string]bool{"X-Amz-Signature": true}
	m.DeferredLimits = &wirefixture.DeferredLimits{MaxBytes: 1 << 20, MaxExchanges: 8}
	c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
	if err != nil {
		t.Fatal(err)
	}
	policyBytes, err := json.Marshal(m.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "final-policy.json"), policyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://gateway.example.test/v1?X-Amz-Signature=secret", nil)
	response, err := c.recorder.Wrap("LLM", wireCaptureTransport(func(*http.Request) (*http.Response, error) {
		body := `{"id":"raw-task-1","ok":true}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	})).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.export(nil); err != nil {
		t.Fatal(err)
	}
	candidateBytes, err := os.ReadFile(filepath.Join(c.run, "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(candidateBytes), `"id-1"`) || strings.Contains(string(candidateBytes), "raw-task-1") ||
		strings.Contains(string(candidateBytes), "secret") {
		t.Fatalf("candidate = %s", candidateBytes)
	}
	replayBytes, err := os.ReadFile(filepath.Join(c.run, "replay-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var replayPolicy wirefixture.Policy
	if json.Unmarshal(replayBytes, &replayPolicy) != nil || replayPolicy.Projection != nil ||
		len(replayPolicy.PublicHosts) != 1 || !replayPolicy.PublicHosts["gateway.example.test"] ||
		!replayPolicy.QueryKeys["X-Amz-Signature"] {
		t.Fatalf("replay policy = %s", replayBytes)
	}
	var exchanges []wirefixture.Exchange
	if json.Unmarshal(candidateBytes, &exchanges) != nil {
		t.Fatal("candidate is invalid JSON")
	}
	replay, err := wirefixture.NewReplay(exchanges, replayPolicy)
	if err != nil {
		t.Fatal(err)
	}
	replayRequest, _ := http.NewRequest(http.MethodGet, "https://example.test/v1", nil)
	replayed, err := replay.RoundTrip(replayRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, replayed.Body)
	_ = replayed.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func wireCaptureFixture(t *testing.T) (string, string, wireCaptureManifest, map[string]string) {
	t.Helper()
	// Offline tests use a synthetic worktree because CI may bind-mount the checkout.
	worktree := t.TempDir()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	catalogBytes, err := os.ReadFile("../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), catalogBytes, 0600); err != nil {
		t.Fatal(err)
	}
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("offline fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(catalogBytes)
	m := wireCaptureManifest{Address: "127.0.0.1:18765", SourceSHA: strings.Repeat("a", 40), CatalogSHA256: hex.EncodeToString(digest[:]), WebAssetsRoot: web,
		Scenario: "offline-guard-test", PolicyName: "guard-v1", DurationSeconds: 60, Connections: []string{"LLM"}, Policy: wirefixture.Policy{
			MaxBodyBytes: 1 << 20, Keys: map[string]bool{"ok": true},
			Allowed: map[string]bool{"v1": true, "chat": true, "completions": true, "guard-v1": true, "example.test": true},
			Aliases: map[string]string{"gateway.example.test": "example.test"},
		}}
	env := map[string]string{"APP_ENV": "development", "PROVIDER_MODE": "live", "AGENT_MODE": "live", "LLM_BASE_URL": "https://gateway.example.test/v1", "LLM_API_KEY": "offline-test-key", "LLM_MODEL": "deepseek"}
	return worktree, root, m, env
}

func wireCapturePrepareFixture(t *testing.T, worktree, root string, m wireCaptureManifest, env map[string]string) (*wireCapture, error) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "capture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := prepareWireCapture(worktree, root, "isolated-wire-capture-only", strings.Repeat("a", 40), func(key string) string { return env[key] })
	if c != nil {
		t.Cleanup(c.close)
	}
	return c, err
}

func TestWireCaptureGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*wireCaptureManifest, map[string]string)
	}{
		{"wildcard", func(m *wireCaptureManifest, _ map[string]string) { m.Address = "0.0.0.0:18765" }},
		{"hostname", func(m *wireCaptureManifest, _ map[string]string) { m.Address = "localhost:18765" }},
		{"ephemeral", func(m *wireCaptureManifest, _ map[string]string) { m.Address = "127.0.0.1:0" }},
		{"public", func(m *wireCaptureManifest, _ map[string]string) { m.Address = "8.8.8.8:18765" }},
		{"revision", func(m *wireCaptureManifest, _ map[string]string) { m.SourceSHA = strings.Repeat("b", 40) }},
		{"catalog", func(m *wireCaptureManifest, _ map[string]string) { m.CatalogSHA256 = strings.Repeat("b", 64) }},
		{"missing policy", func(m *wireCaptureManifest, _ map[string]string) { m.PolicyName = "" }},
		{"unbounded duration", func(m *wireCaptureManifest, _ map[string]string) { m.DurationSeconds = 0 }},
		{"unbounded policy", func(m *wireCaptureManifest, _ map[string]string) { m.Policy.MaxBodyBytes = 0 }},
		{"unsafe policy", func(m *wireCaptureManifest, _ map[string]string) { m.Policy.Allowed["Bearer abcdefghijklmnop"] = true }},
		{"alias chain", func(m *wireCaptureManifest, _ map[string]string) { m.Policy.Aliases["example.test"] = "other" }},
		{"unsafe metadata", func(m *wireCaptureManifest, _ map[string]string) { m.Scenario = "secret=abcdefg" }},
		{"empty selection", func(m *wireCaptureManifest, _ map[string]string) { m.Connections = nil }},
		{"unknown selection", func(m *wireCaptureManifest, _ map[string]string) { m.Connections = []string{"not-configured"} }},
		{"duplicate selection", func(m *wireCaptureManifest, _ map[string]string) { m.Connections = []string{"LLM", "LLM"} }},
		{"missing key", func(_ *wireCaptureManifest, env map[string]string) { delete(env, "LLM_API_KEY") }},
		{"mock agent", func(_ *wireCaptureManifest, env map[string]string) { env["AGENT_MODE"] = "mock" }},
		{"mock provider", func(_ *wireCaptureManifest, env map[string]string) { env["PROVIDER_MODE"] = "mock" }},
		{"http", func(_ *wireCaptureManifest, env map[string]string) {
			env["LLM_BASE_URL"] = "http://gateway.example.test/v1"
		}},
		{"query", func(_ *wireCaptureManifest, env map[string]string) { env["LLM_BASE_URL"] += "?secret=abcdefg" }},
		{"unregistered host", func(_ *wireCaptureManifest, env map[string]string) {
			env["LLM_BASE_URL"] = "https://other.example.test/v1"
		}},
		{"missing frontend", func(m *wireCaptureManifest, _ map[string]string) { m.WebAssetsRoot = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worktree, root, m, env := wireCaptureFixture(t)
			tc.mutate(&m, env)
			if _, err := wireCapturePrepareFixture(t, worktree, root, m, env); err == nil || strings.Contains(err.Error(), "abcdefg") {
				t.Fatalf("guard did not reject safely: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "run")); !os.IsNotExist(err) {
				t.Fatal("invalid config created runtime storage")
			}
		})
	}
}

func TestWireCapturePrivateRootAndOptIn(t *testing.T) {
	worktree, root, m, env := wireCaptureFixture(t)
	lookup := func(key string) string { return env[key] }
	for _, ack := range []string{"", "yes"} {
		if _, err := prepareWireCapture(worktree, root, ack, m.SourceSHA, lookup); err == nil {
			t.Fatal("missing ACK accepted")
		}
	}
	if _, err := prepareWireCapture(worktree, root, "isolated-wire-capture-only", "", lookup); err == nil {
		t.Fatal("unbound binary accepted")
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareWireCapture(worktree, root, "isolated-wire-capture-only", m.SourceSHA, lookup); err == nil {
		t.Fatal("public directory accepted")
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "capture")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareWireCapture(worktree, link, "isolated-wire-capture-only", m.SourceSHA, lookup); err == nil {
		t.Fatal("symlink root accepted")
	}
	if _, err := prepareWireCapture(root, root, "isolated-wire-capture-only", m.SourceSHA, lookup); err == nil {
		t.Fatal("worktree overlap accepted")
	}
	c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{c.config.StorageRoot, c.config.LegacyGeneratedImagesRoot, c.config.Logging.Directory, c.config.CatalogFile} {
		if !acceptancePathWithin(c.run, path) {
			t.Fatal("storage was not isolated")
		}
	}
	if c.config.AgentMode != "live" || c.config.ProviderMode != "live" || c.config.SessionSecret == "" || c.config.DevelopmentUser != "wire-capture-owner" {
		t.Fatal("unexpected isolated configuration")
	}
	if _, err := wireCapturePrepareFixture(t, worktree, root, m, env); err == nil {
		t.Fatal("previous run reused")
	}
	// The original manifest is not a live control channel.
	m.Policy.Allowed["late-value"] = true
	if c.recorder.Policy.Allowed["late-value"] {
		t.Fatal("policy was not frozen")
	}
}

func TestWireCaptureActualApplicationHookDoesNotRetryCaptureFailure(t *testing.T) {
	worktree, root, m, env := wireCaptureFixture(t)
	c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
	if err != nil {
		t.Fatal(err)
	}
	c.recorder.Default.Provenance = wirefixture.ProvenanceSynthetic
	// Only this offline test grants the administrator role to exercise the real
	// HTTP connection probe. The interactive runner remains a non-admin owner.
	c.config.DevelopmentRoles = []string{"admin"}
	c.config.DevelopmentUser = "owner-a"
	c.config.MockAuth = false
	calls := 0
	defaultTransport := http.DefaultTransport
	application, err := c.assemble(discardLogger(), wireCaptureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://gateway.example.test/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer offline-test-key" {
			t.Fatal("app hook lost connection request")
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		body := `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	response := appChatRequest(application.server.Handler, http.MethodPost, application.schema.LLMSettings.Routes.Test, `{"connection_id":"LLM"}`)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"reachable":true`) {
		t.Fatalf("business probe failed: %d %s", response.Code, response.Body)
	}
	if calls != 1 || http.DefaultTransport != defaultTransport {
		t.Fatal("capture changed retry or global transport behavior")
	}
	exchanges := c.recorder.Exchanges()
	if len(exchanges) != 1 || exchanges[0].Metadata.Connection != "LLM" || exchanges[0].Complete {
		t.Fatal("real app hook did not record the unsupported probe")
	}
	if c.export(nil) == nil {
		t.Fatal("unsupported app capture exported")
	}
}

func TestWireCaptureApplicationShutdownAndMissingHook(t *testing.T) {
	for _, missing := range []bool{false, true} {
		worktree, root, m, env := wireCaptureFixture(t)
		c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
		if err != nil {
			t.Fatal(err)
		}
		if missing {
			c.selected["unassembled"] = true
		}
		// Only the offline lifecycle test uses an ephemeral listener.
		c.config.Address = "127.0.0.1:0"
		application, err := c.assemble(discardLogger(), wireCaptureTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline test forbids upstream traffic")
		}))
		if missing {
			if err == nil || application != nil {
				t.Fatal("unassembled selection accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(100*time.Millisecond, cancel)
		runErr := application.Run(ctx)
		timer.Stop()
		cancel()
		if runErr != nil {
			t.Fatal(runErr)
		}
		if c.export(runErr) == nil || len(c.recorder.Exchanges()) != 0 || len(c.recorder.Failures()) != 0 {
			t.Fatal("empty clean shutdown must fail export without sending upstream requests")
		}
	}
}

func TestWireCaptureExport(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "partial", "transport", "query", "media", "unsafe", "shutdown", "missing-connection", "write-failure"} {
		t.Run(mode, func(t *testing.T) {
			worktree, root, m, env := wireCaptureFixture(t)
			c, err := wireCapturePrepareFixture(t, worktree, root, m, env)
			if err != nil {
				t.Fatal(err)
			}
			// Every byte in this test is synthetic, not a live corpus sample.
			// Captured media needs captured provenance; export substitutes it.
			if mode != "media" {
				c.recorder.Default.Provenance = wirefixture.ProvenanceSynthetic
			}
			if mode != "empty" {
				target := "https://gateway.example.test/v1"
				if mode == "query" {
					target += "?secret=abcdefg"
				}
				request, _ := http.NewRequest(http.MethodGet, target, nil)
				calls := 0
				response, businessErr := c.recorder.Wrap("LLM", wireCaptureTransport(func(*http.Request) (*http.Response, error) {
					calls++
					if mode == "transport" {
						return nil, io.ErrUnexpectedEOF
					}
					body, contentType := `{"ok":true}`, "application/json"
					if mode == "unsafe" {
						body = `{"unregistered":true}`
					}
					if mode == "partial" {
						body = "data: {\"choices\":[]}"
						contentType = "text/event-stream"
					}
					if mode == "media" {
						body = "not-a-supported-media-type"
						contentType = "application/zip"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
				})).RoundTrip(request)
				if mode == "transport" {
					if businessErr != io.ErrUnexpectedEOF {
						t.Fatal("business transport error changed")
					}
				} else {
					if businessErr != nil {
						t.Fatal(businessErr)
					}
					if mode != "partial" {
						_, _ = io.Copy(io.Discard, response.Body)
					}
					if err := response.Body.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 1 {
					t.Fatal("capture retried the transport")
				}
			}
			var runErr error
			if mode == "shutdown" {
				runErr = errors.New("secret=abcdefg")
			}
			if mode == "missing-connection" {
				c.selected["another"] = true
			}
			candidate := filepath.Join(c.run, "candidate.json")
			if mode == "write-failure" {
				if err := os.WriteFile(candidate, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = c.export(runErr)
			if mode == "complete" || mode == "media" {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(candidate)
				if err != nil {
					t.Fatal(err)
				}
				var exported []wirefixture.Exchange
				if json.Unmarshal(data, &exported) != nil || wirefixture.Validate(exported, m.Policy) != nil {
					t.Fatal("invalid exported candidate")
				}
				if mode == "media" {
					if len(exported) != 1 || exported[0].Response.Body.Media == nil ||
						exported[0].Response.Body.Media.Recipe != "zip-empty" ||
						exported[0].Metadata.Provenance != wirefixture.ProvenanceDerived {
						t.Fatalf("media candidate = %+v", exported)
					}
					if _, err := os.Stat(filepath.Join(c.run, "evidence-manifest.json")); err != nil {
						t.Fatal("private evidence manifest missing")
					}
				}
				if c.export(nil) == nil {
					t.Fatal("candidate overwritten")
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "abcdefg") {
					t.Fatalf("export did not fail safely: %v", err)
				}
				if mode == "write-failure" {
					data, _ := os.ReadFile(candidate)
					if string(data) != "preserve" {
						t.Fatal("existing file changed")
					}
				} else if _, err := os.Stat(candidate); !os.IsNotExist(err) {
					t.Fatal("failed capture produced a candidate")
				}
			}
		})
	}
}

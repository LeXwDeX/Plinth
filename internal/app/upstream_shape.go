package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Upstream shape (#219). A stack runs either against the real upstream
// Providers ("live", the default and the only shape formal-test uses) or
// against the fake upstream service deployed in the same Compose project
// ("fake", dev-test's default). The fake shape is opt-in through two runtime
// settings and fails closed: it refuses to start unless every Provider and chat
// connection points at the fake origin and every configured key is a fake key,
// so a half-configured stack never reaches a real Provider with a real key.
const (
	upstreamShapeSetting      = "upstream_shape"
	fakeUpstreamOriginSetting = "fake_upstream_origin"
	upstreamShapeLive         = "live"
	upstreamShapeFake         = "fake"
	// fakeUpstreamKeyPrefix is the fake upstream's key namespace
	// (internal/testutil/fakeupstream.KeyPrefix): every key it accepts
	// starts with it, and no real Provider key does.
	fakeUpstreamKeyPrefix = "fake-"
)

// fakeAssetHost is the host the fake upstream writes into artifact URLs
// (cmd/fake-upstream -artifact-origin https://<fakeAssetHost>). Tripo's native
// processing only reads source models from public HTTPS hosts, so the fake
// shape maps this host back to the fake service instead of loosening that
// rule. ".test" is reserved (RFC 2606) and never resolves; the transport below
// is the only way the name reaches a socket, and only in the fake shape.
const fakeAssetHost = "fake-assets.example.test"

// fakeAssetConnections are the connections that download artifacts from the
// fake asset host (Tripo also through its anonymous native-source client).
var fakeAssetConnections = []string{"tripo", "tripo.native-source", "meshy", "hyper3d", "jimeng-images"}

// fakeAssetProviders are the Providers whose Offerings accept the fake asset
// host as an extra asset host in the fake shape.
var fakeAssetProviders = []string{"tripo", "meshy", "hyper3d", "jimeng"}

// upstreamShape is the resolved shape. origin is nil in the live shape.
type upstreamShape struct {
	origin *url.URL
}

func (s upstreamShape) fake() bool { return s.origin != nil }

// resolveUpstreamShape reads the two settings. The live shape refuses a fake
// origin (no partial relaxation); the fake shape needs an http origin on a
// private name: a single-label Compose service name, or a loopback or private
// address literal. A public host can never become the fake upstream.
func resolveUpstreamShape(config Config) (upstreamShape, error) {
	shape := strings.TrimSpace(config.RuntimeSettings[upstreamShapeSetting])
	raw := strings.TrimSpace(config.RuntimeSettings[fakeUpstreamOriginSetting])
	switch shape {
	case "", upstreamShapeLive:
		if raw != "" {
			return upstreamShape{}, errors.New("FAKE_UPSTREAM_ORIGIN 只能与 UPSTREAM_SHAPE=fake 一起使用")
		}
		return upstreamShape{}, nil
	case upstreamShapeFake:
	default:
		return upstreamShape{}, fmt.Errorf("运行设置 %s 的值无效: %q", upstreamShapeSetting, shape)
	}
	if config.ProviderMode != "live" || config.AgentMode != "live" {
		return upstreamShape{}, errors.New("UPSTREAM_SHAPE=fake 需要 PROVIDER_MODE=live 与 AGENT_MODE=live")
	}
	origin, err := url.Parse(raw)
	if raw == "" || err != nil || origin.Scheme != "http" || origin.Host == "" || origin.User != nil ||
		(origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
		return upstreamShape{}, errors.New("UPSTREAM_SHAPE=fake 需要 FAKE_UPSTREAM_ORIGIN 为无路径、无凭据的 http://主机[:端口]")
	}
	if !privateUpstreamHost(origin.Hostname()) {
		return upstreamShape{}, errors.New("FAKE_UPSTREAM_ORIGIN 必须是单标签服务名或回环/私有地址")
	}
	origin.Path = ""
	return upstreamShape{origin: origin}, nil
}

func privateUpstreamHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		return address.IsLoopback() || address.IsPrivate()
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if host == "" || strings.Contains(host, ".") {
		return false
	}
	for _, r := range host {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// validateFakeUpstreamWiring is the fail-closed check of the fake shape: each
// declared Provider/chat connection's base URL is empty (connection off) or on
// the fake origin, each key is empty or a fake key, and LLM_CONNECTIONS adds
// no other chat connection.
func validateFakeUpstreamWiring(shape upstreamShape, config Config, schema catalog.ApplicationSchema) error {
	if !shape.fake() {
		return nil
	}
	type pair struct{ baseURL, apiKey string }
	pairs := []pair{{schema.Chat.Agent.BaseURLSetting, schema.Chat.Agent.APIKeySetting}}
	for _, connection := range schema.Runtime.ProviderConnections {
		pairs = append(pairs, pair{connection.BaseURLSetting, connection.APIKeySetting})
	}
	for _, account := range schema.Runtime.ProviderAccounts {
		pairs = append(pairs, pair{account.BaseURLSetting, account.APIKeySetting})
	}
	for _, p := range pairs {
		if p.baseURL != "" {
			if value := strings.TrimSpace(config.RuntimeSettings[p.baseURL]); value != "" {
				parsed, err := url.Parse(value)
				if err != nil || !strings.EqualFold(parsed.Scheme, shape.origin.Scheme) ||
					!strings.EqualFold(parsed.Host, shape.origin.Host) {
					return fmt.Errorf("UPSTREAM_SHAPE=fake: 运行设置 %s 未指向 FAKE_UPSTREAM_ORIGIN", p.baseURL)
				}
			}
		}
		if p.apiKey != "" {
			if value := strings.TrimSpace(config.RuntimeSettings[p.apiKey]); value != "" &&
				!strings.HasPrefix(value, fakeUpstreamKeyPrefix) {
				return fmt.Errorf("UPSTREAM_SHAPE=fake: 运行设置 %s 必须为空或 fake 上游密钥", p.apiKey)
			}
		}
	}
	if len(config.ExtraChatConnections) > 0 {
		return errors.New("UPSTREAM_SHAPE=fake: LLM_CONNECTIONS 必须为空")
	}
	return nil
}

// loadCatalogForShape loads the Catalog; in the fake shape the Offerings of the
// fake artifact Providers also accept the fake asset host. The Catalog file
// and every live stack stay untouched.
func loadCatalogForShape(fileName string, shape upstreamShape) (*catalog.Catalog, error) {
	if !shape.fake() {
		return loadCatalog(fileName)
	}
	raw := catalog.DefaultDocument()
	if fileName != "" {
		var err error
		if raw, err = os.ReadFile(fileName); err != nil {
			return nil, fmt.Errorf("打开 CATALOG_FILE: %w", err)
		}
	}
	patched, err := withFakeAssetHost(raw)
	if err != nil {
		return nil, err
	}
	return catalog.Load(bytes.NewReader(patched))
}

// withFakeAssetHost adds fakeAssetHost to asset_hosts of every Offering of the
// fake artifact Providers, leaving all other fields as they are.
func withFakeAssetHost(raw []byte) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("解析 Offering Catalog: %w", err)
	}
	var offerings []map[string]json.RawMessage
	if err := json.Unmarshal(document["offerings"], &offerings); err != nil {
		return nil, fmt.Errorf("解析 Offering Catalog: %w", err)
	}
	for _, offering := range offerings {
		var provider string
		_ = json.Unmarshal(offering["provider_id"], &provider)
		if !slices.Contains(fakeAssetProviders, provider) {
			continue
		}
		var hosts []string
		if existing, ok := offering["asset_hosts"]; ok {
			if err := json.Unmarshal(existing, &hosts); err != nil {
				return nil, fmt.Errorf("解析 Offering asset_hosts: %w", err)
			}
		}
		if !slices.Contains(hosts, fakeAssetHost) {
			hosts = append(hosts, fakeAssetHost)
		}
		encoded, err := json.Marshal(hosts)
		if err != nil {
			return nil, err
		}
		offering["asset_hosts"] = encoded
	}
	encoded, err := json.Marshal(offerings)
	if err != nil {
		return nil, err
	}
	document["offerings"] = encoded
	return json.Marshal(document)
}

type fakeShapeTransport func(*http.Request) (*http.Response, error)

func (f fakeShapeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeShapeHTTPTransport routes the fake artifact host of the artifact
// connections to the fake upstream's plain-HTTP origin. Every other host and
// every other connection keeps the connection's own transport.
func fakeShapeHTTPTransport(fake *url.URL) func(string, http.RoundTripper) http.RoundTripper {
	direct := &http.Transport{Proxy: nil}
	return func(connectionID string, base http.RoundTripper) http.RoundTripper {
		if !slices.Contains(fakeAssetConnections, connectionID) {
			return base
		}
		return fakeShapeTransport(func(r *http.Request) (*http.Response, error) {
			if !strings.EqualFold(r.URL.Hostname(), fakeAssetHost) {
				return base.RoundTrip(r)
			}
			routed := r.Clone(r.Context())
			routed.URL.Scheme, routed.URL.Host = fake.Scheme, fake.Host
			routed.Host = fake.Host
			return direct.RoundTrip(routed)
		})
	}
}

package app

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
)

// Legacy private builders kept for tests that predate the transport hook.
func registerProviders(
	config Config,
	taskCatalog *catalog.Catalog,
	registry *providers.Registry,
	inputs generation.InputAssetResolver,
	assets generation.AssetStore,
	logger *slog.Logger,
	recoveryStores ...generation.RecoveryStore,
) error {
	return registerProvidersWithTransport(config, taskCatalog, registry, inputs, assets, logger, nil, recoveryStores...)
}

func buildProviderAdapter(
	offerings []generation.Offering,
	connection catalog.ProviderConnectionSchema,
	baseURL string,
	apiKey string,
	timeout time.Duration,
	scratchDir string,
	inputs generation.InputAssetResolver,
	assets generation.AssetStore,
	recoveryStores ...generation.RecoveryStore,
) (generation.ProviderPort, error) {
	return buildProviderAdapterWithTransport(offerings, connection, baseURL, apiKey, timeout, scratchDir, inputs, assets, nil, recoveryStores...)
}

func buildChatAgent(
	config Config,
	schema catalog.ApplicationSchema,
) (agents.Adapter, error) {
	return buildChatAgentWithTransport(config, schema, nil)
}

type wireRecordingTransport struct {
	base http.RoundTripper
}

func (transport wireRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.base.RoundTrip(request)
}

func TestWireHTTPClientKeepsDefaultHTTPBehavior(t *testing.T) {
	if client := wireHTTPClient(nil, "connection", time.Second); client != nil {
		t.Fatalf("nil decorator must keep the default client, got %#v", client)
	}
	defaults := http.DefaultTransport.(*http.Transport)
	client := wireHTTPClient(
		func(connectionID string, base http.RoundTripper) http.RoundTripper {
			if connectionID != "connection" {
				t.Fatalf("connection ID = %q", connectionID)
			}
			cloned, ok := base.(*http.Transport)
			if !ok {
				t.Fatalf("decorator base = %T, want *http.Transport", base)
			}
			if cloned == defaults {
				t.Fatal("decorator base is the shared http.DefaultTransport")
			}
			if cloned.Proxy == nil || cloned.DialContext == nil {
				t.Fatal("decorator base lost the default transport settings")
			}
			if cloned.MaxIdleConns != defaults.MaxIdleConns ||
				cloned.IdleConnTimeout != defaults.IdleConnTimeout ||
				cloned.TLSHandshakeTimeout != defaults.TLSHandshakeTimeout ||
				cloned.ExpectContinueTimeout != defaults.ExpectContinueTimeout ||
				cloned.ForceAttemptHTTP2 != defaults.ForceAttemptHTTP2 {
				t.Fatal("decorator base does not preserve the default transport values")
			}
			cloned.MaxIdleConnsPerHost = 99
			return wireRecordingTransport{base: base}
		},
		"connection",
		0,
	)
	if client == nil {
		t.Fatal("decorated client is nil")
	}
	if client.Timeout != httpjson.DefaultRequestTimeout {
		t.Fatalf("timeout = %s, want %s", client.Timeout, httpjson.DefaultRequestTimeout)
	}
	if client.CheckRedirect != nil {
		t.Fatal("decorated client changed the default redirect policy")
	}
	if _, ok := client.Transport.(wireRecordingTransport); !ok {
		t.Fatalf("transport = %T, want the decorator result", client.Transport)
	}
	if defaults.MaxIdleConnsPerHost == 99 {
		t.Fatal("decorating the clone changed http.DefaultTransport")
	}
}

func TestWireHTTPClientRejectsNilDecoratorResult(t *testing.T) {
	client := wireHTTPClient(
		func(string, http.RoundTripper) http.RoundTripper { return nil },
		"connection",
		time.Second,
	)
	if client == nil {
		t.Fatal("decorator error must still produce a rejecting client")
	}
	if _, ok := client.Transport.(rejectingRoundTripper); !ok {
		t.Fatalf("transport = %T, want rejectingRoundTripper", client.Transport)
	}
	request, err := http.NewRequest(http.MethodGet, "https://provider.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Transport.RoundTrip(request); err == nil {
		t.Fatal("nil decorator result must reject instead of using the real network")
	}
}

type customDefaultTransport struct{}

func (customDefaultTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("custom default transport")
}

func TestWireHTTPClientKeepsCustomDefaultTransport(t *testing.T) {
	previous := http.DefaultTransport
	custom := customDefaultTransport{}
	http.DefaultTransport = custom
	t.Cleanup(func() { http.DefaultTransport = previous })
	var base http.RoundTripper
	client := wireHTTPClient(
		func(_ string, value http.RoundTripper) http.RoundTripper {
			base = value
			return value
		},
		"connection",
		time.Second,
	)
	if client == nil || base != custom {
		t.Fatalf("custom default transport = %T, want it passed through", base)
	}
}

func TestNativeSourceTransportScopesConnectionID(t *testing.T) {
	if nativeSourceTransport(nil, "tripo") != nil {
		t.Fatal("nil decorator must keep the secured native source client")
	}
	transport := nativeSourceTransport(
		func(connectionID string, base http.RoundTripper) http.RoundTripper {
			if connectionID != "tripo.native-source" {
				t.Fatalf("connection ID = %q", connectionID)
			}
			return wireRecordingTransport{base: base}
		},
		"tripo",
	)
	if transport == nil {
		t.Fatal("scoped native source transport is nil")
	}
	if transport(http.DefaultTransport) == nil {
		t.Fatal("scoped native source transport returned nil")
	}
}

func assertDefaultTransportClone(t *testing.T, base http.RoundTripper) {
	t.Helper()
	defaults, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport = %T, want *http.Transport", http.DefaultTransport)
	}
	cloned, ok := base.(*http.Transport)
	if !ok {
		t.Fatalf("decorator base = %T, want *http.Transport", base)
	}
	if cloned == defaults {
		t.Fatal("decorator base is the shared http.DefaultTransport")
	}
	if cloned.Proxy == nil || cloned.DialContext == nil {
		t.Fatal("decorator base lost the default transport settings")
	}
}

func TestRemoteProviderAdaptersUseConnectionTransport(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"image-sync", "meshy", "tripo", "hyper3d.rodin", "matting"} {
		t.Run(protocol, func(t *testing.T) {
			var connection catalog.ProviderConnectionSchema
			for _, candidate := range taskCatalog.Application().Runtime.ProviderConnections {
				if candidate.Protocol == protocol {
					connection = candidate
					break
				}
			}
			if connection.Protocol == "" {
				t.Fatalf("no declared connection for protocol %q", protocol)
			}
			connection.ID = "provider-" + protocol
			var connectionIDs []string
			var bases []http.RoundTripper
			decorate := func(connectionID string, base http.RoundTripper) http.RoundTripper {
				connectionIDs = append(connectionIDs, connectionID)
				bases = append(bases, base)
				return wireRecordingTransport{base: base}
			}
			if _, err := buildProviderAdapterWithTransport(
				nil,
				connection,
				"https://provider.test",
				"test-key",
				2*time.Second,
				"",
				nil,
				nil,
				decorate,
			); err != nil {
				t.Fatal(err)
			}
			if len(connectionIDs) == 0 || connectionIDs[0] != connection.ID {
				t.Fatalf("decorated connection IDs = %v", connectionIDs)
			}
			assertDefaultTransportClone(t, bases[0])
			if protocol != "tripo" {
				if len(connectionIDs) != 1 {
					t.Fatalf("decorator calls = %v", connectionIDs)
				}
				return
			}
			if len(connectionIDs) != 2 || connectionIDs[1] != connection.ID+".native-source" {
				t.Fatalf("decorator calls = %v", connectionIDs)
			}
			secure, ok := bases[1].(*http.Transport)
			if !ok || secure == http.DefaultTransport {
				t.Fatalf("native source base = %T, want the secured transport", bases[1])
			}
			if secure.Proxy != nil {
				t.Fatal("native source transport uses a proxy")
			}
			if secure.TLSClientConfig == nil || secure.TLSClientConfig.MinVersion != tls.VersionTLS12 {
				t.Fatalf("native source TLS config = %#v", secure.TLSClientConfig)
			}
			if secure.DialContext == nil || !secure.DisableCompression {
				t.Fatal("native source secure transport settings were lost")
			}
		})
	}
}

func TestChatAgentConnectionsUseConnectionTransport(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	config := Config{
		AgentMode: "live",
		RuntimeSettings: map[string]string{
			schema.Chat.Agent.BaseURLSetting: "https://llm.test/v1",
			schema.Chat.Agent.APIKeySetting:  "test-key",
			schema.Chat.Agent.ModelSetting:   "test-model",
		},
		ExtraChatConnections: []ChatConnectionConfig{{
			ID:      "extra",
			BaseURL: "https://extra.test/v1",
			APIKey:  "extra-key",
			Model:   "extra-model",
		}},
	}
	var connectionIDs []string
	var bases []http.RoundTripper
	_, err = buildChatAgentWithTransport(
		config,
		schema,
		func(connectionID string, base http.RoundTripper) http.RoundTripper {
			connectionIDs = append(connectionIDs, connectionID)
			bases = append(bases, base)
			return wireRecordingTransport{base: base}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	connections := chatConnections(config, schema)
	if len(connectionIDs) != len(connections) {
		t.Fatalf("decorated connections = %v, want %d", connectionIDs, len(connections))
	}
	for index, connection := range connections {
		if connectionIDs[index] != connection.ID {
			t.Fatalf("decorated connection %d = %q, want %q", index, connectionIDs[index], connection.ID)
		}
		assertDefaultTransportClone(t, bases[index])
	}
}

func TestOrdinaryAssemblyHasNoTransportOverride(t *testing.T) {
	overrides := assemblyOverrides{}
	if overrides.httpTransport != nil {
		t.Fatal("ordinary assembly must not set a transport override")
	}
	if client := wireHTTPClient(overrides.httpTransport, "connection", time.Second); client != nil {
		t.Fatalf("ordinary assembly built a client: %#v", client)
	}
}

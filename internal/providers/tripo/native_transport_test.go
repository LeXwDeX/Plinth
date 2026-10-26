package tripo

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
)

type nativeRecordingTransport struct{}

func (nativeRecordingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func TestNewAppliesNativeSourceTransportAfterSecureClient(t *testing.T) {
	var secured http.RoundTripper
	decorated := http.RoundTripper(nativeRecordingTransport{})
	adapter, err := New(Config{
		BaseURL: "https://api.tripo.test",
		APIKey:  "test-key",
		NativeSourceTransport: func(base http.RoundTripper) http.RoundTripper {
			secured = base
			return decorated
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.nativeSourceClient.Transport != decorated {
		t.Fatalf("native source transport = %T, want the decorator result", adapter.nativeSourceClient.Transport)
	}
	if adapter.nativeSourceClient.CheckRedirect == nil {
		t.Fatal("native source redirect policy was replaced")
	}
	if adapter.nativeSourceClient.Timeout != httpjson.DefaultRequestTimeout {
		t.Fatalf("native source timeout = %s", adapter.nativeSourceClient.Timeout)
	}
	secure, ok := secured.(*http.Transport)
	if !ok || secure == http.DefaultTransport {
		t.Fatalf("decorated base = %T, want the secured transport", secured)
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
	if adapter.nativeSourceClient == adapter.client.HTTPClient {
		t.Fatal("native source client must stay separate from the API client")
	}
}

func TestNewRejectsNilNativeSourceTransportResult(t *testing.T) {
	adapter, err := New(Config{
		BaseURL: "https://api.tripo.test",
		APIKey:  "test-key",
		NativeSourceTransport: func(http.RoundTripper) http.RoundTripper {
			return nil
		},
	})
	if err == nil || adapter != nil {
		t.Fatalf("nil native source transport result built an adapter: %v", err)
	}
	if err.Error() != "tripo: native source transport decorator returned nil" {
		t.Fatalf("error = %q", err)
	}
}

func TestNewKeepsNativeSourceSecurityWithoutTransport(t *testing.T) {
	adapter, err := New(Config{
		BaseURL: "https://api.tripo.test",
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	secure, ok := adapter.nativeSourceClient.Transport.(*http.Transport)
	if !ok || secure == http.DefaultTransport {
		t.Fatalf("native source transport = %T, want the secured transport", adapter.nativeSourceClient.Transport)
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
	if adapter.nativeSourceClient.CheckRedirect == nil {
		t.Fatal("native source redirect policy was replaced")
	}
	if adapter.nativeSourceClient.Timeout != httpjson.DefaultRequestTimeout {
		t.Fatalf("native source timeout = %s", adapter.nativeSourceClient.Timeout)
	}
}

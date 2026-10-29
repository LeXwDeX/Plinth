package app

import (
	"net/http"
	"net/url"
	"strings"
)

// fakeAssetHost is the host the fake upstream writes into artifact URLs for a
// stack run. Tripo's native processing only reads source models from public
// HTTPS hosts, so the stack maps this host back to the fake. ".test" never
// resolves; the mapping below is the only way the name reaches a socket.
const fakeAssetHost = "fake-assets.example.test"

type fakeStackTransport func(*http.Request) (*http.Response, error)

func (f fakeStackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeStackHTTPTransport routes the fake artifact host of the Tripo, Meshy,
// Hyper3D and Jimeng image connections (and the anonymous native-source client
// of Tripo) to the fake upstream's plain-HTTP address. Everything else keeps the connection's own
// transport.
func fakeStackHTTPTransport(fake *url.URL) func(string, http.RoundTripper) http.RoundTripper {
	direct := &http.Transport{Proxy: nil}
	return func(connectionID string, base http.RoundTripper) http.RoundTripper {
		switch connectionID {
		case "tripo", "tripo.native-source", "meshy", "hyper3d", "jimeng-images":
		default:
			return base
		}
		return fakeStackTransport(func(r *http.Request) (*http.Response, error) {
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

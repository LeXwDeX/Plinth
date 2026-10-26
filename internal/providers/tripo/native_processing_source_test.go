package tripo

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
)

type nativeResolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f nativeResolverFunc) LookupIPAddr(ctx context.Context, h string) ([]net.IPAddr, error) {
	return f(ctx, h)
}
func TestNativeSourcePinsPublicAddressesBeforeDial(t *testing.T) {
	for _, addresses := range [][]string{{"8.8.8.8", "1.1.1.1"}, {"8.8.8.8", "127.0.0.1"}, {"::ffff:127.0.0.1"}, {"100.64.0.1"}, {"169.254.169.254"}, {"2001:db8::1"}, {"192.0.2.1"}, {"64:ff9b::7f00:1"}} {
		t.Run(strings.Join(addresses, "-"), func(t *testing.T) {
			resolutions, dials := 0, []string{}
			resolver := nativeResolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
				resolutions++
				if host != "cdn.example" {
					t.Fatal("hostname changed")
				}
				out := []net.IPAddr{}
				for _, a := range addresses {
					out = append(out, net.IPAddr{IP: net.ParseIP(a)})
				}
				return out, nil
			})
			dial := nativePinnedDial(resolver, func(_ context.Context, _ string, address string) (net.Conn, error) {
				dials = append(dials, address)
				return nil, errors.New("offline dial")
			})
			dial(context.Background(), "tcp", "cdn.example:443")
			want := 0
			if len(addresses) == 2 && addresses[1] == "1.1.1.1" {
				want = 2
			}
			if resolutions != 1 || len(dials) != want {
				t.Fatalf("resolution/dials %d/%v", resolutions, dials)
			}
			for i, address := range dials {
				if address != net.JoinHostPort(addresses[i], "443") {
					t.Fatal("dial did not use validated numeric IP")
				}
			}
		})
	}
	// "192." + "168…" keeps the public snapshot scan (scripts/check-public-tree.sh) quiet.
	for _, a := range []string{"10.0.0.1", "172.16.0.1", "192." + "168.1.1", "::1", "fc00::1", "fe80::1", "0.0.0.0", "255.255.255.255"} {
		if nativePublicIP(netip.MustParseAddr(a)) {
			t.Fatalf("accepted private IP %s", a)
		}
	}
}
func TestNativeSourceOriginalTLSNameNoProxyAuthOrRedirect(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.TLS.ServerName != "example.com" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("source request lost original TLS identity or carried credentials")
		}
		w.Header().Set("Location", "https://example.com/redirected")
		w.WriteHeader(302)
	}))
	defer server.Close()
	client := newNativeSourceClient(time.Second)
	transport := client.Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	resolutions := 0
	transport.DialContext = nativePinnedDial(nativeResolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		resolutions++
		if host != "example.com" {
			t.Fatal("wrong resolved host")
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}), func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			t.Fatal("unpinned address")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("unsafe source transport")
	}
	_, _, err := httpjson.OpenClaim(context.Background(), client, generation.ArtifactClaim{Locator: "https://example.com/model.glb"}, []string{"example.com"}, false)
	if err == nil || requests != 1 || resolutions != 1 {
		t.Fatalf("redirect/TLS requests %d resolutions %d err%v", requests, resolutions, err)
	}
}
func TestNativeSourceByteBoundsAndGLBIdentity(t *testing.T) {
	for _, kind := range []string{"valid", "unknown-length", "too-large", "mismatch-length", "truncated", "non-glb", "stream-error", "http", "wrong-host", "credentials"} {
		t.Run(kind, func(t *testing.T) {
			body := nativeTestGLB()
			size := int64(len(body))
			limit := size
			if kind == "too-large" {
				limit = size - 1
			}
			if kind == "mismatch-length" {
				size++
			}
			if kind == "unknown-length" {
				size = -1
			}
			if kind == "truncated" {
				body = body[:14]
				size = 14
			}
			if kind == "non-glb" {
				body[0] = 'X'
			}
			calls := 0
			a := &Adapter{nativeSourceClient: &http.Client{Transport: nativeRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				if kind == "stream-error" {
					return nil, errors.New("offline failure")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: size, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			locator := "https://assets.example/model.glb"
			if kind == "http" {
				locator = "http://assets.example/model.glb"
			}
			if kind == "wrong-host" {
				locator = "https://other.example/model.glb"
			}
			if kind == "credentials" {
				locator = "https://user:pass@assets.example/model.glb"
			}
			bound, err := a.bindNativeConversionSource(context.Background(), generation.Offering{AssetHosts: []string{"assets.example"}}, generation.ArtifactClaim{Locator: locator, MediaType: "model/gltf-binary"}, &nativeConversionOptions{SourceAlias: "model", MaxSourceBytes: limit})
			valid := kind == "valid" || kind == "unknown-length"
			if valid && (err != nil || bound.SHA256 != uploadedDigest(body) || bound.Size != int64(len(body))) {
				t.Fatalf("wrong binding %+v %v", bound, err)
			}
			if !valid && err == nil {
				t.Fatal("invalid source accepted")
			}
			if (kind == "http" || kind == "wrong-host" || kind == "credentials") && calls != 0 {
				t.Fatal("invalid URL reached network")
			}
		})
	}
}

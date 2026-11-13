// Package testkit holds the helpers the fake upstream versions' tests share: a
// server fixture, a JSON request helper, a transport that maps an HTTPS artifact
// host back to the fixture and Plinth's delivery check over fetched bytes.
// Test-only, like the rest of fakeupstream.
package testkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// Fixture is a running fake with the given versions mounted.
type Fixture struct {
	T      *testing.T
	Server *httptest.Server
	Fake   *fakeupstream.Server
}

// New starts a fake serving versions.
func New(t *testing.T, config fakeupstream.Config, versions ...fakeupstream.Version) *Fixture {
	t.Helper()
	fake, err := fakeupstream.New(config, versions...)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &Fixture{T: t, Server: server, Fake: fake}
}

// RouteHost returns a transport that sends every request for host to this
// fixture's server and refuses any other host. A fake started with
// fakeupstream.Config.ArtifactOrigin "https://<host>" writes that origin into
// the artifact URLs of its tasks; an Adapter that downloads only from public
// HTTPS hosts (Tripo's anonymous native-source client, installed through
// tripo.Config.NativeSourceTransport) reads them through this transport, so the
// bytes still come from the fake and the Adapter's own host and scheme checks
// stay in force.
func (f *Fixture) RouteHost(host string) http.RoundTripper {
	f.T.Helper()
	return f.RouteHosts(host)
}

// RouteHosts is RouteHost for an Adapter that sends its API requests and reads
// its artifacts through one client: every named host reaches the fixture's
// server and no other host does, so the whole run stays inside the fake and a
// request to a host the test did not name fails instead of leaving the process.
func (f *Fixture) RouteHosts(hosts ...string) http.RoundTripper {
	f.T.Helper()
	target, err := url.Parse(f.Server.URL)
	if err != nil {
		f.T.Fatal(err)
	}
	direct := &http.Transport{Proxy: nil}
	f.T.Cleanup(direct.CloseIdleConnections)
	return hostRouter{hosts: hosts, target: target, next: direct}
}

type hostRouter struct {
	hosts  []string
	target *url.URL
	next   http.RoundTripper
}

func (r hostRouter) RoundTrip(request *http.Request) (*http.Response, error) {
	named := false
	for _, host := range r.hosts {
		named = named || strings.EqualFold(request.URL.Hostname(), host)
	}
	if !named {
		return nil, fmt.Errorf("testkit: a request for %q reached a transport that only routes %q to the fake", request.URL.Hostname(), strings.Join(r.hosts, ", "))
	}
	routed := request.Clone(request.Context())
	routed.URL.Scheme, routed.URL.Host, routed.Host = r.target.Scheme, r.target.Host, r.target.Host
	return r.next.RoundTrip(routed)
}

// Do sends one request to path (which includes the version prefix). key ""
// omits the Authorization header; a nil body sends none. A *Raw body is sent as
// given with its content type.
func (f *Fixture) Do(method, path, key string, body any, headers ...string) (int, http.Header, map[string]any) {
	f.T.Helper()
	status, header, raw := f.Raw(method, path, key, body, headers...)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return status, header, decoded
}

// Body is a pre-encoded request body.
type Body struct {
	ContentType string
	Data        []byte
}

// Raw is Do without decoding the response.
func (f *Fixture) Raw(method, path, key string, body any, headers ...string) (int, http.Header, []byte) {
	f.T.Helper()
	var reader io.Reader
	contentType := ""
	switch typed := body.(type) {
	case nil:
	case Body:
		reader, contentType = bytes.NewReader(typed.Data), typed.ContentType
	default:
		raw, err := json.Marshal(body)
		if err != nil {
			f.T.Fatal(err)
		}
		reader, contentType = bytes.NewReader(raw), "application/json"
	}
	request, err := http.NewRequest(method, f.Server.URL+path, reader)
	if err != nil {
		f.T.Fatal(err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.T.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, response.Header, raw
}

// Download fetches an artifact URL without credentials.
func (f *Fixture) Download(url string) (int, string, []byte) {
	f.T.Helper()
	response, err := http.Get(url)
	if err != nil {
		f.T.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, response.Header.Get("Content-Type"), raw
}

// Field digs a value out of a decoded body or fails the test.
func Field(t *testing.T, body map[string]any, path ...any) any {
	t.Helper()
	value, ok := fakeupstream.Dig(body, path...)
	if !ok {
		t.Fatalf("response has no %v: %v", path, body)
	}
	return value
}

// DeliveryErr runs Plinth's delivery check over fetched bytes.
func DeliveryErr(t *testing.T, data []byte, role, variant, mediaType string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return local.ValidateDeliveredFile(file, role, variant, mediaType)
}

// AssertSameShape fails the test when got (a JSON response) differs in
// structure from sample (the committed real response): field set, nesting and
// JSON types. Values are not compared.
func AssertSameShape(t *testing.T, what string, sample, got []byte) {
	t.Helper()
	want, err := fakeupstream.ShapeOf(sample)
	if err != nil {
		t.Fatalf("%s: sample is not JSON: %v", what, err)
	}
	have, err := fakeupstream.ShapeOf(got)
	if err != nil {
		t.Fatalf("%s: fake response is not JSON: %v (%s)", what, err, got)
	}
	if diff := fakeupstream.DiffShape(want, have); !diff.Empty() {
		t.Errorf("%s drifted from the real sample: %s", what, diff)
	}
}

// AssertEndpointTable checks a version's endpoint table: every real endpoint
// names a committed sample, every row carries a documentation URL and check
// date, and every declared route answers something other than 404 or 405
// (probe sends one request and returns the status).
func AssertEndpointTable(t *testing.T, endpoints []fakeupstream.Endpoint, samples *fakeupstream.Samples, probe func(fakeupstream.Endpoint) int) {
	t.Helper()
	for _, endpoint := range endpoints {
		if endpoint.Evidence == fakeupstream.EvidenceReal && len(samples.Find(endpoint.Sample, "")) == 0 {
			t.Errorf("%s names no committed sample %q", endpoint.Key(), endpoint.Sample)
		}
		if endpoint.DocURL == "" || endpoint.CheckedAt == "" {
			t.Errorf("%s: needs a documentation reference and a check date", endpoint.Key())
		}
		if status := probe(endpoint); status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			t.Errorf("declared endpoint %s is not routed (%d)", endpoint.Key(), status)
		}
	}
}

// AssertAlignmentReport checks the committed alignment report: it names the
// official upstream, was checked on a date, rests on real requests (paid ones
// when paid is true) and holds no unexplained difference.
func AssertAlignmentReport(t *testing.T, path, official string, paid bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Official  string `json:"official_base"`
		CheckedAt string `json:"checked_at"`
		Requests  int    `json:"official_requests"`
		Paid      int    `json:"official_paid_requests"`
		Diffs     []struct {
			Note string `json:"note"`
		} `json:"diffs"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Official != official || report.CheckedAt == "" || report.Requests == 0 || paid && report.Paid == 0 {
		t.Fatalf("report header incomplete: %+v", report)
	}
	for _, diff := range report.Diffs {
		if strings.TrimSpace(diff.Note) == "" {
			t.Fatal("report holds an unexplained difference")
		}
	}
}

// Package testkit holds the helpers the fake upstream versions' tests share: a
// server fixture, a JSON request helper and Plinth's delivery check over
// fetched bytes. Test-only, like the rest of fakeupstream.
package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

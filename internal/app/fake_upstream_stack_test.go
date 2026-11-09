package app

import (
	"net/http"
	"net/url"
)

// fakeStackHTTPTransport is the fake shape's artifact-host routing
// (fakeShapeHTTPTransport, #219) for the test-hosted stacks of #96.
func fakeStackHTTPTransport(fake *url.URL) func(string, http.RoundTripper) http.RoundTripper {
	return fakeShapeHTTPTransport(fake)
}

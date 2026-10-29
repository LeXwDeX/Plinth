package fakeupstream

import (
	"fmt"
	"net/http"
	"strings"
)

// Server mounts every registered Version under its own prefix.
type Server struct {
	env      *Env
	mux      *http.ServeMux
	versions []Version
}

// New builds a Server. Prefixes must be unique and distinct from each other's
// path space (no prefix may contain another); registering the same
// provider/API pair twice is an error because versions are append-only.
func New(config Config, versions ...Version) (*Server, error) {
	server := &Server{env: NewEnv(config), mux: http.NewServeMux(), versions: versions}
	seen := map[string]string{}
	for _, version := range versions {
		if version.Provider == "" || version.API == "" || !strings.HasPrefix(version.Prefix, "/") ||
			strings.HasSuffix(version.Prefix, "/") || version.Handler == nil {
			return nil, fmt.Errorf("fakeupstream: version %q/%q is incomplete", version.Provider, version.API)
		}
		for prefix, owner := range seen {
			if version.Prefix == prefix || strings.HasPrefix(version.Prefix+"/", prefix+"/") ||
				strings.HasPrefix(prefix+"/", version.Prefix+"/") {
				return nil, fmt.Errorf("fakeupstream: prefix %s of %s/%s overlaps %s", version.Prefix, version.Provider, version.API, owner)
			}
		}
		seen[version.Prefix] = version.Provider + "/" + version.API
		for _, endpoint := range version.Endpoints {
			if endpoint.Evidence == EvidenceConstructed && (endpoint.DocURL == "" || endpoint.CheckedAt == "") {
				return nil, fmt.Errorf("fakeupstream: constructed endpoint %s of %s/%s needs DocURL and CheckedAt",
					endpoint.Key(), version.Provider, version.API)
			}
			if endpoint.Evidence == EvidenceReal && endpoint.Sample == "" {
				return nil, fmt.Errorf("fakeupstream: real endpoint %s of %s/%s names no sample",
					endpoint.Key(), version.Provider, version.API)
			}
		}
		handler, err := version.Handler(server.env)
		if err != nil {
			return nil, fmt.Errorf("fakeupstream: %s/%s: %w", version.Provider, version.API, err)
		}
		server.mux.Handle(version.Prefix+"/", http.StripPrefix(version.Prefix, handler))
	}
	return server, nil
}

// Versions returns the registered versions.
func (s *Server) Versions() []Version { return append([]Version(nil), s.versions...) }

// Env exposes the shared state to tests.
func (s *Server) Env() *Env { return s.env }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// ArtifactOriginFor is the origin artifact URLs of a response to r carry.
func (e *Env) ArtifactOriginFor(r *http.Request) string {
	if e.ArtifactOrigin != "" {
		return strings.TrimRight(e.ArtifactOrigin, "/")
	}
	return Origin(r)
}

// Origin returns "scheme://host" of the request as the client addressed it, so
// artifact URLs in responses point back at this fake.
func Origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

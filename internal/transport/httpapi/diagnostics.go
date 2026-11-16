package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

// Administrator diagnostics (#233): a read-only view of runtime facts that
// help an operator confirm what the stack is doing. The route and permission
// come from the Schema (application.diagnostics); the response carries counts,
// limits and the directory the operator configured, never a value from an
// upstream call. It is separate from /api/version and from the public
// projection.

// UpstreamCaptureStatus is the opt-in upstream recorder's state; the
// application passes the recorder itself (a nil one reports "off").
type UpstreamCaptureStatus interface {
	Status() wirecapture.RecorderStatus
}

type diagnosticsView struct {
	UpstreamCapture wirecapture.RecorderStatus `json:"upstream_capture"`
}

func (s *Server) registerDiagnosticsContract() {
	schema := s.application.Diagnostics
	if schema == nil {
		return
	}
	s.mux.HandleFunc("GET "+schema.Routes.Admin, s.adminDiagnostics)
}

func (s *Server) adminDiagnostics(w http.ResponseWriter, r *http.Request) {
	principal, err := s.identity.Identify(r)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", s.application.Authentication.Messages.Unauthenticated)
		return
	}
	if !principal.Can(access.Permission(s.application.Diagnostics.Permission)) {
		writeProblem(w, http.StatusForbidden, "forbidden", s.application.Authentication.Messages.Forbidden)
		return
	}
	view := diagnosticsView{}
	if s.upstreamCapture != nil {
		view.UpstreamCapture = s.upstreamCapture.Status()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}

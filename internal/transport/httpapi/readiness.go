package httpapi

import "net/http"

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.readiness == nil || s.readiness(r.Context()) != nil {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": s.localeMessage(w, "error.internal")})
		return
	}
	s.health(w, r)
}

package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/mcp"
)

func (s *Server) registerMCPContract() {
	path := s.application.AgentConfiguration.Routes.MCP
	if s.mcp == nil || s.agentConfiguration == nil || path == "" {
		return
	}
	s.mux.HandleFunc("POST "+path, s.withPrincipal(s.workspaceMCP))
}

func (s *Server) workspaceMCP(w http.ResponseWriter, r *http.Request, principal access.Principal) {
	if !principal.Can(access.PermissionTasksReadOwn) {
		writeProblem(w, http.StatusForbidden, "forbidden", s.application.Authentication.Messages.Forbidden)
		return
	}
	session := r.URL.Query().Get("session_id")
	if session == "" || s.ownsAgentSession == nil || !s.ownsAgentSession(r.Context(), principal.ID(), session) {
		writeProblem(w, http.StatusNotFound, "session_not_found", s.application.AgentConfiguration.UI["session_not_found"])
		return
	}
	snapshot, err := s.agentConfiguration.Snapshot(r.Context())
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "configuration_unavailable", s.application.AgentConfiguration.UI["load_failed"], err)
		return
	}
	peer, err := s.mcp.ScopedPeer(snapshot.Configuration.EnabledMCPPresetIDs, mcp.Scope{OwnerID: principal.ID(), SessionID: session})
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "mcp_unavailable", s.application.AgentConfiguration.UI["load_failed"], err)
		return
	}
	policy := s.application.AgentConfiguration.MCPPolicy
	mcphttp.NewHandler(peer, mcphttp.Policy{MaxResponseBytes: policy.MaxResponseBytes, MaxTools: policy.MaxTools}).ServeHTTP(w, r)
}

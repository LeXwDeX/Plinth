package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/adapters/mcphttp"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// The external MCP listener (#332).
//
// A program that is not a browser, an external MCP client, acts as one person
// with that person's personal access key. Its requests are not served by the
// web server that serves the pages and /api/v1/*: the process opens a second
// listener, on an address only the runtime configuration names, and MCPPublicHandler
// is that listener's whole handler. It mounts the MCP endpoint and the asset
// download route and nothing else, so a request for any other path is a 404 and
// a key never reaches the main API, whose identity does not accept keys.
//
// The handlers are written once: the endpoint is the mcphttp public handler,
// the tools read through the same task, offering and asset projections the web
// API uses, and a download is the web API's own downloadAsset, called with the
// key's Principal. The identity is the key owner's scope only (the Principal of
// accesskeys.Authenticator carries no administrator permission), so a Task
// of another owner is a Task that does not exist.
//
// There is no TLS here. The listener is meant for a LAN or the local host;
// a deployment that exposes it to the public network configures the
// certificate in the process or puts a TLS terminator in front of it.

type mcpPrincipalKey struct{}

// mcpBaseKey carries "scheme://host" of the request, so the download URLs a
// tool names are ones the caller can reach on the address it used.
type mcpBaseKey struct{}

func mcpPrincipalFrom(ctx context.Context) (access.Principal, bool) {
	principal, ok := ctx.Value(mcpPrincipalKey{}).(access.Principal)
	return principal, ok
}

// MCPPublicHandler returns the handler of the external MCP listener, or nil
// when the Product Schema does not enable the service or the identity or the
// status block is not assembled. The caller starts a listener only for a
// non-nil handler.
func (s *Server) MCPPublicHandler() (http.Handler, error) {
	service := s.application.MCPPublic
	if service == nil || !service.Enabled || s.mcpIdentity == nil || s.mcpStatus == nil {
		return nil, nil
	}
	if service.ProtocolVersion != mcphttp.ProtocolVersion {
		return nil, fmt.Errorf("httpapi: mcp_public.protocol_version %q 不是服务实现的协议版本 %q", service.ProtocolVersion, mcphttp.ProtocolVersion)
	}
	downloadRoute, err := routing.ParseTwo(service.DownloadPath, "taskID", "assetID")
	if err != nil {
		return nil, fmt.Errorf("httpapi: mcp_public.download_path: %w", err)
	}
	runtime, err := s.newMCPRuntime(*service, downloadRoute)
	if err != nil {
		return nil, err
	}
	endpoint, err := mcphttp.NewPublicHandler(runtime, mcphttp.PublicPolicy{
		ProtocolVersion:  service.ProtocolVersion,
		ServerName:       service.ServerName,
		MaxRequestBytes:  service.MaxRequestBytes,
		MaxResponseBytes: service.MaxResponseBytes,
		Instructions: func(request *http.Request) string {
			return s.localizedText(requestLocale(request), service.Instructions)
		},
		Detail:  s.mcpProblemDetail,
		Observe: s.mcpObserve,
	})
	if err != nil {
		return nil, fmt.Errorf("httpapi: mcp_public 端点: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(service.Path, s.mcpEndpoint(endpoint))
	mux.HandleFunc("GET "+service.DownloadPath, s.withMCPKey(s.mcpDownload))
	mux.HandleFunc("/", s.mcpNotFound)
	// The access log is outermost, as on the main server: every 4xx and 5xx is
	// logged with its request ID. The log never holds the key, the
	// Authorization header, a prompt or an argument: it records the method,
	// the path, the status and the reason the handler noted.
	return requestLogMiddleware(s.logger, s.providerAssetRoute, s.providerWebhookSecretRoutes(),
		localeTraceMiddleware(s.application, s.texts, clientAddressMiddleware(s.trustedProxies,
			s.securityHeadersMiddleware(
				recoverMiddleware(s.logger, s.application.MCPPublic.Messages.Internal, routeTraceMiddleware(mux)),
			),
		)),
	), nil
}

// mcpEndpoint serves the JSON-RPC endpoint: POST only, a valid key, the
// base address of the request in the context for the download URLs.
func (s *Server) mcpEndpoint(endpoint http.Handler) http.HandlerFunc {
	authenticated := s.withMCPKey(func(writer http.ResponseWriter, request *http.Request, _ access.Principal) {
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		base := ""
		if request.Host != "" {
			base = scheme + "://" + request.Host
		}
		endpoint.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), mcpBaseKey{}, base)))
	})
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			// Checked before the key: a client that probes the endpoint for a
			// stream (GET) or a session (DELETE) is told so plainly, not asked
			// for credentials.
			writer.Header().Set("Allow", http.MethodPost)
			s.writeMCPProblem(writer, request, http.StatusMethodNotAllowed, -32600, "method_not_allowed", "Method not allowed",
				s.application.MCPPublic.Messages.MethodNotAllowed)
			return
		}
		authenticated(writer, request)
	}
}

func (s *Server) mcpNotFound(writer http.ResponseWriter, request *http.Request) {
	s.writeMCPProblem(writer, request, http.StatusNotFound, -32601, "not_found", "Not found", s.application.MCPPublic.Messages.NotFound)
}

type mcpPrincipalHandler func(http.ResponseWriter, *http.Request, access.Principal)

// withMCPKey authenticates with a personal access key and nothing else: no
// cookie, no deployment token, no development identity. A request without a
// key, with a key that is wrong, revoked or expired, or whose owner can no
// longer act, is 401 with a Bearer challenge; the caller can never tell which.
func (s *Server) withMCPKey(next mcpPrincipalHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.mcpIdentity.Identify(request)
		switch {
		case err == nil && principal.Credential().Kind == access.CredentialAccessKey:
		case err == nil, errors.Is(err, accesskeys.ErrNotAKey), errors.Is(err, accesskeys.ErrUnauthorized):
			writer.Header().Set("WWW-Authenticate", `Bearer realm="`+s.application.MCPPublic.ServerName+`"`)
			s.writeMCPProblem(writer, request, http.StatusUnauthorized, -32001, "unauthenticated", "Unauthenticated",
				s.application.MCPPublic.Messages.Unauthenticated)
			return
		default:
			s.writeMCPProblem(writer, request, http.StatusServiceUnavailable, -32603, "authentication_unavailable", "Internal error",
				s.application.MCPPublic.Messages.Internal, err)
			return
		}
		if trace := requestTraceFrom(request.Context()); trace != nil {
			trace.noteOwner(principal.ID())
		}
		next(writer, request.WithContext(context.WithValue(request.Context(), mcpPrincipalKey{}, principal)), principal)
	}
}

// mcpDownload streams one product file of the key owner's Task. It is the web
// API's own download (the same owner scope, delivery headers, preview and
// audit), reached with a key; a key without the read scope is refused.
func (s *Server) mcpDownload(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	if !principal.Credential().HasScope(accesskeys.ScopeRead) {
		writer.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+accesskeys.ScopeRead+`"`)
		s.writeMCPProblem(writer, request, http.StatusForbidden, -32003, "insufficient_scope", "Insufficient scope",
			s.mcpInsufficientScope(request, accesskeys.ScopeRead))
		return
	}
	s.downloadAsset(writer, request, principal)
}

// writeMCPProblem answers a refusal of the listener in JSON-RPC form: the
// standard code and message, the Schema's wording of the reason in the request's
// language and the request ID the access log records. The causes go to the
// access log only.
func (s *Server) writeMCPProblem(writer http.ResponseWriter, request *http.Request, status, rpcCode int, code, rpcMessage, detail string, causes ...error) {
	data := map[string]any{"detail": s.localizedText(requestLocale(request), detail)}
	if trace := requestTraceFrom(request.Context()); trace != nil {
		trace.noteProblem(code, detail, causes)
		data["request_id"] = trace.id
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"jsonrpc": "2.0", "id": nil,
		"error": map[string]any{"code": rpcCode, "message": rpcMessage, "data": data},
	})
}

func (s *Server) mcpInsufficientScope(request *http.Request, scope string) string {
	text := s.localizedText(requestLocale(request), s.application.MCPPublic.Messages.InsufficientScope)
	return strings.ReplaceAll(text, "{scope}", scope)
}

// mcpProblemDetail words a problem of the public endpoint with the Schema's
// copy, in the language of the request.
func (s *Server) mcpProblemDetail(request *http.Request, kind, scope string) string {
	messages := s.application.MCPPublic.Messages
	locale := requestLocale(request)
	switch kind {
	case mcphttp.ProblemRequestTooLarge:
		return s.localizedText(locale, messages.RequestTooLarge)
	case mcphttp.ProblemInsufficientScope:
		return s.mcpInsufficientScope(request, scope)
	case mcphttp.ProblemInternal:
		return s.localizedText(locale, messages.Internal)
	case mcphttp.ProblemMethodNotAllowed:
		return s.localizedText(locale, messages.MethodNotAllowed)
	}
	return ""
}

var mcpProblemCodes = map[int]string{
	-32600: "mcp_invalid_request",
	-32601: "mcp_method_not_found",
	-32602: "mcp_invalid_params",
	-32603: "mcp_internal",
	-32003: "mcp_insufficient_scope",
	-32020: "mcp_header_mismatch",
	-32022: "mcp_unsupported_protocol_version",
}

// mcpObserve records what the public endpoint refused or failed, with the
// internal cause, in the request's access log entry. The response says nothing
// of the cause.
func (s *Server) mcpObserve(request *http.Request, status, code int, cause error) {
	trace := requestTraceFrom(request.Context())
	if trace == nil {
		return
	}
	name := mcpProblemCodes[code]
	if name == "" {
		name = "mcp_refused"
	}
	trace.noteProblem(name, fmt.Sprintf("MCP request answered %d", status), []error{cause})
}

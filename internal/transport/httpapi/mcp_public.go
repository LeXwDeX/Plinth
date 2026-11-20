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
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// The external MCP listener (#332).
//
// A program that is not a browser, an external MCP client, acts as one person
// with that person's personal access key. Its requests are not served by the
// web server that serves the pages and /api/v1/*: the process opens a second
// listener, on an address only the runtime configuration names, and MCPPublicHandler
// is that listener's whole handler. It mounts the MCP endpoint and the asset
// download routes and nothing else, so a request for any other path is a 404
// and a key never reaches the main API, whose identity does not accept keys.
// The routes are three: the endpoint, the key-scoped download and the
// short-lived signed download (#332), whose token is its own credential and
// asks for no key.
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

// mcpClientKey carries who is calling, as the product audit writes it.
type mcpClientKey struct{}

type mcpClient struct{ address, userAgent string }

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
	signedRoute, err := routing.ParseOne(service.SignedDownloadPath, "token")
	if err != nil {
		return nil, fmt.Errorf("httpapi: mcp_public.signed_download_path: %w", err)
	}
	runtime, err := s.newMCPRuntime(*service, downloadRoute, signedRoute)
	if err != nil {
		return nil, err
	}
	policy := mcphttp.PublicPolicy{
		ProtocolVersion:  service.ProtocolVersion,
		ServerName:       service.ServerName,
		MaxRequestBytes:  service.MaxRequestBytes,
		MaxResponseBytes: service.MaxResponseBytes,
		Instructions: func(request *http.Request) string {
			return s.localizedText(requestLocale(request), service.Instructions)
		},
		Detail:  s.mcpProblemDetail,
		Observe: s.mcpObserve,
	}
	// The Tasks extension (#332 stage 2): the discover capabilities advertise
	// it, and only the methods the Schema lists are served.
	if service.Tasks.Enabled {
		policy.TasksExtension = catalog.MCPTasksExtension
		policy.TasksMethods = service.Tasks.Methods
	}
	endpoint, err := mcphttp.NewPublicHandler(runtime, policy)
	if err != nil {
		return nil, fmt.Errorf("httpapi: mcp_public 端点: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(service.Path, s.mcpEndpoint(endpoint))
	mux.HandleFunc("GET "+service.DownloadPath, s.withMCPKey(s.mcpDownload))
	mux.HandleFunc("GET "+service.SignedDownloadPath, s.mcpSignedDownload)
	mux.HandleFunc("/", s.mcpNotFound)
	// The listener serves its callers directly and never sits behind the web
	// server's reverse proxy, so it trusts no proxy: the client address is the
	// TCP peer, and a forwarding header can neither free a throttled caller nor
	// throttle someone else, whatever trusted_proxy_cidrs lists for the main
	// server (a Docker gateway registered there would otherwise make every
	// connection that arrives through it trust X-Forwarded-For).
	// The access log is outermost, as on the main server: every 4xx and 5xx is
	// logged with its request ID. The log never holds the key, the
	// Authorization header, a prompt or an argument: it records the method,
	// the path, the status and the reason the handler noted.
	return requestLogMiddleware(s.logger, append([]routing.One{s.providerAssetRoute, signedRoute}, s.providerWebhookSecretRoutes()...),
		localeTraceMiddleware(s.application, s.texts, clientAddressMiddleware(nil,
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
		headers := s.application.Administration.AuditLogs.RequestHeaders
		client := mcpClient{address: request.Header.Get(headers.ForwardedFor), userAgent: request.Header.Get(headers.UserAgent)}
		if client.address == "" {
			client.address = request.Header.Get(headers.RealIP)
		}
		ctx := context.WithValue(context.WithValue(request.Context(), mcpBaseKey{}, base), mcpClientKey{}, client)
		endpoint.ServeHTTP(writer, request.WithContext(ctx))
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
//
// The listener's limits (access_keys.rate_limit, mcp_public_limits.go) bracket
// the check: a client address that failed to authenticate too often is refused
// with 429 before the key is looked up, every 401 counts as a failure of the
// address, and an authenticated key is counted before its tool or download runs.
func (s *Server) withMCPKey(next mcpPrincipalHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !s.mcpAccessKeysAllowed(writer, request) {
			return
		}
		limits := s.mcpRateLimit()
		client := ""
		if limits != nil {
			// Checked before the key is looked up and counted after it fails:
			// sequential guesses stop at the limit; attempts in flight at once
			// all pass the check and overshoot it by their number (a 256-bit
			// secret stays unguessable either way). Reserving a slot per attempt
			// would instead throttle valid callers that share an address while
			// their keys are being checked.
			client = mcpClientBucket(request)
			if throttled, wait := s.mcpAuthFailures.exceeded(client, limits.AuthFailuresPerMinute); throttled {
				s.writeMCPAuthThrottled(writer, request, wait, limits.AuthFailuresPerMinute)
				return
			}
		}
		principal, err := s.mcpIdentity.Identify(request)
		switch {
		case err == nil && principal.Credential().Kind == access.CredentialAccessKey:
		case err == nil, errors.Is(err, accesskeys.ErrNotAKey), errors.Is(err, accesskeys.ErrUnauthorized):
			if limits != nil {
				s.mcpAuthFailures.record(client)
			}
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
		if limits != nil {
			keyID := principal.Credential().KeyID
			if allowed, wait := s.mcpKeyRequests.take(keyID, limits.RequestsPerMinute); !allowed {
				s.writeMCPKeyLimited(writer, request, wait, keyID, limits.RequestsPerMinute)
				return
			}
		}
		next(writer, request.WithContext(context.WithValue(request.Context(), mcpPrincipalKey{}, principal)), principal)
	}
}

// mcpAccessKeysAllowed answers whether the administrator's overall switch
// lets the listener serve the request (#332). Off refuses every key request
// and every signed download before any credential is looked at; the refusal
// is an explicit JSON-RPC error, not a 401. A switch that cannot be read is
// an internal failure, never an implicit "on".
func (s *Server) mcpAccessKeysAllowed(writer http.ResponseWriter, request *http.Request) bool {
	if s.administrationAccessKeys == nil {
		return true
	}
	enabled, err := s.administrationAccessKeys.Enabled(request.Context())
	if err != nil {
		s.writeMCPProblem(writer, request, http.StatusServiceUnavailable, -32603,
			"internal", "Internal error", s.application.MCPPublic.Messages.Internal, err)
		return false
	}
	if !enabled {
		s.writeMCPProblem(writer, request, http.StatusForbidden, -32005,
			"access_keys_disabled", "Access keys disabled", s.application.MCPPublic.Messages.AccessKeysDisabled)
		return false
	}
	return true
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

// mcpSignedDownload streams one product file named by a short-lived signed
// token (#332): no key, no rate limit of a key and no detail about a refusal,
// which is always a 404. The token's owner is the download's Principal, so the
// same owner scope, delivery headers and audit apply as on the key route.
func (s *Server) mcpSignedDownload(writer http.ResponseWriter, request *http.Request) {
	if !s.mcpAccessKeysAllowed(writer, request) {
		return
	}
	if s.taskAssetLinks == nil {
		http.NotFound(writer, request)
		return
	}
	taskID, assetID, ownerID, err := s.taskAssetLinks.OpenTaskAsset(request.PathValue("token"))
	if err != nil {
		if trace := requestTraceFrom(request.Context()); trace != nil {
			trace.noteProblem("signed_download_invalid", "signed task asset link refused", []error{err})
		}
		http.NotFound(writer, request)
		return
	}
	request.SetPathValue("taskID", string(taskID))
	request.SetPathValue("assetID", string(assetID))
	if trace := requestTraceFrom(request.Context()); trace != nil {
		trace.noteOwner(string(ownerID))
	}
	s.downloadAsset(writer, request, access.Principal{Claims: access.Claims{ID: string(ownerID)}})
}

// writeMCPProblem answers a refusal of the listener in JSON-RPC form: the
// standard code and message, the Schema's wording of the reason in the request's
// language and the request ID the access log records. The causes go to the
// access log only.
func (s *Server) writeMCPProblem(writer http.ResponseWriter, request *http.Request, status, rpcCode int, code, rpcMessage, detail string, causes ...error) {
	s.writeMCPProblemData(writer, request, status, rpcCode, code, rpcMessage, detail, nil, causes...)
}

// writeMCPProblemData is writeMCPProblem with more members in the error's data,
// such as the wait a 429 asks the caller to honour. The detail and the request
// ID are the problem's own and cannot be replaced by them.
func (s *Server) writeMCPProblemData(writer http.ResponseWriter, request *http.Request, status, rpcCode int, code, rpcMessage, detail string, extra map[string]any, causes ...error) {
	data := map[string]any{}
	for name, value := range extra {
		data[name] = value
	}
	data["detail"] = s.localizedText(requestLocale(request), detail)
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
	-32005: "mcp_access_keys_disabled",
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
